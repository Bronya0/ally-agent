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
	"os/exec"
	"syscall"
)

// terminalCommand 在 Windows 上于指定目录打开终端：优先 Windows Terminal
// （wt.exe -d），未安装时回退到新开的 cmd.exe 窗口（start /D <dir> cmd.exe）。
// HideWindow 隐藏中间的 cmd.exe 宿主控制台，避免一闪而过；终端自身会开
// 自己的可见窗口。
func terminalCommand(path string) *exec.Cmd {
	if _, err := exec.LookPath("wt.exe"); err == nil {
		cmd := exec.Command("wt.exe", "-d", path)
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
		return cmd
	}
	cmd := exec.Command("cmd.exe", "/c", "start", "", "/D", path, "cmd.exe")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	return cmd
}
