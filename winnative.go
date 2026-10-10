//go:build windows

package main

// winnative.go 原生界面：用 Windows 自带控件绘制，不依赖浏览器引擎。
// 任务列表自绘，输入用系统控件，日志用 RichEdit，仅用一个 500ms 定时器轮询且状态未变不重绘。

import (
	"encoding/json"
	"fmt"
	"runtime"
	"strings"
	"syscall"
	"unsafe"
)

// 常量

const (
	natWinW = 1080
	natWinH = 730

	// 窗口最小尺寸下限：界面按像素摆放，过窄会使右侧内容重叠。
	natMinWinW = 880
	natMinWinH = 700

	natTopH      = 46  // 顶部栏高度
	natListW     = 306 // 左侧任务列表宽度
	natPad       = 16
	natRowH      = 54  // 任务条目高度
	natGroupH    = 26  // 分组标题高度
	natFieldH    = 54  // 一个字段占的高度（标签 + 输入框）
	natAddrH     = 104 // 地址卡片高度（值一行放得下）
	natAddrH2    = 136 // 地址卡片高度（值折成两行；中继链接常较长）
	natAddrLineH = 26  // 地址卡片里那行值的行高
	natCtlH      = 32  // 输入框/按钮高度
	natMinLogH   = 130
	natTimerID   = 1
	natPollMs    = 500
	natLogKeep   = 3000 // 日志最多保留多少行（与网页版一致）
	natLogDropN  = 600  // 超了就先删掉最老的这么多行
)

// 控件 ID
const (
	natIDRun      = 1001
	natIDStop     = 1002
	natIDQuit     = 1003
	natIDClearLog = 1004
	natIDCopyLog  = 1005
	natIDCopyAddr = 1006
	natIDCopyJoin = 1007
	natIDField0   = 2000 // 字段 i 用 natIDField0+i
)

// 颜色（COLORREF = 0x00BBGGRR，注意是 BGR 不是 RGB）
const (
	natBg      = 0x00140B07 // #070b14 窗口底色（渐变的下端）
	natBgTop   = 0x001A0F0A // #0a0f1a 窗口底色（渐变的上端）
	natBgLite  = 0x00160D08 // #080d16 比底色略亮一点，用在面板渐变的下端
	natHeadHi  = 0x00332015 // #152033 顶栏渐变的上端
	natSurface = 0x002C1A11 // #111a2c
	natSurf2   = 0x003A2016 // #16203a
	natSelTop  = 0x00442A15 // #152a44 选中条目渐变的上端
	natSelBot  = 0x003A2011 // #11203a 选中条目渐变的下端
	natLine    = 0x004C3022 // #22304c
	natInk     = 0x00F9EEE8 // #e8eef9
	natInk2    = 0x00CCB09F // #9fb0cc
	natMuted   = 0x00997C6B // #6b7c99
	natTeal    = 0x00BFD42D // #2dd4bf
	natCyan    = 0x00F8BD38 // #38bdf8
	natAmber   = 0x0024BFFB // #fbbf24
	natRose    = 0x008571FB // #fb7185
	natViolet  = 0x00FA8BA7 // #a78bfa 进房间
	natGreen   = 0x0080DE4A // #4ade80 小工具
	natPink    = 0x00B672F4 // #f472b6 中继
	natOkInk   = 0x00DCF07F // #7ff0dc
	natBadInk  = 0x00BFB3FF // #ffb3bf
	natWarnInk = 0x008AD9FF // #ffd98a
)

// 功能模块的配色与图标：每组配一个颜色与手绘图标（不引字体/图片）。
const (
	natIconSearch = iota // 放大镜：体检
	natIconDoor          // 门：开房间
	natIconEnter         // 箭头进门：进房间
	natIconAlert         // 感叹号：连不上时
	natIconTools         // 两根滑杆：小工具
	natIconRelay         // 两点一线：中继
)

const natIconSize = 14 // 图标边长（逻辑像素）

type natGroupStyle struct {
	Name  string
	Color uint32
	Icon  int
}

// natGroupStyles 顺序即左侧分组顺序；Name 必须与 guiTasks 的 Group 完全一致。
var natGroupStyles = []natGroupStyle{
	{"环境检测", natTeal, natIconSearch},
	{"房主模式", natCyan, natIconDoor},
	{"玩家模式", natViolet, natIconEnter},
	{"故障排查", natAmber, natIconAlert},
	{"实用工具", natGreen, natIconTools},
	{"中继服务", natPink, natIconRelay},
}

func groupStyle(name string) *natGroupStyle {
	for i := range natGroupStyles {
		if natGroupStyles[i].Name == name {
			return &natGroupStyles[i]
		}
	}
	return nil
}

// groupColor 某个分组的强调色（没配对就退回中性色）
func groupColor(name string) uint32 {
	if s := groupStyle(name); s != nil {
		return s.Color
	}
	return natMuted
}

// 子进程输出里以 ##KIND## 开头的行是机器标记，不进日志、只更新地址卡片
const natMarkerPrefix = "##"

// Win32

var (
	pSetTimer          = user32.NewProc("SetTimer")
	pKillTimer         = user32.NewProc("KillTimer")
	pInvalidateRect    = user32.NewProc("InvalidateRect")
	pBeginPaint        = user32.NewProc("BeginPaint")
	pEndPaint          = user32.NewProc("EndPaint")
	pDrawTextW         = user32.NewProc("DrawTextW")
	pGetWindowTextW    = user32.NewProc("GetWindowTextW")
	pSetWindowTextW    = user32.NewProc("SetWindowTextW")
	pSetFocus          = user32.NewProc("SetFocus")
	pOpenClipboard     = user32.NewProc("OpenClipboard")
	pCloseClipboard    = user32.NewProc("CloseClipboard")
	pEmptyClipboard    = user32.NewProc("EmptyClipboard")
	pSetClipboard      = user32.NewProc("SetClipboardData")
	pGetSysMetrics     = user32.NewProc("GetSystemMetrics")
	pGetWindowLongW    = user32.NewProc("GetWindowLongW")
	pSetWindowLongW    = user32.NewProc("SetWindowLongW")
	pSetWindowLongPtrW = user32.NewProc("SetWindowLongPtrW")
	pLoadLibraryW      = kernel32.NewProc("LoadLibraryW")
	pGetDpiForWin      = user32.NewProc("GetDpiForWindow")
	pMessageBoxW       = user32.NewProc("MessageBoxW")
	pGetScrollPos      = user32.NewProc("GetScrollPos")
	pGetScrollRange    = user32.NewProc("GetScrollRange")

	pGlobalAlloc  = kernel32.NewProc("GlobalAlloc")
	pGlobalLock   = kernel32.NewProc("GlobalLock")
	pGlobalUnlock = kernel32.NewProc("GlobalUnlock")

	pCreateFontW       = gdi32.NewProc("CreateFontW")
	pDeleteObject      = gdi32.NewProc("DeleteObject")
	pSetBkMode         = gdi32.NewProc("SetBkMode")
	pSetTextColor      = gdi32.NewProc("SetTextColor")
	pSelectObject      = gdi32.NewProc("SelectObject")
	pGetStockObject    = gdi32.NewProc("GetStockObject")
	pCreateCompatDC    = gdi32.NewProc("CreateCompatibleDC")
	pCreateCompatBmp   = gdi32.NewProc("CreateCompatibleBitmap")
	pBitBlt            = gdi32.NewProc("BitBlt")
	pDeleteDC          = gdi32.NewProc("DeleteDC")
	pCreatePen         = gdi32.NewProc("CreatePen")
	pRoundRect         = gdi32.NewProc("RoundRect")
	pMoveToEx          = gdi32.NewProc("MoveToEx")
	pLineTo            = gdi32.NewProc("LineTo")
	pEllipse           = gdi32.NewProc("Ellipse")
	pGetTextExtentPoin = gdi32.NewProc("GetTextExtentPoint32W")
	pGetGlyphIndicesW  = gdi32.NewProc("GetGlyphIndicesW")
	pGetDIBits         = gdi32.NewProc("GetDIBits")
	pGetDC             = user32.NewProc("GetDC")
	pReleaseDC         = user32.NewProc("ReleaseDC")
	pDwmSetAttr        = syscall.NewLazyDLL("dwmapi.dll").NewProc("DwmSetWindowAttribute")
)

// 消息/样式常量；其中 wmDestroy、wmSize、wmClose 等已在 winwebview.go 声明，此处不得重复。
const (
	wmPaint          = 0x000F
	wmEraseBkg       = 0x0014
	wmSetFont        = 0x0030
	wmTimer          = 0x0113
	wmCommand        = 0x0111
	wmMouseMove      = 0x0200
	wmMouseWheel     = 0x020A
	wmLButtonDown    = 0x0201
	wmLButtonUp      = 0x0202
	wmDrawItem       = 0x002B
	wmPrintClient    = 0x0318
	wmEnable         = 0x00F4
	wmCtlColorEdit   = 0x0133
	wmCtlColorStatic = 0x0138
	wmCtlColorBtn    = 0x0135

	bnClicked = 0

	bmSetCheck = 0x00F1 // 勾选项：设成"勾上"
	bmGetCheck = 0x00F0 // 勾选项：读回来勾没勾

	wsChild        = 0x40000000
	wsVisible      = 0x10000000
	wsTabStop      = 0x00010000
	wsVScroll      = 0x00200000
	wsBorder       = 0x00800000
	wsClipChildren = 0x02000000

	esMultiline   = 0x0004
	esReadOnly    = 0x0800
	esAutoVScroll = 0x0040
	bsPushButton  = 0x00000000
	bsCheckbox    = 0x00000003
	bsOwnerDraw   = 0x0000000B

	dtLeft        = 0x00000000
	dtCenter      = 0x00000001
	dtVCenter     = 0x00000004
	dtSingleLine  = 0x00000020
	dtEndEllipsis = 0x00008000
	dtWordBreak   = 0x00000010
	dtNoPrefix    = 0x00000800

	// GetGlyphIndicesW 的开关：字形缺失时返回 0xFFFF
	ggiMarkNonExisting = 0x0001

	transparent = 1

	emSetReadOnly   = 0x00CF
	emSetSel        = 0x00B1
	emReplaceSel    = 0x00C2
	emLineIndex     = 0x00BB
	emLineFromChar  = 0x00C9
	emGetLineCount  = 0x00BA
	emScrollCaret   = 0x0115
	emSetCharFormat = 0x0444
	emSetBkndColor  = 0x0443

	scfSelection = 0x0001
	cfmColor     = 0x40000000

	cfUnicodeText = 13
	gmemMoveable  = 0x0002

	colorWindow = 5

	dwmwaUseImmersiveDarkMode = 20
)

