package desktop

import "testing"

func TestTaskCenterReceivesScheduledLifecycle(t *testing.T) {
	a := &desktop{}
	for _, tc := range []struct {
		event, status string
		running       bool
		count         int
	}{
		{"scheduled:update", "scheduled", false, 0},
		{"scheduled:run_start", "running", true, 0},
		{"scheduled:run_done", "completed", false, 1},
		{"scheduled:run_start", "running", true, 1},
		{"scheduled:run_error", "failed", false, 2},
	} {
		a.applyEvent(tc.event, map[string]any{"task": map[string]any{"id": "task", "lastStatus": tc.status, "running": tc.running, "runCount": tc.count, "lastSummary": "current log"}})
		if len(a.tasks) != 1 {
			t.Fatalf("task missing/duplicated after %s", tc.event)
		}
		got := a.tasks[0]
		if got.LastStatus != tc.status || got.Running != tc.running || got.RunCount != tc.count || got.LastSummary != "current log" {
			t.Fatalf("stale task after %s: %+v", tc.event, got)
		}
	}
	a.applyEvent("scheduled:update", map[string]any{"deleted": "task"})
	if len(a.tasks) != 0 {
		t.Fatal("deleted task still visible")
	}
}
