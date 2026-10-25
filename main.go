// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.

//go:build !server

package main

import (
	"embed"
	"log/slog"
	"net/http"
	"os"
	"runtime"

	backend "ally-dev/internal/app"
	"github.com/wailsapp/wails/v3/pkg/application"
)

//go:embed all:frontend/dist
var assets embed.FS

//go:embed build/appicon.png
var appIconPNG []byte

func main() {
	if runScreenshotCaptureHelper() {
		return
	}
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

	// Error logging: open ~/.ally_agent/logs/error.log and route ALL errors
	// (wails system + backend panics + frontend-reported) through one on-disk
	// channel. A failure here is non-fatal — the app still boots and errors
	// fall back to stderr.
	var errorLogger *slog.Logger
	if logger, _, lerr := backend.InitErrorLogger(); lerr != nil {
		println("init error logger failed:", lerr.Error())
	} else {
		errorLogger = logger
		app.SetErrorLogger(logger)
	}

	wailsApp := application.New(application.Options{
		Name:        "Ally",
		Description: "Ally — AI coding agent desktop",
		Icon:        appIconPNG,
		// Route Wails system errors into the same on-disk error log as the app.
		Logger:   errorLogger,
		LogLevel: slog.LevelError,
		Services: []application.Service{
			application.NewService(app),
			application.NewService(notifier),
		},
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
			// 插件资源由 app 包提供（/plugins/<id>/... 从 ~/.ally_agent/plugins 读），
			// 与页面同源，所以插件能用原生相对 import 加载自己的多文件 bundle。
			// application.Middleware 的底层类型就是 func(http.Handler) http.Handler，
			// 所以实现留在 app 包、这里只接线，不把 Wails 依赖带进去。
			Middleware: func(next http.Handler) http.Handler {
				return backend.GeneratedImageMiddleware(app)(backend.PluginAssetMiddleware(app)(next))
			},
		},
		Windows: application.WindowsOptions{
			WndClass: backend.WindowsWindowClassName,
			// UseVisualHosting (DirectComposition) 强依赖 Direct3D 设备。系统锁屏、显示器休眠
			// 或显卡重置时 DirectComposition 设备会丢失，而 Wails v3 缺少 DComp 设备丢失重建逻辑，
			// 会导致页面永久白屏死锁。恢复默认 Windowed (HWND) 模式后由 Win32 DWM 原生恢复表面。
			// 同时添加后台保活参数，防止系统锁屏或长时间后台时 Chromium 挂起渲染进程与节流定时器。
			AdditionalBrowserArgs: []string{
				"--disable-background-timer-throttling",
				"--disable-backgrounding-occluded-windows",
				"--disable-renderer-backgrounding",
			},
		},
		SingleInstance: &application.SingleInstanceOptions{
			UniqueID: "io.github.bronya0.ally",
			OnSecondInstanceLaunch: func(data application.SecondInstanceData) {
				// Second launch: focus the existing window instead of starting
				// a second process that would fight over config/MCP/schedules.
				app.ShowMainWindow()
			},
		},
		Mac: application.MacOptions{
			// Match the existing single-window close/quit behaviour.
			ApplicationShouldTerminateAfterLastWindowClosed: true,
		},
	})

	mainWindow := wailsApp.Window.NewWithOptions(func() application.WebviewWindowOptions {
		opts := application.WebviewWindowOptions{
			Name:             "main",
			Title:            "Ally",
			Width:            backend.DefaultWindowWidth,
			Height:           backend.DefaultWindowHeight,
			MinWidth:         backend.MinWindowWidth,
			MinHeight:        backend.MinWindowHeight,
			Frameless:        runtime.GOOS != "darwin",
			BackgroundColour: application.NewRGBA(12, 12, 12, 255),
			Windows: application.WindowsWindow{
				Theme: application.Dark,
			},
			KeyBindings: devToolsKeyBindings(),
			URL:         "/",
		}
		if runtime.GOOS == "darwin" {
			// Keep native traffic lights while extending the HTML header under
			// the transparent titlebar. The compact toolbar fits our 44px row.
			titleBar := application.MacTitleBarHiddenInset
			titleBar.ToolbarStyle = application.MacToolbarStyleUnifiedCompact
			opts.Mac.TitleBar = titleBar
			// Leave InvisibleTitleBarHeight at zero: an OS-wide drag strip would
			// swallow clicks on the workspace tabs and buttons in the same row.
		}
		// 恢复上次退出时持久化的窗口几何（~/.ally_agent/window.json）；
		// 无持久化状态时保持上面的默认参数。
		backend.ApplySavedWindowGeometry(&opts)
		return opts
	}())

	app.SetApp(wailsApp)
	app.SetWindow(mainWindow)

	if err := wailsApp.Run(); err != nil {
		if errorLogger != nil {
			errorLogger.Error("Ally startup failed", "error", err.Error())
		}
		println("Error:", err.Error())
		wailsApp.Dialog.Error().SetTitle("Ally 启动失败").SetMessage(err.Error()).Show()
	}
}
