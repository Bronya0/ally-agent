package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/gofrs/flock"
)

type runtimeLifecycle struct {
	workspace       string
	requestShutdown context.CancelFunc
	mcpCwdOnce      sync.Once
	mcpCwd          string
	mcpCwdErr       error
	stopMu          sync.Mutex
	background      sync.WaitGroup
	lock            *flock.Flock
	cancel          context.CancelFunc
}

// acquireRuntimeLock excludes all hosts from concurrently writing one data directory.
func (a *App) acquireRuntimeLock() error {
	if a.runtime.lock != nil {
		return nil
	}
	if err := os.MkdirAll(appDataDir(), 0700); err != nil {
		return err
	}
	lock := flock.New(filepath.Join(appDataDir(), "runtime.lock"))
	locked, err := lock.TryLock()
	if err != nil {
		return err
	}
	if !locked {
		return fmt.Errorf("data directory already in use: %s", appDataDir())
	}

	a.runtime.lock = lock
	return nil
}

// startRuntime is shared by desktop and server hosts. Install event sinks first.
func (a *App) startRuntime(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := a.acquireRuntimeLock(); err != nil {
		return err
	}
	lock := a.runtime.lock
	ctx, cancel := context.WithCancel(ctx)
	a.runtime.cancel = cancel
	a.ctx = ctx
	if err := a.ensureInitialized(); err != nil {
		cancel()
		_ = lock.Unlock()
		a.runtime.lock = nil
		return err
	}
	// Warm the one-time POSIX login-shell PATH probe without delaying the UI.
	// command/service wait on the same sync.Once if needed.
	go warmCommandEnvironment()
	if err := a.startScheduledTaskManager(); err != nil {
		a.logAppError("scheduled tasks load failed", "error", err.Error())
	}
	// Keep filesystem maintenance off the startup path and outside a.mu.
	// One tracked worker prevents cleanup/index repair from flooding disk at boot.
	a.mu.Lock()
	workspace := a.config.Workspace
	a.mu.Unlock()
	a.runtime.background.Add(1)
	go func() {
		defer a.runtime.background.Done()
		timer := time.NewTimer(2 * time.Second)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		a.runStartupMaintenance(ctx, workspace)
	}()

	// Load persisted token stats in the background and start the async flusher.
	// Neither startup disk IO nor persistence can block normal chat handling.
	if a.stats != nil {
		a.stats.start(ctx)
	}
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
		root, _ := a.mcpWorkspaceRoot(a.runtimeConfig(cfg))
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
			a.runtime.background.Add(1)
			go func() {
				defer a.runtime.background.Done()
				if err := manager.StartAll(ctx); err != nil {
					// MCP start errors are non-fatal.
				}
				a.emitMcpStatus()
			}()

		}
	}
	return nil
}

func (a *App) runStartupMaintenance(ctx context.Context, workspace string) {
	if ctx.Err() != nil {
		return
	}
	cleanupCommandSpillFiles(workspace)
	if ctx.Err() != nil {
		return
	}
	_ = a.loadServiceHistory()
	if ctx.Err() != nil {
		return
	}
	a.cleanupStaleTempWorkspaces()
	if ctx.Err() != nil {
		return
	}
	if _, err := a.repairSessionIndex(ctx); err != nil && ctx.Err() == nil {
		a.logAppError("session index repair failed", "error", err.Error())
	}
}

// stopRuntime closes admission before cancelling runs, and waits for runChat's
// deferred checkpoints before tearing down resources or releasing the data lock.
func (a *App) stopRuntime(timeout time.Duration) error {
	a.runtime.stopMu.Lock()
	defer a.runtime.stopMu.Unlock()
	if a.runtime.cancel != nil {
		a.runtime.cancel()
	}

	a.mu.Lock()
	a.stopping = true
	cancels := make([]context.CancelFunc, 0, len(a.runs)+len(a.compactingCancels))
	for _, cancel := range a.runs {
		cancels = append(cancels, cancel)
	}
	for _, cancel := range a.compactingCancels {
		cancels = append(cancels, cancel)
	}
	a.mu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
	deadline, stopDeadline := context.WithTimeout(context.Background(), timeout)
	defer stopDeadline()
	end, _ := deadline.Deadline()
	// Kill managed services immediately, in parallel with run checkpointing.
	// Do not skip their cleanup when checkpointing consumes the shutdown budget.
	servicesDone := make(chan error, 1)
	go func() { servicesDone <- a.stopAllServicesWithError() }()
	shutdownErr := a.shutdownScheduledTasks(time.Until(end))
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
waitRuns:
	for {
		a.mu.Lock()
		pending := len(a.runs) + len(a.compactingSessions)
		a.mu.Unlock()
		if pending == 0 {
			break
		}
		select {
		case <-deadline.Done():
			shutdownErr = errors.Join(shutdownErr, errors.New("shutdown timed out waiting for task checkpoints"))
			break waitRuns
		case <-tick.C:
		}
	}
	var servicesErr error
	resourceErr := waitRuntimeShutdown(deadline, func() error {
		servicesErr = <-servicesDone
		if manager := a.activeMcpManager(); manager != nil {
			manager.Shutdown()
		}
		return nil
	})
	if resourceErr != nil {
		return errors.Join(shutdownErr, resourceErr)
	}
	backgroundDone := make(chan struct{})
	go func() { a.runtime.background.Wait(); close(backgroundDone) }()
	select {
	case <-backgroundDone:
	case <-deadline.Done():
		shutdownErr = errors.Join(shutdownErr, errors.New("shutdown timed out waiting for background initialization"))
	}
	err := waitRuntimeShutdown(deadline, func() error {
		var cleanupErr error
		if shutdownErr == nil && servicesErr == nil {
			a.cleanupTempWorkspacesOnExit()
			if a.runtime.mcpCwd != "" {
				cleanupErr = os.RemoveAll(a.runtime.mcpCwd)
			}
		}
		a.mu.Lock()
		oauth := a.chatGPT
		a.mu.Unlock()
		if oauth != nil {
			oauth.Close()
		}
		if a.stats != nil {
			cleanupErr = errors.Join(cleanupErr, a.stats.stop(min(statsShutdownTimeout, max(0, time.Until(end)))))
		}
		return cleanupErr
	})
	if errors.Is(err, context.DeadlineExceeded) {
		return errors.Join(shutdownErr, servicesErr, err)
	}
	err = errors.Join(shutdownErr, servicesErr, err)
	// A live service can still write workspace data: preserve its directories
	// and the data lock, but always close OAuth and flush statistics above.
	if shutdownErr == nil && servicesErr == nil && a.runtime.lock != nil {
		err = errors.Join(err, a.runtime.lock.Unlock())
		a.runtime.lock = nil
	}
	return err
}

// Bound every shutdown phase by the same deadline. On timeout the data lock is
// retained: unfinished workers may still be writing until the process exits.
func waitRuntimeShutdown(ctx context.Context, work func() error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- work() }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}
