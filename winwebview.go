//go:build windows

package main

// winwebview.go 用系统自带的 WebView2 运行时创建原生窗口（单 exe、单进程），
// 通过 syscall 直接调用 COM 接口；失败时由调用方退回 Edge 应用模式。

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"
)

//go:embed WebView2Loader.dll
var webView2LoaderBytes []byte

//go:embed mclbx.ico
var appIconBytes []byte

var (
	user32   = syscall.NewLazyDLL("user32.dll")
	gdi32    = syscall.NewLazyDLL("gdi32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")
	ole32    = syscall.NewLazyDLL("ole32.dll")

	pRegisterClassExW = user32.NewProc("RegisterClassExW")
	pCreateWindowExW  = user32.NewProc("CreateWindowExW")
	pDefWindowProcW   = user32.NewProc("DefWindowProcW")
	pGetMessageW      = user32.NewProc("GetMessageW")
	pTranslateMessage = user32.NewProc("TranslateMessage")
	pDispatchMessageW = user32.NewProc("DispatchMessageW")
	pPostQuitMessage  = user32.NewProc("PostQuitMessage")
	pShowWindow       = user32.NewProc("ShowWindow")
	pUpdateWindow     = user32.NewProc("UpdateWindow")
	pLoadImageW       = user32.NewProc("LoadImageW")
	pSendMessageW     = user32.NewProc("SendMessageW")
	pGetClientRect    = user32.NewProc("GetClientRect")
	pDestroyWindow    = user32.NewProc("DestroyWindow")
	pCreateSolidBrush = gdi32.NewProc("CreateSolidBrush") // 在 gdi32 而非 user32
	pLoadCursorW      = user32.NewProc("LoadCursorW")
	pGetModuleHandleW = kernel32.NewProc("GetModuleHandleW")
	pGetConsoleWindow = kernel32.NewProc("GetConsoleWindow")
	pFreeConsole      = kernel32.NewProc("FreeConsole")
	pCoInitializeEx   = ole32.NewProc("CoInitializeEx")
	pCoUninitialize   = ole32.NewProc("CoUninitialize")

	// 高 DPI 与窗口尺寸相关函数；缺失时退回 96 DPI。
	pSetWindowPos        = user32.NewProc("SetWindowPos")
	pIsWindow            = user32.NewProc("IsWindow")
	pSendMessageTimeW    = user32.NewProc("SendMessageTimeoutW")
	pSetDpiAwareness     = user32.NewProc("SetProcessDpiAwarenessContext")
	pGetDpiForSystem     = user32.NewProc("GetDpiForSystem")
	pPostMessageW        = user32.NewProc("PostMessageW")
	pCoTaskMemFree       = ole32.NewProc("CoTaskMemFree")
	pFindWindowW         = user32.NewProc("FindWindowW")
	pSetForegroundWindow = user32.NewProc("SetForegroundWindow")
	pIsIconic            = user32.NewProc("IsIconic")
)

// callIfPresent 调用可能不存在的系统函数：缺失时返回 false，不 panic。
func callIfPresent(p *syscall.LazyProc, args ...uintptr) (uintptr, bool) {
	if err := p.Find(); err != nil {
		return 0, false
	}
	r, _, _ := p.Call(args...)
	return r, true
}

// consoleWindowClass 是固定的窗口类名，FindWindow 靠它找出已在运行的窗口。
// 声明为变量以便用 -ldflags -X 覆盖，用于性能对照测量。
var consoleWindowClass = "mclbxConsoleWnd"

// findExistingGuiWindow 查找已运行的界面窗口并置前，返回是否已处理（调用方可直接退出）。
// 通过 hasExistingGuiWindow 变量调用，便于测试替换。
func findExistingGuiWindow() bool {
	cls, _ := syscall.UTF16PtrFromString(consoleWindowClass)
	h, _, _ := pFindWindowW.Call(uintptr(unsafe.Pointer(cls)), 0)
	if h == 0 {
		return false
	}
	// 最小化则先还原再置前
	if r, _, _ := pIsIconic.Call(h); r != 0 {
		pShowWindow.Call(h, swRestore)
	}
	pSetForegroundWindow.Call(h)
	return true
}

