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
	goruntime "runtime"
	"sync/atomic"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/services/notifications"
)

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
func (a *App) SetNotifier(n completionNotifier) {
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
