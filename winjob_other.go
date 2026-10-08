//go:build !windows

package main

// adoptChild 在非 Windows 平台为空实现：无 Job Object，仅保证代码可编译。
func adoptChild(pid int) {}
