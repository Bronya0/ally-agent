// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"time"

	"github.com/egoist/mygo"
)

// Run completion reaches this host on the UI thread, including sessions with
// no open tab. MyGo owns notification delivery, sound and click handling.
func (a *desktop) notifyRunCompletion(event string, payload map[string]any) {
	title := completionTitle(event, payload)
	if title == "" || a.win == nil || mygo.App.IsActive() || time.Since(a.lastCompletionNotice) < 700*time.Millisecond {
		return
	}
	a.lastCompletionNotice = time.Now()
	notification := mygo.NewNotification(mygo.NotificationOptions{
		ID:    "ally-" + event,
		Title: "Ally",
		Body:  title,
	})
	win := a.win
	notification.OnClick(func() { win.Show(); win.Focus(); notification.Close() })
	// Permission prompts on macOS can block Show until the user answers.
	go func() { _ = notification.Show() }()
}

func completionTitle(event string, payload map[string]any) string {
	switch event {
	case "run:done", "scheduled:run_done":
		return "任务完成"
	case "run:error", "scheduled:run_error":
		if str(payload, "status") == "cancelled" {
			return "任务已取消"
		}
		return "任务出错"
	default:
		return ""
	}
}
