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
)

// explorerCommand 非 Windows 平台不会走到（openPathInFileManager 的
// windows 分支独占），保留同签名实现以满足跨平台编译。
func explorerCommand(path string) *exec.Cmd {
	return exec.Command("xdg-open", path)
}
