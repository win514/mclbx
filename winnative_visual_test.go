//go:build windows

package main

// winnative_visual_test.go 视觉回归：把界面离线渲染成图，与 testdata/visual 下的基准逐像素比较。
//
// 基准与机器绑定（DPI/字体），manifest 记录指纹，指纹不同则跳过。更新基准：-update。

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"unsafe"
)

var updateVisual = flag.Bool("update", false, "重写视觉基准图（testdata/visual）")
var dumpPages = flag.String("dumppages", "", "把每个任务页各渲染一张 PNG 到这个目录（用来拼总览图、人工整体过一遍）")

// TestDumpAllTaskPages 渲染每个任务页并写盘，用于整体目视检查。
//
//	go test -run TestDumpAllTaskPages -dumppages <目录>
func TestDumpAllTaskPages(t *testing.T) {
	if *dumpPages == "" {
		t.Skip("没有指定 -dumppages，跳过（这是个人工看的工具，不是断言）")
	}
	st := uiStates()[2] // 运行中 + 长状态 + 地址卡片
	for i, task := range guiTasks {
		t.Logf("渲染 [%d/%d] %s", i+1, len(guiTasks), task.Key)
		s := visScene{
			Name:  fmt.Sprintf("%02d-%s", i, task.Key),
			Task:  i,
			State: st,
			W:     natWinW, H: natWinH, DPI: 96,
			Extra: func(u *nativeUI) { u.running = true; u.taskTitle = task.Name },
		}
		img := renderScene(t, s)
		writePNG(t, filepath.Join(*dumpPages, s.Name+".png"), img)
	}
	t.Logf("渲染了 %d 个任务页 -> %s", len(guiTasks), *dumpPages)
}

// visScene 描述一个待渲染场景。
type visScene struct {
	Name  string
	Task  int     // 选中的任务下标
	State uiState // 状态文字/命令/地址
	W, H  int32   // 逻辑尺寸（按 DPI 放大）
	DPI   int32
	Extra func(u *nativeUI) // 补充状态（运行中、报错、日志行）
}

func visScenes() []visScene {
	st := uiStates()
	return []visScene{
		{
			Name: "idle-envcheck", Task: 0, State: st[0], W: natWinW, H: natWinH, DPI: 96,
			Extra: func(u *nativeUI) { u.done = true },
		},
		{
			Name: "running-join-longlink", Task: 4, State: st[2], W: natWinW, H: natWinH, DPI: 96,
			Extra: func(u *nativeUI) { u.running = true; u.taskTitle = "加入房间" },
		},
		{
			Name: "running-join-longlink-144", Task: 4, State: st[2], W: natWinW, H: natWinH, DPI: 144,
			Extra: func(u *nativeUI) { u.running = true; u.taskTitle = "加入房间" },
		},
		{
			Name: "error-minwindow", Task: 7, State: st[3], W: natMinWinW, H: natMinWinH, DPI: 96,
			Extra: func(u *nativeUI) { u.errMsg = "连不上中继服务器" },
		},
		{
			Name: "host-room", Task: 1, State: st[1], W: natWinW, H: natWinH, DPI: 120,
			Extra: func(u *nativeUI) { u.done = true },
		},
	}
}

