package app

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"time"

	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

const (
	DefaultWindowWidth     = 1180
	DefaultWindowHeight    = 760
	MinWindowWidth         = 860
	MinWindowHeight        = 600
	WindowsWindowClassName = "AllyMainWindow"
)

// wailsEventSink adapts the host-neutral eventSink contract to Wails runtime
// events. It is the only type in package app that imports wruntime.
type wailsEventSink struct {
	Context context.Context
}

func (s wailsEventSink) Emit(name string, payload any) {
	if s.Context == nil || s.Context.Err() != nil {
		return
	}
	wruntime.EventsEmit(s.Context, name, payload)
}

// Startup is the Wails lifecycle adapter. It installs the desktop event sink
// and delegates long-lived Agent services to their host-neutral modules.
func (a *App) Startup(ctx context.Context) {
	a.ctx = ctx
	a.events = wailsEventSink{Context: ctx}
	a.fitInitialWindowToScreen(ctx)
	_ = a.ensureInitialized()
	_ = a.loadServiceHistory()
	_ = a.startScheduledTaskManager()
	// Load persisted token stats in the background and start the async flusher.
	// Neither startup disk IO nor persistence can block normal chat handling.
	if a.stats != nil {
		go func() {
			a.stats.load()
			a.stats.run(ctx)
		}()
	}
	// Clean up any Ally.exe.bak left from a previous self-update.
	cleanupUpdateBackup()
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
}

// fitInitialWindowToScreen adapts the Wails desktop window to the active
// display. Agent/runtime code does not depend on these host APIs.
func (a *App) fitInitialWindowToScreen(ctx context.Context) {
	screens, err := wruntime.ScreenGetAll(ctx)
	if err != nil || len(screens) == 0 {
		return
	}
	screen := screens[0]
	for _, candidate := range screens {
		if candidate.IsCurrent {
			screen = candidate
			break
		}
		if candidate.IsPrimary {
			screen = candidate
		}
	}
	screenWidth := screen.Size.Width
	screenHeight := screen.Size.Height
	if screenWidth <= 0 {
		screenWidth = screen.Width
	}
	if screenHeight <= 0 {
		screenHeight = screen.Height
	}
	if screenWidth <= 0 || screenHeight <= 0 {
		return
	}
	maxWidth := int(float64(screenWidth) * 0.92)
	maxHeight := int(float64(screenHeight) * 0.86)
	runtimeMinWidth := minInt(MinWindowWidth, maxWidth)
	runtimeMinHeight := minInt(MinWindowHeight, maxHeight)
	width := clampInt(DefaultWindowWidth, runtimeMinWidth, maxWidth)
	height := clampInt(DefaultWindowHeight, runtimeMinHeight, maxHeight)
	wruntime.WindowSetMinSize(ctx, runtimeMinWidth, runtimeMinHeight)
	wruntime.WindowSetSize(ctx, width, height)
	wruntime.WindowCenter(ctx)
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
	selected, err := wruntime.OpenDirectoryDialog(a.ctx, wruntime.OpenDialogOptions{
		Title:            "选择 Agent 工作区",
		DefaultDirectory: current,
	})
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
