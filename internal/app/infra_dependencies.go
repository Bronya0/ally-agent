// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
package app

import (
	goruntime "runtime"

	"ally-dev/internal/tools/grep"
)

func (a *App) emitRipgrepMissingIfNeeded() {
	if _, err := grep.Find(); err == nil {
		return
	}
	a.emit("dependency:missing", map[string]any{
		"tool":         "rg",
		"name":         "ripgrep",
		"message":      "ripgrep (`rg`) was not found. Install ripgrep and restart Ally.",
		"installSteps": grep.InstallInstructions(),
	})
}

func (a *App) emitGitBashMissingIfNeeded() {
	if goruntime.GOOS != "windows" {
		return
	}
	cfg, err := a.getConfig()
	if err != nil {
		return
	}
	if _, bashName := findWindowsBash(cfg.GitBashPath); bashName != "" {
		return
	}
	a.emit("dependency:missing", map[string]any{
		"tool":       "bash",
		"name":       "Git Bash",
		"messageKey": "app.dependency.gitBashMissing",
		"message":    "Git Bash was not found. command will fall back to PowerShell. Install Git for Windows, or set the Git Bash path in Settings → General → Git Bash Path.",
		"installStepKeys": []string{
			"app.dependency.gitBashDownload",
			"app.dependency.gitBashConfigure",
		},
		"installSteps": []string{
			"Download from https://git-scm.com/download/win",
			"Or set the path manually in Settings, e.g. C:\\Program Files\\Git\\bin\\bash.exe",
		},
	})
}
