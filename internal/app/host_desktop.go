// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
package app

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"sync/atomic"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

const (
	// DefaultWindowWidth/Height follow the industry-standard default for
	// desktop coding tools (e.g. VS Code's 1200x800). Used on first launch
	// before the user has resized the window manually.
	DefaultWindowWidth  = 1200
	DefaultWindowHeight = 800
	MinWindowWidth      = 860
	MinWindowHeight     = 600
	WindowsWindowClassName = "AllyMainWindow"
)

// wailsAppHandle is the minimal Wails v3 host binding injected into App by
// SetApp/SetWindow. Concrete Wails v3 types stay in this file (and its
// platform siblings) so core Agent code never imports the Wails runtime.
type wailsAppHandle struct {
	app    *application.App
	window *application.WebviewWindow
	tray   *application.SystemTray
	// quitForced marks an explicit app quit (tray exit, self-update) so the
	// close-to-tray WindowClosing hook never intercepts a real quit.
	quitForced atomic.Bool
}

// SetApp injects the Wails v3 application handle into the Agent core. It must
// be called before app.Run() so ServiceStartup can install the host event
// sink. Wails desktop lifecycle stays in this file; the Agent core only sees
// the host-neutral eventSink interface.
func (a *App) SetApp(app *application.App) {
	if a.wails == nil {
		a.wails = &wailsAppHandle{}
	}
	a.wails.app = app
}

// SetWindow injects the main window handle. Used for initial window sizing
// and desktop-only window operations.
func (a *App) SetWindow(window *application.WebviewWindow) {
	if a.wails == nil {
		a.wails = &wailsAppHandle{}
	}
	a.wails.window = window
	a.installWebviewZoomResync(window)
}

// installWebviewZoomResync guards against WebView2 restoring a stale zoom
// state after the window leaves the minimised state (UI fonts render too
// small). Wails only resyncs the WebView2 rasterization scale when the monitor
// DPI actually changed across the minimise/restore (#5544/#5605); the
// same-monitor restore path is a blind spot, so we re-assert zoom 1.0 after
// the webview has fully resumed. The write is skipped when the zoom is already
// correct, so normal restores are no-ops with no relayout flicker.
func (a *App) installWebviewZoomResync(window *application.WebviewWindow) {
	if goruntime.GOOS != "windows" || window == nil {
		return
	}
	window.OnWindowEvent(events.Windows.WindowUnMinimise, func(*application.WindowEvent) {
		// Wait until WebView2 has finished resuming; touching the controller
		// too early can be fatal while its render/GPU process restarts
		// (Wails #5605). SetZoom/GetZoom marshal to the main thread and are
		// nil-safe after the window is destroyed.
		time.AfterFunc(500*time.Millisecond, func() {
			if current := window.GetZoom(); current != 1.0 {
				window.SetZoom(1.0)
			}
		})
	})
}

// wailsEventSink adapts the host-neutral eventSink contract to Wails v3
// runtime events. It is the only type in package app that publishes events
// through the Wails runtime.
type wailsEventSink struct {
	app *application.App
}

func (s wailsEventSink) Emit(name string, payload any) {
	if s.app == nil {
		return
	}
	s.app.Event.Emit(name, payload)
}

// ServiceStartup is the Wails v3 lifecycle adapter (registered through
// application.NewService). It installs the desktop event sink and delegates
// long-lived Agent services to their host-neutral modules.
func (a *App) ServiceStartup(ctx context.Context, _ application.ServiceOptions) error {
	a.ctx = ctx
	fan := newFanoutEventSink()
	if a.wails != nil {
		fan.Add(wailsEventSink{app: a.wails.app})
	}
	// 网络事件出口（SSE/轮询/未来 WS）：默认关闭，由 ALLY_NETWORK_EVENTS 环境变量启用。
	// 失败只记日志不阻断启动，绝不破坏桌面端功能。
	if netSink := newNetworkEventSinkFromEnv(ctx); netSink != nil {
		fan.Add(netSink)
	}
	a.events = fan
	// System tray support is kept in host_tray.go but disabled for the
	// current release. Do not intercept window close, so closing exits Ally.
	// a.setupSystemTray()
	// a.setupCloseToTrayHook()
	_ = a.ensureInitialized()
	// Warm the one-time POSIX login-shell PATH probe without delaying the UI.
	// command/service wait on the same sync.Once if needed.
	go warmCommandEnvironment()
	_ = a.loadServiceHistory()
	_ = a.startScheduledTaskManager()
	// Load persisted token stats in the background and start the async flusher.
	// Neither startup disk IO nor persistence can block normal chat handling.
	if a.stats != nil {
		a.stats.start(ctx)
	}
	// Windows backups can be removed immediately. macOS keeps the previous
	// bundle until this process has remained alive past the startup grace period.
	scheduleUpdateBackupCleanup(ctx)
	go func() {
		<-ctx.Done()
		a.stopScheduledTaskManager()
		a.stopAllServices()
	}()
	go func() {
		timer := time.NewTimer(2 * time.Second)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			a.emitRipgrepMissingIfNeeded()
			a.emitGitBashMissingIfNeeded()
		}
	}()
	// Initialize MCP manager.
	cfg, err := a.getConfig()
	if err == nil {
		root, _ := workspaceRoot(cfg)
		if root != "" {
			a.mcpManager = NewMcpManager(root, func(tools []McpDiscoveredTool) {
				a.emitMcpStatus()
				a.invalidateContextStaticCache()
			})
			a.mcpManager.SetNetworkConfigProvider(func() ConfigState { return a.effectiveConfig(ConfigState{}) })
			go func() {
				if err := a.mcpManager.StartAll(ctx); err != nil {
					// MCP start errors are non-fatal.
				}
				a.emitMcpStatus()
			}()
			go func() {
				<-ctx.Done()
				if a.mcpManager != nil {
					a.mcpManager.Shutdown()
				}
			}()
		}
	}
	return nil
}

