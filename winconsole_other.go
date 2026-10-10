//go:build !windows

package main

// winconsole_other.go 非 Windows 下无需挂终端：进程本来就有标准输出。
func attachConsole() bool { return false }