// renderScene 把场景离屏渲染成一张图（窗口不显示、不碰屏幕）。
//
// 不创建子控件：paintTo 本就不含它们，且连续创建 RichEdit 会卡在 syscall 中。
// 只建无装饰父窗口、填字段下标，再调 paintTo。
func renderScene(t *testing.T, s visScene) *image.RGBA {
	t.Helper()
	withTempConfig(t) // 避免任何路径写入用户真实配置目录
	w := s.W * s.DPI / 96
	h := s.H * s.DPI / 96

	hInst, _, _ := pGetModuleHandleW.Call(0)
	cls, _ := syscall.UTF16PtrFromString("STATIC")
	parent, _, perr := pCreateWindowExW.Call(0, uintptr(unsafe.Pointer(cls)), 0,
		uintptr(wsPopupT), 0, 0, uintptr(w), uintptr(h), 0, 0, hInst, 0)
	if parent == 0 {
		t.Skipf("建不出离屏窗口，跳过视觉对比：%v", perr)
	}
	defer pDestroyWindow.Call(parent)

	u := &nativeUI{ctl: &guiController{}, hwnd: parent, dpi: s.DPI, sel: -1, hover: -1}
	u.makeGDI()
	u.buildRows()
	// 直接指定选中任务，避免 selectTask 读取真控件并写入配置
	u.sel = s.Task
	fillFields(u)
	defer u.freeGDI()

	// 冻结状态：把"会变的东西"都写成固定的
	u.statusTxt, u.command = s.State.status, s.State.cmd
	u.addr, u.room, u.join = s.State.addr, s.State.room, s.State.join
	if s.Extra != nil {
		s.Extra(u)
	}
	u.layout()

	// 内存 DC 与位图须以屏幕 DC 为模板创建（CreateCompatible* 不接受 0）
	screen, _, _ := pGetDC.Call(0)
	if screen == 0 {
		t.Skip("拿不到屏幕 DC，跳过视觉对比")
	}
	defer pReleaseDC.Call(0, screen)
	mem, _, _ := pCreateCompatDC.Call(screen)
	bmp, _, _ := pCreateCompatBmp.Call(screen, uintptr(w), uintptr(h))
	if mem == 0 || bmp == 0 {
		t.Skip("建不出内存 DC / 位图，跳过视觉对比")
	}
	oldBmp, _, _ := pSelectObject.Call(mem, bmp)
	u.paintTo(mem, w, h)
	pSelectObject.Call(mem, oldBmp)

	// 把位图读成一行行字节（顶向下 32bpp）
	type bmiHeader struct {
		Size          uint32
		Width         int32
		Height        int32
		Planes        uint16
		BitCount      uint16
		Compression   uint32
		SizeImage     uint32
		XPelsPerMeter int32
		YPelsPerMeter int32
		ClrUsed       uint32
		ClrImportant  uint32
	}
	hdr := bmiHeader{Size: 40, Width: w, Height: -h, Planes: 1, BitCount: 32, Compression: 0}
	raw := make([]byte, int(w)*int(h)*4)
	n, _, _ := pGetDIBits.Call(mem, bmp, 0, uintptr(h), uintptr(unsafe.Pointer(&raw[0])),
		uintptr(unsafe.Pointer(&hdr)), 0) // DIB_RGB_COLORS = 0
	pDeleteObject.Call(bmp)
	pDeleteDC.Call(mem)
	if n == 0 {
		t.Fatalf("%s：GetDIBits 一行都没读到", s.Name)
	}

	img := image.NewRGBA(image.Rect(0, 0, int(w), int(h)))
	for i := 0; i+3 < len(raw); i += 4 {
		img.Pix[i+0] = raw[i+2] // BGRA -> RGBA
		img.Pix[i+1] = raw[i+1]
		img.Pix[i+2] = raw[i+0]
		img.Pix[i+3] = 0xFF
	}
	return img
}

type visEntry struct {
	Name        string `json:"name"`
	W           int32  `json:"w"`
	H           int32  `json:"h"`
	DPI         int32  `json:"dpi"`
	Fingerprint string `json:"fingerprint"`
}

type visManifest struct {
	Note    string     `json:"note"`
	Entries []visEntry `json:"entries"`
}

const visDir = "testdata/visual"

