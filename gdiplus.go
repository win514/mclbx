//go:build windows

package main

// gdiplus.go 基于 gdiplus.dll 的绘图层：抗锯齿圆角、圆、直线与渐变，不可用时回退 GDI。
// Gdip 平铺 API 优先用整数版本（I 后缀），浮点参数仅用于画笔宽度与圆弧角度。

import (
	"math"
	"runtime"
	"sync"
	"syscall"
	"unsafe"
)

var (
	gdiplus = syscall.NewLazyDLL("gdiplus.dll")

	pGdiplusStartup      = gdiplus.NewProc("GdiplusStartup")
	pGdiplusShutdown     = gdiplus.NewProc("GdiplusShutdown")
	pGdipCreateFromHDC   = gdiplus.NewProc("GdipCreateFromHDC")
	pGdipDeleteGraphics  = gdiplus.NewProc("GdipDeleteGraphics")
	pGdipFlush           = gdiplus.NewProc("GdipFlush")
	pGdipSmoothingMode   = gdiplus.NewProc("GdipSetSmoothingMode")
	pGdipSolidFill       = gdiplus.NewProc("GdipCreateSolidFill")
	pGdipDeleteBrush     = gdiplus.NewProc("GdipDeleteBrush")
	pGdipLineBrushRectI  = gdiplus.NewProc("GdipCreateLineBrushFromRectI")
	pGdipCreatePen1      = gdiplus.NewProc("GdipCreatePen1")
	pGdipDeletePen       = gdiplus.NewProc("GdipDeletePen")
	pGdipCreatePath      = gdiplus.NewProc("GdipCreatePath")
	pGdipDeletePath      = gdiplus.NewProc("GdipDeletePath")
	pGdipAddPathArcI     = gdiplus.NewProc("GdipAddPathArcI")
	pGdipAddPathRectI    = gdiplus.NewProc("GdipAddPathRectangleI")
	pGdipClosePathFigure = gdiplus.NewProc("GdipClosePathFigure")
	pGdipFillPath        = gdiplus.NewProc("GdipFillPath")
	pGdipDrawPath        = gdiplus.NewProc("GdipDrawPath")
	pGdipFillRectangleI  = gdiplus.NewProc("GdipFillRectangleI")
	pGdipFillEllipseI    = gdiplus.NewProc("GdipFillEllipseI")
	pGdipDrawEllipseI    = gdiplus.NewProc("GdipDrawEllipseI")
	pGdipDrawLineI       = gdiplus.NewProc("GdipDrawLineI")
)

// gdiplusStartupInput 对应 GdiplusStartupInput；x64 下 Version 后补 4 字节以满足指针对齐。
type gdiplusStartupInput struct {
	Version                  uint32
	_                        uint32
	DebugEventCallback       uintptr
	SuppressBackgroundThread int32
	SuppressExternalCodecs   int32
}

var (
	gpToken uintptr
	gpOK    bool
	gpOnce  sync.Once

	// gpCur 为当前帧的 GDI+ 画布，0 表示不可用，此时所有画法回退 GDI。
	gpCur uintptr

	// gpBeginFails 记录取不到 GDI+ 画布的次数，gpBeginFailCode 为最近一次的状态码。
	gpBeginFails    int
	gpBeginFailCode int32

	gpBrushCache  = map[uint32]uintptr{} // ARGB → 纯色画刷缓存
	gdiBrushCache = map[uint32]uintptr{} // GDI 回退路径的画刷缓存
)

// gpStart 初始化 GDI+；缺少 gdiplus.dll 时返回 false，界面回退纯 GDI。
// 进程级且只初始化一次，退出前不得关闭，否则 GDI+ 无法再次启动。
func gpStart() bool {
	if gpOK { // 已初始化或本机不支持，直接返回
		return true
	}
	gpOnce.Do(func() {
		if err := pGdiplusStartup.Find(); err != nil {
			guiLog("这台机器没有 gdiplus.dll，界面退回 GDI 画法（圆角会有点锯齿）")
			return
		}
		in := gdiplusStartupInput{Version: 1}
		st, _, _ := pGdiplusStartup.Call(uintptr(unsafe.Pointer(&gpToken)),
			uintptr(unsafe.Pointer(&in)), 0)
		runtime.KeepAlive(&in)
		if st != 0 {
			gpToken = 0
			guiLog("GDI+ 起不来（状态 %d），界面退回 GDI 画法", st)
			return
		}
		gpOK = true
	})
	return gpOK
}

