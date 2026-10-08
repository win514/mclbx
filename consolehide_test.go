//go:build windows

package main

// consolehide_test.go —— 校验任务子进程不会弹出可见的控制台窗口。

import (
	"os/exec"
	"syscall"
	"testing"
	"time"
	"unsafe"
)

var (
	tUser32          = syscall.NewLazyDLL("user32.dll")
	tEnumWindows     = tUser32.NewProc("EnumWindows")
	tIsWindowVisible = tUser32.NewProc("IsWindowVisible")
	tGetClassNameW   = tUser32.NewProc("GetClassNameW")
)

// visibleConsoleWindows 返回系统中当前可见的控制台窗口数。
func visibleConsoleWindows() int {
	n := 0
	cb := syscall.NewCallback(func(hwnd, _ uintptr) uintptr {
		if v, _, _ := tIsWindowVisible.Call(hwnd); v == 0 {
			return 1 // 继续枚举
		}
		var buf [128]uint16
		tGetClassNameW.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
		if syscall.UTF16ToString(buf[:]) == consoleClassicWnd {
			n++
		}
		return 1
	})
	tEnumWindows.Call(cb, 0)
	return n
}

func TestHiddenConsoleProcAttrShape(t *testing.T) {
	a := hiddenConsoleProcAttr()
	if a == nil {
		t.Fatal("hiddenConsoleProcAttr 返回了 nil：子进程会继承不到" +
			"任何控制台约定，Windows 会给它新开一个")
	}
	if !a.HideWindow {
		t.Error("没有 HideWindow：子进程的控制台窗口不会被隐藏")
	}
	if a.CreationFlags != createNoWindow {
		t.Errorf("CreationFlags = %#x，想要 CREATE_NO_WINDOW(%#x)；"+
			"少了它就是一个可见的黑框，换成 DETACHED_PROCESS 又会让孙进程各弹一个",
			a.CreationFlags, uintptr(createNoWindow))
	}
}

func TestTaskChildDoesNotFlashAConsoleWindow(t *testing.T) {
	before := visibleConsoleWindows()

	// 用 cmd 跑 ping：任务本身是子进程，还会再拉系统命令当孙进程。
	cmd := exec.Command("cmd.exe", "/c", "ping", "-n", "3", "127.0.0.1")
	cmd.SysProcAttr = hiddenConsoleProcAttr()
	if err := cmd.Start(); err != nil {
		t.Skipf("起不了子进程，跳过：%v", err)
	}

	worst := before
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if n := visibleConsoleWindows(); n > worst {
			worst = n
		}
		time.Sleep(40 * time.Millisecond)
	}
	_ = cmd.Wait()

	if worst != before {
		t.Fatalf("子进程跑的过程中多出了可见的控制台窗口：之前 %d 个，最多 %d 个", before, worst)
	}
}