// visualFingerprint 返回本机字体渲染度量（DPI、字宽、字体覆盖），随机器/字体变化。
func visualFingerprint(t *testing.T, dpi int32) string {
	t.Helper()
	u := newMeasureUI(t, dpi)
	defer u.freeGDI()
	return fmt.Sprintf("dpi=%d ui=%d/%d mono=%d title=%d cjk=%v",
		dpi,
		u.measure("mclbx 联机工具", u.fUI),
		u.measure("0123456789", u.fSmall),
		u.measure("mclbx", u.fMono),
		u.measure("mclbx 联机工具", u.fTitle),
		u.fontCovers(u.fMonoCJK, "中文"),
	)
}

func loadVisManifest() *visManifest {
	m := &visManifest{}
	b, err := os.ReadFile(filepath.Join(visDir, "manifest.json"))
	if err == nil {
		_ = json.Unmarshal(b, m)
	}
	return m
}

func (m *visManifest) find(name string) *visEntry {
	for i := range m.Entries {
		if m.Entries[i].Name == name {
			return &m.Entries[i]
		}
	}
	return nil
}

// visOutDir 返回输出目录：默认临时目录，MCLBX_VIS_KEEP 可指定保留位置。
func visOutDir(t *testing.T) string {
	t.Helper()
	keep := os.Getenv("MCLBX_VIS_KEEP")
	if keep == "" {
		return t.TempDir()
	}
	if err := os.MkdirAll(keep, 0o755); err != nil {
		t.Fatalf("建 %s 失败：%v", keep, err)
	}
	return keep
}

// frameLooksBlank 判断整帧是否为单一颜色（即未绘制），以区分空帧与界面改动。
func frameLooksBlank(img *image.RGBA) bool {
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	if w == 0 || h == 0 {
		return true
	}
	i0 := img.PixOffset(0, 0)
	r0, g0, b0, a0 := img.Pix[i0], img.Pix[i0+1], img.Pix[i0+2], img.Pix[i0+3]
	for y := 7; y < h; y += 13 {
		for x := 7; x < w; x += 13 {
			i := img.PixOffset(x, y)
			if img.Pix[i] != r0 || img.Pix[i+1] != g0 || img.Pix[i+2] != b0 || img.Pix[i+3] != a0 {
				return false
			}
		}
	}
	return true
}

// diffPixels 逐像素比较，返回变化点数与变化区域的外接矩形。
func diffPixels(a, b *image.RGBA) (int, image.Rectangle) {
	changed := 0
	// 不能用 image.Rect 构造空矩形，会把 min/max 颠倒
	box := image.Rectangle{
		Min: image.Point{X: 1 << 30, Y: 1 << 30},
		Max: image.Point{X: -1, Y: -1},
	}
	for y := 0; y < a.Bounds().Dy(); y++ {
		for x := 0; x < a.Bounds().Dx(); x++ {
			i := a.PixOffset(x, y)
			if a.Pix[i] == b.Pix[i] && a.Pix[i+1] == b.Pix[i+1] &&
				a.Pix[i+2] == b.Pix[i+2] && a.Pix[i+3] == b.Pix[i+3] {
				continue
			}
			changed++
			if x < box.Min.X {
				box.Min.X = x
			}
			if y < box.Min.Y {
				box.Min.Y = y
			}
			if x > box.Max.X {
				box.Max.X = x
			}
			if y > box.Max.Y {
				box.Max.Y = y
			}
		}
	}
	return changed, box
}

// blockName 把像素坐标映射为界面区域名称。
func blockName(t *testing.T, s visScene, dpi int32, pt image.Point) string {
	u := newMeasureUI(t, dpi)
	defer u.freeGDI()
	u.sel = s.Task
	fillFields(u)
	w := s.W * dpi / 96
	h := s.H * dpi / 96
	head, addr, log, logLabelY := u.contentRects(w, h)
	lr := u.listRect(h)
	hb := u.headerRects(w)

	in := func(r rectT) bool {
		return int32(pt.X) >= r.Left && int32(pt.X) < r.Right && int32(pt.Y) >= r.Top && int32(pt.Y) < r.Bottom
	}
	switch {
	case in(hb.Title):
		return "顶栏·名称"
	case in(hb.Status):
		return "顶栏·状态文字"
	case in(hb.Quit):
		return "顶栏·退出按钮"
	case pt.Y < int(hb.Quit.Bottom):
		return "顶栏"
	case in(lr):
		return "左侧任务列表"
	case in(head):
		return "右侧·任务信息与字段"
	case in(addr):
		return "右侧·地址卡片"
	case in(log):
		return "右侧·日志区"
	case int32(pt.Y) >= logLabelY && int32(pt.Y) < log.Top:
		return "右侧·日志标题行"
	}
	return "别处"
}

