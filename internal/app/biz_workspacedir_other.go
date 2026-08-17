// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
//
//go:build !windows

package app

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// defaultWorkspaceDir 返回首次启动的默认工作空间：用户文档目录。
// macOS 用 ~/Documents（系统约定）。Linux 优先读 XDG user-dirs.dirs 的
// XDG_DOCUMENTS_DIR；解析不到时回退 ~/Documents。
func defaultWorkspaceDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	if runtime.GOOS == "linux" {
		if dir := linuxUserDirDocuments(home); dir != "" {
			return dir
		}
	}
	return filepath.Join(home, "Documents")
}

func linuxUserDirDocuments(home string) string {
	data, err := os.ReadFile(filepath.Join(home, ".config", "user-dirs.dirs"))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		const prefix = "XDG_DOCUMENTS_DIR="
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		val := strings.TrimSpace(strings.Trim(strings.TrimPrefix(line, prefix), `"`))
		if val == "" {
			return ""
		}
		val = strings.ReplaceAll(val, "$HOME", home)
		return filepath.Clean(val)
	}
	return ""
}
