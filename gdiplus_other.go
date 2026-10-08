//go:build !windows

package main

// gpStop 非 Windows 平台为空实现，保证跨平台可编译。
func gpStop() {}