// charFormatW 即 Windows CHARFORMATW，大小须与 C 一致，否则系统忽略格式设置。
type charFormatW struct {
	CbSize          uint32     // 0
	DwMask          uint32     // 4
	DwEffects       uint32     // 8
	YHeight         int32      // 12
	YOffset         int32      // 16
	CrTextColor     uint32     // 20
	BCharSet        uint8      // 24
	BPitchAndFamily uint8      // 25
	SzFaceName      [32]uint16 // 26 → 26+64 = 90，结构体按 4 对齐 = 92
}

// 结构

type natFieldCtl struct {
	index  int     // 在 task.Fields 里的下标
	id     uintptr // 控件 ID
	hwnd   uintptr
	isBool bool
	rect   rectT // 标签位置（输入框控件自己记位置）
}

type natRow struct {
	isGroup bool
	group   string
	taskIdx int
	y, h    int32
}

type nativeUI struct {
	hwnd uintptr
	ctl  *guiController

	sel     int // 当前选中的任务下标（-1 = 没选）
	hover   int // 鼠标悬停的任务下标（-1 = 没有）
	listTop int32
	rows    []natRow
	rowsH   int32

	// 日志
	logHwnd     uintptr
	richEdit    bool
	colorWarned bool
	seq         int64
	jobID       string
	logLines    int // 日志控件里当前有多少行（用来控制上限）
	logText     []string

	// 状态（来自快照）
	running   bool
	done      bool
	statusTxt string
	command   string
	taskTitle string
	errMsg    string

	addr, room, join string
	addrKey          string // 上一次的地址/房间码/加入命令，用于判断卡片是否出现或消失

	// rosterRaw 是上次推来的 ##GUESTS## 原文，用于去重。
	rosterRaw string

	// 控件
	hRun, hStop, hQuit, hClear, hCopyLog, hCopyAddr, hCopyJoin uintptr
	fields                                                     []natFieldCtl

	// 画笔/字体：fMono 等宽（命令/链接/IP）；fMonoCJK 含中文时改用，避免同行混用两套字形。
	fUI, fUIB, fTitle, fMono, fMonoCJK, fSmall      uintptr
	brBg, brSurface, brSurf2, brLine, brTeal, brSel uintptr
	penLine, penTeal                                uintptr
	penTransparent                                  uintptr
	dcMeasure                                       uintptr // 只用来量文字宽度的 DC
	bmpMeasure                                      uintptr // 上面那块 DC 垫着的位图

	dpi          int32
	scale        func(int32) int32
	lastW, lastH int32
}

var curNative *nativeUI // WndProc 是回调，没法带上下文，只能放一个当前实例

func (u *nativeUI) px(v int32) int32 { return v * u.dpi / 96 }

// dpiOf 取窗口所在显示器的 DPI；GetDpiForWindow 需先 Find，直接调用会 panic。
func dpiOf(hwnd uintptr) int32 {
	if err := pGetDpiForWin.Find(); err == nil {
		if r, _, _ := pGetDpiForWin.Call(hwnd); r >= 96 {
			return int32(r)
		}
	}
	return 96
}

// 入口

// runNativeUI 打开原生界面，阻塞到窗口关闭。
func runNativeUI(ctl *guiController) error {
	// Win32 消息队列/窗口/GDI 绑定创建线程，须 LockOSThread 防止 goroutine 漂移导致收不到消息。
	runtime.LockOSThread()

	// 声明 per-monitor v2 DPI 感知（否则窗口被当位图拉伸）；API 缺失时回退 96 DPI。
	dpi := int32(96)
	if _, ok := callIfPresent(pSetDpiAwareness, dpiAwarePerMonitorV2); ok {
		if v, ok2 := callIfPresent(pGetDpiForSystem); ok2 && v >= 96 && v <= 480 {
			dpi = int32(v)
		}
	} else {
		logf("此系统的窗口 API 较旧（不支持高 DPI），按 96 DPI 显示")
	}

	u := &nativeUI{ctl: ctl, sel: -1, hover: -1, dpi: dpi}

	// 与 WebView2 版共用窗口类名，使「只能有一个窗口」的判断对两种界面都成立
	clsName, _ := syscall.UTF16PtrFromString(consoleWindowClass)
	title, _ := syscall.UTF16PtrFromString(guiWindowTitle)

	hInst, _, _ := pGetModuleHandleW.Call(0)
	cursor, _, _ := pLoadCursorW.Call(0, 32512) // IDC_ARROW

	wc := wndClassExW{
		CbSize:      uint32(unsafe.Sizeof(wndClassExW{})),
		Style:       0x0002 | 0x0001, // CS_HREDRAW | CS_VREDRAW
		LpfnWndProc: syscall.NewCallback(natWndProc),
		HInstance:   hInst,
		HCursor:     cursor,
		// 背景自己画（双缓冲），交给系统擦背景只会闪
		HbrBackground: 0,
		LpszClassName: clsName,
	}
	if r, _, err := pRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc))); r == 0 {
		return fmt.Errorf("注册窗口类失败：%v", err)
	}

	// 居中显示，尺寸按 DPI 放大
	winW := natWinW * dpi / 96
	winH := natWinH * dpi / 96
	sw, _, _ := pGetSysMetrics.Call(0) // SM_CXSCREEN
	sh, _, _ := pGetSysMetrics.Call(1) // SM_CYSCREEN
	x := (int32(sw) - winW) / 2
	y := (int32(sh) - winH) / 2

	hwnd, _, err := pCreateWindowExW.Call(
		0, uintptr(unsafe.Pointer(clsName)), uintptr(unsafe.Pointer(title)),
		wsOverlappedWindow|wsClipChildren,
		uintptr(x), uintptr(y), uintptr(winW), uintptr(winH),
		0, 0, hInst, 0,
	)
	if hwnd == 0 {
		return fmt.Errorf("创建窗口失败：%v", err)
	}
	u.hwnd = hwnd
	curNative = u
	u.dpi = dpiOf(hwnd) // 以窗口所在显示器为准（可能是另一块屏）

	// 建窗与建后 DPI 可能不同（多显示器），窗口尺寸按最终生效 DPI 重算。
	if need := natWinW * u.dpi / 96; need != winW {
		winW, winH = need, natWinH*u.dpi/96
		x, y = (int32(sw)-winW)/2, (int32(sh)-winH)/2 // 位置随之重算，避免窗口落到屏幕外
		pSetWindowPos.Call(hwnd, 0, uintptr(x), uintptr(y),
			uintptr(winW), uintptr(winH), swpNoZOrder|swpNoActivate)
	}

	// 暗色标题栏（Win10 1809+），须在窗口创建后调用。
	var dark int32 = 1
	pDwmSetAttr.Call(hwnd, dwmwaUseImmersiveDarkMode, uintptr(unsafe.Pointer(&dark)), 4)

	u.makeGDI()
	u.buildRows()
	u.createControls()
	u.selectTask(0)
	// 开界面即自动体检一次
	if autoProbeOn() {
		u.startAutoProbe()
	}

	pShowWindow.Call(hwnd, swShow)
	pUpdateWindow.Call(hwnd)

	// 仅用一个定时器轮询任务状态，状态未变不重绘
	pSetTimer.Call(hwnd, natTimerID, natPollMs, 0)

	var msg msgT
	for {
		r, _, _ := pGetMessageW.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		if int32(r) <= 0 {
			break
		}
		pTranslateMessage.Call(uintptr(unsafe.Pointer(&msg)))
		pDispatchMessageW.Call(uintptr(unsafe.Pointer(&msg)))
	}

	pKillTimer.Call(hwnd, natTimerID)
	// 关窗即退出，并停止在跑的任务
	ctl.stop()
	guiLog("原生界面窗口已关闭，退出")
	u.freeGDI()
	curNative = nil
	return nil
}

// 资源

func (u *nativeUI) makeGDI() {
	font := func(h int32, weight uint32, face string) uintptr {
		f, _ := syscall.UTF16PtrFromString(face)
		hf, _, _ := pCreateFontW.Call(
			uintptr(-u.px(h)), 0, 0, 0, uintptr(weight), 0, 0, 0,
			1,       // DEFAULT_CHARSET
			0, 0, 5, // CLEARTYPE_QUALITY
			0, uintptr(unsafe.Pointer(f)),
		)
		return hf
	}
	u.fUI = font(13, 400, "Microsoft YaHei UI")
	u.fUIB = font(13, 700, "Microsoft YaHei UI")
	u.fTitle = font(15, 700, "Microsoft YaHei UI")
	u.fSmall = font(11, 400, "Microsoft YaHei UI")
	u.fMono = font(12, 400, "Consolas")
	u.fMonoCJK = font(12, 400, "Microsoft YaHei UI")

	// 量文字宽度的 DC，垫一张同格式位图以免测量偏差（空 DC 会导致行尾切字）。
	u.dcMeasure, _, _ = pCreateCompatDC.Call(0)
	if u.dcMeasure != 0 {
		u.bmpMeasure, _, _ = pCreateCompatBmp.Call(u.dcMeasure, 8, 8)
		if u.bmpMeasure != 0 {
			pSelectObject.Call(u.dcMeasure, u.bmpMeasure)
		}
	}

	u.brBg, _, _ = pCreateSolidBrush.Call(natBg)
	u.brSurface, _, _ = pCreateSolidBrush.Call(natSurface)
	u.brSurf2, _, _ = pCreateSolidBrush.Call(natSurf2)
	u.brLine, _, _ = pCreateSolidBrush.Call(natLine)
	u.brTeal, _, _ = pCreateSolidBrush.Call(natTeal)
	u.brSel, _, _ = pCreateSolidBrush.Call(uintptr(0x00301B12)) // 选中条目底色（略亮）
	u.penLine, _, _ = pCreatePen.Call(0, 1, natLine)
	u.penTeal, _, _ = pCreatePen.Call(0, uintptr(u.px(2)), natTeal)
	u.penTransparent, _, _ = pCreatePen.Call(5, 0, 0) // PS_NULL
}

