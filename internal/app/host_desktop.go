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
	DefaultWindowWidth  = 1180
	DefaultWindowHeight = 760
	MinWindowWidth      = 860
	MinWindowHeight     = 600
	// InitialWindowRatio is the golden-ratio share of the primary screen used
	// on first launch, before the user has resized the window manually.
	InitialWindowRatio     = 0.618
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
	if a.wails != nil {
		a.events = wailsEventSink{app: a.wails.app}
	}
	a.fitInitialWindowToScreen()
	a.setupSystemTray()
	a.setupCloseToTrayHook()
	_ = a.ensureInitialized()
	// Warm the one-time POSIX login-shell PATH probe without delaying the UI.
	// run_command/background_process wait on the same sync.Once if needed.
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
	// Persist the window size as a fallback for direct quits (tray Quit)
	// where the WindowClosing event may never fire.
	a.persistWindowSize()
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

// fitInitialWindowToScreen adapts the Wails desktop window to the active
// display. On first launch (no saved size) the window opens at a golden-ratio
// share of the primary screen (61.8% x 61.8%); afterwards the user's last
// manually resized size is restored. Agent/runtime code does not depend on
// these host APIs.
func (a *App) fitInitialWindowToScreen() {
	if a.wails == nil || a.wails.app == nil || a.wails.window == nil {
		return
	}
	cfg, err := a.getConfig()
	if err != nil {
		return
	}
	window := a.wails.window
	screens := a.wails.app.Screen.GetAll()
	if len(screens) == 0 {
		return
	}
	screen := screens[0]
	for _, candidate := range screens {
		if candidate.IsPrimary {
			screen = candidate
			break
		}
	}
	screenWidth := screen.Size.Width
	screenHeight := screen.Size.Height
	if screenWidth <= 0 || screenHeight <= 0 {
		return
	}
	maxWidth := int(float64(screenWidth) * 0.92)
	maxHeight := int(float64(screenHeight) * 0.86)
	// A saved user size wins and is clamped to the current screen; without
	// one (first launch) the window opens at 61.8% of the primary screen.
	var width, height int
	if cfg.WindowWidth > 0 && cfg.WindowHeight > 0 {
		width = clampInt(cfg.WindowWidth, minInt(MinWindowWidth, maxWidth), maxWidth)
		height = clampInt(cfg.WindowHeight, minInt(MinWindowHeight, maxHeight), maxHeight)
		window.SetMinSize(minInt(MinWindowWidth, maxWidth), minInt(MinWindowHeight, maxHeight))
	} else {
		width = int(float64(screenWidth) * InitialWindowRatio)
		height = int(float64(screenHeight) * InitialWindowRatio)
		runtimeMinWidth := minInt(MinWindowWidth, width)
		runtimeMinHeight := minInt(MinWindowHeight, height)
		window.SetMinSize(runtimeMinWidth, runtimeMinHeight)
	}
	window.SetSize(width, height)
	window.Center()
	// Save the user's manual resize afterwards; the listener only sees
	// user-driven WindowDidResize events, never our SetSize above.
	a.rememberWindowSize()
}

// rememberWindowSize persists the main window's size to config.json when the
// window closes (close-to-tray hides it, keeping the last visible size), with
// a ServiceShutdown fallback for direct quits. It is registered once from
// fitInitialWindowToScreen. No write ever happens while the user is dragging,
// so resize events cannot cause disk writes.
func (a *App) rememberWindowSize() {
	if a.wails == nil || a.wails.window == nil {
		return
	}
	a.wails.window.OnWindowEvent(events.Common.WindowClosing, func(_ *application.WindowEvent) {
		a.persistWindowSize()
	})
}

// persistWindowSize writes the main window's current size to config.json when
// it differs from the stored value. No-op when the window is gone or the size
// is unknown.
func (a *App) persistWindowSize() {
	if a.wails == nil || a.wails.window == nil {
		return
	}
	w, h := a.wails.window.Size()
	if w <= 0 || h <= 0 {
		return
	}
	a.mu.Lock()
	cfg := a.config
	a.mu.Unlock()
	if cfg.WindowWidth == w && cfg.WindowHeight == h {
		return
	}
	cfg.WindowWidth = w
	cfg.WindowHeight = h
	_ = a.saveConfig(cfg)
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
	if !info.IsDir() {
		path = filepath.Dir(path)
	}
	var cmd *exec.Cmd
	switch goruntime.GOOS {
	case "windows":
		cmd = exec.Command("explorer.exe", path)
	case "darwin":
		cmd = exec.Command("open", path)
	default:
		cmd = exec.Command("xdg-open", path)
	}
	return cmd.Start()
}
