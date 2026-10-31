package desktop

import (
	"fmt"
	"testing"
	"time"
)

func TestCompletionNoticeDuplicates(t *testing.T) {
	now := time.Now()
	a := &desktop{}
	p := map[string]any{"runId": "run-1"}
	if !a.acceptCompletionNotice("run:done", p, false, now) {
		t.Fatal("first completion was suppressed")
	}
	if a.acceptCompletionNotice("run:done", p, false, now.Add(time.Minute)) ||
		a.acceptCompletionNotice("run:error", p, false, now.Add(2*time.Minute)) {
		t.Fatal("the same run notified again")
	}
	if !a.acceptCompletionNotice("run:done", map[string]any{"runId": "run-2"}, false, now.Add(3*time.Minute)) {
		t.Fatal("a new run must still notify")
	}
}

func TestCompletionNoticeSuppressedRunsStaySeen(t *testing.T) {
	for _, active := range []bool{true, false} {
		now := time.Now()
		a := &desktop{lastCompletionNotice: now}
		p := map[string]any{"runId": "run-1"}
		if a.acceptCompletionNotice("run:done", p, active, now) ||
			a.acceptCompletionNotice("run:done", p, false, now.Add(time.Minute)) {
			t.Fatal("foreground or rate-limited completion was replayed")
		}
	}
}

func TestScheduledCompletionNoticePerExecution(t *testing.T) {
	now := time.Now()
	a := &desktop{}
	task := map[string]any{"id": "task-1", "lastRunAt": float64(1000), "runCount": float64(1)}
	p := map[string]any{"task": task}
	if !a.acceptCompletionNotice("scheduled:run_done", p, false, now) ||
		a.acceptCompletionNotice("scheduled:run_done", p, false, now.Add(time.Minute)) {
		t.Fatal("scheduled completion must notify once")
	}
	task["lastRunAt"], task["runCount"] = float64(2000), float64(2)
	if !a.acceptCompletionNotice("scheduled:run_done", p, false, now.Add(2*time.Minute)) {
		t.Fatal("next scheduled execution must notify")
	}
}

func TestScheduledNoticeIdentifiesTask(t *testing.T) {
	p := map[string]any{"task": map[string]any{"name": "周期心跳", "lastStatus": "completed"}}
	if got := completionTitle("scheduled:run_done", p); got != "定时任务「周期心跳」已完成" {
		t.Fatalf("ambiguous scheduled notice: %q", got)
	}
	p["task"].(map[string]any)["lastStatus"] = "cancelled"
	if got := completionTitle("scheduled:run_error", p); got != "定时任务「周期心跳」已取消" {
		t.Fatalf("wrong scheduled cancellation: %q", got)
	}
}

func TestOperationNotices(t *testing.T) {
	a := &desktop{}
	a.notify("")
	if len(a.notifications) != 0 {
		t.Fatal("empty notice queued")
	}
	a.notifySuccess("设置已保存")
	a.notifySuccess("设置已保存")
	a.notifyError("保存失败")
	if len(a.notifications) != 2 || a.notifications[0].Type != "success" || a.notifications[1].Type != "error" {
		t.Fatalf("wrong outcomes: %+v", a.notifications)
	}
	for i := 0; i < 20; i++ {
		a.queueNotice(string(rune('a'+i)), "info")
	}
	if len(a.notifications) != 8 {
		t.Fatal("notice queue not bounded")
	}
}

func TestPrefsSaveOutcomeSeverity(t *testing.T) {
	a := &desktop{}
	a.prefsSaveResult(nil, []string{"外观设置已保存"})
	if len(a.notifications) != 1 || a.notifications[0].Type != "success" {
		t.Fatalf("save success must not use error icon: %+v", a.notifications)
	}
	a.prefsSaveResult(fmt.Errorf("disk failure"), []string{"外观设置已保存"})
	if len(a.notifications) != 2 || a.notifications[1].Type != "error" {
		t.Fatal("save failure must use error notice")
	}
}

// The rail footer carries no operation status line, so a hint that used to be
// written there has to reach the user as a notice instead of disappearing.
func TestComposerHintsSurfaceAsNotices(t *testing.T) {
	a := &desktop{ready: true}
	if a.send(&conversation{}, "hi") {
		t.Fatal("send without a model must not start a run")
	}
	if len(a.notifications) != 1 || a.notifications[0].Title != "请先在模型页面配置并选择模型" {
		t.Fatalf("model setup hint lost: %+v", a.notifications)
	}
	a = &desktop{ready: true}
	if a.send(&conversation{Busy: true}, "hi") {
		t.Fatal("an injection into a run that has not started must be refused")
	}
	if len(a.notifications) != 1 || a.notifications[0].Type != "error" {
		t.Fatalf("injection hint lost: %+v", a.notifications)
	}
}
