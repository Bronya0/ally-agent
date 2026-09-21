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
	"os/exec"

	goruntime "runtime"
)

// terminalCommand 非 Windows 平台：darwin 用 Terminal.app，Linux 按常见
// 终端依次探测，全部缺失时返回 nil（openPathInTerminal 报错）。
func terminalCommand(path string) *exec.Cmd {
	switch goruntime.GOOS {
	case "darwin":
		return exec.Command("open", "-a", "Terminal", path)
	default:
		candidates := [][]string{
			{"gnome-terminal", "--working-directory", path},
			{"konsole", "--workdir", path},
			{"xfce4-terminal", "--working-directory", path},
			{"kitty", "--directory", path},
			{"x-terminal-emulator", "--working-directory", path},
		}
		for _, c := range candidates {
			if _, err := exec.LookPath(c[0]); err == nil {
				return exec.Command(c[0], c[1:]...)
			}
		}
		if _, err := exec.LookPath("xterm"); err == nil {
			cmd := exec.Command("xterm")
			cmd.Dir = path
			return cmd
		}
		return nil
	}
}