// COM 虚表下标，照官方 WebView2.h 数（ICoreWebView2Environment / Controller / WebView 等）。
// 下标写错时调用会"成功"却不生效，故必须对着头文件核对。
const (
	envIdxCreateController = 3
	ctrlIdxPutVisible      = 4
	ctrlIdxPutBounds       = 6
	ctrlIdxClose           = 24 // ICoreWebView2Controller::Close
	ctrlIdxGetCoreWebView2 = 25
	iunknownRelease        = 2

	// ICoreWebView2：3 get_Settings / 4 get_Source / 5 Navigate / … / 15 add_NavigationCompleted
	viewIdxGetSource              = 4
	viewIdxNavigate               = 5
	viewIdxAddNavigationCompleted = 15

	// ICoreWebView2NavigationCompletedEventArgs：3 get_IsSuccess / 4 get_WebErrorStatus
	navArgsIdxIsSuccess      = 3
	navArgsIdxWebErrorStatus = 4
)

const (
	wsOverlappedWindow = 0x00CF0000
	swShow             = 5
	swHide             = 0

	wmDestroy       = 0x0002
	wmSize          = 0x0005
	wmSetIcon       = 0x0080
	wmClose         = 0x0010
	wmEraseBkgnd    = 0x0014
	wmGetMinMaxInfo = 0x0024
	wmDpiChanged    = 0x02E0
	wmNull          = 0x0000

	iconSmall      = 0
	iconBig        = 1
	imageIcon      = 1
	lrLoadFromFile = 0x0010
	lrDefaultSize  = 0x0040
	swRestore      = 9

	swpNoZOrder    = 0x0004
	swpNoActivate  = 0x0010
	smtAbortIfHung = 0x0002

	coinitApartmentThreaded = 0x2

	// DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2：值为 -4 的句柄
	dpiAwarePerMonitorV2 = ^uintptr(3)
)

// CW_USEDEFAULT：窗口位置由系统决定（只读低 32 位，用无符号承载）。
var cwUseDefault = uintptr(uint32(0x80000000))

type wndClassExW struct {
	CbSize        uint32
	Style         uint32
	LpfnWndProc   uintptr
	CbClsExtra    int32
	CbWndExtra    int32
	HInstance     uintptr
	HIcon         uintptr
	HCursor       uintptr
	HbrBackground uintptr
	LpszMenuName  *uint16
	LpszClassName *uint16
	HIconSm       uintptr
}

type msgT struct {
	Hwnd    uintptr
	Message uint32
	_       uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	PtX     int32
	PtY     int32
	_       uint32
}

type rectT struct{ Left, Top, Right, Bottom int32 }

type minMaxInfoT struct {
	PtReservedX, PtReservedY int32
	PtMaxSizeX, PtMaxSizeY   int32
	PtMaxPosX, PtMaxPosY     int32
	PtMinTrackX, PtMinTrackY int32
	PtMaxTrackX, PtMaxTrackY int32
}

type comHandler struct {
	vtbl   *[4]uintptr // 必须是第一个字段：接口指针指向这里
	refs   int32
	invoke func(a, b uintptr) // a/b 为两个入参的原始值（HRESULT 或接口指针）
}

var (
	comMu   sync.Mutex
	comKeep []*comHandler // 防止被 GC 回收
)

func comQueryInterface(this, riid, ppv uintptr) uintptr {
	if ppv != 0 {
		*(*uintptr)(unsafe.Pointer(ppv)) = this
	}
	atomic.AddInt32(&(*comHandler)(unsafe.Pointer(this)).refs, 1)
	return 0 // S_OK
}

func comAddRef(this uintptr) uintptr {
	return uintptr(atomic.AddInt32(&(*comHandler)(unsafe.Pointer(this)).refs, 1))
}

