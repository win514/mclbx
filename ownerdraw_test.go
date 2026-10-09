//go:build windows

package main

// ownerdraw_test.go 验证 WM_ENABLE 改掉 BUTTON 类型位后，keepOwnerDraw 能补回 BS_OWNERDRAW。
// BUTTON 收到 WM_ENABLE 会把类型位由 BS_OWNERDRAW(0x0B) 改为 0x00/0x01。

import (
	"syscall"
	"testing"
	"unsafe"
)

// wsPopupT 测试用的无装饰父窗口样式（WS_POPUP）。
const wsPopupT = 0x80000000

func TestKeepOwnerDrawAfterEnable(t *testing.T) {
	cls, _ := syscall.UTF16PtrFromString("BUTTON")
	txt, _ := syscall.UTF16PtrFromString("probe")
	hInst, _, _ := pGetModuleHandleW.Call(0)

	// WS_CHILD 控件必须挂在父窗口下，父窗口填 0 会被系统拒绝
	pcls, _ := syscall.UTF16PtrFromString("STATIC")
	parent, _, perr := pCreateWindowExW.Call(0, uintptr(unsafe.Pointer(pcls)), 0,
		uintptr(wsPopupT), 0, 0, 10, 10, 0, 0, hInst, 0)
	if parent == 0 {
		t.Skipf("建不出父窗口，跳过：%v", perr)
	}
	defer pDestroyWindow.Call(parent)

	btn, _, err := pCreateWindowExW.Call(0,
		uintptr(unsafe.Pointer(cls)), uintptr(unsafe.Pointer(txt)),
		uintptr(wsChild|wsVisible|bsOwnerDraw), 0, 0, 10, 10,
		parent, 0, hInst, 0)
	if btn == 0 {
		t.Skipf("建不出按钮，跳过：%v", err)
	}
	defer pDestroyWindow.Call(btn)

	if got := windowStyle(btn) & 0x0F; got != bsOwnerDraw {
		t.Fatalf("刚建出来的按钮类型位 = %#x，期望 BS_OWNERDRAW(%#x)", got, bsOwnerDraw)
	}

	// 禁用一次（界面上每次 poll 都会执行）
	pSendMessageW.Call(btn, wmEnable, 0, 0)
	after := windowStyle(btn) & 0x0F
	if after == bsOwnerDraw {
		t.Logf("这个系统上 WM_ENABLE 没有改写类型位（行为变了？），下面的断言仍然有意义")
	} else {
		t.Logf("确认了平台行为：WM_ENABLE 之后类型位 = %#x（自绘属性被丢掉）", after)
	}

	keepOwnerDraw(btn)
	if got := windowStyle(btn) & 0x0F; got != bsOwnerDraw {
		t.Fatalf("keepOwnerDraw 之后类型位 = %#x，期望补回 BS_OWNERDRAW(%#x)", got, bsOwnerDraw)
	}

	// 重新启用后仍须保持
	pSendMessageW.Call(btn, wmEnable, 1, 0)
	keepOwnerDraw(btn)
	if got := windowStyle(btn) & 0x0F; got != bsOwnerDraw {
		t.Fatalf("重新启用之后类型位 = %#x，期望 BS_OWNERDRAW(%#x)", got, bsOwnerDraw)
	}
}
