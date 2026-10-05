// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.

//go:build devtools

// devtools 构建（wails3 build EXTRA_TAGS=devtools）下 F12 打开 WebView2
// DevTools。Wails v3 不会自动绑定 F12，必须在这里显式挂 KeyBindings。
package main

import "github.com/wailsapp/wails/v3/pkg/application"

func devToolsKeyBindings() map[string]func(application.Window) {
	return map[string]func(application.Window){
		"F12": func(w application.Window) { w.OpenDevTools() },
	}
}
