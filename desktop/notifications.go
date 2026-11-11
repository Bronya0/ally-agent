// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"ally-dev/locales"
	"fmt"
	"runtime"
	"time"

	"ally-dev/resources"

	"github.com/egoist/mygo"
)

// Run completion reaches this host on the UI thread, including sessions with
// no open tab. MyGo owns notification delivery, sound and click handling.
func (a *desktop) notifyRunCompletion(event string, payload map[string]any) {
	title := completionTitle(event, payload)
	if title == "" || a.win == nil || !a.acceptCompletionNotice(event, payload, mygo.App.IsActive(), time.Now()) {
		return
	}
	opts := mygo.NotificationOptions{Title: "Ally", Body: title}
	if runtime.GOOS == "windows" {
		// A balloon otherwise enlarges the 16–32px tray icon for its main image.
		// Other platforms already show the app icon; Image adds an attachment there.
		opts.Image, _ = resources.Assets.ReadFile("icon.png")
	}
	notification := mygo.NewNotification(opts)
	notification.OnClick(func() { a.showMainWindow(); notification.Close() })
	// Permission prompts on macOS can block Show until the user answers.
	go func() {
		if notification.Show() == nil {
			// Explicitly retire balloons even if the platform misses dismissal.
			time.AfterFunc(8*time.Second, notification.Close)
		}
	}()
}

// Record completions even in the foreground or while rate limited: a delayed
// duplicate must not become a new notification after the user switches away.
func (a *desktop) acceptCompletionNotice(event string, payload map[string]any, active bool, now time.Time) bool {
	if completionTitle(event, payload) == "" {
		return false
	}
	key := ""
	if runID := str(payload, "runId"); runID != "" {
		key = "run:" + runID
	} else if task, ok := payload["task"].(map[string]any); ok && str(task, "id") != "" {
		key = fmt.Sprintf("scheduled:%s:%v:%v", str(task, "id"), task["lastRunAt"], task["runCount"])
	}
	if key != "" {
		if a.completionNotices[key] {
			return false
		}
		if a.completionNotices == nil {
			a.completionNotices = make(map[string]bool)
		}
		// Bound memory without expiring recent runs on a short timer.
		if len(a.completionNoticeOrder) == 2048 {
			delete(a.completionNotices, a.completionNoticeOrder[0])
			a.completionNoticeOrder = a.completionNoticeOrder[1:]
		}
		a.completionNotices[key] = true
		a.completionNoticeOrder = append(a.completionNoticeOrder, key)
	}
	if active || now.Sub(a.lastCompletionNotice) < 700*time.Millisecond {
		return false
	}
	a.lastCompletionNotice = now
	return true
}

func completionTitle(event string, payload map[string]any) string {
	if event == "scheduled:run_done" || event == "scheduled:run_error" {
		task, _ := payload["task"].(map[string]any)
		name := str(task, "name")
		if name == "" {
			name = str(task, "id")
		}
		result := locales.Text("已完成")
		if event == "scheduled:run_error" {
			result = locales.Text("出错")
			if str(task, "lastStatus") == "cancelled" {
				result = locales.Text("已取消")
			}
		}
		if name == "" {
			return locales.Text("定时任务") + result
		}
		return locales.Text("定时任务「") + name + "」" + result
	}
	switch event {
	case "run:done":
		return locales.Text("任务完成")
	case "run:error":
		if str(payload, "status") == "cancelled" {
			return locales.Text("任务已取消")
		}
		return locales.Text("任务出错")
	default:
		return ""
	}
}
