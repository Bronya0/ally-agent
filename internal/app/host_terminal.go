// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.

package app

import (
	"errors"
	"path/filepath"
	"strings"
)

// OpenWorkspaceTerminalAt opens a system terminal whose working directory is
// the given workspace root (the persisted chat workspace when req.Workspace is
// empty). Mirrors OpenWorkspacePathInFileManagerAt so KB/temp Tabs open their
// own directory instead of the shared chat workspace.
func (a *App) OpenWorkspaceTerminalAt(req WorkspacePathRequest) error {
	if err := a.ensureInitialized(); err != nil {
		return err
	}
	cfg, err := a.configForWorkspace(strings.TrimSpace(req.Workspace))
	if err != nil {
		return err
	}
	root, err := workspaceRoot(cfg)
	if err != nil {
		return err
	}
	return openPathInTerminal(root)
}

// openPathInTerminal launches the platform terminal in the given directory.
// Platform-specific command construction lives in terminalCommand
// (host_terminal_windows.go / host_terminal_other.go).
func openPathInTerminal(path string) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("path is required")
	}
	path = filepath.Clean(path)
	cmd := terminalCommand(path)
	if cmd == nil {
		return errors.New("no terminal emulator found")
	}
	// wt/start 均立即返回；POSIX 终端由进程自身接管，Start 后异步收割，
	// 避免 POSIX 侧留下僵尸进程。
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}
