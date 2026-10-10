//go:build windows

package main

// winnative_text_test.go 界面文字自检：对 contentTexts / listTexts / headerTexts 的每段文字
// 检查矩形非空、单行放得下、字体覆盖该串字。

import (
	"fmt"
	"runtime"
	"sync"
	"testing"
)

// gdiThreadOnce 将测试 goroutine 固定在一个线程上（GDI 对象与线程绑定）。
var gdiThreadOnce sync.Once

// newMeasureUI 创建仅含字体、无窗口的界面对象，供布局纯函数在各尺寸/DPI 下计算。
func newMeasureUI(t *testing.T, dpi int32) *nativeUI {
	t.Helper()
	gdiThreadOnce.Do(runtime.LockOSThread)
	u := &nativeUI{sel: -1, hover: -1, dpi: dpi}
	u.makeGDI()
	if u.dcMeasure == 0 || u.fUI == 0 || u.fUIB == 0 || u.fSmall == 0 ||
		u.fTitle == 0 || u.fMono == 0 || u.fMonoCJK == 0 {
		u.freeGDI()
		t.Fatalf("%d DPI：字体或量字用的 DC 没建起来，后面的检查没有意义", dpi)
	}
	u.buildRows()
	return u
}

// fillFields 按任务定义构造字段控制（自检只需下标与类型）。
func fillFields(u *nativeUI) {
	u.fields = nil
	if u.sel < 0 || u.sel >= len(guiTasks) {
		return
	}
	for i, f := range guiTasks[u.sel].Fields {
		u.fields = append(u.fields, natFieldCtl{index: i, isBool: f.Kind == "check"})
	}
}

// checkTexts 检查矩形非空、字体已指定且覆盖文字、单行是否放得下。
func checkTexts(t *testing.T, u *nativeUI, where string, list []natText) {
	t.Helper()
	for _, tx := range list {
		if tx.s == "" {
			continue
		}
		if tx.r.Right <= tx.r.Left || tx.r.Bottom <= tx.r.Top {
			t.Errorf("%s：%q 拿到的是空矩形（左 %d、右 %d）—— 这么交给 GDI 等于永远画不出来",
				where, tx.s, tx.r.Left, tx.r.Right)
			continue
		}
		if tx.font == 0 {
			t.Errorf("%s：%q 没有指定字体", where, tx.s)
			continue
		}
		if !u.fontCovers(tx.font, tx.s) {
			t.Errorf("%s：%q 里有这套字体没有的字，画的时候系统会临时换字体顶上"+
				"（一行混两套字形，看着就像字叠在一起）", where, tx.s)
		}
		if tx.flags&dtSingleLine == 0 || tx.flags&dtEndEllipsis != 0 {
			continue
		}
		if need, avail := u.measure(tx.s, tx.font), tx.r.Right-tx.r.Left; need > avail {
			t.Errorf("%s：单行文字 %q 要 %d 像素宽，只给了 %d，又没给省略号 —— 会在半个字上被切断",
				where, tx.s, need, avail)
		}
	}
}

// uiState 表示界面的一组状态文本。
type uiState struct {
	status, cmd, addr, room, join string
}

// testLongLink 是一段真实长度的中继链接。
const testLongLink = "mclbx://relay.example.com:3478?turn=turn%3Arelay.example.com%3A3478" +
	"&tcp=8090&sig=8091&room=abc123&secret=kQm3v7Xp9dRt5wYb"

func uiStates() []uiState {
	return []uiState{
		{status: "空闲"},
		{status: "空闲", cmd: "mclbx relaybox --turn-port 3478 --tcp-port 8090", addr: testLongLink},
		{status: "运行中 · 只开公网入口（IPv6 直连） · 3 分 12 秒",
			cmd:  "mclbx expose --mode raw --web off --port 25565 --allow 小明,小红",
			addr: "[2001:db8:1a2b:3c4d:5e6f:7a8b:9c0d:1e2f]:25565",
			join: "mclbx join --relay-server mclbx://[2001:db8::1]:8090?room=abc123"},
		{status: "已结束 · 连不上中继服务器：dial tcp [2001:db8::9]:8090: connect: no route to host",
			room: "abc123"},
	}
}