func comRelease(this uintptr) uintptr {
	return uintptr(atomic.AddInt32(&(*comHandler)(unsafe.Pointer(this)).refs, -1))
}

func comInvoke(this, a, b uintptr) uintptr {
	h := (*comHandler)(unsafe.Pointer(this))
	if h.invoke != nil {
		h.invoke(a, b)
	}
	return 0 // S_OK
}

// newComHandler 创建 COM 回调对象，入参按 uintptr 原样接收（HRESULT/接口指针或 sender/args）。
func newComHandler(fn func(a, b uintptr)) uintptr {
	vtbl := [4]uintptr{
		syscall.NewCallback(comQueryInterface),
		syscall.NewCallback(comAddRef),
		syscall.NewCallback(comRelease),
		syscall.NewCallback(comInvoke),
	}
	h := &comHandler{vtbl: &vtbl, refs: 1, invoke: fn}
	comMu.Lock()
	comKeep = append(comKeep, h)
	comMu.Unlock()
	return uintptr(unsafe.Pointer(h))
}

// 虚表第 idx 个方法的地址
func method(p uintptr, idx int) uintptr {
	return (*(**[64]uintptr)(unsafe.Pointer(p)))[idx]
}

// GUID 内存布局与 C 一致（4+2+2+8，无填充）。
type guidT struct {
	Data1 uint32
	Data2 uint16
	Data3 uint16
	Data4 [8]byte
}

// IID 照官方 WebView2.h 抄录。
var (
	iidController = guidT{0x4d00c0d1, 0x9434, 0x4eb6, [8]byte{0x80, 0x78, 0x86, 0x97, 0xa5, 0x60, 0x33, 0x4f}}
	iidView       = guidT{0x76eceacb, 0x0462, 0x4d94, [8]byte{0xac, 0x83, 0x42, 0x3a, 0x67, 0x93, 0x77, 0x5e}}
)

// qi 调 QueryInterface 并返回 (拿到的指针, HRESULT)
func qi(p uintptr, iid *guidT) (uintptr, int32) {
	var out uintptr
	r, _, _ := syscall.SyscallN(method(p, 0), p, uintptr(unsafe.Pointer(iid)), uintptr(unsafe.Pointer(&out)))
	return out, int32(uint32(r))
}

type webviewHost struct {
	hwnd  uintptr
	ctrl  uintptr
	view  uintptr
	url   string
	title string
	dpi   uint32
	ready chan error
	once  sync.Once

	mu       sync.Mutex
	firstErr error // 回调链上第一个失败原因（窗口循环结束时返回给上层）
	loaded   bool  // 导航完成事件报过成功
}

func (h *webviewHost) isLoaded() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.loaded
}

var hostMu sync.Mutex
var currentHost *webviewHost

// webView2Available 体检用：这台机器上有没有能用的 WebView2 运行时
func webView2Available() bool {
	_, err := loadWebView2()
	return err == nil
}

// detachConsole 彻底释放本进程的控制台（FreeConsole），而非隐藏其窗口。
// 隐藏时关闭终端仍会连带退出进程；释放后双击即为干净窗口。代价是标准输出消失。
func detachConsole() {
	pFreeConsole.Call()
}

// createNoWindow 为 CreateProcess 标志：给子进程一个无窗口的控制台。
// 不用 DETACHED_PROCESS 是因为那样完全没有控制台，孙进程会各弹黑框。
const createNoWindow = 0x08000000

// consoleClassicWnd 为经典控制台窗口类名，用于检查是否出现可见黑框。
const consoleClassicWnd = "ConsoleWindowClass"

// hiddenConsoleProcAttr 为界面拉起的任务子进程提供无窗口控制台的启动属性。
func hiddenConsoleProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
}