func (u *nativeUI) freeGDI() {
	// 不在此处关闭 GDI+（进程级资源，重开会导致后续帧退回纯 GDI）。
	for _, h := range []uintptr{u.fUI, u.fUIB, u.fTitle, u.fMono, u.fMonoCJK, u.fSmall,
		u.brBg, u.brSurface, u.brSurf2, u.brLine, u.brTeal, u.brSel,
		u.penLine, u.penTeal, u.penTransparent} {
		if h != 0 {
			pDeleteObject.Call(h)
		}
	}
	if u.bmpMeasure != 0 {
		pDeleteObject.Call(u.bmpMeasure)
		u.bmpMeasure = 0
	}
	if u.dcMeasure != 0 {
		pDeleteDC.Call(u.dcMeasure)
		u.dcMeasure = 0
	}
}

// 文字度量

// measure 量一串文字在指定字体下的宽度（像素）
func (u *nativeUI) measure(s string, font uintptr) int32 {
	if s == "" || u.dcMeasure == 0 || font == 0 {
		return 0
	}
	w16 := syscall.StringToUTF16(s)
	old, _, _ := pSelectObject.Call(u.dcMeasure, font)
	var sz struct{ Cx, Cy int32 }
	pGetTextExtentPoin.Call(u.dcMeasure, uintptr(unsafe.Pointer(&w16[0])),
		uintptr(len(w16)-1), uintptr(unsafe.Pointer(&sz)))
	runtime.KeepAlive(w16) // 保持 Go 堆上的 UTF-16 缓冲区存活，防止调用期间被 GC 回收
	pSelectObject.Call(u.dcMeasure, old)
	return sz.Cx
}

// fontCovers 判断字体是否包含 sample 的字形；GetGlyphIndicesW 以 0xFFFF 标记缺失字形，须逐位检查。
func (u *nativeUI) fontCovers(font uintptr, sample string) bool {
	if font == 0 || u.dcMeasure == 0 {
		return false
	}
	w16 := syscall.StringToUTF16(sample)
	if len(w16) <= 1 {
		return true
	}
	old, _, _ := pSelectObject.Call(u.dcMeasure, font)
	out := make([]uint16, len(w16)-1)
	r, _, _ := pGetGlyphIndicesW.Call(u.dcMeasure, uintptr(unsafe.Pointer(&w16[0])),
		uintptr(len(w16)-1), uintptr(unsafe.Pointer(&out[0])), ggiMarkNonExisting)
	runtime.KeepAlive(w16) // 保持 Go 堆上的 UTF-16 缓冲区存活，防止调用期间被 GC 回收
	pSelectObject.Call(u.dcMeasure, old)
	if r == 0xFFFFFFFF { // GDI_ERROR
		return false
	}
	for _, g := range out {
		if g == 0xFFFF {
			return false
		}
	}
	return true
}

// techFont 挑字体：纯 ASCII 用等宽，含非 ASCII 则换界面字体。
func (u *nativeUI) techFont(s string) uintptr {
	for _, r := range s {
		if r > 0x7F {
			return u.fMonoCJK
		}
	}
	return u.fMono
}

// pickLogFont 挑日志字体：等宽优先，但不认识中文时退回界面字体。
func (u *nativeUI) pickLogFont() uintptr {
	if u.fontCovers(u.fMono, "字") {
		return u.fMono
	}
	return u.fMonoCJK
}

// 布局

// natText 描述一段要绘制文字的内容与位置；绘制与自检共用同一份数据。
type natText struct {
	s     string
	r     rectT
	font  uintptr
	color uintptr
	flags uint32
}

// headerBox 顶部栏中会相互挤占的几块（名字、版本、状态点、状态文字、退出按钮）
type headerBox struct {
	Title  rectT
	Ver    rectT
	Dot    rectT
	Status rectT
	Quit   rectT
}

// headerRects 排顶部栏：先量状态文字宽度，再右对齐贴到按钮左侧。
func (u *nativeUI) headerRects(w int32) headerBox {
	pad := u.px(natPad)
	quitW := u.px(78)
	by := u.px(9)
	quit := rectT{w - pad - quitW, by, w - pad, by + u.px(natCtlH)}

	x := u.px(46) // 徽标右边
	title := rectT{x, u.px(13), x + u.measure(guiWindowTitle, u.fTitle), u.px(34)}
	x = title.Right + u.px(14)
	ver := rectT{x, u.px(13), x + u.measure(version, u.fSmall), u.px(34)}

	dot := u.px(8)
	dy := (u.px(natTopH) - dot) / 2
	right := quit.Left - pad
	// 状态宽度上限；窗口过窄时算得 <=0，则直接不画状态。
	maxW := right - (ver.Right + pad + dot + u.px(8))
	if maxW < 0 {
		maxW = 0
	}
	sw := u.measure(u.statusTxt, u.fSmall)
	if sw > maxW {
		sw = maxW
	}
	status := rectT{right - sw, u.px(10), right, u.px(36)}
	dotR := rectT{status.Left - u.px(8) - dot, dy, status.Left - u.px(8), dy + dot}
	if sw <= 0 {
		// 无空间放状态时同时收起圆点
		dotR = rectT{}
	}
	return headerBox{Title: title, Ver: ver, Dot: dotR, Status: status, Quit: quit}
}

// headerTexts 顶部栏要画的文字
func (u *nativeUI) headerTexts(w int32) []natText {
	hb := u.headerRects(w)
	out := []natText{
		{guiWindowTitle, hb.Title, u.fTitle, natInk, dtLeft | dtVCenter | dtSingleLine | dtEndEllipsis},
		{version, hb.Ver, u.fSmall, natMuted, dtLeft | dtVCenter | dtSingleLine | dtEndEllipsis},
	}
	if u.statusTxt != "" && hb.Status.Right > hb.Status.Left {
		out = append(out, natText{u.statusTxt, hb.Status, u.fSmall, natInk2,
			dtLeft | dtVCenter | dtSingleLine | dtEndEllipsis})
	}
	return out
}

func (u *nativeUI) buildRows() {
	u.rows = u.rows[:0]
	var y int32
	last := ""
	for i, t := range guiTasks {
		if t.Group != last {
			u.rows = append(u.rows, natRow{isGroup: true, group: t.Group, taskIdx: -1, y: y, h: u.px(natGroupH)})
			y += u.px(natGroupH)
			last = t.Group
		}
		u.rows = append(u.rows, natRow{isGroup: false, taskIdx: i, y: y, h: u.px(natRowH)})
		y += u.px(natRowH)
	}
	u.rowsH = y
}

func (u *nativeUI) listRect(h int32) rectT {
	return rectT{0, u.px(natTopH), u.px(natListW), h}
}

// rowVisible 判断条目是否落在列表可视区内。
func (u *nativeUI) rowVisible(row natRow, lr rectT) bool {
	ry := row.y - u.listTop + u.px(natTopH)
	return ry+row.h >= u.px(natTopH) && ry <= lr.Bottom
}

// listTexts 返回左侧列表要画的文字（绘制与自检共用）。
func (u *nativeUI) listTexts(h int32) []natText {
	lr := u.listRect(h)
	if lr.Bottom <= lr.Top || u.rowsH <= 0 {
		return nil
	}
	var out []natText
	for _, row := range u.rows {
		if !u.rowVisible(row, lr) {
			continue
		}
		ry := row.y - u.listTop + u.px(natTopH)
		if row.isGroup {
			// 分组标题缩进一个图标位，颜色随模块
			out = append(out, natText{strings.ToUpper(row.group),
				rectT{u.px(20) + u.px(natIconSize) + u.px(9), ry + u.px(6), lr.Right - u.px(12), ry + row.h},
				u.fSmall, uintptr(groupColor(row.group)), dtLeft | dtVCenter | dtSingleLine | dtEndEllipsis})
			continue
		}
		if row.taskIdx < 0 || row.taskIdx >= len(guiTasks) {
			continue
		}
		t := guiTasks[row.taskIdx]
		out = append(out,
			natText{t.Name, rectT{u.px(24), ry + u.px(7), lr.Right - u.px(18), ry + u.px(27)},
				u.fUIB, natInk, dtLeft | dtVCenter | dtSingleLine | dtEndEllipsis},
			natText{t.Desc, rectT{u.px(24), ry + u.px(27), lr.Right - u.px(18), ry + row.h - u.px(6)},
				u.fSmall, natMuted, dtLeft | dtVCenter | dtSingleLine | dtEndEllipsis})
	}
	return out
}

// natGroupIcon 一个分组标题旁边的小图标
type natGroupIcon struct {
	kind  int
	r     rectT
	color uint32
}

// listGroupIcons 返回分组图标位置，算法与 listTexts 标题一致。
func (u *nativeUI) listGroupIcons(h int32) []natGroupIcon {
	lr := u.listRect(h)
	if lr.Bottom <= lr.Top || u.rowsH <= 0 {
		return nil
	}
	d := u.px(natIconSize)
	var out []natGroupIcon
	for _, row := range u.rows {
		if !row.isGroup || !u.rowVisible(row, lr) {
			continue
		}
		st := groupStyle(row.group)
		if st == nil {
			continue
		}
		ry := row.y - u.listTop + u.px(natTopH)
		// 标题文字从 ry+px(6) 起、高 20，图标在其中居中
		iy := ry + u.px(6) + (u.px(20)-d)/2
		out = append(out, natGroupIcon{
			kind:  st.Icon,
			r:     rectT{u.px(20), iy, u.px(20) + d, iy + d},
			color: st.Color,
		})
	}
	return out
}