// gpStop 释放缓存对象并关闭 GDI+，只在进程退出时调用。
func gpStop() {
	for _, b := range gpBrushCache {
		pGdipDeleteBrush.Call(b)
	}
	gpBrushCache = map[uint32]uintptr{}
	for _, b := range gdiBrushCache {
		pDeleteObject.Call(b)
	}
	gdiBrushCache = map[uint32]uintptr{}
	if gpOK {
		pGdiplusShutdown.Call(gpToken)
		gpOK = false
	}
}

// gpBegin 在某个 DC 上开一块画布并打开抗锯齿。
func gpBegin(hdc uintptr) uintptr {
	if hdc == 0 || !gpStart() {
		return 0
	}
	var g uintptr
	if st, _, _ := pGdipCreateFromHDC.Call(hdc, uintptr(unsafe.Pointer(&g))); st != 0 || g == 0 {
		gpBeginFails++
		gpBeginFailCode = int32(st)
		return 0
	}
	pGdipSmoothingMode.Call(g, 4) // SmoothingModeAntiAlias
	return g
}

func gpEnd(g uintptr) {
	if g != 0 {
		pGdipFlush.Call(g, 1) // FlushIntentionSync，确保内容落到 DC
		pGdipDeleteGraphics.Call(g)
	}
}

// gpSync 在切回 GDI 前刷新 GDI+ 批次，避免 GDI 文字被其覆盖。
func gpSync() {
	if gpCur != 0 {
		pGdipFlush.Call(gpCur, 1)
	}
}

func argb(color uint32) uint32 {
	return 0xFF000000 |
		((color & 0xFF) << 16) | // COLORREF 低字节为红
		(color & 0xFF00) |
		((color >> 16) & 0xFF)
}

func argbA(color uint32, alpha uint8) uint32 {
	return (uint32(alpha) << 24) | (argb(color) & 0x00FFFFFF)
}

func gpSolid(c uint32) uintptr {
	if b, ok := gpBrushCache[c]; ok {
		return b
	}
	var b uintptr
	if st, _, _ := pGdipSolidFill.Call(uintptr(c), uintptr(unsafe.Pointer(&b))); st != 0 {
		return 0
	}
	gpBrushCache[c] = b
	return b
}

// gdiBrush 返回 GDI 回退路径的画刷，按颜色缓存。
func gdiBrush(c uint32) uintptr {
	if b, ok := gdiBrushCache[c]; ok {
		return b
	}
	b, _, _ := pCreateSolidBrush.Call(uintptr(c))
	gdiBrushCache[c] = b
	return b
}

// gpGrad 建一个竖向（上→下）线性渐变画刷，用完须调 gpGradDone。
// 该调用有 6 个参数（rect、两色、模式、wrapmode、输出画刷），不可漏传 wrapmode。
func gpGrad(r rectT, c1, c2 uint32) uintptr {
	if r.Right <= r.Left || r.Bottom <= r.Top {
		return 0
	}
	// rect 放堆上：栈上数组传 DLL 有被 runtime 搬走的风险
	rc := new([4]int32)
	rc[0], rc[1] = r.Left, r.Top
	rc[2], rc[3] = r.Right-r.Left, r.Bottom-r.Top
	var b uintptr
	// LinearGradientModeVertical = 1；WrapModeTile = 0
	st, _, _ := pGdipLineBrushRectI.Call(uintptr(unsafe.Pointer(rc)),
		uintptr(c1), uintptr(c2), 1, 0, uintptr(unsafe.Pointer(&b)))
	// 仅传 uintptr 出去时 rc 可能被 GC 回收，故调用后 KeepAlive。
	runtime.KeepAlive(rc)
	if st != 0 {
		return 0
	}
	return b
}

func gpGradDone(b uintptr) {
	if b != 0 {
		pGdipDeleteBrush.Call(b)
	}
}

