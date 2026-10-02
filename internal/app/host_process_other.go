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
	"time"
)

func hideCommandWindow(cmd *exec.Cmd) {}

func prepareServiceCommand(cmd *exec.Cmd) uintptr {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return 0
}

func registerProcessJob(pid int, job uintptr) error { return nil }
func unregisterProcessJob(pid int)                  {}
func discardProcessJob(job uintptr)                 {}

// isProcessAlive 探测进程是否仍在运行（kill 0 只做权限/存在性检查）。
func isProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	return syscall.Kill(pid, 0) == nil
}

// mcpProcessGroupKillGrace 是 MCP 子进程退出后、对遗留进程组先 SIGTERM 再
// SIGKILL 之间的宽限期（对齐 codex 的两段式：先给机会优雅退出，再强杀）。
const mcpProcessGroupKillGrace = time.Second

// reapProcessGroupLeftovers 清理 stdio MCP 子进程退出后脱管的孙进程：
// prepareServiceCommand 已用 Setpgid 给它建了独立进程组，组里还活着的先
// SIGTERM 再 SIGKILL。npx / sh 包裹脚本的 node 后代就靠这一步，否则根进程
// 一死它们就变成常驻孤儿。
func reapProcessGroupLeftovers(pid int) {
	if pid <= 0 {
		return
	}
	_ = gracefulStopProcessTree(pid)
	time.Sleep(mcpProcessGroupKillGrace)
	_ = stopProcessTree(pid)
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