// drawGroupIcon 画分组图标。都只用圆 / 线 / 圆角矩形拼，笔画按 DPI 缩放。
func (u *nativeUI) drawGroupIcon(hdc uintptr, ic natGroupIcon) {
	r := ic.r
	w := u.px(2) // 笔画粗细
	if w < 1 {
		w = 1
	}
	d := r.Right - r.Left
	cx := r.Left + d/2
	cy := r.Top + d/2
	switch ic.kind {
	case natIconSearch: // 放大镜：一个圈 + 一根斜把手
		ring := rectT{r.Left, r.Top, r.Left + d*3/4, r.Top + d*3/4}
		strokeCircle(hdc, ring, ic.color, w)
		line(hdc, ring.Right-w, ring.Bottom-w, r.Right, r.Bottom, ic.color, w)

	case natIconDoor: // 门：一个竖着的圆角框 + 右边一个小把手
		strokeRound(hdc, rectT{r.Left + d/6, r.Top, r.Right - d/6, r.Bottom}, ic.color, w, u.px(2))
		fillCircle(hdc, rectT{r.Right - d/3, cy - u.px(1), r.Right - d/3 + u.px(2), cy + u.px(1)}, ic.color)

	case natIconEnter: // 进门：一根横线 + 右边一个箭头，左边一竖（门框）
		line(hdc, r.Left, r.Top, r.Left, r.Bottom, ic.color, w)
		line(hdc, r.Left+d/4, cy, r.Right, cy, ic.color, w)
		line(hdc, r.Right-d/4, cy-d/4, r.Right, cy, ic.color, w)
		line(hdc, r.Right-d/4, cy+d/4, r.Right, cy, ic.color, w)

	case natIconAlert: // 圆圈里一个感叹号
		strokeCircle(hdc, r, ic.color, w)
		line(hdc, cx, r.Top+d/5, cx, cy+d/8, ic.color, w)
		fillCircle(hdc, rectT{cx - u.px(1), r.Bottom - d/4 - u.px(1), cx + u.px(1), r.Bottom - d/4 + u.px(1)}, ic.color)

	case natIconTools: // 两根滑杆，各带一个滑块
		y1 := r.Top + d/3
		y2 := r.Bottom - d/3
		line(hdc, r.Left, y1, r.Right, y1, ic.color, w)
		line(hdc, r.Left, y2, r.Right, y2, ic.color, w)
		fillCircle(hdc, rectT{r.Left + d/4 - u.px(2), y1 - u.px(2), r.Left + d/4 + u.px(2), y1 + u.px(2)}, ic.color)
		fillCircle(hdc, rectT{r.Right - d/4 - u.px(2), y2 - u.px(2), r.Right - d/4 + u.px(2), y2 + u.px(2)}, ic.color)

	case natIconRelay: // 中继：两个点，中间一条线
		line(hdc, r.Left+d/4, cy, r.Right-d/4, cy, ic.color, w)
		fillCircle(hdc, rectT{r.Left, cy - d/4, r.Left + d/2, cy + d/4}, ic.color)
		fillCircle(hdc, rectT{r.Right - d/2, cy - d/4, r.Right, cy + d/4}, ic.color)
	}
}

func (u *nativeUI) clientH() int32 {
	var r rectT
	pGetClientRect.Call(u.hwnd, uintptr(unsafe.Pointer(&r)))
	return r.Bottom
}

func (u *nativeUI) clientW() int32 {
	var r rectT
	pGetClientRect.Call(u.hwnd, uintptr(unsafe.Pointer(&r)))
	return r.Right
}

// contentRects 计算右侧三块（头部、地址卡片、日志）的矩形；尺寸作为参数使布局成为纯函数。
func (u *nativeUI) contentRects(w, h int32) (head, addr, log rectT, logLabelY int32) {
	x0 := u.px(natListW) + u.px(natPad)
	x1 := w - u.px(natPad)
	y := u.px(natTopH) + u.px(natPad)

	// 头部：标题(24) + 说明(20) + 命令(22) + 字段区 + 按钮行(40)
	headH := u.px(24+20+22+8) + int32(len(u.fieldIdx()))*u.px(natFieldH) + u.px(44)
	head = rectT{x0, y, x1, y + headH}
	y = head.Bottom + u.px(natPad)

	if u.hasAddress() {
		addr = rectT{x0, y, x1, y + u.addrCardHeight(w)}
		y = addr.Bottom + u.px(natPad)
	}

	logLabelY = y
	y += u.px(26)
	logH := h - y - u.px(natPad)
	if logH < u.px(natMinLogH) {
		logH = u.px(natMinLogH)
	}
	log = rectT{x0, y, x1, y + logH}
	return
}

// contentTexts 返回右侧内容区要画的文字（按绘制顺序），供绘制与自检共用。
func (u *nativeUI) contentTexts(w, h int32) []natText {
	head, addr, log, logLabelY := u.contentRects(w, h)
	var out []natText

	if u.sel >= 0 && u.sel < len(guiTasks) {
		t := guiTasks[u.sel]
		out = append(out,
			natText{t.Name, rectT{head.Left, head.Top, head.Right, head.Top + u.px(24)},
				u.fTitle, natInk, dtLeft | dtVCenter | dtSingleLine | dtEndEllipsis},
			natText{t.Desc, rectT{head.Left, head.Top + u.px(24), head.Right, head.Top + u.px(44)},
				u.fSmall, natInk2, dtLeft | dtVCenter | dtSingleLine | dtEndEllipsis},
		)
		cmd := u.command
		if cmd == "" {
			cmd = "执行后在此显示等价命令"
		}
		out = append(out, natText{cmd, rectT{head.Left, head.Top + u.px(46), head.Right, head.Top + u.px(66)},
			u.techFont(cmd), natMuted, dtLeft | dtVCenter | dtSingleLine | dtEndEllipsis})

		for i, fc := range u.fields {
			f := t.Fields[fc.index]
			if fc.isBool {
				continue // 勾选项的文字画在控件自己身上
			}
			ly := head.Top + u.px(24+20+22+8) + int32(i)*u.px(natFieldH)
			lbl := f.Label
			if f.Required {
				lbl += " *"
			}
			out = append(out, natText{lbl, rectT{head.Left, ly, head.Right, ly + u.px(20)},
				u.fUI, natInk2, dtLeft | dtVCenter | dtSingleLine | dtEndEllipsis})
		}
	}

	if u.hasAddress() {
		y := addr.Top + u.px(10)
		l, r := addr.Left+u.px(natPad), addr.Right-u.px(natPad)
		if u.addr != "" {
			font := u.valueFont(u.addr, r-l)
			l1, l2 := u.splitTwoLines(u.addr, r-l, font)
			out = append(out, natText{addrCardTitle(u.addr), rectT{l, y, r, y + u.px(18)},
				u.fSmall, natMuted, dtLeft | dtVCenter | dtSingleLine | dtEndEllipsis})
			vr := rectT{l, y + u.px(18), r, y + u.px(18) + u.px(natAddrLineH)}
			out = append(out, natText{l1, vr, font, natInk,
				dtLeft | dtVCenter | dtSingleLine | dtEndEllipsis})
			if l2 != "" {
				out = append(out, natText{l2, rectT{l, vr.Bottom, r, vr.Bottom + u.px(natAddrLineH)},
					font, natInk, dtLeft | dtVCenter | dtSingleLine | dtEndEllipsis})
			}
		} else {
			out = append(out, natText{"房间码 " + u.room, rectT{l, y, r, y + u.px(26)},
				u.fTitle, natInk, dtLeft | dtVCenter | dtSingleLine | dtEndEllipsis})
		}
	}

	out = append(out, natText{"日志", rectT{log.Left, logLabelY, log.Left + u.px(60), logLabelY + u.px(24)},
		u.fSmall, natMuted, dtLeft | dtVCenter | dtSingleLine})
	if !u.running && !u.done && len(u.logText) == 0 {
		out = append(out, natText{"在左侧选择操作后点击「开始执行」",
			rectT{log.Left + u.px(48), logLabelY, log.Right - u.px(160), logLabelY + u.px(24)},
			u.fSmall, natMuted, dtLeft | dtVCenter | dtSingleLine | dtEndEllipsis})
	}
	return out
}

// valueFont 为长值挑字体：等宽放不下则换更紧凑的界面字体。
func (u *nativeUI) valueFont(s string, avail int32) uintptr {
	f := u.techFont(s)
	if u.measure(s, f) <= avail {
		return f
	}
	return u.fSmall
}

// addrValueWidth 地址卡片里那行值能占多宽（卡片去掉左右内边距）
func (u *nativeUI) addrValueWidth(w int32) int32 {
	x0 := u.px(natListW) + u.px(natPad) // 卡片左边界
	x1 := w - u.px(natPad)              // 卡片右边界
	return (x1 - u.px(natPad)) - (x0 + u.px(natPad))
}

// addrCardHeight 按值是否需要折两行返回地址卡片高度。
func (u *nativeUI) addrCardHeight(w int32) int32 {
	if u.addr != "" {
		if _, l2 := u.splitTwoLines(u.addr, u.addrValueWidth(w), u.valueFont(u.addr, u.addrValueWidth(w))); l2 != "" {
			return u.px(natAddrH2)
		}
	}
	return u.px(natAddrH)
}

