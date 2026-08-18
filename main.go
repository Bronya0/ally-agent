// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.

package main

import (
	"embed"
	"os"

	backend "ally-dev/internal/app"
	"github.com/wailsapp/wails/v3/pkg/application"
)

//go:embed all:frontend/dist
var assets embed.FS

//go:embed build/appicon.png
var appIconPNG []byte

func main() {
	if handled, err := backend.RunUpdateRelaunchHelper(os.Args[1:]); handled {
		if err != nil {
			println("Update relaunch helper error:", err.Error())
		}
		return
	}
	app := backend.NewApp()

	// 桌面通知服务：仅用于任务完成/出错/取消的系统提示音（带声音）。
	// SafeNotificationsService 包装平台后端：启动失败（macOS 无
	// bundle、Linux 无会话总线）时降级为静默，绝不阻断应用启动。
	notifier := backend.NewSafeNotificationsService()
	backend.SetNotifier(app, notifier)

	wailsApp := application.New(application.Options{
		Name:        "Ally",
		Description: "Ally — AI coding agent desktop",
		Icon:        appIconPNG,
		Services: []application.Service{
			application.NewService(app),
			application.NewService(notifier),
		},
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
		Windows: application.WindowsOptions{
			WndClass:         backend.WindowsWindowClassName,
			UseVisualHosting: true,
		},
		SingleInstance: &application.SingleInstanceOptions{
			UniqueID: "io.github.bronya0.ally",
			OnSecondInstanceLaunch: func(data application.SecondInstanceData) {
				// Second launch: focus the existing window instead of starting
				// a second process that would fight over config/MCP/schedules.
				app.ShowMainWindow()
			},
		},
	})

	mainWindow := wailsApp.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:             "main",
		Title:            "Ally",
		Width:            backend.DefaultWindowWidth,
		Height:           backend.DefaultWindowHeight,
		MinWidth:         backend.MinWindowWidth,
		MinHeight:        backend.MinWindowHeight,
		Frameless:        true,
		BackgroundColour: application.NewRGBA(12, 12, 12, 255),
		Windows: application.WindowsWindow{
			Theme: application.Dark,
		},
		URL: "/",
	})

	app.SetApp(wailsApp)
	app.SetWindow(mainWindow)
	// Tray support is kept for a future release but is currently disabled.
	// app.SetTrayIcon(appIconPNG)

	if err := wailsApp.Run(); err != nil {
		println("Error:", err.Error())
	}
}