// materialize 将内嵌的 WebView2Loader.dll / 图标解出到 internalDir（首次运行时）。
// 不放入存档目录：这些是 exe 解出的机器内部产物。
func materialize(name string, data []byte) (string, error) {
	p := filepath.Join(internalDir(), name)
	if st, err := os.Stat(p); err == nil && st.Size() == int64(len(data)) {
		return p, nil
	}
	if err := os.WriteFile(p, data, 0o644); err != nil {
		return "", err
	}
	return p, nil
}

func loadWebView2() (*syscall.LazyDLL, error) {
	// 优先用 exe 同目录的，其次用从 exe 解出的那份
	var cands []string
	if exe, err := os.Executable(); err == nil {
		cands = append(cands, filepath.Join(filepath.Dir(exe), "WebView2Loader.dll"))
	}
	p, err := materialize("WebView2Loader.dll", webView2LoaderBytes)
	if err == nil {
		cands = append(cands, p)
	}
	for _, c := range cands {
		if _, err := os.Stat(c); err != nil {
			continue
		}
		dll := syscall.NewLazyDLL(c)
		if err := dll.Load(); err == nil {
			return dll, nil
		}
	}
	return nil, fmt.Errorf("找不到可用的 WebView2Loader.dll")
}

func (h *webviewHost) resize() {
	if h.ctrl == 0 {
		return
	}
	var r rectT
	pGetClientRect.Call(h.hwnd, uintptr(unsafe.Pointer(&r)))
	if r.Right == 0 || r.Bottom == 0 {
		logf("窗口客户区是空的（%d,%d - %d,%d），界面可能显示不出来", r.Left, r.Top, r.Right, r.Bottom)
	}
	// RECT 为 16 字节，超出寄存器传值范围，须传指向临时副本的地址。
	if hr, _, _ := syscall.SyscallN(method(h.ctrl, ctrlIdxPutBounds), h.ctrl, uintptr(unsafe.Pointer(&r))); int32(uint32(hr)) < 0 {
		logf("设置界面区域失败 HRESULT=0x%08X", uint32(hr))
	}
}

// finish 汇总回调链结果；失败时必须关掉窗口，失败原因由窗口循环返回上层并写入 gui.log。
func (h *webviewHost) finish(err error) {
	h.mu.Lock()
	if err != nil && h.firstErr == nil {
		h.firstErr = err
	}
	out, hwnd := h.firstErr, h.hwnd
	h.mu.Unlock()
	h.once.Do(func() {
		h.ready <- out
		if out != nil && hwnd != 0 {
			pPostMessageW.Call(hwnd, wmClose, 0, 0)
		}
	})
}

func (h *webviewHost) err() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.firstErr
}

// closeWebview 主动关闭 WebView2，而非等待进程自然结束。
// 显式调用控制器的 Close 可让运行时立即回收 msedgewebview2.exe 子进程、释放显存；幂等。
func (h *webviewHost) closeWebview() {
	h.mu.Lock()
	ctrl, view := h.ctrl, h.view
	h.ctrl, h.view = 0, 0
	h.mu.Unlock()
	if ctrl != 0 {
		// ICoreWebView2Controller::Close（关闭 WebView 本体）
		syscall.SyscallN(method(ctrl, ctrlIdxClose), ctrl)
		syscall.SyscallN(method(ctrl, iunknownRelease), ctrl)
	}
	if view != 0 {
		syscall.SyscallN(method(view, iunknownRelease), view)
	}
}

// requestClose 从其他 goroutine 请求关闭界面窗口，返回本次是否已接管。
// 跨线程销毁窗口为未定义行为，故仅 PostMessage(WM_CLOSE)。
func requestClose() bool {
	hostMu.Lock()
	h := currentHost
	hostMu.Unlock()
	if h == nil {
		return false
	}
	h.mu.Lock()
	hwnd := h.hwnd
	h.mu.Unlock()
	if hwnd == 0 {
		return false
	}
	if r, _, _ := pIsWindow.Call(hwnd); r == 0 {
		return false // 窗口已不存在，调用方自行退出
	}
	pPostMessageW.Call(hwnd, wmClose, 0, 0)
	return true
}

