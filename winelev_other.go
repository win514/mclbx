//go:build !windows

package main

// 非 Windows 平台无 UAC：保留同名实现以保证跨平台编译。

import (
	"fmt"
	"os/exec"
)

func isElevated() bool { return false }

func elevateSelf() error {
	return fmt.Errorf("当前平台没有 UAC；需要权限请用 sudo 重新运行")
}

func runCmd(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).CombinedOutput()
	return string(out), err
}
