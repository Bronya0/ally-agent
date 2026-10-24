// SPDX-License-Identifier: GPL-3.0-only
package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"testing/synctest"
	"time"
)

// Model protocol tests exercise retries against local servers without spending
// wall time on backoff. Delay calculation and the real timer remain tested.
func newModelTestApp() *App {
	a := NewApp()
	a.retryWait = func(ctx context.Context, _ time.Duration) error { return ctx.Err() }
	return a
}

// Filesystem/process tests that do not use legacy singleton helpers can run
// concurrently. Their credentials and all fixture files stay in t.TempDir.
func newIsolatedTestApp(t *testing.T) *App {
	t.Helper()
	return newAppState(t.TempDir())
}

// newIsolatedChatTestApp builds a fully isolated App for chat/session tests: the
// data directory, config, sessions and histories all live under t.TempDir and the
// retry wait never sleeps on a real timer. It touches no process environment and
// no package-level singleton, so callers may use t.Parallel.
func newIsolatedChatTestApp(t *testing.T) *App {
	t.Helper()
	root := t.TempDir()
	a := newAppState(filepath.Join(root, ".ally_agent"))
	a.initialized = true
	a.retryWait = func(ctx context.Context, _ time.Duration) error { return ctx.Err() }
	a.configPath = filepath.Join(root, "config.json")
	a.historiesDir = filepath.Join(root, "histories")
	a.sessionsDir = filepath.Join(root, "sessions")
	a.config = defaultConfigState()
	a.config.Workspace = filepath.Join(root, "workspace")
	for _, path := range []string{a.AppDataDir(), a.historiesDir, a.sessionsDir, a.config.Workspace} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	return a
}

// newIsolatedToolTestApp is the tool-only variant: no retry clock, no model
// config, just a private workspace and data directory.
func newIsolatedToolTestApp(t *testing.T) *App {
	t.Helper()
	dir := t.TempDir()
	a := newAppState(dir)
	a.initialized = true
	a.configPath = filepath.Join(dir, "config.json")
	a.historiesDir = filepath.Join(dir, "histories")
	a.sessionsDir = filepath.Join(dir, "sessions")
	a.config = defaultConfigState()
	a.config.Workspace = filepath.Join(dir, "workspace")
	for _, path := range []string{a.historiesDir, a.sessionsDir, a.config.Workspace} {
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	return a
}

func TestRetryTimerHonorsDelayAndCancellation(t *testing.T) {
	for _, cancelWait := range []bool{false, true} {
		synctest.Test(t, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- (&App{}).waitLLMRetry(ctx, time.Hour) }()
			synctest.Wait()
			select {
			case <-done:
				t.Fatal("retry did not wait for its deadline")
			default:
			}
			if cancelWait {
				cancel()
			} else {
				time.Sleep(time.Hour)
			}
			synctest.Wait()
			if err := <-done; err != ctx.Err() {
				t.Fatalf("retry result: %v, want %v", err, ctx.Err())
			}
		})
	}
}