// gpRoundPath 用四段圆弧拼一个圆角矩形。半径会自动夹到不超过一半宽高。
func gpRoundPath(r rectT, rad int32) uintptr {
	var p uintptr
	if st, _, _ := pGdipCreatePath.Call(0, uintptr(unsafe.Pointer(&p))); st != 0 {
		return 0
	}
	w, h := r.Right-r.Left, r.Bottom-r.Top
	if rad > w/2 {
		rad = w / 2
	}
	if rad > h/2 {
		rad = h / 2
	}
	if rad < 0 {
		rad = 0
	}
	if rad <= 0 {
		// 半径 0 时 GDI+ 圆弧不构成矩形，需显式加矩形图元
		pGdipAddPathRectI.Call(p, uintptr(r.Left), uintptr(r.Top), uintptr(w), uintptr(h))
		pGdipClosePathFigure.Call(p)
		return p
	}
	d := rad * 2
	arc := func(x, y, start int32) {
		pGdipAddPathArcI.Call(p, uintptr(x), uintptr(y), uintptr(d), uintptr(d),
			uintptr(math.Float32bits(float32(start))), uintptr(math.Float32bits(90)))
	}
	arc(r.Left, r.Top, 180)       // 左上
	arc(r.Right-d, r.Top, 270)    // 右上
	arc(r.Right-d, r.Bottom-d, 0) // 右下
	arc(r.Left, r.Bottom-d, 90)   // 左下
	pGdipClosePathFigure.Call(p)
	return p
}

// fill 纯色直角矩形
func fill(hdc uintptr, r rectT, color uint32) {
	if r.Right <= r.Left || r.Bottom <= r.Top {
		return
	}
	if gpCur != 0 {
		if b := gpSolid(argb(color)); b != 0 {
			pGdipFillRectangleI.Call(gpCur, b, uintptr(r.Left), uintptr(r.Top),
				uintptr(r.Right-r.Left), uintptr(r.Bottom-r.Top))
			return
		}
	}
	old, _, _ := pSelectObject.Call(hdc, gdiBrush(color))
	pRoundRect.Call(hdc, uintptr(r.Left), uintptr(r.Top), uintptr(r.Right), uintptr(r.Bottom), 0, 0)
	pSelectObject.Call(hdc, old)
}

// fillA 半透明直角矩形，用于覆盖。
func fillA(hdc uintptr, r rectT, color uint32, alpha uint8) {
	if r.Right <= r.Left || r.Bottom <= r.Top {
		return
	}
	if gpCur != 0 {
		if b := gpSolid(argbA(color, alpha)); b != 0 {
			pGdipFillRectangleI.Call(gpCur, b, uintptr(r.Left), uintptr(r.Top),
				uintptr(r.Right-r.Left), uintptr(r.Bottom-r.Top))
		}
		return
	}
	fill(hdc, r, color)
}

// fillRound 圆角矩形（抗锯齿）
func fillRound(hdc uintptr, r rectT, color uint32, rad int32) {
	if r.Right <= r.Left || r.Bottom <= r.Top {
		return
	}
	if gpCur != 0 {
		p := gpRoundPath(r, rad)
		if p != 0 {
			if b := gpSolid(argb(color)); b != 0 {
				pGdipFillPath.Call(gpCur, b, p)
			}
			pGdipDeletePath.Call(p)
			return
		}
	}
	old, _, _ := pSelectObject.Call(hdc, gdiBrush(color))
	pRoundRect.Call(hdc, uintptr(r.Left), uintptr(r.Top), uintptr(r.Right), uintptr(r.Bottom),
		uintptr(rad*2), uintptr(rad*2))
	pSelectObject.Call(hdc, old)
}

// fillRoundA 半透明圆角矩形
func fillRoundA(hdc uintptr, r rectT, color uint32, alpha uint8, rad int32) {
	if r.Right <= r.Left || r.Bottom <= r.Top {
		return
	}
	if gpCur != 0 {
		p := gpRoundPath(r, rad)
		if p != 0 {
			if b := gpSolid(argbA(color, alpha)); b != 0 {
				pGdipFillPath.Call(gpCur, b, p)
			}
			pGdipDeletePath.Call(p)
			return
		}
	}
	fillRound(hdc, r, color, rad)
}