// checkUIStates 遍历每组状态 × 每种窗口尺寸 × 每个任务。
func checkUIStates(t *testing.T, u *nativeUI) {
	t.Helper()
	sizes := [][2]int32{
		{u.px(natMinWinW), u.px(natMinWinH)}, // 窗口能被拖到的最小尺寸
		{u.px(natWinW), u.px(natWinH)},       // 默认大小
		{u.px(1600), u.px(940)},              // 放大 / 大屏
	}
	for _, st := range uiStates() {
		u.statusTxt, u.command = st.status, st.cmd
		u.addr, u.room, u.join = st.addr, st.room, st.join
		for _, sz := range sizes {
			w, h := sz[0], sz[1]
			where := fmt.Sprintf("%d DPI / %d×%d / 状态 %q", u.dpi, w, h, st.status)
			checkTexts(t, u, where+" · 顶栏", u.headerTexts(w))
			checkTexts(t, u, where+" · 任务列表", u.listTexts(h))
			for i := range guiTasks {
				u.sel = i
				fillFields(u)
				checkTexts(t, u, where+" · "+guiTasks[i].Name, u.contentTexts(w, h))
			}
			u.sel = -1
		}
	}
}

// 各种 DPI、窗口尺寸与任务下文字都不被截断、字体不混用。
func TestTextNeverCutOrFontMixed(t *testing.T) {
	for _, dpi := range []int32{96, 120, 144, 192} {
		u := newMeasureUI(t, dpi)
		checkUIStates(t, u)
		u.freeGDI()
	}
}

// 顶栏各块不得互相压住，状态文字宽度不得为 0。
func TestHeaderStaysOrdered(t *testing.T) {
	for _, dpi := range []int32{96, 120, 144, 192} {
		u := newMeasureUI(t, dpi)
		u.statusTxt = "运行中 · 直连（房主侧） · 3 分 12 秒"
		for w := u.px(natMinWinW); w <= u.px(natWinW)+u.px(500); w += u.px(20) {
			hb := u.headerRects(w)
			if hb.Quit.Right > w {
				t.Errorf("%d DPI / 宽 %d：退出按钮跑到窗口外面去了（右边界 %d）", dpi, w, hb.Quit.Right)
			}
			if hb.Title.Right > hb.Ver.Left {
				t.Errorf("%d DPI / 宽 %d：名称压到版本号上了", dpi, w)
			}
			if hb.Status.Right <= hb.Status.Left {
				t.Fatalf("%d DPI / 宽 %d：状态文字宽度是 %d —— 这么算它是画不出来的",
					dpi, w, hb.Status.Right-hb.Status.Left)
			}
			if hb.Dot.Right <= hb.Dot.Left {
				t.Fatalf("%d DPI / 宽 %d：有地方放状态，状态点却是空的", dpi, w)
			}
			if hb.Ver.Right > hb.Dot.Left {
				t.Errorf("%d DPI / 宽 %d：版本号压到状态点上了（版本右 %d，点左 %d）",
					dpi, w, hb.Ver.Right, hb.Dot.Left)
			}
			if hb.Dot.Right > hb.Status.Left || hb.Status.Right > hb.Quit.Left {
				t.Errorf("%d DPI / 宽 %d：状态文字那一块和点/按钮压住了", dpi, w)
			}
		}
		u.freeGDI()
	}
}

// 同一窗口宽度下状态文字宽度须随 DPI 增大。
func TestHeaderGrowsWithDPI(t *testing.T) {
	var lastW int32
	for _, dpi := range []int32{96, 120, 144} {
		u := newMeasureUI(t, dpi)
		u.statusTxt = "空闲"
		hb := u.headerRects(u.px(natWinW))
		if got := hb.Status.Right - hb.Status.Left; got <= 0 {
			t.Errorf("%d DPI：状态文字宽度是 %d", dpi, got)
		} else if got <= lastW {
			t.Errorf("%d DPI：状态文字宽度 %d 没跟着 DPI 变大（上一档 %d）—— 排版像是写死的",
				dpi, got, lastW)
		} else {
			lastW = got
		}
		u.freeGDI()
	}
}