// 环境就绪 → 建控制器 → 拿 CoreWebView2 → 导航
func (h *webviewHost) onEnv(hrRaw, env uintptr) {
	hr := int32(uint32(hrRaw))
	if hr != 0 || env == 0 {
		h.finish(fmt.Errorf("创建 WebView2 环境失败 HRESULT=0x%08X", uint32(hr)))
		return
	}
	handler := newComHandler(func(hrRaw2, ctrl uintptr) {
		hr2 := int32(uint32(hrRaw2))
		if hr2 != 0 || ctrl == 0 {
			h.finish(fmt.Errorf("创建 WebView2 控制器失败 HRESULT=0x%08X", uint32(hr2)))
			return
		}
		h.ctrl = ctrl
		h.resize()
		if r, _, _ := syscall.SyscallN(method(ctrl, ctrlIdxPutVisible), ctrl, 1); int32(uint32(r)) < 0 {
			h.finish(fmt.Errorf("put_IsVisible 失败 HRESULT=0x%08X", uint32(r)))
			return
		}

		var view uintptr
		r, _, _ := syscall.SyscallN(method(ctrl, ctrlIdxGetCoreWebView2), ctrl, uintptr(unsafe.Pointer(&view)))
		if int32(uint32(r)) < 0 || view == 0 {
			h.finish(fmt.Errorf("拿不到 CoreWebView2 接口：HRESULT=0x%08X，指针=%#x", uint32(r), view))
			return
		}
		h.view = view
		// 自检控制器/浏览器接口：虚表下标写错时调用会成功却不生效。
		if _, cHr := qi(ctrl, &iidController); cHr != 0 {
			h.finish(fmt.Errorf("控制器接口自检失败 HRESULT=0x%08X（虚表下标可能不对）", uint32(cHr)))
			return
		}
		if _, vHr := qi(view, &iidView); vHr != 0 {
			h.finish(fmt.Errorf("浏览器接口自检失败 HRESULT=0x%08X（虚表下标可能不对）", uint32(vHr)))
			return
		}
		h.watchNavigation()
		u, _ := syscall.UTF16PtrFromString(h.url)
		if r, _, _ := syscall.SyscallN(method(view, viewIdxNavigate), view, uintptr(unsafe.Pointer(u))); int32(uint32(r)) < 0 {
			h.finish(fmt.Errorf("打开界面地址失败 HRESULT=0x%08X", uint32(r)))
			return
		}
		// Navigate 返回 S_OK 不代表真的加载，另起 ensureLoaded 确认。
		go h.ensureLoaded()
		h.finish(nil)
	})
	if r, _, _ := syscall.SyscallN(method(env, envIdxCreateController), env, h.hwnd, handler); int32(uint32(r)) < 0 {
		h.finish(fmt.Errorf("CreateCoreWebView2Controller 调用失败 HRESULT=0x%08X", uint32(r)))
	}
}

// watchNavigation 订阅导航完成事件，取得 WebView 报告的成败与错误码。
func (h *webviewHost) watchNavigation() {
	nav := newComHandler(func(sender, args uintptr) {
		var ok, status int32
		if args != 0 {
			syscall.SyscallN(method(args, navArgsIdxIsSuccess), args, uintptr(unsafe.Pointer(&ok)))
			syscall.SyscallN(method(args, navArgsIdxWebErrorStatus), args, uintptr(unsafe.Pointer(&status)))
		}
		if ok != 0 {
			h.mu.Lock()
			h.loaded = true
			h.mu.Unlock()
			return
		}
		h.finish(fmt.Errorf("界面加载失败：%s", webErrorText(status)))
	})
	var token uintptr
	if r, _, _ := syscall.SyscallN(method(h.view, viewIdxAddNavigationCompleted), h.view, nav, uintptr(unsafe.Pointer(&token))); int32(uint32(r)) < 0 {
		logf("订阅导航结果失败 HRESULT=0x%08X（不影响使用）", uint32(r))
	}
}

