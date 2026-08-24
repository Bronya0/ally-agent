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
	"path/filepath"
	"syscall"
)

// explorerCommand 构造打开指定目录的 explorer.exe 命令。
// 必须通过 SysProcAttr.CmdLine 整段构造命令行并强制给路径加引号：
// exec.Command 的默认转义只在参数含空格时加引号，形如 D:\doc\=项目文档=
// 这种"无空格但含 = 等特殊字符"的路径会被裸传，explorer.exe 解析命令行
// 失败后静默回退到默认视图（此电脑/文档）。实测整段引号可正确打开
// 含 = 与逗号的目录；explorer.exe 总以非零码退出，故不等待其结束。
func explorerCommand(path string) *exec.Cmd {
	cmd := exec.Command("explorer.exe")
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CmdLine: `explorer.exe "` + filepath.Clean(path) + `"`,
	}
	return cmd
}