// ServiceShutdown waits for telemetry's final queue drain and disk flush.
// Wails calls this lifecycle hook before process teardown, so recently
// completed requests are not lost when the window closes inside the periodic
// flush interval.
func (a *App) ServiceShutdown() error {
	if a.stats != nil {
		_ = a.stats.stop(statsShutdownTimeout)
	}
	return nil
}

// quitApp asks the Wails application to quit. No-op when the desktop host is
// absent (tests/headless). Used by the self-update flow and tray exit.
func (a *App) quitApp() {
	if a.wails != nil && a.wails.app != nil {
		a.wails.quitForced.Store(true)
		a.wails.app.Quit()
	}
}

// ShowMainWindow displays and focuses the main window. Used by the tray menu
// and the second-instance activation callback.
func (a *App) ShowMainWindow() {
	a.showMainWindow()
}

// GetAutostartEnabled reports whether Ally is registered to launch at login.
func (a *App) GetAutostartEnabled() (bool, error) {
	if a.wails == nil || a.wails.app == nil {
		return false, errors.New("desktop host not initialized")
	}
	return a.wails.app.Autostart.IsEnabled()
}

// SetAutostartEnabled registers or removes Ally from OS login startup.
func (a *App) SetAutostartEnabled(enabled bool) error {
	if a.wails == nil || a.wails.app == nil {
		return errors.New("desktop host not initialized")
	}
	if enabled {
		return a.wails.app.Autostart.Enable()
	}
	return a.wails.app.Autostart.Disable()
}

func (a *App) SelectWorkspace() (string, error) {
	if err := a.ensureInitialized(); err != nil {
		return "", err
	}
	current := a.config.Workspace
	// If the saved workspace no longer exists, fall back to the user's home
	// directory so the directory dialog can still open and the user can pick
	// a valid workspace.
	if info, err := os.Stat(current); err != nil || !info.IsDir() {
		if homeDir, err := os.UserHomeDir(); err == nil {
			current = homeDir
		}
	}
	if a.wails == nil || a.wails.app == nil {
		return "", errors.New("desktop host not initialized")
	}
	selected, err := a.wails.app.Dialog.OpenFile().
		SetTitle("选择 Agent 工作区").
		SetDirectory(current).
		CanChooseDirectories(true).
		CanChooseFiles(false).
		PromptForSingleSelection()
	if err != nil || selected == "" {
		return selected, err
	}
	cfg := a.config
	cfg.Workspace = selected
	if err := a.SaveConfig(cfg); err != nil {
		return "", err
	}
	return selected, nil
}

// SelectBackgroundImage opens a native file picker for image files, writes
// the chosen bytes to ~/.ally_agent/background.<ext> via SaveBackgroundImage,
// and returns the stored filename. Rejects oversized or non-image files at
// the dialog boundary so the heavy save path never runs for invalid input.
func (a *App) SelectBackgroundImage() (string, error) {
	if err := a.ensureInitialized(); err != nil {
		return "", err
	}
	if a.wails == nil || a.wails.app == nil {
		return "", errors.New("desktop host not initialized")
	}
	selected, err := a.wails.app.Dialog.OpenFile().
		SetTitle("选择对话背景图").
		AddFilter("图片 (*.png *.jpg *.jpeg *.webp *.gif *.bmp)", "*.png;*.jpg;*.jpeg;*.webp;*.gif;*.bmp").
		CanChooseFiles(true).
		CanChooseDirectories(false).
		PromptForSingleSelection()
	if err != nil {
		return "", err
	}
	if selected == "" {
		return "", nil // user cancelled
	}
	return a.saveBackgroundImageFromFile(selected)
}