// webErrorText 将 COREWEBVIEW2_WEB_ERROR_STATUS 转为可读文本。
func webErrorText(s int32) string {
	switch s {
	case 0:
		return "未知错误（WebErrorStatus=0）"
	case 1, 2, 3, 4, 5:
		return fmt.Sprintf("证书问题（WebErrorStatus=%d）", s)
	case 6:
		return "服务器不可达（WebErrorStatus=6）"
	case 7:
		return "超时（WebErrorStatus=7）"
	case 8:
		return fmt.Sprintf("服务端返回无法解析（WebErrorStatus=%d）", s)
	case 9:
		return "连接被中断（WebErrorStatus=9）"
	case 10:
		return "连接被重置（WebErrorStatus=10）"
	case 11:
		return "网络断开（WebErrorStatus=11）"
	case 12:
		return "连不上（WebErrorStatus=12，常见于被防火墙/代理拦下）"
	case 13:
		return "域名解析不了（WebErrorStatus=13）"
	case 14:
		return "操作被取消（WebErrorStatus=14）"
	case 16:
		return "意料之外的错误（WebErrorStatus=16）"
	default:
		return fmt.Sprintf("WebErrorStatus=%d", s)
	}
}

// ensureLoaded 监控加载是否完成，必要时重发导航。
// WebView2 的 Navigate 会偶发被静默丢弃（返回 S_OK 但不加载），故最多重试 3 次后报错。
func (h *webviewHost) ensureLoaded() {
	for attempt := 1; attempt <= 3; attempt++ {
		if attempt > 1 {
			logf("界面第 %d 次导航没有动静，重试", attempt-1)
			u, _ := syscall.UTF16PtrFromString(h.url)
			if r, _, _ := syscall.SyscallN(method(h.view, viewIdxNavigate), h.view, uintptr(unsafe.Pointer(u))); int32(uint32(r)) < 0 {
				h.finish(fmt.Errorf("打开界面地址失败 HRESULT=0x%08X", uint32(r)))
				return
			}
		}
		for w := 0; w < 8; w++ { // 每次最多等 4 秒
			time.Sleep(500 * time.Millisecond)
			if h.isLoaded() {
				return
			}
			if h.err() != nil {
				return // 已有明确失败原因，无需再管
			}
		}
	}
	h.finish(fmt.Errorf("界面加载失败（WebView 停留在 %s）", h.currentSource()))
}

// currentSource 读取 WebView 当前地址，用于诊断。
func (h *webviewHost) currentSource() string {
	view := h.view
	if view == 0 {
		return "?"
	}
	var src uintptr
	if r, _, _ := syscall.SyscallN(method(view, viewIdxGetSource), view, uintptr(unsafe.Pointer(&src))); int32(uint32(r)) >= 0 && src != 0 {
		cur := utf16At(src)
		pCoTaskMemFree.Call(src)
		return cur
	}
	return "?"
}

// utf16At 将 C 宽字符串读为 Go 字符串（读到首个 NUL）。
func utf16At(p uintptr) string {
	var n int
	for n < 4096 {
		if *(*uint16)(unsafe.Pointer(p + uintptr(n)*2)) == 0 {
			break
		}
		n++
	}
	return syscall.UTF16ToString((*[1 << 16]uint16)(unsafe.Pointer(p))[:n])
}

func (h *webviewHost) scale() float64 {
	d := h.dpi
	if d == 0 {
		d = 96
	}
	return float64(d) / 96
}

