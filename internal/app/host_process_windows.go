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
	"syscall"
)

func hideCommandWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
}

func prepareServiceCommand(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000 | syscall.CREATE_NEW_PROCESS_GROUP}
}

func stopProcessTree(pid int) error {
	if pid <= 0 {
		return fmt.Errorf("invalid pid: %d", pid)
	}
	cmd := exec.Command("taskkill.exe", "/PID", fmt.Sprintf("%d", pid), "/T", "/F")
	hideCommandWindow(cmd)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("taskkill failed: %w: %s", err, string(out))
	}
	return nil
}
