// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.

//go:build windows

package app

import (
	"fmt"
	"os/exec"
	"sync"
	"syscall"
	"unsafe"
)

// Job Object 用于终止整棵进程树。taskkill /T 依赖 Win32 父子链枚举子进程，
// 但 Git Bash 的子 shell（括号子 shell、后台任务等）在 Windows 上通过 msys
// fork 模拟创建，Win32 父链经常断裂（孙进程的 ppid 指向已退出的中间进程），
// 导致 taskkill 漏杀 scp / sleep 等孙进程。Job Object 是内核级归属：根进程
// 入组后所有后代自动继承，TerminateJobObject 无论父链是否完整都能杀光。

var (
	processJobsMu sync.Mutex
	processJobs   = map[int]syscall.Handle{} // 根进程 pid -> job object handle
)

var (
	kernel32                     = syscall.NewLazyDLL("kernel32.dll")
	procCreateJobObjectW         = kernel32.NewProc("CreateJobObjectW")
	procSetInformationJobObject  = kernel32.NewProc("SetInformationJobObject")
	procAssignProcessToJobObject = kernel32.NewProc("AssignProcessToJobObject")
	procTerminateJobObject       = kernel32.NewProc("TerminateJobObject")
	procCloseHandle              = kernel32.NewProc("CloseHandle")
	procOpenProcess              = kernel32.NewProc("OpenProcess")
)

const (
	jobObjectExtendedLimitInformation = 9
	// JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE: 最后一个 job handle 关闭时，自动
	// 终止 job 内所有进程。作为进程退出后的兜底，清理残留的孤儿孙进程。
	jobObjectLimitKillOnJobClose = 0x2000
	// AssignProcessToJobObject 所需的进程访问权限。
	processSetQuota  = 0x0100
	processTerminate = 0x0001
)

func hideCommandWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
}

// prepareServiceCommand 配置子进程：隐藏窗口、独立进程组，并创建一个
// KILL_ON_JOB_CLOSE 的 Job Object。返回的 job handle 需在 cmd.Start() 后
// 通过 registerProcessJob 注册（创建失败时返回 0，调用方回退旧行为）。
func prepareServiceCommand(cmd *exec.Cmd) uintptr {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000 | syscall.CREATE_NEW_PROCESS_GROUP}
	job, err := newJobObject()
	if err != nil {
		return 0
	}
	return uintptr(job)
}

func newJobObject() (syscall.Handle, error) {
	h, _, err := procCreateJobObjectW.Call(0, 0)
	if h == 0 {
		return 0, fmt.Errorf("CreateJobObjectW failed: %v", err)
	}
	job := syscall.Handle(h)
	// 布局对应 JOBOBJECT_EXTENDED_LIMIT_INFORMATION（64 位自然对齐，字段顺序
	// 与 C 结构一致）。仅设置 LimitFlags，其余保持零值。
	type jobExtendedLimitInfo struct {
		basic struct {
			perProcessUserTimeLimit uint64
			perJobUserTimeLimit     uint64
			limitFlags              uint32
			minimumWorkingSetSize   uintptr
			maximumWorkingSetSize   uintptr
			activeProcessLimit      uint32
			affinity                uintptr
			priorityClass           uint32
			schedulingClass         uint32
		}
		ioCounters            [6]uint64
		processMemoryLimit    uintptr
		jobMemoryLimit        uintptr
		peakProcessMemoryUsed uintptr
		peakJobMemoryUsed     uintptr
	}
	info := new(jobExtendedLimitInfo)
	info.basic.limitFlags = jobObjectLimitKillOnJobClose
	r, _, err := procSetInformationJobObject.Call(
		uintptr(job),
		uintptr(jobObjectExtendedLimitInformation),
		uintptr(unsafe.Pointer(info)),
		unsafe.Sizeof(*info),
	)
	if r == 0 {
		procCloseHandle.Call(uintptr(job))
		return 0, fmt.Errorf("SetInformationJobObject failed: %v", err)
	}
	return job, nil
}

