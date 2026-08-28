// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.

//go:build !windows

package app

import (
	"fmt"
	"os/exec"
	"syscall"
)

func hideCommandWindow(cmd *exec.Cmd) {}

func prepareServiceCommand(cmd *exec.Cmd) uintptr {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return 0
}

func registerProcessJob(pid int, job uintptr) error { return nil }
func unregisterProcessJob(pid int)                   {}
func discardProcessJob(job uintptr)                  {}

// isProcessAlive 探测进程是否仍在运行（kill 0 只做权限/存在性检查）。
func isProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	return syscall.Kill(pid, 0) == nil
}

// gracefulStopProcessTree 向进程组投递 SIGTERM（prepareServiceCommand 已用
// Setpgid 建组，组内含 bash 及其后代）。进程已退出时返回 ESRCH，调用方忽略，
// 等待 waitDone 即可。
func gracefulStopProcessTree(pid int) error {
	if pid <= 0 {
		return fmt.Errorf("invalid pid: %d", pid)
	}
	return syscall.Kill(-pid, syscall.SIGTERM)
}

// stopProcessTree 强杀整棵进程组。只对直接子进程的 cancel() 兜底不够：
// bash 死后孙进程会脱管残留，组级 SIGKILL 才能清干净。
func stopProcessTree(pid int) error {
	if pid <= 0 {
		return fmt.Errorf("invalid pid: %d", pid)
	}
	return syscall.Kill(-pid, syscall.SIGKILL)
}