// watchdog 每 5 秒用带超时的 SendMessage 探测界面线程是否响应；超过 2.5 秒无响应则记日志。
func (h *webviewHost) watchdog() {
	var out uintptr
	hung := false
	for {
		time.Sleep(5 * time.Second)
		if h.hwnd == 0 {
			continue
		}
		if r, _, _ := pIsWindow.Call(h.hwnd); r == 0 {
			return // 窗口已销毁，停止探测
		}
		r, _, err := pSendMessageTimeW.Call(h.hwnd, wmNull, 0, 0, smtAbortIfHung, 2500, uintptr(unsafe.Pointer(&out)))
		if r == 0 {
			if !hung {
				hung = true
				logf("界面线程超过 2.5 秒未响应（%v）——窗口会显示「未响应」", err)
			}
			continue
		}
		if hung {
			hung = false
			logf("界面线程已恢复响应")
		}
	}
}

func (h *webviewHost) wndProc(hwnd, msg, wparam, lparam uintptr) uintptr {
	switch msg {
	case wmSize:
		h.resize()
		return 0
	case wmGetMinMaxInfo:
		mm := (*minMaxInfoT)(unsafe.Pointer(lparam))
		s := h.scale()
		mm.PtMinTrackX = int32(760 * s)
		mm.PtMinTrackY = int32(520 * s)
		return 0
	case wmDpiChanged:
		// wParam 低 16 位是新 DPI，lParam 是系统建议的新窗口矩形
		h.dpi = uint32(wparam & 0xFFFF)
		r := (*rectT)(unsafe.Pointer(lparam))
		pSetWindowPos.Call(hwnd, 0,
			uintptr(int32(r.Left)), uintptr(int32(r.Top)),
			uintptr(uint32(r.Right-r.Left)), uintptr(uint32(r.Bottom-r.Top)),
			swpNoZOrder|swpNoActivate)
		h.resize()
		return 0
	case wmEraseBkgnd:
		return 1
	case wmClose:
		// 先隐藏窗口再做收尾，使用户感知的关闭耗时仅为一帧。
		pShowWindow.Call(hwnd, swHide)
		h.closeWebview()
		pDestroyWindow.Call(hwnd)
		return 0
	case wmDestroy:
		h.closeWebview() // 幂等：WM_CLOSE 已经收过的话这里什么都不做
		pPostQuitMessage.Call(0)
		return 0
	}
	r, _, _ := pDefWindowProcW.Call(hwnd, msg, wparam, lparam)
	return r
}