// registerProcessJob 把已启动的根进程放入 job 并登记。失败（例如进程已被
// 其他 job 接管）时关闭 handle 并返回错误，调用方可忽略以回退 taskkill。
func registerProcessJob(pid int, job uintptr) error {
	if job == 0 {
		return nil
	}
	// AssignProcessToJobObject 需要进程句柄而非 PID：先用 OpenProcess 按
	// PID 打开（PROCESS_SET_QUOTA | PROCESS_TERMINATE 即所需权限），用完
	// 立即关闭。
	hProc, _, err := procOpenProcess.Call(processSetQuota|processTerminate, 0, uintptr(pid))
	if hProc == 0 {
		procCloseHandle.Call(job)
		return fmt.Errorf("OpenProcess(%d) failed: %v", pid, err)
	}
	defer procCloseHandle.Call(hProc)
	r, _, err := procAssignProcessToJobObject.Call(job, hProc)
	if r == 0 {
		procCloseHandle.Call(job)
		return fmt.Errorf("AssignProcessToJobObject(%d) failed: %v", pid, err)
	}
	processJobsMu.Lock()
	processJobs[pid] = syscall.Handle(job)
	processJobsMu.Unlock()
	return nil
}

// unregisterProcessJob 移除登记并关闭 job handle。KILL_ON_JOB_CLOSE 会在此
// 时自动终止 job 内仍存活的进程，兜底清理进程退出后残留的孤儿孙进程。
func unregisterProcessJob(pid int) {
	processJobsMu.Lock()
	if job, ok := processJobs[pid]; ok {
		delete(processJobs, pid)
		procCloseHandle.Call(uintptr(job))
	}
	processJobsMu.Unlock()
}

// discardProcessJob 关闭一个尚未注册的 job handle（cmd.Start 失败时使用）。
func discardProcessJob(job uintptr) {
	if job != 0 {
		procCloseHandle.Call(job)
	}
}

// gracefulStopProcessTree 尽力触发目标树的优雅退出：taskkill 不带 /F 只对
// 有窗口的进程投递 WM_CLOSE；无窗口控制台进程（dev server 常态）会立即失败
// 返回，属预期行为——宽限等待随后照常进行，超时由 stopProcessTree 强杀。
// 不用 GenerateConsoleCtrlEvent：它要求调用方 AttachConsole 到子进程的
// 控制台且 msys 层信号转发不可靠，误配时可能波及 Ally 自身。
func gracefulStopProcessTree(pid int) error {
	if pid <= 0 {
		return fmt.Errorf("invalid pid: %d", pid)
	}
	cmd := exec.Command("taskkill.exe", "/PID", fmt.Sprintf("%d", pid), "/T")
	hideCommandWindow(cmd)
	_, _ = cmd.CombinedOutput()
	return nil
}

// stopProcessTree 强杀整棵进程树：优先 Job Object（内核级归属，父链断裂也
// 能杀光），失败回退 taskkill /T /F。
func stopProcessTree(pid int) error {
	if pid <= 0 {
		return fmt.Errorf("invalid pid: %d", pid)
	}
	processJobsMu.Lock()
	job, ok := processJobs[pid]
	processJobsMu.Unlock()
	if ok {
		r, _, err := procTerminateJobObject.Call(uintptr(job), 1)
		if r != 0 {
			return nil
		}
		// 终止失败时回退到 taskkill（job handle 随后由 unregister 关闭）。
		return fmt.Errorf("TerminateJobObject(%d) failed: %v", pid, err)
	}
	// 无 job（创建失败或进程已退出）时回退旧行为。
	cmd := exec.Command("taskkill.exe", "/PID", fmt.Sprintf("%d", pid), "/T", "/F")
	hideCommandWindow(cmd)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("taskkill failed: %w: %s", err, string(out))
	}
	return nil
}
