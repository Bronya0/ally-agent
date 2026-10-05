// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
package app

// 桌面宿主桥：应用/窗口注入、窗口几何持久化（~/.ally_agent/window.json）、桌面通知
// 与系统对话框。Wails 运行时只允许出现在 host_* 文件里。

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
	"github.com/wailsapp/wails/v3/pkg/services/notifications"

	"ally-dev/internal/tools/pathutil"
)

const (
	// DefaultWindowWidth/Height follow the industry-standard default for
	// desktop coding tools (e.g. VS Code's 1200x800). Used on first launch
	// before the user has resized the window manually.
	DefaultWindowWidth     = 1200
	DefaultWindowHeight    = 800
	MinWindowWidth         = 860
	MinWindowHeight        = 600
	WindowsWindowClassName = "AllyMainWindow"
)

// wailsAppHandle is the minimal Wails v3 host binding injected into App by
// SetApp/SetWindow. Concrete Wails v3 types stay in this file (and its
// platform siblings) so core Agent code never imports the Wails runtime.
type wailsAppHandle struct {
	app    *application.App
	window *application.WebviewWindow
	// windowState snapshots the main window geometry (updated from window
	// events, persisted once at shutdown). Kept here so main-window sizing
	// state stays in the host layer, mirroring installWebviewZoomResync.
	windowState *windowStateTracker
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
	a.installWindowStateTracking(window)
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
	_ = a.ensureInitialized()
	// Warm the one-time POSIX login-shell PATH probe without delaying the UI.
	// command/service wait on the same sync.Once if needed.
	go warmCommandEnvironment()
	_ = a.loadServiceHistory()
	_ = a.startScheduledTaskManager()
	// Sweep stale temp-workspace leftovers from crashed sessions (async, never
	// blocks startup) and remove this process's temp workspaces on exit.
	go a.cleanupStaleTempWorkspaces()
	go func() {
		<-ctx.Done()
		a.cleanupTempWorkspacesOnExit()
	}()
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
			manager := NewMcpManager(root, func() {
				a.emitMcpStatus()
			})
			manager.SetNetworkConfigProvider(func() ConfigState { return a.effectiveConfig(ConfigState{}) })
			manager.SetWarningHandler(func(message string) {
				a.emit("config:warning", map[string]any{"field": "mcp", "message": message})
			})
			// 指针发布持 a.mu，读点统一走 activeMcpManager：启动期前端绑定
			// 与聊天可能已经开始并发读这个字段。
			a.mu.Lock()
			a.mcpManager = manager
			a.mu.Unlock()
			go func() {
				if err := manager.StartAll(ctx); err != nil {
					// MCP start errors are non-fatal.
				}
				a.emitMcpStatus()
			}()
			go func() {
				<-ctx.Done()
				if current := a.activeMcpManager(); current != nil {
					current.Shutdown()
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
	// Persist the main window geometry before the process tears down; the
	// tracker only replays its in-memory snapshot, so a window already
	// destroyed by the default WindowClosing listener is fine.
	a.saveWindowState()
	if a.stats != nil {
		_ = a.stats.stop(statsShutdownTimeout)
	}
	return nil
}

// quitApp asks the Wails application to quit. No-op when the desktop host is
// absent (tests/headless). Used by the self-update flow.
func (a *App) quitApp() {
	if a.wails != nil && a.wails.app != nil {
		a.wails.app.Quit()
	}
}

// showMainWindow displays and focuses the main window. Used by the exposed
// ShowMainWindow binding for the second-instance activation callback.
func (a *App) showMainWindow() {
	if a.wails == nil || a.wails.window == nil {
		return
	}
	a.wails.window.Show()
	a.wails.window.Focus()
}

// ShowMainWindow displays and focuses the main window. Used by the
// second-instance activation callback.
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
	a.mu.Lock()
	current := a.config.Workspace
	a.mu.Unlock()
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
	// 系统根目录一当选成工作区，所有边界检查（写根、VCS、危险删除黑名单）就
	// 形同虚设——整个磁盘都算“工作区内”。三个平台都在入口拒绝。
	if pathutil.IsSystemRootPath(selected) {
		return "", errors.New("系统根目录不能用作工作区：整个磁盘都会变成 Agent 的可写范围，请选择一个具体的项目目录")
	}
	cfg := a.config
	cfg.Workspace = selected
	if err := a.SaveConfig(cfg); err != nil {
		return "", err
	}
	return selected, nil
}

// SelectKnowledgeBaseRoot opens a native directory picker for the knowledge
// base root and returns the chosen path without persisting it — the settings
// draft (and the KB empty-state card) own the save flow, so the picked path
// round-trips through the normal SaveConfig boundary.
func (a *App) SelectKnowledgeBaseRoot() (string, error) {
	if err := a.ensureInitialized(); err != nil {
		return "", err
	}
	a.mu.Lock()
	current := a.config.KBRoot
	a.mu.Unlock()
	if info, err := os.Stat(current); err != nil || !info.IsDir() {
		if homeDir, err := os.UserHomeDir(); err == nil {
			current = homeDir
		}
	}
	if a.wails == nil || a.wails.app == nil {
		return "", errors.New("desktop host not initialized")
	}
	selected, err := a.wails.app.Dialog.OpenFile().
		SetTitle("选择知识库目录").
		SetDirectory(current).
		CanChooseDirectories(true).
		CanChooseFiles(false).
		PromptForSingleSelection()
	if err != nil {
		return "", err
	}
	return selected, nil
}

// SelectDirectory opens a native directory picker and returns the chosen absolute
// path without persisting it. Settings pages that own their own draft (the sandbox
// deny-read list, for example) call this instead of a page-specific picker; the
// picked path round-trips through the normal SaveConfig boundary.
func (a *App) SelectDirectory(current string) (string, error) {
	if err := a.ensureInitialized(); err != nil {
		return "", err
	}
	if a.wails == nil || a.wails.app == nil {
		return "", errors.New("desktop host not initialized")
	}
	start := strings.TrimSpace(current)
	if info, err := os.Stat(start); err != nil || !info.IsDir() {
		if homeDir, err := os.UserHomeDir(); err == nil {
			start = homeDir
		}
	}
	selected, err := a.wails.app.Dialog.OpenFile().
		SetTitle("选择目录").
		SetDirectory(start).
		CanChooseDirectories(true).
		CanChooseFiles(false).
		PromptForSingleSelection()
	if err != nil {
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

// SelectPrivateKeyFile opens a native file picker without file type restrictions
// and returns the chosen file path (or "" if cancelled).
func (a *App) SelectPrivateKeyFile() (string, error) {
	if a.wails == nil || a.wails.app == nil {
		return "", errors.New("desktop host not initialized")
	}
	defaultDir := ""
	if homeDir, err := os.UserHomeDir(); err == nil {
		sshDir := filepath.Join(homeDir, ".ssh")
		if info, err := os.Stat(sshDir); err == nil && info.IsDir() {
			defaultDir = sshDir
		} else {
			defaultDir = homeDir
		}
	}
	dialog := a.wails.app.Dialog.OpenFile().
		SetTitle("选择 SSH 私钥文件").
		CanChooseFiles(true).
		CanChooseDirectories(false)
	if defaultDir != "" {
		dialog.SetDirectory(defaultDir)
	}
	selected, err := dialog.PromptForSingleSelection()
	if err != nil {
		return "", err
	}
	return selected, nil
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
		cmd = explorerCommand(path)
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
	// explorer.exe（Windows）不退出属预期，交给 explorerCommand 内部处理；
	// POSIX 上 open/xdg-open 通常立即退出，Start 后不 Wait 会积累僵尸进程，
	// 异步收割即可。
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

// completionNotifier is the minimal notifications surface the Agent core
// needs: sending a completion toast with sound. The concrete implementation
// is injected through SetNotifier from the host wiring (main.go); tests and
// headless embeddings leave it nil and get silent no-ops.
type completionNotifier interface {
	SendNotification(options notifications.NotificationOptions) error
}

// SafeNotificationsService wraps the Wails notifications service so platform
// startup failures (macOS unbundled binary without a bundle identifier, Linux
// without a session bus) degrade to silent no-ops instead of aborting the
// whole application startup — Wails aborts App.Run when any service Startup
// returns an error, which would make the completion sound a startup
// hard-dependency on every platform that cannot honour it.
type SafeNotificationsService struct {
	inner     *notifications.NotificationService
	available atomic.Bool
}

// NewSafeNotificationsService creates the wrapped notifications service.
func NewSafeNotificationsService() *SafeNotificationsService {
	return &SafeNotificationsService{inner: notifications.New()}
}

// ServiceStartup implements application.ServiceStartup. Failures only disable
// notifications; the application must keep starting.
func (s *SafeNotificationsService) ServiceStartup(ctx context.Context, options application.ServiceOptions) error {
	if err := s.inner.ServiceStartup(ctx, options); err != nil {
		println("Ally notifications disabled:", err.Error())
		return nil
	}
	s.available.Store(true)
	return nil
}

// ServiceShutdown implements application.ServiceShutdown.
func (s *SafeNotificationsService) ServiceShutdown() error {
	return s.inner.ServiceShutdown()
}

// SendNotification implements completionNotifier and no-ops when the platform
// backend failed to start.
func (s *SafeNotificationsService) SendNotification(options notifications.NotificationOptions) error {
	if !s.available.Load() {
		return nil
	}
	return s.inner.SendNotification(options)
}

// mainWindowMinimised reports whether the main window is currently
// minimised. A missing window handle (headless embedding, tests) counts as
// not minimised so completion notifications stay silent.
func (a *App) mainWindowMinimised() bool {
	if a.wails == nil || a.wails.window == nil {
		return false
	}
	return a.wails.window.IsMinimised()
}

// SetNotifier injects the notifications service used for task completion
// sounds. Must be called before app.Run().
//
// This is a package-level function rather than an (a *App) method on purpose:
// the parameter is a non-empty interface, and Wails' binding generator would
// reject any exported method on *App (which is registered as a service) that
// takes one, since encoding/json cannot unmarshal into a concrete type it
// cannot infer. Package-level functions are not scanned by the generator, so
// keeping the injection here keeps the interface while leaving the generated
// frontend bindings clean.
func SetNotifier(a *App, n completionNotifier) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.notifier = n
}

// completionNotifyCooldown mirrors the old frontend sound throttle so rapid
// consecutive runs (e.g. quick send bursts) don't spam the Action Center.
const completionNotifyCooldown = 700 * time.Millisecond

// completionToastBodies is the toast body per run-end kind. The backend has
// no i18n pipeline (locale detection lives in the frontend), so the strings
// are centralized here in the primary UI language.
var completionToastBodies = map[string]string{
	"done":      "任务完成",
	"cancelled": "任务已取消",
	"error":     "任务出错",
}

// notifyCompletion sends a short sound-carrying system notification when a
// chat run finishes, but only while the main window is minimised. When the
// window is visible the run result is already on screen, so a toast would
// only interrupt. kind is "done", "error" or "cancelled". Non-Windows
// platforms use the platform default sound; Windows picks a distinct built-in
// toast event sound per kind. The send itself is async so a slow platform
// backend (Windows PowerShell fallback, unresponsive Linux daemon) never
// blocks the run teardown path (checkpoint save, session release).
func (a *App) notifyCompletion(kind, workspace string) {
	a.mu.Lock()
	n := a.notifier
	if n == nil || time.Since(a.lastCompletionNotifyAt) < completionNotifyCooldown {
		a.mu.Unlock()
		return
	}
	a.lastCompletionNotifyAt = time.Now()
	a.mu.Unlock()

	// 仅当主窗口处于最小化状态时才发系统通知：窗口可见（含最大化）时
	// 结果就在眼前，弹窗与提示音纯属打扰。窗口句柄缺失（headless
	// 嵌入/测试）时按不可见对待，保持静默。
	if !a.mainWindowMinimised() {
		return
	}

	body := completionToastBodies[kind]
	if body == "" {
		body = completionToastBodies["error"]
	}
	if name := workspaceDisplayName(workspace); name != statsUnknownName {
		body += " · " + name
	}

	opts := notifications.NotificationOptions{
		ID:    "ally-run-" + kind,
		Title: "Ally",
		Body:  body,
	}
	if goruntime.GOOS == "windows" {
		sound := "Default"
		if kind != "done" {
			sound = "Reminder"
		}
		opts.Sound = &notifications.NotificationSound{Name: sound}
	}
	go func() {
		_ = n.SendNotification(opts)
	}()
}

// 主窗口几何持久化：~/.ally_agent/window.json 记录 x/y/width/height/maximized，
// 启动时经 WebviewWindowOptions 恢复（创建即定位，无闪烁），退出时落盘一次。
//
// 坐标全程使用 Wails 的 DIP（逻辑像素）：Size()/Position()/options 均为 DIP，
// DPI 换算由 Wails 在创建（ScreenNearestDipRect + dipToPhysicalRect）时自动完成，
// 跨显示器缩放无需手工处理。最大化期间不更新普通 bounds（否则还原尺寸会被
// 最大化几何污染）；最小化期间 Windows 上 Position() 返回 -32000 一类占位值，
// 直接跳过。落盘只读内存快照、绝不查询窗口——ServiceShutdown 时窗口通常已销毁
// （WindowClosing 默认监听器注册在先，会先执行 markAsDestroyed）。
const (
	windowStateFilename = "window.json"
	// windowStateMaxDimension 是恢复时的宽高上限，挡住损坏/异常值；
	// 下限复用 MinWindowWidth/Height。
	windowStateMaxDimension = 16384
	// 判定窗口"可见"的最小交叉尺寸：窗口矩形与任一屏幕的可见交叉小于该值时
	// 视为离屏（保存坐标来自已断开的显示器），一次性居中修复。
	windowVisibleMinWidth  = 100
	windowVisibleMinHeight = 50
)

// windowState 是 window.json 的持久化形状。
type windowState struct {
	X         int  `json:"x"`
	Y         int  `json:"y"`
	Width     int  `json:"width"`
	Height    int  `json:"height"`
	Maximized bool `json:"maximized"`
}

func windowStatePath() string {
	return filepath.Join(appDataDir(), windowStateFilename)
}

// loadWindowState 读取并校验持久化的窗口几何。ok=false 表示没有可用状态
// （首次启动、文件缺失/损坏或数值越界），调用方应使用默认窗口参数。
func loadWindowState() (windowState, bool) {
	var s windowState
	data, err := os.ReadFile(windowStatePath())
	if err != nil {
		return s, false
	}
	if err := json.Unmarshal(data, &s); err != nil {
		return windowState{}, false
	}
	if s.Width < MinWindowWidth || s.Height < MinWindowHeight ||
		s.Width > windowStateMaxDimension || s.Height > windowStateMaxDimension {
		return windowState{}, false
	}
	return s, true
}

// persistWindowState 原子写入窗口几何。失败返回错误；调用方按宿主状态降级，
// 不影响应用退出。
func persistWindowState(s windowState) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return safeWriteFileWithDir(windowStatePath(), data, 0o600, true)
}

// ApplySavedWindowGeometry 把持久化的窗口几何应用到主窗口 options。
// main.go 在 NewWithOptions 前调用；返回是否应用了持久化状态。
//
// InitialPosition 语义（Windows PostCreate 顺序：先处理 StartState、后定位）：
//   - 普通恢复：X/Y 非零时设 WindowXY，PostCreate 的 setPosition 会重新断言
//     保存的坐标（与创建时的放置一致；两条路径都经最近屏幕换算）。
//   - 最大化恢复：只设 StartState=Maximised，不设 WindowXY——PostCreate 对
//     最大化窗口执行 setPosition（SetWindowPos 带尺寸）会破坏最大化状态，而
//     默认的 center() 走 SWP_NOSIZE 对最大化窗口近似 no-op。窗口创建本身仍
//     使用非零 X/Y/W/H，因此取消最大化后能精确回到保存的位置和尺寸。
func ApplySavedWindowGeometry(opts *application.WebviewWindowOptions) bool {
	s, ok := loadWindowState()
	if !ok {
		return false
	}
	opts.Width = s.Width
	opts.Height = s.Height
	if s.X != 0 || s.Y != 0 {
		opts.X, opts.Y = s.X, s.Y
		if !s.Maximized {
			opts.InitialPosition = application.WindowXY
		}
	}
	if s.Maximized {
		opts.StartState = application.WindowStateMaximised
	}
	return true
}

// windowStateTracker 维护窗口几何的内存快照。窗口事件回调里实时查询窗口
// （IsMaximised/Size/Position 均经 InvokeSync 到主线程，事件回调跑在独立
// goroutine，安全）；落盘只发生在 ServiceShutdown。
type windowStateTracker struct {
	mu      sync.Mutex
	state   windowState
	has     bool
	checked bool // 离屏一次性修复是否已执行
}

// update 用事件时刻的窗口实况刷新快照。最大化只更新标志位，普通 bounds 保留
// 最大化之前的值；宽高非正（销毁途中的事件）直接忽略。
func (t *windowStateTracker) update(maximized bool, x, y, w, h int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.state.Maximized = maximized
	if maximized || w <= 0 || h <= 0 {
		return
	}
	// Windows 上 X=Y=0 会让创建路径走 CW_USEDEFAULT 系统级联定位，
	// 偏移 1px 规避；对用户不可见。
	if x == 0 && y == 0 {
		x, y = 1, 1
	}
	t.state.X, t.state.Y, t.state.Width, t.state.Height = x, y, w, h
	t.has = true
}

func (t *windowStateTracker) snapshot() (windowState, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.state, t.has
}

// installWindowStateTracking 注册几何监听并初始化 tracker。用已保存状态播种，
// 保证"整轮未触发任何窗口事件"的会话也会写回一致值。main.go 的 SetWindow
// 在 Run 之前调用，此时注册 OnWindowEvent 是安全的（与 installWebviewZoomResync
// 同机制）。
func (a *App) installWindowStateTracking(window *application.WebviewWindow) {
	if window == nil {
		return
	}
	tracker := &windowStateTracker{}
	if s, ok := loadWindowState(); ok {
		tracker.state = s
		tracker.has = true
	}
	if a.wails == nil {
		a.wails = &wailsAppHandle{}
	}
	a.wails.windowState = tracker

	onGeometry := func(*application.WindowEvent) {
		// 最小化期间 Windows 报告占位坐标（-32000），跳过；快照保留
		// 最小化之前的普通几何。
		if window.IsMinimised() {
			return
		}
		maximized := window.IsMaximised()
		x, y, w, h := 0, 0, 0, 0
		if !maximized {
			x, y = window.Position()
			w, h = window.Size()
		}
		tracker.update(maximized, x, y, w, h)
		a.ensureWindowOnScreen(window, tracker)
	}
	window.OnWindowEvent(events.Common.WindowDidResize, onGeometry)
	window.OnWindowEvent(events.Common.WindowDidMove, onGeometry)
	window.OnWindowEvent(events.Common.WindowRuntimeReady, func(*application.WindowEvent) {
		a.ensureWindowOnScreen(window, tracker)
	})
}

// ensureWindowOnScreen 一次性校验窗口是否落在任一当前屏幕内；不在（保存坐标
// 来自已断开的显示器，Wails 创建路径只做最近屏幕的 DPI 换算、不吸附位置）
// 则居中。窗口创建时的 resize 事件与 RuntimeReady 双保险触发；屏幕缓存尚未
// 填充时不消耗一次性机会。
func (a *App) ensureWindowOnScreen(window *application.WebviewWindow, tracker *windowStateTracker) {
	if a.wails == nil || a.wails.app == nil {
		return
	}
	screens := a.wails.app.Screen.GetAll()
	if len(screens) == 0 {
		return
	}
	tracker.mu.Lock()
	if tracker.checked {
		tracker.mu.Unlock()
		return
	}
	tracker.checked = true
	tracker.mu.Unlock()

	x, y := window.Position()
	w, h := window.Size()
	if w <= 0 || h <= 0 {
		return
	}
	for _, screen := range screens {
		visible := screen.Bounds.Intersect(application.Rect{X: x, Y: y, Width: w, Height: h})
		if visible.Width >= windowVisibleMinWidth && visible.Height >= windowVisibleMinHeight {
			return
		}
	}
	window.Center()
}

// saveWindowState 在应用关闭（ServiceShutdown）时把最近的窗口几何落盘。
// 只读 tracker 快照，不查询窗口。
func (a *App) saveWindowState() {
	if a.wails == nil || a.wails.windowState == nil {
		return
	}
	s, ok := a.wails.windowState.snapshot()
	if !ok {
		return
	}
	_ = persistWindowState(s)
}
