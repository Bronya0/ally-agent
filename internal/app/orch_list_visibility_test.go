package app

import "testing"

func TestServiceListFinishedVisibility(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
	a := NewApp()
	for _, status := range []string{"starting", "running", "stopped", "exited"} {
		a.services[status] = &managedService{
			info:   ServiceInfo{ID: status, Status: status},
			output: newRollingBuffer(serviceOutputLimit),
		}
	}
	for _, include := range []bool{false, true} {
		result := a.listServicesForTool(include)
		want := 2
		if include {
			want = 4
		}
		if len(result.Services) != want || result.ActiveCount != 2 {
			t.Fatalf("includeFinished=%v: %#v", include, result)
		}
	}
	if len(a.ListServices().Services) != 4 {
		t.Fatal("management list must retain finished services")
	}
}

func TestScheduledListFinishedVisibility(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
	a := NewApp()
	a.scheduled = &scheduledTaskManager{tasks: map[string]*ScheduledTask{
		"pending":   {ID: "pending", NextRunAt: 100, LastStatus: "scheduled"},
		"retry":     {ID: "retry", NextRunAt: 200, LastStatus: "failed"},
		"running":   {ID: "running", Running: true},
		"completed": {ID: "completed", LastStatus: "completed"},
		"missed":    {ID: "missed", LastStatus: "missed"},
		"invalid":   {ID: "invalid", LastStatus: "invalid"},
	}}
	for _, include := range []bool{false, true} {
		raw, err := a.executeScheduledTaskTool(ConfigState{}, ScheduledTaskToolRequest{Action: "list", IncludeFinished: include})
		if err != nil {
			t.Fatal(err)
		}
		result := raw.(ScheduledTaskToolResult)
		want := 3
		if include {
			want = 6
		}
		if len(result.Tasks) != want || result.Count != want || result.Truncated {
			t.Fatalf("includeFinished=%v: %#v", include, result)
		}
		for _, task := range result.Tasks {
			if !include && !task.Running && task.NextRunAt == 0 {
				t.Fatalf("inactive task leaked: %#v", task)
			}
		}
	}
	if len(a.ListScheduledTasks()) != 6 {
		t.Fatal("management list must retain inactive tasks")
	}
}
