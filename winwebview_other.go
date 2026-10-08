//go:build !windows

package main

import "fmt"

// 非 Windows 平台没有内置窗口，控制台界面统一走浏览器。
func runNativeWindow(url, title string) error {
	return fmt.Errorf("当前平台不支持内置窗口")
}

func hideConsoleWindow() {}

// 非 Windows 无内置窗口：关闭请求由调用方自行收尾。
func requestClose() bool { return false }

// 非 Windows 无 FindWindow，一律返回 false，由调用方走通用端口探测。
func findExistingGuiWindow() bool { return false }

// 体检用：非 Windows 平台无内置窗口。
func webView2Available() bool { return false }
