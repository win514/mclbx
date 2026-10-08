//go:build windows

package main

// winjob_windows.go 让子进程随控制台一起退出：把子进程放进带 KILL_ON_JOB_CLOSE 的 Job Object。

import (
	"sync"
	"syscall"
	"unsafe"
)

var (
	jobK32                 = syscall.NewLazyDLL("kernel32.dll")
	pCreateJobObjectW      = jobK32.NewProc("CreateJobObjectW")
	pSetInformationJobObj  = jobK32.NewProc("SetInformationJobObject")
	pAssignProcessToJobObj = jobK32.NewProc("AssignProcessToJobObject")
	pOpenProcess           = jobK32.NewProc("OpenProcess")
	pCloseHandle           = jobK32.NewProc("CloseHandle")
)

const (
	jobObjectExtendedLimitInformation = 9
	jobObjectLimitKillOnJobClose      = 0x2000
	processTerminate                  = 0x0001
	processSetQuota                   = 0x0100
)

type ioCountersT struct {
	ReadOperationCount  uint64
	WriteOperationCount uint64
	OtherOperationCount uint64
	ReadTransferCount   uint64
	WriteTransferCount  uint64
	OtherTransferCount  uint64
}

type jobBasicLimitT struct {
	PerProcessUserTimeLimit int64
	PerJobUserTimeLimit     int64
	LimitFlags              uint32
	MinimumWorkingSetSize   uintptr
	MaximumWorkingSetSize   uintptr
	ActiveProcessLimit      uint32
	Affinity                uintptr
	PriorityClass           uint32
	SchedulingClass         uint32
}

type jobExtendedLimitT struct {
	BasicLimitInformation jobBasicLimitT
	IoInfo                ioCountersT
	ProcessMemoryLimit    uintptr
	JobMemoryLimit        uintptr
	PeakProcessMemoryUsed uintptr
	PeakJobMemoryUsed     uintptr
}

var (
	jobOnce   sync.Once
	jobHandle uintptr
)

// jobForChildren 建一次 Job 并一直持有句柄（句柄活着即子进程活着）。
func jobForChildren() uintptr {
	jobOnce.Do(func() {
		// 结构体布局须与系统头文件一致（x64 为 144 字节），不符则跳过该保护。
		if unsafe.Sizeof(jobExtendedLimitT{}) != 144 {
			logf("Job Object 结构体尺寸不符（%d），跳过孤儿进程兜底", unsafe.Sizeof(jobExtendedLimitT{}))
			return
		}
		h, _, _ := pCreateJobObjectW.Call(0, 0)
		if h == 0 {
			return
		}
		info := jobExtendedLimitT{}
		info.BasicLimitInformation.LimitFlags = jobObjectLimitKillOnJobClose
		if r, _, _ := pSetInformationJobObj.Call(
			h, jobObjectExtendedLimitInformation,
			uintptr(unsafe.Pointer(&info)), unsafe.Sizeof(info),
		); r == 0 {
			pCloseHandle.Call(h)
			return
		}
		jobHandle = h
	})
	return jobHandle
}

// adoptChild 把刚起来的子进程放进 Job，失败不影响其运行。
func adoptChild(pid int) {
	job := jobForChildren()
	if job == 0 || pid <= 0 {
		return
	}
	h, _, _ := pOpenProcess.Call(processSetQuota|processTerminate, 0, uintptr(uint32(pid)))
	if h == 0 {
		return
	}
	defer pCloseHandle.Call(h)
	if r, _, _ := pAssignProcessToJobObj.Call(job, h); r == 0 {
		logf("子进程 %d 未能纳入 Job（孤儿兜底失效，功能不受影响）", pid)
	}
}