// fillRectGrad 直角矩形 + 竖向渐变。
func fillRectGrad(hdc uintptr, r rectT, top, bottom uint32) {
	if r.Right <= r.Left || r.Bottom <= r.Top {
		return
	}
	if gpCur != 0 {
		p := gpRoundPath(r, 0)
		b := gpGrad(r, argb(top), argb(bottom))
		if p != 0 && b != 0 {
			pGdipFillPath.Call(gpCur, b, p)
		}
		gpGradDone(b)
		if p != 0 {
			pGdipDeletePath.Call(p)
		}
		if b != 0 {
			return
		}
	}
	fill(hdc, r, top)
}

// fillRoundGrad 圆角矩形 + 竖向渐变。
func fillRoundGrad(hdc uintptr, r rectT, top, bottom uint32, rad int32) {
	if r.Right <= r.Left || r.Bottom <= r.Top {
		return
	}
	if gpCur != 0 {
		p := gpRoundPath(r, rad)
		b := gpGrad(r, argb(top), argb(bottom))
		if p != 0 && b != 0 {
			pGdipFillPath.Call(gpCur, b, p)
		}
		gpGradDone(b)
		if p != 0 {
			pGdipDeletePath.Call(p)
		}
		if b != 0 {
			return
		}
	}
	fillRound(hdc, r, top, rad)
}

// fillRoundGlass 卡片：淡竖向渐变 + 1px 高光边。
func fillRoundGlass(hdc uintptr, r rectT, top, bottom, border uint32, rad int32) {
	fillRoundGrad(hdc, r, top, bottom, rad)
	if border != 0 {
		strokeRound(hdc, r, border, 1, rad)
	}
}

// strokeRound 圆角描边（抗锯齿）
func strokeRound(hdc uintptr, r rectT, color uint32, width int32, rad int32) {
	if r.Right <= r.Left || r.Bottom <= r.Top {
		return
	}
	if gpCur != 0 {
		p := gpRoundPath(r, rad)
		if p != 0 {
			var pen uintptr
			st, _, _ := pGdipCreatePen1.Call(uintptr(argb(color)),
				uintptr(math.Float32bits(float32(width))), 0, uintptr(unsafe.Pointer(&pen)))
			if st == 0 && pen != 0 {
				pGdipDrawPath.Call(gpCur, pen, p)
				pGdipDeletePen.Call(pen)
			}
			pGdipDeletePath.Call(p)
			return
		}
	}
	pen, _, _ := pCreatePen.Call(0, uintptr(width), uintptr(color))
	oldPen, _, _ := pSelectObject.Call(hdc, pen)
	hollow, _, _ := pGetStockObject.Call(nullBrush)
	oldBr, _, _ := pSelectObject.Call(hdc, hollow)
	pRoundRect.Call(hdc, uintptr(r.Left), uintptr(r.Top), uintptr(r.Right), uintptr(r.Bottom),
		uintptr(rad*2), uintptr(rad*2))
	pSelectObject.Call(hdc, oldBr)
	pSelectObject.Call(hdc, oldPen)
	pDeleteObject.Call(pen)
}

// fillCircle 圆（r 是外接矩形）
func fillCircle(hdc uintptr, r rectT, color uint32) {
	if r.Right <= r.Left || r.Bottom <= r.Top {
		return
	}
	if gpCur != 0 {
		if b := gpSolid(argb(color)); b != 0 {
			pGdipFillEllipseI.Call(gpCur, b, uintptr(r.Left), uintptr(r.Top),
				uintptr(r.Right-r.Left), uintptr(r.Bottom-r.Top))
			return
		}
	}
	old, _, _ := pSelectObject.Call(hdc, gdiBrush(color))
	pEllipse.Call(hdc, uintptr(r.Left), uintptr(r.Top), uintptr(r.Right), uintptr(r.Bottom))
	pSelectObject.Call(hdc, old)
}

// strokeCircle 圆圈描边。
func strokeCircle(hdc uintptr, r rectT, color uint32, width int32) {
	if r.Right <= r.Left || r.Bottom <= r.Top {
		return
	}
	if gpCur == 0 {
		return
	}
	var pen uintptr
	if st, _, _ := pGdipCreatePen1.Call(uintptr(argb(color)),
		uintptr(math.Float32bits(float32(width))), 0, uintptr(unsafe.Pointer(&pen))); st != 0 {
		return
	}
	pGdipDrawEllipseI.Call(gpCur, pen, uintptr(r.Left), uintptr(r.Top),
		uintptr(r.Right-r.Left), uintptr(r.Bottom-r.Top))
	pGdipDeletePen.Call(pen)
}