// runNativeWindow 打开原生窗口并加载界面，窗口关闭后返回。
// 本文件的 uintptr↔unsafe.Pointer 往返为手工 COM 宿主所需，go vet 的 unsafeptr 报错属误报。
func runNativeWindow(url, title string) (err error) {
	// 兜住 panic，避免带走整个进程，交由上层退回浏览器模式
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("内置窗口异常：%v", r)
		}
	}()

	// Win32 窗口、消息队列、COM 单元与 WebView2 宿主均绑定在固定 OS 线程上；
	// 故须 LockOSThread，否则 GetMessage 取不到消息，窗口会被标记为未响应。
	runtime.LockOSThread()

	// 启用 per-monitor v2 DPI 感知，避免高分屏被拉伸；相关 API 较新，须先探测存在性。
	dpi := uint32(96)
	if _, ok := callIfPresent(pSetDpiAwareness, dpiAwarePerMonitorV2); ok {
		if v, ok2 := callIfPresent(pGetDpiForSystem); ok2 && v >= 96 && v <= 480 {
			dpi = uint32(v)
		}
	} else {
		logf("这个系统的窗口 API 比较老（没有高 DPI 那套），按 96 DPI 显示")
	}

	// 界面只连本机回环，故禁用代理（等价于 AdditionalBrowserArguments，用官方环境变量传入）。
	// 用户已设置则不覆盖。
	if os.Getenv("WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS") == "" {
		_ = os.Setenv("WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS", "--no-proxy-server")
	}

	loader, err := loadWebView2()
	if err != nil {
		return err
	}
	h := &webviewHost{hwnd: 0, url: url, title: title, dpi: dpi, ready: make(chan error, 1)}
	hostMu.Lock()
	currentHost = h
	hostMu.Unlock()

	pCoInitializeEx.Call(0, coinitApartmentThreaded)
	defer pCoUninitialize.Call()

	hInst, _, _ := pGetModuleHandleW.Call(0)
	className, _ := syscall.UTF16PtrFromString(consoleWindowClass)

	iconPath, _ := materialize("mclbx.ico", appIconBytes)
	var hIcon, hIconSm uintptr
	if iconPath != "" {
		p, _ := syscall.UTF16PtrFromString(iconPath)
		hIcon, _, _ = pLoadImageW.Call(0, uintptr(unsafe.Pointer(p)), imageIcon, 0, 0, lrLoadFromFile|lrDefaultSize)
		hIconSm = hIcon
	}
	brush, _, _ := pCreateSolidBrush.Call(0x00140B07) // 与页面底色一致，避免打开时闪白
	arrow, _, _ := pLoadCursorW.Call(0, 32512)        // IDC_ARROW

	wc := wndClassExW{
		CbSize:        uint32(unsafe.Sizeof(wndClassExW{})),
		LpfnWndProc:   syscall.NewCallback(h.wndProc),
		HInstance:     hInst,
		HIcon:         hIcon,
		HCursor:       arrow,
		HbrBackground: brush,
		LpszClassName: className,
		HIconSm:       hIconSm,
	}
	if r, _, err := pRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc))); r == 0 {
		return fmt.Errorf("注册窗口类失败：%v", err)
	}

	titlePtr, _ := syscall.UTF16PtrFromString(title)
	winW, winH := uintptr(1280*h.scale()), uintptr(880*h.scale())
	hwnd, _, err := pCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(className)),
		uintptr(unsafe.Pointer(titlePtr)),
		wsOverlappedWindow,
		cwUseDefault, cwUseDefault, winW, winH,
		0, 0, hInst, 0,
	)
	if hwnd == 0 {
		return fmt.Errorf("创建窗口失败：%v", err)
	}
	h.hwnd = hwnd

	// 起环境（异步）→ 回调里建控制器；同时开始跑消息循环
	// 浏览器用户目录走 internalDir：文件多且与本机绑定，放存档里不合适。
	dataDir, _ := syscall.UTF16PtrFromString(filepath.Join(internalDir(), "webview"))
	proc := loader.NewProc("CreateCoreWebView2EnvironmentWithOptions")
	handler := newComHandler(h.onEnv)
	if r, _, _ := proc.Call(0, uintptr(unsafe.Pointer(dataDir)), 0, handler); int32(uint32(r)) < 0 {
		return fmt.Errorf("CreateCoreWebView2EnvironmentWithOptions 失败 HRESULT=0x%08X", uint32(r))
	}

	// WebView2 异步初始化；等待回执，超时按失败处理（自行关窗退回浏览器）。
	go func() {
		select {
		case err := <-h.ready:
			if err != nil {
				logf("内置窗口起不来：%v", err)
			} else {
				logf("内置窗口已就绪，加载 %s", url)
			}
		case <-time.After(20 * time.Second):
			h.finish(fmt.Errorf("WebView2 初始化超过 20 秒没有回执"))
		}
	}()

	pShowWindow.Call(hwnd, swShow)
	pUpdateWindow.Call(hwnd)
	h.resize() // 显示后再调一次尺寸：部分运行时在窗口不可见时会挂起首次渲染
	go h.watchdog()

	var msg msgT
	for {
		r, _, gerr := pGetMessageW.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		switch int32(r) {
		case 0: // WM_QUIT：窗口关掉了
			if err := h.err(); err != nil {
				return err
			}
			logf("窗口已关闭")
			return nil
		case -1: // GetMessage 出错，记录后退出
			return fmt.Errorf("GetMessage 失败：%v", gerr)
		}
		pTranslateMessage.Call(uintptr(unsafe.Pointer(&msg)))
		pDispatchMessageW.Call(uintptr(unsafe.Pointer(&msg)))
	}
}