// ExportTextFile opens a native save dialog and writes content to the chosen
// path. suggestedFilename seeds the dialog; the user may change it. Returns
// the saved path, or "" when the user cancels. WKWebView (macOS) ignores the
// HTML5 <a download> attribute, so exports must go through this binding
// instead of a frontend blob download to work on every platform.
func (a *App) ExportTextFile(suggestedFilename, content, filterName, filterPattern string) (string, error) {
	if err := a.ensureInitialized(); err != nil {
		return "", err
	}
	if suggestedFilename == "" || content == "" {
		return "", errors.New("filename and content are required")
	}
	if a.wails == nil || a.wails.app == nil {
		return "", errors.New("desktop host not initialized")
	}
	dialogOpts := &application.SaveFileDialogOptions{
		Title:    "导出文件",
		Filename: suggestedFilename,
	}
	if filterName != "" && filterPattern != "" {
		dialogOpts.Filters = []application.FileFilter{{DisplayName: filterName, Pattern: filterPattern}}
	}
	dialog := a.wails.app.Dialog.SaveFile()
	dialog.SetOptions(dialogOpts)
	selected, err := dialog.PromptForSingleSelection()
	if err != nil || selected == "" {
		return selected, err
	}
	// NSSavePanel appends the extension automatically, but the Windows
	// IFileSaveDialog does not; mirror the suggested name's extension so the
	// exported file always opens as expected.
	if filepath.Ext(selected) == "" {
		if ext := filepath.Ext(suggestedFilename); ext != "" {
			selected += ext
		}
	}
	if err := os.WriteFile(selected, []byte(content), 0o644); err != nil {
		return "", err
	}
	return selected, nil
}

func (a *App) OpenWorkspaceInFileManager() error {
	if err := a.ensureInitialized(); err != nil {
		return err
	}
	a.mu.Lock()
	cfg := a.config
	a.mu.Unlock()
	root, err := workspaceRoot(cfg)
	if err != nil {
		return err
	}
	return openPathInFileManager(root)
}

// OpenWorkspacePathInFileManager opens a workspace-relative file or directory
// in the system file manager. An empty path opens the workspace root.
func (a *App) OpenWorkspacePathInFileManager(path string) error {
	return a.openWorkspacePathInFileManagerAt("", path)
}

func (a *App) OpenWorkspacePathInFileManagerAt(req WorkspacePathRequest) error {
	return a.openWorkspacePathInFileManagerAt(req.Workspace, req.Path)
}

func (a *App) openWorkspacePathInFileManagerAt(workspace, path string) error {
	if err := a.ensureInitialized(); err != nil {
		return err
	}
	cfg, err := a.configForWorkspace(workspace)
	if err != nil {
		return err
	}
	root, err := workspaceRoot(cfg)
	if err != nil {
		return err
	}
	if strings.TrimSpace(path) == "" {
		return openPathInFileManager(root)
	}
	target, err := resolveReadablePath(cfg, path)
	if err != nil {
		return err
	}
	if !insideRoot(root, target) {
		return errors.New("path is outside workspace")
	}
	return openPathInFileManager(target)
}

// OpenPathInFileManager opens a file or directory in the system file manager.
// If path points to a file, the parent directory is opened instead.
func (a *App) OpenPathInFileManager(path string) error {
	if err := a.ensureInitialized(); err != nil {
		return err
	}
	return openPathInFileManager(path)
}

func openPathInFileManager(path string) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("path is required")
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	// 文件：打开其所在目录（并可选选中该文件）
	if !info.IsDir() {
		path = filepath.Dir(path)
	}
	// 路径归一化为各平台原生分隔符，避免 explorer.exe 收到混合分隔符
	path = filepath.Clean(path)
	var cmd *exec.Cmd
	switch goruntime.GOOS {
	case "windows":
		// explorer.exe 对含 / 或未引号的路径可能打开“此电脑”或系统目录。
		// 统一用 \ 分隔并用 /select 选项确保打开到正确位置。
		cmd = exec.Command("explorer.exe", path)
	case "darwin":
		cmd = exec.Command("open", path)
	default:
		// Linux：优先 xdg-open，回退常见文件管理器
		if _, err := exec.LookPath("xdg-open"); err == nil {
			cmd = exec.Command("xdg-open", path)
		} else if _, err := exec.LookPath("nautilus"); err == nil {
			cmd = exec.Command("nautilus", path)
		} else if _, err := exec.LookPath("dolphin"); err == nil {
			cmd = exec.Command("dolphin", path)
		} else if _, err := exec.LookPath("thunar"); err == nil {
			cmd = exec.Command("thunar", path)
		} else {
			cmd = exec.Command("xdg-open", path)
		}
	}
	return cmd.Start()
}
