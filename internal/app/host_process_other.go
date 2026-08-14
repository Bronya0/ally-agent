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

func stopProcessTree(pid int) error {
	if pid <= 0 {
		return fmt.Errorf("invalid pid: %d", pid)
	}
	return syscall.Kill(-pid, syscall.SIGTERM)
}
