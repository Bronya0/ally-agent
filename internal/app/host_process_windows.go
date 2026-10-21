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
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Job Object 用于终止整棵进程树。taskkill /T 依赖 Win32 父子链枚举子进程，
// 但 Git Bash 的子 shell（括号子 shell、后台任务等）在 Windows 上通过 msys
// fork 模拟创建，Win32 父链经常断裂（孙进程的 ppid 指向已退出的中间进程），
// 导致 taskkill 漏杀 scp / sleep 等孙进程。Job Object 是内核级归属：根进程
// 入组后所有后代自动继承，TerminateJobObject 无论父链是否完整都能杀光。

var (
	processJobsMu sync.Mutex
	processJobs   = map[int]*processJob{} // 根进程 pid -> job object handle
)

// A stop closure holds this identity, never a PID that may be reused. Closing
// and terminating share the same lock so a recycled handle cannot be touched.
type processJob struct {
	mu     sync.Mutex
	handle syscall.Handle
}

func (j *processJob) stop() error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.handle == 0 {
		return nil // last handle was closed: kill-on-close applied
	}
	r, _, err := procTerminateJobObject.Call(uintptr(j.handle), 1)
	if r == 0 {
		return fmt.Errorf("TerminateJobObject failed: %v", err)
	}
	deadline := time.Now().Add(serviceForceKillConfirmWait)
	for {
		var info struct {
			Times                                                            [4]int64
			PageFaults, TotalProcesses, ActiveProcesses, TerminatedProcesses uint32
		}
		err := windows.QueryInformationJobObject(windows.Handle(j.handle), windows.JobObjectBasicAccountingInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info)), nil)
		if err != nil {
			return fmt.Errorf("confirm job exit: %w", err)
		}
		if info.ActiveProcesses == 0 {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("process job still has %d active processes", info.ActiveProcesses)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func serviceProcessStop(pid int) (func() error, error) {
	processJobsMu.Lock()
	j := processJobs[pid]
	processJobsMu.Unlock()
	if j == nil {
		return nil, fmt.Errorf("no owned process job for pid %d", pid)
	}
	return j.stop, nil
}

// Start suspended so even the fastest shell cannot spawn untracked children.
// Ownership failure is fatal: never silently degrade to PID-based taskkill.
func startTrackedCommand(cmd *exec.Cmd) error {
	job := prepareServiceCommand(cmd)
	if job == 0 {
		return fmt.Errorf("cannot create process job")
	}
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_SUSPENDED
	// Context cancellation must not race ahead of job assignment. Publish the
	// exact job identity before allowing os/exec's cancellation goroutine through.
	ready := make(chan struct{})
	release := sync.OnceFunc(func() { close(ready) })
	defer release()
	var stopOwned func() error
	if cmd.Cancel != nil {
		cmd.Cancel = func() error {
			<-ready
			if stopOwned != nil {
				return stopOwned()
			}
			return cmd.Process.Kill()
		}
	}
	if err := cmd.Start(); err != nil {
		discardProcessJob(job)
		return err
	}
	if err := registerProcessJob(cmd.Process.Pid, job); err != nil {
		release()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return err
	}
	stopOwned, _ = serviceProcessStop(cmd.Process.Pid)
	if err := resumeTrackedProcess(cmd.Process.Pid); err != nil {
		release()
		unregisterProcessJob(cmd.Process.Pid)
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return err
	}
	return nil
}

func resumeTrackedProcess(pid int) error {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPTHREAD, 0)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(snapshot)
	entry := windows.ThreadEntry32{Size: uint32(unsafe.Sizeof(windows.ThreadEntry32{}))}
	for err = windows.Thread32First(snapshot, &entry); err == nil; err = windows.Thread32Next(snapshot, &entry) {
		if entry.OwnerProcessID != uint32(pid) {
			continue
		}
		thread, err := windows.OpenThread(windows.THREAD_SUSPEND_RESUME, false, entry.ThreadID)
		if err != nil {
			return err
		}
		_, err = windows.ResumeThread(thread)
		windows.CloseHandle(thread)
		return err
	}
	return fmt.Errorf("cannot find suspended thread for pid %d: %w", pid, err)
}

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
// 其他 job 接管）时关闭 handle 并返回错误；调用方不得再次关闭同一 handle。
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
	processJobs[pid] = &processJob{handle: syscall.Handle(job)}
	processJobsMu.Unlock()
	return nil
}

// unregisterProcessJob 移除登记并关闭 job handle。KILL_ON_JOB_CLOSE 会在此
// 时自动终止 job 内仍存活的进程，兜底清理进程退出后残留的孤儿孙进程。
func unregisterProcessJob(pid int) {
	processJobsMu.Lock()
	if job, ok := processJobs[pid]; ok {
		delete(processJobs, pid)
		job.mu.Lock()
		procCloseHandle.Call(uintptr(job.handle))
		job.handle = 0
		job.mu.Unlock()
	}
	processJobsMu.Unlock()
}

// discardProcessJob 关闭一个尚未注册的 job handle（cmd.Start 失败时使用）。
func discardProcessJob(job uintptr) {
	if job != 0 {
		procCloseHandle.Call(job)
	}
}

// reapProcessGroupLeftovers 在 Windows 上无事可做：整棵树的回收交给 Job
// Object —— unregisterProcessJob 关掉 job handle 时 KILL_ON_JOB_CLOSE 会连带
// 终止 job 内仍存活的进程，不存在 Unix 那种需要单独补刀的进程组。
func reapProcessGroupLeftovers(pid int) {}

// isProcessAlive 探测进程是否仍在运行，供 MCP job 守护轮询使用。不触碰
// transport 持有的 exec.Cmd 字段，避免与其 Wait 竞争。
func isProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	// OpenProcess alone also succeeds for terminated processes while another
	// handle is open. Wait on the kernel object to distinguish that state.
	h, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		return err == windows.ERROR_ACCESS_DENIED
	}
	defer windows.CloseHandle(h)
	state, err := windows.WaitForSingleObject(h, 0)
	return err != nil || state != windows.WAIT_OBJECT_0
}

func stopProcessTree(pid int) error {
	if pid <= 0 || pid == os.Getpid() {
		return fmt.Errorf("invalid target pid: %d", pid)
	}
	stop, err := serviceProcessStop(pid)
	if err != nil {
		return err // never chase a stale PID outside an owned job
	}
	return stop()
}
