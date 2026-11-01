// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"strings"
	"testing"

	backend "ally-dev/internal/app"
	"github.com/egoist/mygo/ui"
)

// 回到最新 has to move the log to its newest line, not just refetch it: the
// dialog used to redraw the same output at the same offset, so the button
// looked dead. It must also work when the service already left the list.
func TestServiceLogBackToLatestJumpsToTheEnd(t *testing.T) {
	a := &desktop{}
	a.prefs.Appearance = "dark"
	a.tasksF.LogOpen = true
	a.tasksF.LogService = "svc-gone"
	a.tasksF.LogTitle = "生成服务"
	a.tasksF.LogMeta = "运行中"
	a.tasksF.LogBody = strings.Repeat("output line\n", 200)
	a.tasksF.LogScroll.Y = 0

	tt := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(a.theme())
		c.DisableAnimations()
		a.taskLogDialog(c)
	}, 1000, 700)
	if a.serviceLogAtEnd() {
		t.Fatalf("log starts at the end (y=%v maxY=%v), the jump cannot be told apart", a.tasksF.LogScroll.Y, a.tasksF.LogScroll.MaxY)
	}
	if err := tt.Click("回到最新"); err != nil {
		t.Fatalf("回到最新 is not clickable: %v", err)
	}
	// The container settles the offset it was asked for and writes it back, so
	// the end reads as its own value rather than math.MaxFloat32.
	if !a.serviceLogAtEnd() {
		t.Fatalf("回到最新 left the log at y=%v of maxY=%v, want the end", a.tasksF.LogScroll.Y, a.tasksF.LogScroll.MaxY)
	}
}

// Opening a service log starts at the newest output: the card already shows the
// tail, so a reader opening it expects the same end.
func TestServiceLogOpensAtTheNewestLine(t *testing.T) {
	a := &desktop{}
	a.tasksF.LogScroll.Y = 0
	a.showServiceLog(backend.ServiceInfo{ID: "svc_1", Name: "生成服务", Status: "running", OutputTail: "latest"})
	if !a.tasksF.LogOpen || a.tasksF.LogService != "svc_1" || a.tasksF.LogBody != "latest" {
		t.Fatalf("log dialog state not set up: %+v", a.tasksF)
	}
	if a.tasksF.LogScroll.Y < 1e8 {
		t.Fatalf("opened at y=%v, want the end", a.tasksF.LogScroll.Y)
	}
}

// A task log has no live end to chase: it opens at its first line.
func TestTaskLogOpensAtTheTop(t *testing.T) {
	a := &desktop{}
	a.tasksF.LogScroll.X, a.tasksF.LogScroll.Y = 30, 1e9
	a.openTaskLog(backend.ScheduledTask{ID: "t1", Name: "每日汇总", LastSummary: "done"})
	if a.tasksF.LogScroll.X != 0 || a.tasksF.LogScroll.Y != 0 {
		t.Fatalf("task log opened at %v/%v, want the top", a.tasksF.LogScroll.X, a.tasksF.LogScroll.Y)
	}
}
