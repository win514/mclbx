//go:build !windows

package main

import (
	"errors"
	"syscall"
)

// 非 Windows 没有原生界面，永远返回错误，cmdGui 据此落到网页版分支。
func runNativeUI(ctl *guiController) error {
	return errors.New("这个平台上没有原生界面，用网页版")
}

// 非 Windows 无控制台子系统，这两件事什么都不用做。
func detachConsole() {}

func hiddenConsoleProcAttr() *syscall.SysProcAttr { return nil }
