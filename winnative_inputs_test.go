//go:build windows

package main

// winnative_inputs_test.go —— 校验 inputs() 记录勾选项的未勾状态。

import (
	"syscall"
	"testing"
	"unsafe"
)

// newFieldHost 建一个不带装饰的父窗口用于挂测试控件
func newFieldHost(t *testing.T) uintptr {
	t.Helper()
	hInst, _, _ := pGetModuleHandleW.Call(0)
	pcls, _ := syscall.UTF16PtrFromString("STATIC")
	parent, _, perr := pCreateWindowExW.Call(0, uintptr(unsafe.Pointer(pcls)), 0,
		uintptr(wsPopupT), 0, 0, 10, 10, 0, 0, hInst, 0)
	if parent == 0 {
		t.Skipf("建不出父窗口，跳过：%v", perr)
	}
	t.Cleanup(func() { pDestroyWindow.Call(parent) })
	return parent
}

// mkTestCheck 建一个勾选框，按 checked 设好状态
func mkTestCheck(t *testing.T, parent uintptr, label string, checked bool) uintptr {
	t.Helper()
	cls, _ := syscall.UTF16PtrFromString("BUTTON")
	txt, _ := syscall.UTF16PtrFromString(label)
	hInst, _, _ := pGetModuleHandleW.Call(0)
	h, _, err := pCreateWindowExW.Call(0, uintptr(unsafe.Pointer(cls)), uintptr(unsafe.Pointer(txt)),
		uintptr(wsChild|wsVisible|wsTabStop|bsCheckbox), 0, 0, 10, 10, parent, 0, hInst, 0)
	if h == 0 {
		t.Skipf("建不出勾选框，跳过：%v", err)
	}
	t.Cleanup(func() { pDestroyWindow.Call(h) })
	if checked {
		pSendMessageW.Call(h, bmSetCheck, 1, 0)
	}
	return h
}

// findTaskKey 在任务表里找一个带勾选项的任务
func findTaskKey(t *testing.T, want string) (int, guiTask) {
	t.Helper()
	for i, task := range guiTasks {
		if task.Key != want {
			continue
		}
		for _, f := range task.Fields {
			if f.Kind == "check" {
				return i, task
			}
		}
	}
	t.Skipf("任务表里没有带勾选项的 %q", want)
	return -1, guiTask{}
}

// 「没勾」必须写成空串，不能省略
func TestInputsRecordsUncheckedBoxes(t *testing.T) {
	withTempConfig(t) // 本用例会写盘，避免落到真实用户目录
	parent := newFieldHost(t)
	sel, task := findTaskKey(t, "probe")

	u := &nativeUI{sel: sel, hwnd: parent, dpi: 96}
	for i, f := range task.Fields {
		if f.Kind != "check" {
			continue
		}
		// 第一个勾上、第二个不勾，两种都要被记录
		checked := f.ID == "quick"
		u.fields = append(u.fields, natFieldCtl{
			index: i, hwnd: mkTestCheck(t, parent, f.Label, checked), isBool: true,
		})
	}
	if len(u.fields) < 2 {
		t.Skip("这个任务上的勾选项少于两个，测不出对比")
	}

	in := u.inputs()
	for _, f := range task.Fields {
		if f.Kind != "check" {
			continue
		}
		v, ok := in[f.ID]
		if !ok {
			t.Errorf("勾选项 %q 没出现在输入里 —— 只记「勾上」的话，用户取消勾选就再也改不掉了", f.ID)
			continue
		}
		want := ""
		if f.ID == "quick" {
			want = "1"
		}
		if v != want {
			t.Errorf("勾选项 %q 读回来是 %q，期望 %q", f.ID, v, want)
		}
	}

	// 存盘并清缓存重开后，勾选状态须保持
	rememberFieldValues(task.Key, in)
	resetGuiConfigCache()
	for _, f := range task.Fields {
		if f.Kind != "check" {
			continue
		}
		want := f.ID == "quick"
		if got := fieldChecked(task.Key, f); got != want {
			t.Errorf("重开之后 %q 的勾选状态是 %v，期望 %v", f.ID, got, want)
		}
	}
}