// splitTwoLines 一行放不下时折成两行，优先在中点附近的 & ? / 空格 , 处断开。
func (u *nativeUI) splitTwoLines(s string, avail int32, font uintptr) (string, string) {
	if s == "" || avail <= 0 || u.measure(s, font) <= avail {
		return s, ""
	}
	// 先试自然断点：取离中点最近且两半都放得下的位置
	mid := len(s) / 2
	best, bestGap := -1, 0
	for i := 1; i < len(s)-1; i++ {
		switch s[i] {
		case '&', '?', '/', ' ', ',':
		default:
			continue
		}
		if u.measure(s[:i], font) > avail || u.measure(s[i:], font) > avail {
			continue
		}
		gap := i - mid
		if gap < 0 {
			gap = -gap
		}
		if best < 0 || gap < bestGap {
			best, bestGap = i, gap
		}
	}
	if best > 0 {
		return s[:best], s[best:]
	}
	// 无自然断点时按宽度断，断点落在字符边界上（不劈开多字节字符）。
	cut := 0
	for i := 1; i < len(s); i++ {
		if s[i]&0xC0 == 0x80 {
			continue // 多字节字符的续字节，不可在此断开
		}
		if u.measure(s[:i], font) > avail {
			break
		}
		cut = i
	}
	if cut > 0 {
		return s[:cut], s[cut:]
	}
	return s, ""
}

// fieldIdx 当前选中任务的字段下标（没有选中就是空）
func (u *nativeUI) fieldIdx() []int {
	if u.sel < 0 || u.sel >= len(guiTasks) {
		return nil
	}
	n := len(guiTasks[u.sel].Fields)
	out := make([]int, n)
	for i := range out {
		out[i] = i
	}
	return out
}

func (u *nativeUI) hasAddress() bool {
	return u.addr != "" || u.room != "" || u.join != ""
}

// layout 按当前尺寸摆放所有子控件
func (u *nativeUI) layout() {
	if u.hwnd == 0 {
		return
	}
	w, h := u.clientW(), u.clientH()
	head, addr, log, _ := u.contentRects(w, h)
	x0 := head.Left

	// 字段：标签画在字段矩形上方（WM_PAINT 里），输入框摆在下方
	fy := head.Top + u.px(24+20+22+8)
	for i := range u.fields {
		fc := &u.fields[i]
		fc.rect = rectT{x0, fy, head.Right, fy + u.px(natFieldH)}
		cy := fy + u.px(22)
		ctrlW := head.Right - x0
		if fc.isBool {
			// 按文字宽度给勾选控件留足够宽度，避免文字被切断。
			ctrlW = u.boolControlWidth(u.boolLabel(fc.index), ctrlW)
		}
		pSetWindowPos.Call(fc.hwnd, 0, uintptr(fc.rect.Left), uintptr(cy),
			uintptr(ctrlW), uintptr(u.px(natCtlH)), swpNoZOrder)
		fy += u.px(natFieldH)
	}

	// 按钮行
	by := head.Bottom - u.px(40)
	bx := x0
	bx = u.placeBtn(u.hRun, bx, by, 92)
	bx = u.placeBtn(u.hStop, bx+u.px(8), by, 92)

	// 地址卡片里的复制按钮
	if u.hasAddress() {
		ax := addr.Left + u.px(natPad)
		ay := addr.Bottom - u.px(natCtlH) - u.px(12)
		u.showCtl(u.hCopyAddr, true)
		ax = u.placeBtn(u.hCopyAddr, ax, ay, 108)
		ax = u.placeBtn(u.hCopyJoin, ax+u.px(8), ay, 150)
		u.showCtl(u.hCopyJoin, true)
	} else {
		// 无卡片时隐藏复制按钮，否则控件会缩到左上角。
		u.showCtl(u.hCopyAddr, false)
		u.showCtl(u.hCopyJoin, false)
	}

	// 日志区右上角的两个小按钮
	lx := log.Right - u.px(150)
	ly := log.Top - u.px(26) - u.px(0)
	lx = u.placeBtn(u.hClear, lx, ly, 68)
	u.placeBtn(u.hCopyLog, lx+u.px(8), ly, 74)

	// 日志控件
	pSetWindowPos.Call(u.logHwnd, 0, uintptr(log.Left), uintptr(log.Top),
		uintptr(log.Right-log.Left), uintptr(log.Bottom-log.Top), swpNoZOrder)

	// 右上角按钮位置与顶部栏文字一起计算
	q := u.headerRects(w).Quit
	u.placeBtn(u.hQuit, q.Left, q.Top, 78)

	u.lastW, u.lastH = w, h
}

// boolLabel 取第 i 个勾选项的文字
func (u *nativeUI) boolLabel(i int) string {
	if u.sel < 0 || u.sel >= len(guiTasks) {
		return ""
	}
	fs := guiTasks[u.sel].Fields
	if i < 0 || i >= len(fs) {
		return ""
	}
	return fs[i].Label
}

// boolControlWidth 返回勾选项文字刚好放得下且不超过可用宽度的宽度。
func (u *nativeUI) boolControlWidth(label string, avail int32) int32 {
	w := u.measure(label, u.fSmall) + u.px(34) // 方框 + 文字左右边距
	if w > avail {
		w = avail
	}
	if w < u.px(120) {
		w = u.px(120)
	}
	return w
}

func (u *nativeUI) placeBtn(hwnd uintptr, x, y, w int32) int32 {
	if hwnd == 0 {
		return x + u.px(w)
	}
	pSetWindowPos.Call(hwnd, 0, uintptr(x), uintptr(y), uintptr(u.px(w)), uintptr(u.px(natCtlH)), swpNoZOrder)
	return x + u.px(w)
}

// showCtl 显示/隐藏子控件；必须带 SWP_NOSIZE | SWP_NOMOVE，否则控件会被缩成一点。
func (u *nativeUI) showCtl(hwnd uintptr, on bool) {
	if hwnd == 0 {
		return
	}
	const swpNoSize, swpNoMove = 0x0001, 0x0002
	flag := uintptr(swpNoSize | swpNoMove | swpNoZOrder)
	if on {
		flag |= 0x0040 // SWP_SHOWWINDOW
	} else {
		flag |= 0x0080 // SWP_HIDEWINDOW
	}
	pSetWindowPos.Call(hwnd, 0, 0, 0, 0, 0, flag)
}

// 控件

func (u *nativeUI) mkChild(class, text string, style uint32, id uintptr) uintptr {
	cls, _ := syscall.UTF16PtrFromString(class)
	txt, _ := syscall.UTF16PtrFromString(text)
	hInst, _, _ := pGetModuleHandleW.Call(0)
	h, _, _ := pCreateWindowExW.Call(
		0, uintptr(unsafe.Pointer(cls)), uintptr(unsafe.Pointer(txt)),
		uintptr(style), 0, 0, 10, 10,
		u.hwnd, id, hInst, 0,
	)
	if h != 0 {
		pSendMessageW.Call(h, wmSetFont, u.fUI, 1)
	}
	return h
}

// newLogControl 优先用系统自带 RichEdit（可上色），退不到则用普通 EDIT。
func (u *nativeUI) newLogControl() {
	style := uint32(wsChild | wsVisible | wsVScroll | wsBorder |
		esMultiline | esReadOnly | esAutoVScroll)
	tried := []struct {
		dll, class string
	}{
		{"MsftEdit.dll", "RichEdit50W"},
		{"Riched20.dll", "RichEdit20W"},
	}
	for _, t := range tried {
		d, _ := syscall.UTF16PtrFromString(t.dll)
		if r, _, _ := pLoadLibraryW.Call(uintptr(unsafe.Pointer(d))); r == 0 {
			continue
		}
		h := u.mkChild(t.class, "", style, 0)
		if h == 0 {
			continue
		}
		u.logHwnd, u.richEdit = h, true
		pSendMessageW.Call(h, wmSetFont, u.pickLogFont(), 1)
		pSendMessageW.Call(h, emSetBkndColor, 0, uintptr(0x000D0704)) // #04070d
		return
	}
	// 退回普通 EDIT：没有颜色，但滚动、选中、复制都还在
	u.logHwnd = u.mkChild("EDIT", "", style, 0)
	if u.logHwnd != 0 {
		pSendMessageW.Call(u.logHwnd, wmSetFont, u.pickLogFont(), 1)
	}
}

func (u *nativeUI) createControls() {
	u.newLogControl()
	btn := uint32(wsChild | wsVisible | wsTabStop | bsOwnerDraw)
	u.hRun = u.mkChild("BUTTON", "开始执行", btn, natIDRun)
	u.hStop = u.mkChild("BUTTON", "停止", btn, natIDStop)
	u.hQuit = u.mkChild("BUTTON", "退出", btn, natIDQuit)
	u.hClear = u.mkChild("BUTTON", "清空", btn, natIDClearLog)
	u.hCopyLog = u.mkChild("BUTTON", "复制", btn, natIDCopyLog)
	u.hCopyAddr = u.mkChild("BUTTON", "复制地址", btn, natIDCopyAddr)
	u.hCopyJoin = u.mkChild("BUTTON", "复制命令", btn, natIDCopyJoin)
}

// rebuildFields 选中任务变化时重建字段控件
func (u *nativeUI) rebuildFields() {
	for _, f := range u.fields {
		pDestroyWindow.Call(f.hwnd)
	}
	u.fields = nil
	if u.sel < 0 || u.sel >= len(guiTasks) {
		return
	}
	for i, f := range guiTasks[u.sel].Fields {
		id := uintptr(natIDField0 + i)
		key := guiTasks[u.sel].Key
		if f.Kind == "check" {
			h := u.mkChild("BUTTON", f.Label, wsChild|wsVisible|wsTabStop|bsCheckbox, id)
			// 显式指定与标签相同的字体，避免一屏混用两种字形。
			pSendMessageW.Call(h, wmSetFont, u.fSmall, 1)
			// 沿用上次的勾选状态（无记录则用字段默认值）
			if fieldChecked(key, f) {
				pSendMessageW.Call(h, bmSetCheck, 1, 0)
			}
			u.fields = append(u.fields, natFieldCtl{index: i, id: id, hwnd: h, isBool: true})
			continue
		}
		// 输入框预填上次的值（无则用字段默认值）
		h := u.mkChild("EDIT", fieldDefault(key, f), wsChild|wsVisible|wsTabStop|wsBorder|0x0080 /* ES_AUTOHSCROLL */, id)
		pSendMessageW.Call(h, wmSetFont, u.fUI, 1)
		u.fields = append(u.fields, natFieldCtl{index: i, id: id, hwnd: h})
	}
}