// vLine 一条竖线。
func vLine(hdc uintptr, x, y1, y2 int32, color uint32, width int32) {
	if y2 <= y1 {
		return
	}
	if gpCur != 0 {
		var pen uintptr
		if st, _, _ := pGdipCreatePen1.Call(uintptr(argb(color)),
			uintptr(math.Float32bits(float32(width))), 0, uintptr(unsafe.Pointer(&pen))); st != 0 {
			return
		}
		pGdipDrawLineI.Call(gpCur, pen, uintptr(x), uintptr(y1), uintptr(x), uintptr(y2))
		pGdipDeletePen.Call(pen)
		return
	}
	pen, _, _ := pCreatePen.Call(0, uintptr(width), uintptr(color))
	old, _, _ := pSelectObject.Call(hdc, pen)
	pMoveToEx.Call(hdc, uintptr(x), uintptr(y1), 0)
	pLineTo.Call(hdc, uintptr(x), uintptr(y2))
	pSelectObject.Call(hdc, old)
	pDeleteObject.Call(pen)
}

// line 任意两点之间的直线。
func line(hdc uintptr, x1, y1, x2, y2 int32, color uint32, width int32) {
	if gpCur != 0 {
		var pen uintptr
		if st, _, _ := pGdipCreatePen1.Call(uintptr(argb(color)),
			uintptr(math.Float32bits(float32(width))), 0, uintptr(unsafe.Pointer(&pen))); st == 0 && pen != 0 {
			pGdipDrawLineI.Call(gpCur, pen, uintptr(x1), uintptr(y1), uintptr(x2), uintptr(y2))
			pGdipDeletePen.Call(pen)
			return
		}
	}
	// 退回 GDI：GDI 直线仅支持水平/垂直/45°
	pen, _, _ := pCreatePen.Call(0, uintptr(width), uintptr(color))
	old, _, _ := pSelectObject.Call(hdc, pen)
	pMoveToEx.Call(hdc, uintptr(x1), uintptr(y1), 0)
	pLineTo.Call(hdc, uintptr(x2), uintptr(y2))
	pSelectObject.Call(hdc, old)
	pDeleteObject.Call(pen)
}

// hLine 一条横线。
func hLine(hdc uintptr, x1, x2, y int32, color uint32, width int32) {
	if x2 <= x1 {
		return
	}
	if gpCur != 0 {
		var pen uintptr
		if st, _, _ := pGdipCreatePen1.Call(uintptr(argb(color)),
			uintptr(math.Float32bits(float32(width))), 0, uintptr(unsafe.Pointer(&pen))); st != 0 {
			return
		}
		pGdipDrawLineI.Call(gpCur, pen, uintptr(x1), uintptr(y), uintptr(x2), uintptr(y))
		pGdipDeletePen.Call(pen)
		return
	}
	old, _, _ := pSelectObject.Call(hdc, gdiBrush(color))
	rc := rectT{x1, y, x2, y + width}
	pRoundRect.Call(hdc, uintptr(rc.Left), uintptr(rc.Top), uintptr(rc.Right), uintptr(rc.Bottom), 0, 0)
	pSelectObject.Call(hdc, old)
}

// shadowRound 用几圈由深到浅的半透明圆角矩形叠出阴影。
func shadowRound(hdc uintptr, r rectT, rad int32, alpha uint8) {
	if gpCur == 0 {
		return
	}
	for i := int32(4); i >= 1; i-- {
		rr := rectT{r.Left - i, r.Top + i, r.Right + i, r.Bottom + i + 1}
		a := uint8(int(alpha) / (int(i) + 1))
		if a == 0 {
			continue
		}
		p := gpRoundPath(rr, rad+i)
		if p == 0 {
			continue
		}
		if b := gpSolid(argbA(0x000000, a)); b != 0 {
			pGdipFillPath.Call(gpCur, b, p)
		}
		pGdipDeletePath.Call(p)
	}
}
