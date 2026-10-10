//go:build windows

package main

// winconsole_windows.go 让不创建控制台的产物在命令行下照常输出。
//
// 产物按 -H=windowsgui 构建：双击时不再多出一个控制台窗口。
// 从终端运行时，标准输出已由调用方接好（终端、管道或文件），直接用即可；
// 只有在完全没有可用输出句柄时才挂到调用方的终端上。
// 双击启动既没有可用的输出句柄，也没有可挂的终端，因此不会开窗口。

import (
	"os"
	"syscall"
)

var (
	kernel32Con    = syscall.NewLazyDLL("kernel32.dll")
	pAttachConsole = kernel32Con.NewProc("AttachConsole")
	pGetStdHandle  = kernel32Con.NewProc("GetStdHandle")
	pGetFileType   = kernel32Con.NewProc("GetFileType")
)

const (
	// AttachConsole 的固定取值 (DWORD)-1：挂到父进程的控制台。
	attachParentProcess = uintptr(0xFFFFFFFF)
	// GetStdHandle 的固定取值 (DWORD)-11：标准输出。
	stdOutputHandle = uintptr(0xFFFFFFF5)
	// GetFileType 的取值：0 表示句柄无效或不可用。
	fileTypeUnknown = 0
)

// stdoutUsable 判断调用方是否已经接好了标准输出。
func stdoutUsable() bool {
	h, _, _ := pGetStdHandle.Call(stdOutputHandle)
	if h == 0 || h == ^uintptr(0) {
		return false
	}
	t, _, _ := pGetFileType.Call(h)
	return t != fileTypeUnknown
}

// attachConsole 在没有任何可用输出句柄时挂到调用方的终端上；其余情况不动标准流。
func attachConsole() bool {
	if stdoutUsable() {
		return false
	}
	if r, _, _ := pAttachConsole.Call(attachParentProcess); r == 0 {
		return false
	}
	if f, err := os.OpenFile("CONOUT$", os.O_WRONLY, 0); err == nil {
		os.Stdout, os.Stderr = f, f
	}
	if f, err := os.OpenFile("CONIN$", os.O_RDONLY, 0); err == nil {
		os.Stdin = f
	}
	return true
}