// 长链接放不下时改用更紧凑的界面字体，放得下则仍用等宽。
func TestValueFontPicksNarrowerOne(t *testing.T) {
	u := newMeasureUI(t, 96)
	defer u.freeGDI()

	mono, ui := u.measure(testLongLink, u.fMono), u.measure(testLongLink, u.fSmall)
	t.Logf("这段链接：等宽 %d 像素，界面字体 %d 像素", mono, ui)

	if f := u.valueFont(testLongLink, 200); f != u.fSmall {
		t.Errorf("一行只给 200 像素时，应该换成更紧凑的界面字体")
	}
	if f := u.valueFont("mclbx://[::1]:8090", 4000); f != u.fMono {
		t.Errorf("放得下的时候应该用等宽字体 —— IP、链接看着才对齐")
	}
}

// 长链接放不下时折成两行且卡片随之长高，不压住复制按钮。
func TestLongLinkWrapsToTwoLines(t *testing.T) {
	u := newMeasureUI(t, 96)
	defer u.freeGDI()

	w, h := u.px(natWinW), u.px(natWinH)
	avail := u.addrValueWidth(w)
	font := u.valueFont(testLongLink, avail)
	l1, l2 := u.splitTwoLines(testLongLink, avail, font)
	if l2 == "" {
		t.Fatalf("这么长的链接该折成两行，结果还是一行（可用宽度 %d 像素）", avail)
	}
	if l1+l2 != testLongLink {
		t.Errorf("折行不能凭空多字或少字：%q + %q", l1, l2)
	}
	for _, ln := range []string{l1, l2} {
		if need := u.measure(ln, font); need > avail {
			t.Errorf("折出来的这行还是放不下：要 %d 像素，只有 %d（%q）", need, avail, ln)
		}
	}

	// 卡片高度要跟着变
	u.addr = testLongLink
	_, addr, _, _ := u.contentRects(w, h)
	if got, want := addr.Bottom-addr.Top, u.px(natAddrH2); got != want {
		t.Errorf("要折两行时卡片高度该是 %d，算出来 %d", want, got)
	}
	btnTop := addr.Bottom - u.px(natCtlH) - u.px(12) // 复制按钮那一行的上边
	for _, tx := range u.contentTexts(w, h) {
		if tx.r.Top >= addr.Top && tx.r.Bottom <= addr.Bottom && tx.r.Bottom > btnTop {
			t.Errorf("卡片里的 %q 压在复制按钮上了（文字底 %d，按钮顶 %d）", tx.s, tx.r.Bottom, btnTop)
		}
	}

	// 一行放得下时卡片还是矮的那个
	u.addr = "mclbx://[::1]:8090"
	_, short, _, _ := u.contentRects(w, h)
	if got, want := short.Bottom-short.Top, u.px(natAddrH); got != want {
		t.Errorf("一行放得下时卡片高度该是 %d，算出来 %d", want, got)
	}
}