// rememberCurrentFields 把当前任务上填的值记下来（下次打开预填）。
func (u *nativeUI) rememberCurrentFields() {
	if u.sel < 0 || u.sel >= len(guiTasks) || len(u.fields) == 0 {
		return
	}
	rememberFieldValues(guiTasks[u.sel].Key, u.inputs())
}

func (u *nativeUI) selectTask(i int) {
	if i < 0 || i >= len(guiTasks) {
		return
	}
	if u.sel == i {
		return
	}
	// 换页前记下本页填入的值
	u.rememberCurrentFields()
	u.sel = i
	u.rebuildFields()
	u.layout()
	u.repaint()
}

// inputs 从控件里读出当前输入值
func (u *nativeUI) inputs() map[string]string {
	in := map[string]string{}
	if u.sel < 0 || u.sel >= len(guiTasks) {
		return in
	}
	for _, fc := range u.fields {
		f := guiTasks[u.sel].Fields[fc.index]
		if fc.isBool {
			r, _, _ := pSendMessageW.Call(fc.hwnd, bmGetCheck, 0, 0) // BM_GETCHECK
			// 勾上写 "1"，未勾也须写空串，否则取消勾选不会被保存。
			if r == 1 {
				in[f.ID] = "1"
				continue
			}
			in[f.ID] = ""
			continue
		}
		buf := make([]uint16, 4096)
		pGetWindowTextW.Call(fc.hwnd, uintptr(unsafe.Pointer(&buf[0])), 4096)
		in[f.ID] = syscall.UTF16ToString(buf)
	}
	return in
}

// 绘制：具体画法（圆角/渐变/半透明）在 gdiplus.go，本层只管画在哪儿。

func (u *nativeUI) repaint() {
	pInvalidateRect.Call(u.hwnd, 0, 0)
}

func (u *nativeUI) text(hdc uintptr, s string, r rectT, font, color uintptr, flags uint32) {
	if s == "" {
		return
	}
	// 交给 GDI 前先冲掉 GDI+ 批次，避免绘制顺序错乱。
	gpSync()
	old, _, _ := pSelectObject.Call(hdc, font)
	pSetBkMode.Call(hdc, transparent)
	pSetTextColor.Call(hdc, color)
	p, err := syscall.UTF16PtrFromString(s)
	if err != nil {
		pSelectObject.Call(hdc, old)
		return
	}
	pDrawTextW.Call(hdc, uintptr(unsafe.Pointer(p)), ^uintptr(0),
		uintptr(unsafe.Pointer(&r)), uintptr(flags))
	// p 是 Go 堆上的 UTF-16 缓冲区，LazyProc.Call 的转义不可靠，须 KeepAlive 防止调用期间被 GC 回收。
	runtime.KeepAlive(p)
	pSelectObject.Call(hdc, old)
}

func (u *nativeUI) paint() {
	var ps struct {
		Hdc         uintptr
		Erase       int32
		RcPaint     rectT
		Restore     int32
		IncUpdate   int32
		RgbReserved [32]byte
	}
	hdc, _, _ := pBeginPaint.Call(u.hwnd, uintptr(unsafe.Pointer(&ps)))
	if hdc == 0 {
		return
	}
	defer pEndPaint.Call(u.hwnd, uintptr(unsafe.Pointer(&ps)))
	u.paintTo(hdc, u.clientW(), u.clientH())
}

// paintTo 把这一帧画到任意 DC；截图对比、DPI 渲染与 WM_PRINTCLIENT 都走此入口。
func (u *nativeUI) paintTo(hdc uintptr, w, h int32) {
	if hdc == 0 || w <= 0 || h <= 0 {
		return
	}

	// 双缓冲：整屏先画到内存位图再一次性贴上
	mem, _, _ := pCreateCompatDC.Call(hdc)
	bmp, _, _ := pCreateCompatBmp.Call(hdc, uintptr(w), uintptr(h))
	oldBmp, _, _ := pSelectObject.Call(mem, bmp)
	// 在内存 DC 上开 GDI+ 画布用于抗锯齿；不可用时退回纯 GDI。
	gpCur = gpBegin(mem)
	defer func() {
		gpEnd(gpCur)
		gpCur = 0
		pSelectObject.Call(mem, oldBmp)
		pDeleteObject.Call(bmp)
		pDeleteDC.Call(mem)
	}()

	// 底色：淡竖向渐变
	fillRectGrad(mem, rectT{0, 0, w, h}, natBgTop, natBg)

	// 顶部栏
	fillRectGrad(mem, rectT{0, 0, w, u.px(natTopH)}, natHeadHi, natSurface)
	hLine(mem, 0, w, u.px(natTopH)-1, natLine, 1)
	// 徽标：青→蓝的渐变小方块
	fillRoundGrad(mem, rectT{u.px(20), u.px(14), u.px(20) + u.px(18), u.px(14) + u.px(18)}, natTeal, natCyan, u.px(5))
	hb := u.headerRects(w)
	for _, tx := range u.headerTexts(w) {
		u.text(mem, tx.s, tx.r, tx.font, tx.color, tx.flags)
	}

	// 状态点（运行/出错时外加一圈淡环）
	var dotColor uint32 = natMuted
	switch {
	case u.errMsg != "":
		dotColor = natRose
	case u.running:
		dotColor = natTeal
	}
	if u.running || u.errMsg != "" {
		pad := (hb.Dot.Right - hb.Dot.Left) / 2
		if pad > 0 {
			strokeCircle(mem, rectT{hb.Dot.Left - pad, hb.Dot.Top - pad, hb.Dot.Right + pad, hb.Dot.Bottom + pad},
				argbA(dotColor, 0x66), 1)
		}
	}
	if hb.Dot.Right > hb.Dot.Left {
		fillCircle(mem, hb.Dot, dotColor)
	}

	// 左侧列表
	lr := u.listRect(h)
	fillRectGrad(mem, lr, natSurface, natBgLite)
	hLine(mem, lr.Right-1, lr.Right, lr.Top, natLine, 1)
	if lr.Bottom-lr.Top > 0 && u.rowsH > 0 {
		// 先铺选中/悬停底色再统一画字；用逐行判断 y 代替裁剪（行数很少）。
		for _, row := range u.rows {
			if row.isGroup || !u.rowVisible(row, lr) {
				continue
			}
			ry := row.y - u.listTop + u.px(natTopH)
			rr := rectT{u.px(12), ry + u.px(2), lr.Right - u.px(14), ry + row.h - u.px(4)}
			switch {
			case row.taskIdx == u.sel:
				fillRoundGrad(mem, rr, natSelTop, natSelBot, u.px(8))
				// 强调条与淡底色随任务所属模块着色
				accent := groupColor(guiTasks[row.taskIdx].Group)
				fillRoundA(mem, rr, accent, 0x12, u.px(8))
				strokeRound(mem, rectT{rr.Left + u.px(3), rr.Top + u.px(9), rr.Left + u.px(3) + u.px(3), rr.Bottom - u.px(9)}, accent, u.px(3), u.px(1))
			case row.taskIdx == u.hover:
				fillRound(mem, rr, natSurf2, u.px(8))
			}
		}
		for _, ic := range u.listGroupIcons(h) {
			u.drawGroupIcon(mem, ic)
		}
		for _, tx := range u.listTexts(h) {
			u.text(mem, tx.s, tx.r, tx.font, tx.color, tx.flags)
		}
		// 列表里的滚动条（自己画一条细的，够用）
		if u.rowsH > lr.Bottom-lr.Top-u.px(natTopH) {
			trackH := lr.Bottom - lr.Top - u.px(natTopH)
			visH := trackH
			thumbH := visH * trackH / u.rowsH
			if thumbH < u.px(24) {
				thumbH = u.px(24)
			}
			maxTop := u.rowsH - trackH
			ty := lr.Top
			if maxTop > 0 {
				ty += (trackH - thumbH) * u.listTop / maxTop
			}
			fillRound(mem, rectT{lr.Right - u.px(6), ty, lr.Right - u.px(3), ty + thumbH}, natMuted, u.px(2))
		}
	}

	// 右侧内容
	_, addr, _, _ := u.contentRects(w, h)
	// 地址卡片先铺底（影子 + 渐变 + 高光边）再画字
	if u.hasAddress() {
		shadowRound(mem, addr, u.px(10), 0x88)
		fillRoundCard(mem, addr, natSurf2, natBgLite, natLine, u.px(10))
	}
	// 所有文字按 contentTexts 的数据绘制（自检共用同一份数据）。
	for _, tx := range u.contentTexts(w, h) {
		u.text(mem, tx.s, tx.r, tx.font, tx.color, tx.flags)
	}

	pBitBlt.Call(hdc, 0, 0, uintptr(w), uintptr(h), mem, 0, 0, 0x00CC0020 /* SRCCOPY */)
}

// addrCardTitle 按卡片内容返回抬头文案（中继链接与游戏地址措辞不同）。
func addrCardTitle(v string) string {
	s := strings.TrimSpace(v)
	if strings.HasPrefix(s, "mclbx://") || strings.HasPrefix(s, "turn:") ||
		strings.HasPrefix(s, "turns:") || strings.HasPrefix(s, "tcp:") {
		return "发送给玩家，填入「中继服务器」"
	}
	return "玩家在游戏中连接的地址"
}

// minMaxInfoT（WM_GETMINMAXINFO 的 lParam）已在 winwebview.go 声明，两种界面共用。

// drawItemT 对应 Windows 的 DRAWITEMSTRUCT，仅 BS_OWNERDRAW 控件会收到
type drawItemT struct {
	CtlType    uint32
	CtlID      uint32
	ItemID     uint32
	ItemAction uint32
	ItemState  uint32
	_          uint32 // x64 下后面是指针，要补齐到 8 字节对齐
	HwndItem   uintptr
	Hdc        uintptr
	RcItem     rectT
	ItemData   uintptr
}