func writePNG(t *testing.T, path string, img *image.RGBA) {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("编码 PNG 失败：%v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("建目录失败：%v", err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatalf("写 %s 失败：%v", path, err)
	}
}

// TestVisualBaseline 视觉回归主用例；-update 会把当前渲染结果写为新基准。
func TestVisualBaseline(t *testing.T) {
	scenes := visScenes()
	if *updateVisual {
		m := &visManifest{Note: "由 go test -run TestVisualBaseline -update 生成；指纹不同 = 换机器了，对比会自动跳过"}
		for _, s := range scenes {
			img := renderScene(t, s)
			if frameLooksBlank(img) {
				t.Fatalf("%s：渲出来是**空白帧**，拒绝把它录成基准 —— "+
					"空白基准会把之后每一次对比都变成噪音（每次都报「画面变了」）。"+
					"先查 gpStart/gpStop 那类「进程状态」问题，或者这一帧根本没画上去。", s.Name)
			}
			writePNG(t, filepath.Join(visDir, s.Name+".png"), img)
			m.Entries = append(m.Entries, visEntry{
				Name: s.Name, W: s.W, H: s.H, DPI: s.DPI,
				Fingerprint: visualFingerprint(t, s.DPI),
			})
			t.Logf("写入基准：%s (%dx%d @%d DPI)", s.Name, img.Bounds().Dx(), img.Bounds().Dy(), s.DPI)
		}
		b, _ := json.MarshalIndent(m, "", "  ")
		if err := os.MkdirAll(visDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(visDir, "manifest.json"), append(b, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}

	m := loadVisManifest()
	if len(m.Entries) == 0 {
		t.Skipf("还没有基准图（%s/manifest.json 不存在）。第一次生成：go test -run TestVisualBaseline -update", visDir)
	}
	outDir := visOutDir(t)
	compared, skipped := 0, 0
	for _, s := range scenes {
		e := m.find(s.Name)
		if e == nil {
			t.Errorf("%s：基准里没有这个场景（是不是加了新场景忘了 -update？）", s.Name)
			continue
		}
		fp := visualFingerprint(t, s.DPI)
		if e.Fingerprint != fp {
			skipped++
			t.Logf("%s：跳过（基准是在另一台机器/另一个 DPI 下录的）\n    基准 %s\n    本机 %s", s.Name, e.Fingerprint, fp)
			continue
		}
		wantPath := filepath.Join(visDir, s.Name+".png")
		b, err := os.ReadFile(wantPath)
		if err != nil {
			t.Errorf("%s：读不到基准图 %s", s.Name, wantPath)
			continue
		}
		want, err := png.Decode(bytes.NewReader(b))
		if err != nil {
			t.Errorf("%s：基准图坏了（%v）—— 重新生成：go test -run TestVisualBaseline -update", s.Name, err)
			continue
		}
		got := renderScene(t, s)

		wb := want.Bounds()
		if wb.Dx() != got.Bounds().Dx() || wb.Dy() != got.Bounds().Dy() {
			t.Errorf("%s：图尺寸变了 %dx%d -> %dx%d（窗口尺寸/DPI 改过？确认后 -update）",
				s.Name, wb.Dx(), wb.Dy(), got.Bounds().Dx(), got.Bounds().Dy())
			continue
		}
		wantRGBA := image.NewRGBA(wb)
		for y := 0; y < wb.Dy(); y++ {
			for x := 0; x < wb.Dx(); x++ {
				r, g, bb, a := want.At(wb.Min.X+x, wb.Min.Y+y).RGBA()
				i := wantRGBA.PixOffset(x, y)
				wantRGBA.Pix[i] = uint8(r >> 8)
				wantRGBA.Pix[i+1] = uint8(g >> 8)
				wantRGBA.Pix[i+2] = uint8(bb >> 8)
				wantRGBA.Pix[i+3] = uint8(a >> 8)
			}
		}

		changed, box := diffPixels(wantRGBA, got)
		compared++
		if changed == 0 {
			t.Logf("%s：和基准一致（%dx%d）", s.Name, wb.Dx(), wb.Dy())
			continue
		}
		// 先落盘实际图与基准图，便于人眼查看
		writePNG(t, filepath.Join(outDir, s.Name+"-actual.png"), got)
		writePNG(t, filepath.Join(outDir, s.Name+"-want.png"), wantRGBA)
		// 空帧与界面改动分开报告
		if frameLooksBlank(got) {
			t.Fatalf("%s：这一帧几乎是空的 —— 不是「界面变了」，是这一帧没画上去。"+
				"别急着 -update（那会把空白录成基线）。图在 %s", s.Name, outDir)
		}
		total := wb.Dx() * wb.Dy()
		centre := image.Point{X: (box.Min.X + box.Max.X) / 2, Y: (box.Min.Y + box.Max.Y) / 2}
		where := blockName(t, s, s.DPI, centre)
		t.Errorf("%s：画面变了 —— %d/%d 个像素不同（%.2f%%），主要集中在「%s」，范围 x %d..%d y %d..%d\n"+
			"     想看图：%s\n     确认这是有意的：go test -run TestVisualBaseline -update",
			s.Name, changed, total, float64(changed)*100/float64(total), where,
			box.Min.X, box.Max.X, box.Min.Y, box.Max.Y, outDir)
	}
	// 全部跳过须判为失败：否则测试会静默通过而实际未做任何比较
	if compared == 0 {
		t.Fatalf("%d 个场景全被跳过，一条都没比 —— 这样的「通过」等于什么都没验。"+
			"最常见的原因是基准的机器指纹和本机不一致（换了机器/DPI/字体），"+
			"确认界面没变之后再重新录：go test -run TestVisualBaseline -update", skipped)
	}
	t.Logf("视觉回归：比了 %d 个场景，跳过 %d 个", compared, skipped)
}

// TestRenderingDoesNotDependOnProcessHistory 验证同场景在同进程内两次渲染逐像素一致。
//
// 中间穿插别的场景，确保渲染结果不取决于进程历史。GDI+ 生命周期见 gdiplus.go。
func TestRenderingDoesNotDependOnProcessHistory(t *testing.T) {
	scenes := visScenes()
	if len(scenes) < 2 {
		t.Skip("场景太少，插不进去中间那一帧")
	}
	outDir := visOutDir(t)
	for i, s := range scenes {
		first := renderScene(t, s)
		_ = renderScene(t, scenes[(i+1)%len(scenes)]) // 中间穿插渲染另一场景
		again := renderScene(t, s)
		if n, _ := diffPixels(first, again); n != 0 {
			writePNG(t, filepath.Join(outDir, s.Name+"-first.png"), first)
			writePNG(t, filepath.Join(outDir, s.Name+"-again.png"), again)
			t.Errorf("%s：同一个进程里渲两次不一样（%d 个像素不同）—— "+
				"渲染结果取决于「这个进程之前跑过什么」，视觉基准就不可信了。图在 %s",
				s.Name, n, outDir)
			continue
		}
		t.Logf("%s：两次渲染逐像素一致（%dx%d）", s.Name, first.Bounds().Dx(), first.Bounds().Dy())
	}
}
