//go:build windows

package main

// winelev.go Windows 提权与执行外部命令，仅在需要时按需提权。
// 需要管理员权限的只有修改防火墙：到该步骤再以 runas 重启自身。

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"unsafe"
)

var (
	kernel32El = syscall.NewLazyDLL("kernel32.dll")
	advapi32El = syscall.NewLazyDLL("advapi32.dll")
	shell32El  = syscall.NewLazyDLL("shell32.dll")

	pGetCurrentProcess   = kernel32El.NewProc("GetCurrentProcess")
	pCloseHandleEl       = kernel32El.NewProc("CloseHandle")
	pOpenProcessToken    = advapi32El.NewProc("OpenProcessToken")
	pGetTokenInformation = advapi32El.NewProc("GetTokenInformation")
	pShellExecuteExW     = shell32El.NewProc("ShellExecuteExW")
)

const (
	tokenQuery     = 0x0008
	tokenElevation = 20
	seeMaskNoClose = 0x00000040
	swShowNormal   = 1
)

type shellExecuteInfoW struct {
	CbSize       uint32
	FMask        uint32
	Hwnd         uintptr
	LpVerb       *uint16
	LpFile       *uint16
	LpParameters *uint16
	LpDirectory  *uint16
	NShow        int32
	HInstApp     uintptr
	LpIDList     uintptr
	LpClass      *uint16
	HkeyClass    uintptr
	DwHotKey     uint32
	HIcon        uintptr
	HProcess     uintptr
}

// isElevated 通过 GetTokenInformation(TokenElevation) 判断当前是否为管理员。
func isElevated() bool {
	hProc, _, _ := pGetCurrentProcess.Call()
	var token uintptr
	if r, _, _ := pOpenProcessToken.Call(hProc, tokenQuery, uintptr(unsafe.Pointer(&token))); r == 0 {
		return false
	}
	defer pCloseHandleEl.Call(token)

	var elevated uint32
	var ret uint32
	r, _, _ := pGetTokenInformation.Call(
		token, tokenElevation,
		uintptr(unsafe.Pointer(&elevated)), unsafe.Sizeof(elevated),
		uintptr(unsafe.Pointer(&ret)),
	)
	if r == 0 {
		return false
	}
	return elevated != 0
}

// elevateSelf 用同一份参数把自己重新拉起来，这次带管理员令牌（会弹一次 UAC）。
func elevateSelf() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	// 原样传递当前命令行参数（不含 exe 自身）
	params := ""
	if len(os.Args) > 1 {
		params = joinArgs(os.Args[1:])
	}
	verb, _ := syscall.UTF16PtrFromString("runas")
	file, _ := syscall.UTF16PtrFromString(exe)
	par, _ := syscall.UTF16PtrFromString(params)
	dir, _ := syscall.UTF16PtrFromString(mustGetwd())

	sei := shellExecuteInfoW{
		CbSize:       uint32(unsafe.Sizeof(shellExecuteInfoW{})),
		FMask:        seeMaskNoClose,
		LpVerb:       verb,
		LpFile:       file,
		LpParameters: par,
		LpDirectory:  dir,
		NShow:        swShowNormal,
	}
	if r, _, err2 := pShellExecuteExW.Call(uintptr(unsafe.Pointer(&sei))); r == 0 {
		return fmt.Errorf("申请管理员权限失败（可能被用户在 UAC 里点了取消）：%v", err2)
	}
	fmt.Println(" 已用管理员权限重新启动。")
	return nil
}

func mustGetwd() string {
	d, err := os.Getwd()
	if err != nil {
		return ""
	}
	return d
}

// joinArgs 保守加引号：仅当参数含空格、制表符或引号时处理。
func joinArgs(args []string) string {
	out := make([]string, 0, len(args))
	for _, a := range args {
		if strings.ContainsAny(a, " \t\"") {
			out = append(out, `"`+strings.ReplaceAll(a, `"`, `\"`)+`"`)
			continue
		}
		out = append(out, a)
	}
	return strings.Join(out, " ")
}

func runCmd(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}