const (
	odsSelected = 0x0001
	odsDisabled = 0x0004
	nullBrush   = 5 // GetStockObject(NULL_BRUSH)
)

// drawButton 自绘按钮，以适配暗色界面并区分主按钮与禁用态。
func (u *nativeUI) drawButton(d *drawItemT) {
	r := d.RcItem
	primary := d.CtlID == natIDRun
	disabled := d.ItemState&odsDisabled != 0

	// 按钮有自己的 DC，单独开一块 GDI+ 画布
	g := gpBegin(d.Hdc)
	oldGP := gpCur
	gpCur = g
	defer func() {
		gpEnd(g)
		gpCur = oldGP
	}()

	ink := uintptr(natInk2)
	switch {
	case disabled:
		fillRound(d.Hdc, r, natSurface, u.px(7))
		strokeRound(d.Hdc, r, natLine, 1, u.px(7))
		ink = natMuted
	case primary:
		// 主按钮：青→蓝竖渐变 + 内侧亮边
		fillRoundGrad(d.Hdc, r, natTeal, natCyan, u.px(7))
		inset := rectT{r.Left + u.px(1), r.Top + u.px(1), r.Right - u.px(1), r.Bottom - u.px(1)}
		strokeRound(d.Hdc, inset, argbA(0xFFFFFF, 0x33), 1, u.px(6))
		ink = 0x001F2304 // 深墨色字配青底，对比更足
	default:
		fillRoundGrad(d.Hdc, r, natSurf2, natSurface, u.px(7))
		strokeRound(d.Hdc, r, natLine, 1, u.px(7))
	}
	if d.ItemState&odsSelected != 0 && !disabled {
		fillRoundA(d.Hdc, r, 0xFFFFFF, 0x14, u.px(7))
	}

	buf := make([]uint16, 128)
	pGetWindowTextW.Call(d.HwndItem, uintptr(unsafe.Pointer(&buf[0])), 128)
	label := syscall.UTF16ToString(buf)
	font := u.fUI
	if primary {
		font = u.fUIB
	}
	tr := r
	tr.Top++
	// 按钮文字加省略号，避免字体或 DPI 变化时文字被切断。
	u.text(d.Hdc, label, tr, font, ink, dtCenter|dtVCenter|dtSingleLine|dtNoPrefix|dtEndEllipsis)
}

// 日志

// lineColor 与网页版共用同一套判断规则。
func lineColor(s string) uintptr {
	has := func(subs ...string) bool {
		for _, sub := range subs {
			if strings.Contains(s, sub) {
				return true
			}
		}
		return false
	}
	switch {
	case has(" ✓ "):
		// 结论里的通过项
		return natOkInk
	case has(" ✗ "):
		return natBadInk
	case has(" ! ", "原因：", "处理：", "建议："):
		return natWarnInk
	case has("错误", "失败", "不可用", "拒绝", "超时", "异常"):
		return natBadInk
	case has("成功", "已建立", "已就绪", "已连接", "完成"):
		return natOkInk
	case has("警告", "注意", "受限"):
		return natWarnInk
	}
	return 0x00D9D0C5 // #c5d0d9 普通行
}

// utf16z 返回以 0 结尾的 UTF-16 切片供 -W API 使用：永不返回空切片，并先剥离 NUL。
func utf16z(s string) []uint16 {
	if strings.IndexByte(s, 0) >= 0 {
		s = strings.ReplaceAll(s, "\x00", "")
	}
	b, err := syscall.UTF16FromString(s)
	if err != nil || len(b) == 0 {
		return []uint16{0}
	}
	return b
}

// appendLine 往日志控件尾部追加一行（RichEdit 下顺带上色）
func (u *nativeUI) appendLine(s string) {
	h := u.logHwnd
	if h == 0 {
		return
	}
	// 只读控件会忽略 EM_REPLACESEL，所以先解开、写完再关上
	pSendMessageW.Call(h, emSetReadOnly, 0, 0)
	pSendMessageW.Call(h, emSetSel, ^uintptr(0), ^uintptr(0)) // EM_SETSEL(-1,-1) = 移到末尾
	start, _, _ := pSendMessageW.Call(h, 0x000E, 0, 0)        // WM_GETTEXTLENGTH
	txt := utf16z(s + "\r\n")
	pSendMessageW.Call(h, emReplaceSel, 0, uintptr(unsafe.Pointer(&txt[0])))
	runtime.KeepAlive(txt) // 保持 Go 堆上的 UTF-16 缓冲区存活，防止调用期间被 GC 回收
	if u.richEdit {
		end := start + uintptr(len(txt)-1)
		pSendMessageW.Call(h, emSetSel, start, end)
		cf := charFormatW{
			CbSize:      uint32(unsafe.Sizeof(charFormatW{})),
			DwMask:      cfmColor,
			CrTextColor: uint32(lineColor(s)),
		}
		// 上色失败是静默的，故只提示一次。
		if r, _, _ := pSendMessageW.Call(h, emSetCharFormat, scfSelection, uintptr(unsafe.Pointer(&cf))); r == 0 && !u.colorWarned {
			u.colorWarned = true
			guiLog("日志上色未生效（EM_SETCHARFORMAT 返回 0），不影响功能，仅日志不再按行分色")
		}
	}
	pSendMessageW.Call(h, emSetReadOnly, 1, 0)
	pSendMessageW.Call(h, emScrollCaret, 0, 0)

	u.logText = append(u.logText, s)
	u.logLines++
	if u.logLines > natLogKeep {
		u.trimLog()
	}
}

// trimLog 删除最老的一段行以限制日志控件行数。
func (u *nativeUI) trimLog() {
	h := u.logHwnd
	if h == 0 {
		return
	}
	pSendMessageW.Call(h, emSetReadOnly, 0, 0)
	cut, _, _ := pSendMessageW.Call(h, emLineIndex, natLogDropN, 0)
	if int32(cut) > 0 {
		pSendMessageW.Call(h, emSetSel, 0, cut)
		empty, _ := syscall.UTF16FromString("")
		pSendMessageW.Call(h, emReplaceSel, 0, uintptr(unsafe.Pointer(&empty[0])))
		u.logLines -= natLogDropN
		if natLogDropN < len(u.logText) {
			u.logText = append([]string(nil), u.logText[natLogDropN:]...)
		}
	}
	pSendMessageW.Call(h, emSetReadOnly, 1, 0)
}

func (u *nativeUI) clearLog() {
	if u.logHwnd == 0 {
		return
	}
	txt, _ := syscall.UTF16FromString("")
	pSetWindowTextW.Call(u.logHwnd, uintptr(unsafe.Pointer(&txt[0])))
	runtime.KeepAlive(txt) // 保持 Go 堆上的 UTF-16 缓冲区存活，防止调用期间被 GC 回收
	u.logText = nil
	u.logLines = 0
}

// resetLog 换了一个新任务：清空并归零序号
func (u *nativeUI) resetLog() {
	u.clearLog()
	u.seq = 0
}

// 轮询

func (u *nativeUI) poll() {
	st := u.ctl.snapshot(u.seq)

	jobID, _ := st["jobId"].(string)
	if jobID != u.jobID {
		u.jobID = jobID
		u.resetLog()
		u.addr, u.room, u.join = "", "", ""
		u.rosterRaw = "" // 换任务需重新播一次名单
	}

	if lines, ok := st["lines"].([]string); ok {
		changed := false
		for _, ln := range lines {
			if strings.HasPrefix(ln, natMarkerPrefix) {
				u.noteMarker(ln)
				changed = true
				continue
			}
			u.appendLine(ln)
			changed = true
		}
		if changed {
			u.repaint()
		}
	}
	// 地址卡片有无变化时须重排右侧控件（否则复制按钮会残留）。
	if key := u.addr + "\x00" + u.room + "\x00" + u.join; key != u.addrKey {
		u.addrKey = key
		u.layout()
		u.repaint()
	}
	if v, ok := st["seq"].(int64); ok {
		u.seq = v
	}
	u.command, _ = st["command"].(string)
	u.taskTitle, _ = st["task"].(string)
	u.errMsg, _ = st["err"].(string)

	running, _ := st["running"].(bool)
	done, _ := st["done"].(bool)
	uptime, _ := st["uptime"].(string)

	prevStatus, prevRunning := u.statusTxt, u.running
	u.running, u.done = running, done
	switch {
	case running:
		u.statusTxt = "执行中 · " + u.taskTitle + " · " + uptime
	case done:
		if code, _ := st["code"].(int); u.errMsg != "" || code != 0 {
			u.statusTxt = "已结束 · " + firstNonEmpty(u.errMsg, fmt.Sprintf("退出码 %d", code))
		} else {
			u.statusTxt = "已结束 · 正常"
		}
	default:
		u.statusTxt = "空闲"
	}
	if u.statusTxt != prevStatus || u.running != prevRunning {
		u.repaint()
	}
	// 运行/停止按钮的可用状态跟着变
	u.enable(u.hRun, !running)
	u.enable(u.hStop, running)
}

func (u *nativeUI) enable(h uintptr, on bool) {
	if h == 0 {
		return
	}
	v := uintptr(0)
	if on {
		v = 1
	}
	pSendMessageW.Call(h, wmEnable, v, 0)
	keepOwnerDraw(h)
}

// 自绘按钮的类型位：BUTTON 收到 WM_ENABLE 会改写类型位，须在改变可用状态后恢复 BS_OWNERDRAW。
func keepOwnerDraw(h uintptr) {
	if h == 0 || windowStyle(h)&0x0F == bsOwnerDraw {
		return
	}
	setWindowStyle(h, (windowStyle(h)&^uintptr(0x0F))|uintptr(bsOwnerDraw))
	pInvalidateRect.Call(h, 0, 0)
}

// GWL_STYLE 为 -16；此处按无符号写成补码形式，避免每次转换（32/64 位均适用）
const gwlStyle = ^uintptr(15)

