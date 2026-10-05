// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.

//go:build !devtools

// 正式构建：不挂任何 DevTools 快捷键（见 main_devtools.go 的 devtools 变体）。
package main

import "github.com/wailsapp/wails/v3/pkg/application"

func devToolsKeyBindings() map[string]func(application.Window) {
	return nil
}