// 分组图标须为正方形、位于自己的分组行内，且不与文字重叠。
func TestGroupIconsDoNotCollide(t *testing.T) {
	for _, dpi := range []int32{96, 120, 144, 192} {
		u := newMeasureUI(t, dpi)
		for _, h := range []int32{u.px(natMinWinH), u.px(natWinH)} {
			icons := u.listGroupIcons(h)
			if len(icons) == 0 {
				t.Fatalf("%d DPI：一个分组图标都没算出来", dpi)
			}
			texts := u.listTexts(h)
			for _, ic := range icons {
				d := ic.r.Right - ic.r.Left
				if d != ic.r.Bottom-ic.r.Top {
					t.Errorf("%d DPI：图标不是正方形（%d x %d）", dpi, d, ic.r.Bottom-ic.r.Top)
				}
				if ic.r.Left < u.px(12) || ic.r.Right > u.px(natListW)-u.px(6) {
					t.Errorf("%d DPI：图标超出列表左边距（%d..%d）", dpi, ic.r.Left, ic.r.Right)
				}
				for _, tx := range texts {
					if rectsOverlap(ic.r, tx.r) {
						t.Errorf("%d DPI：图标和文字压在一起了：图标 %+v，文字 %q %+v", dpi, ic.r, tx.s, tx.r)
					}
				}
				// 图标要落在某个分组行里（上下都不能越界）
				inRow := false
				for _, row := range u.rows {
					if !row.isGroup {
						continue
					}
					ry := row.y - u.listTop + u.px(natTopH)
					if ic.r.Top >= ry && ic.r.Bottom <= ry+row.h {
						inRow = true
						break
					}
				}
				if !inRow {
					t.Errorf("%d DPI：图标没落在任何分组行里：%+v", dpi, ic.r)
				}
			}
		}
		u.freeGDI()
	}
}

// 各分组的图标编号与颜色须互不相同。
func TestGroupIconsAreDistinct(t *testing.T) {
	seenKind := map[int]string{}
	seenColor := map[uint32]string{}
	for _, s := range natGroupStyles {
		if prev, dup := seenKind[s.Icon]; dup {
			t.Errorf("%q 和 %q 用了同一个图标编号 %d", s.Name, prev, s.Icon)
		}
		seenKind[s.Icon] = s.Name
		if prev, dup := seenColor[s.Color]; dup {
			t.Errorf("%q 和 %q 用了同一个颜色", s.Name, prev)
		}
		seenColor[s.Color] = s.Name
	}
}

func rectsOverlap(a, b rectT) bool {
	return a.Left < b.Right && b.Left < a.Right && a.Top < b.Bottom && b.Top < a.Bottom
}

// 等宽字体不含汉字时，日志字体须回退（fontCovers 能如实判断）。
func TestMonoFontFallsBackForChinese(t *testing.T) {
	u := newMeasureUI(t, 96)
	defer u.freeGDI()

	if !u.fontCovers(u.fUI, "中文和 English 混在一起") {
		t.Errorf("界面字体应该认识中文")
	}
	if !u.fontCovers(u.fMono, "mclbx --port 25565") {
		t.Errorf("等宽字体应该认识 ASCII")
	}
	if u.fontCovers(u.fMono, "字") {
		// 真碰上一台等宽字体带汉字的系统，那用等宽反而是对的
		t.Log("这台机器的等宽字体认识汉字，日志继续用等宽即可")
	} else if u.pickLogFont() == u.fMono {
		t.Errorf("等宽字体不认识汉字，日志却还在用它 —— 中文会被系统临时换字体顶上")
	}

	if got := u.techFont("mclbx room --web off --room abc123"); got != u.fMono {
		t.Errorf("纯 ASCII 的命令行该用等宽字体")
	}
	if got := u.techFont("运行后这里会显示等价命令"); got == u.fMono {
		t.Errorf("带中文的一行不能再用等宽字体画")
	}
}

// 量字基础行为。
func TestMeasureBasics(t *testing.T) {
	u := newMeasureUI(t, 96)
	defer u.freeGDI()

	if got := u.measure("", u.fUI); got != 0 {
		t.Errorf("空串宽度应该是 0，量出来 %d", got)
	}
	a := u.measure("mclbx", u.fUI)
	b := u.measure("mclbx 联机小工具", u.fUI)
	if a <= 0 {
		t.Fatalf("量出来的宽度不该是 %d", a)
	}
	if b <= a {
		t.Errorf("更长的字串应该更宽（%q=%d，长的那串=%d）", "mclbx", a, b)
	}
	if s, ti := u.measure("中文", u.fSmall), u.measure("中文", u.fTitle); s >= ti {
		t.Errorf("小字号应该比标题字号窄（小=%d，标题=%d）—— 量字好像没看字体", s, ti)
	}
}