func windowStyle(h uintptr) uintptr {
	if h == 0 {
		return 0
	}
	v, _, _ := pGetWindowLongW.Call(h, gwlStyle)
	return v
}

func setWindowStyle(h uintptr, style uintptr) {
	if h == 0 {
		return
	}
	// 32 位下没有 SetWindowLongPtrW（宏），需分开处理
	if unsafe.Sizeof(uintptr(0)) == 4 {
		pSetWindowLongW.Call(h, gwlStyle, style)
		return
	}
	pSetWindowLongPtrW.Call(h, gwlStyle, style)
}

func firstNonEmpty(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}

// noteMarker 解析 ##KIND## value 这种机器标记（地址 / 房间码 / 加入命令）
func (u *nativeUI) noteMarker(line string) {
	rest := strings.TrimPrefix(line, natMarkerPrefix)
	end := strings.Index(rest, natMarkerPrefix)
	if end < 0 {
		return
	}
	kind := rest[:end]
	val := strings.TrimSpace(rest[end+len(natMarkerPrefix):])
	switch kind {
	case "ADDR":
		u.addr = val
	case "ROOM":
		u.room = val
	case "JOIN":
		u.join = val
	case "GUESTS":
		// 在场名单压成一行写进日志区。
		u.noteRoster(val)
	}
}

// noteRoster 把 ##GUESTS## 的 JSON 压成一行日志，并对重复内容去重。
func (u *nativeUI) noteRoster(val string) {
	if val == u.rosterRaw {
		return
	}
	u.rosterRaw = val
	if line := rosterLine(val); line != "" {
		u.appendLine(line)
	}
}

// rosterLine 把 ##GUESTS## 的 JSON 压成一行；非法输入返回空串。字段 c/m/s 为界面契约。
func rosterLine(val string) string {
	var p struct {
		N   int `json:"n"`
		Who []struct {
			Code  string `json:"c"`
			Mode  string `json:"m"`
			Since int64  `json:"s"`
		} `json:"who"`
	}
	if err := json.Unmarshal([]byte(val), &p); err != nil {
		return ""
	}
	if p.N == 0 || len(p.Who) == 0 {
		return "[在场] 0 人（尚无人通过身份校验）"
	}
	parts := make([]string, 0, len(p.Who))
	for _, w := range p.Who {
		parts = append(parts, fmt.Sprintf("安全码 %s · %s · 已连 %s", w.Code, w.Mode, humanSince(w.Since)))
	}
	return fmt.Sprintf("[在场] %d 人：%s", p.N, strings.Join(parts, "；"))
}

// humanSince 把已连秒数转为可读时长。界面该格较窄，不追求精度。
func humanSince(sec int64) string {
	if sec < 0 {
		sec = 0
	}
	switch {
	case sec < 60:
		return fmt.Sprintf("%d 秒", sec)
	case sec < 3600:
		return fmt.Sprintf("%d 分", sec/60)
	default:
		return fmt.Sprintf("%d 时 %02d 分", sec/3600, (sec%3600)/60)
	}
}

// 剪贴板

func (u *nativeUI) copyText(s string) {
	if strings.TrimSpace(s) == "" {
		return
	}
	if r, _, _ := pOpenClipboard.Call(u.hwnd); r == 0 {
		return
	}
	defer pCloseClipboard.Call()
	pEmptyClipboard.Call()

	u16, err := syscall.UTF16FromString(s)
	if err != nil {
		return
	}
	size := uintptr(len(u16) * 2)
	hm, _, _ := pGlobalAlloc.Call(gmemMoveable, size)
	if hm == 0 {
		return
	}
	ptr, _, _ := pGlobalLock.Call(hm)
	if ptr == 0 {
		return
	}
	copy(unsafe.Slice((*uint16)(unsafe.Pointer(ptr)), len(u16)), u16)
	pGlobalUnlock.Call(hm)
	pSetClipboard.Call(cfUnicodeText, hm)
}

// 消息

func hiWord(v uintptr) int32 { return int32(v >> 16) }
func loWord(v uintptr) int32 { return int32(v & 0xFFFF) }

// 16 位有符号（鼠标滚轮的方向是带符号的）
func signedHiWord(v uintptr) int32 { return int32(int16(uint16(v >> 16))) }

func natWndProc(hwnd uintptr, msg uint32, wParam, lParam uintptr) uintptr {
	u := curNative
	if u == nil || u.hwnd != hwnd {
		r, _, _ := pDefWindowProcW.Call(hwnd, uintptr(msg), wParam, lParam)
		return r
	}

	switch msg {
	case wmSize:
		u.layout()
		u.repaint()

	case wmPaint:
		u.paint()
		return 0

	case wmEraseBkg:
		return 1 // 自行绘制，不擦背景

	case wmTimer:
		if wParam == natTimerID {
			u.poll()
		}

	case wmLButtonDown:
		x, y := int32(loWord(lParam)), int32(hiWord(lParam))
		lr := u.listRect(u.clientH())
		if y >= u.px(natTopH) && x < lr.Right {
			hit := u.hitRow(y)
			if hit >= 0 {
				u.selectTask(hit)
			}
		}

	case wmMouseMove:
		x, y := int32(loWord(lParam)), int32(hiWord(lParam))
		lr := u.listRect(u.clientH())
		hv := -1
		if y >= u.px(natTopH) && x < lr.Right {
			hv = u.hitRow(y)
		}
		if hv != u.hover {
			u.hover = hv
			u.repaint()
		}

	case wmMouseWheel:
		u.scrollBy(-signedHiWord(wParam) * u.px(60) / 120)

	case wmCommand:
		id := loWord(wParam)
		if hiWord(wParam) != bnClicked {
			break
		}
		// 勾选项点击即记住（输入框的 EN_CHANGE 不走这里，避免每敲一字写盘）。
		if id >= natIDField0 {
			u.rememberCurrentFields()
			break
		}
		switch id {
		case natIDRun:
			u.run()
		case natIDStop:
			u.ctl.stop()
			u.repaint()
		case natIDQuit:
			pDestroyWindow.Call(hwnd)
		case natIDClearLog:
			u.clearLog()
		case natIDCopyLog:
			u.copyText(strings.Join(u.logText, "\r\n"))
		case natIDCopyAddr:
			u.copyText(u.addr)
		case natIDCopyJoin:
			u.copyText(u.join)
		}

	case wmDrawItem:
		if lParam != 0 {
			u.drawButton((*drawItemT)(unsafe.Pointer(lParam)))
			return 1
		}

	case wmPrintClient:
		// 处理 WM_PRINTCLIENT，否则外部截图/打印/录屏只会得到空白。
		if wParam != 0 {
			u.paintTo(wParam, u.clientW(), u.clientH())
			return 0
		}

	case wmCtlColorEdit, wmCtlColorBtn, wmCtlColorStatic:
		// 子控件统一用暗色，避免白底刺眼
		pSetBkMode.Call(wParam, transparent)
		pSetTextColor.Call(wParam, natInk)
		if lParam == u.logHwnd {
			return u.brBg
		}
		return u.brSurf2

	case wmGetMinMaxInfo:
		// 设置窗口最小尺寸下限。
		if lParam != 0 {
			mm := (*minMaxInfoT)(unsafe.Pointer(lParam))
			mm.PtMinTrackX = u.px(natMinWinW)
			mm.PtMinTrackY = u.px(natMinWinH)
		}
		return 0

	case wmClose:
		// 先隐藏窗口再销毁，收尾移到 wmDestroy，避免关闭时出现停顿。
		pShowWindow.Call(hwnd, swHide)
		pDestroyWindow.Call(hwnd)
		return 0

	case wmDestroy:
		u.ctl.stop() // 窗口已经不可见了，现在才收任务
		pKillTimer.Call(hwnd, natTimerID)
		// 关窗前记下本页填入的值
		u.rememberCurrentFields()
		pPostQuitMessage.Call(0)
		return 0
	}
	r, _, _ := pDefWindowProcW.Call(hwnd, uintptr(msg), wParam, lParam)
	return r
}

// hitRow 屏幕上这个 y 落在哪个任务条目上（分组标题返回 -1）
func (u *nativeUI) hitRow(y int32) int {
	ly := y - u.px(natTopH) + u.listTop
	for _, row := range u.rows {
		if row.isGroup {
			continue
		}
		if ly >= row.y && ly < row.y+row.h {
			return row.taskIdx
		}
	}
	return -1
}

func (u *nativeUI) scrollBy(d int32) {
	lr := u.listRect(u.clientH())
	viewH := lr.Bottom - lr.Top - u.px(natTopH)
	maxTop := u.rowsH - viewH
	if maxTop < 0 {
		maxTop = 0
	}
	u.listTop += d
	if u.listTop < 0 {
		u.listTop = 0
	}
	if u.listTop > maxTop {
		u.listTop = maxTop
	}
	u.repaint()
}

func (u *nativeUI) run() {
	if u.sel < 0 || u.sel >= len(guiTasks) {
		return
	}
	t := guiTasks[u.sel]
	in := u.inputs()
	// 先记住填入的值再起进程
	rememberFieldValues(t.Key, in)
	if _, err := u.ctl.startJob(t, in, false); err != nil {
		u.statusTxt = err.Error()
		u.repaint()
		// 启动失败写入日志（不弹窗）
		u.appendLine("启动失败：" + err.Error())
		u.layout()
	}
}

// startAutoProbe 打开界面时自动执行一次环境体检（可在该页取消勾选）。
func (u *nativeUI) startAutoProbe() {
	for i, t := range guiTasks {
		if t.Key != "probe" {
			continue
		}
		u.selectTask(i)
		if u.sel != i {
			return
		}
		in := u.inputs()
		rememberFieldValues(t.Key, in)
		if _, err := u.ctl.startJob(t, in, true); err != nil {
			u.appendLine("自动体检没起来：" + err.Error())
			return
		}
		u.appendLine("（已自动执行一次环境检测；可在「检测本机环境」中取消勾选）")
		return
	}
}
