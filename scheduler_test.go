package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestScheduledTaskCreateListDelete(t *testing.T) {
	root := t.TempDir()
	app := NewApp()
	app.configPath = filepath.Join(root, "config.json")
	app.config = ConfigState{Workspace: root}
	if err := app.startScheduledTaskManager(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(app.stopScheduledTaskManager)

	created, err := app.executeScheduledTaskTool(app.config, ScheduledTaskToolRequest{
		Action:      "create",
		Name:        "hourly check",
		Instruction: "Inspect the workspace and report issues.",
		Schedule:    "1h",
	})
	if err != nil {
		t.Fatal(err)
	}
	createResult := created.(ScheduledTaskToolResult)
	if createResult.Task == nil || createResult.Task.ID == "" {
		t.Fatalf("expected created task id, got %#v", createResult)
	}
	if createResult.Task.MaxSteps != defaultScheduledTaskSteps || createResult.Task.TimeoutSeconds != defaultScheduledTaskTimeout {
		t.Fatalf("expected backend execution defaults, got %#v", createResult.Task)
	}

	listed, err := app.executeScheduledTaskTool(app.config, ScheduledTaskToolRequest{Action: "list"})
	if err != nil {
		t.Fatal(err)
	}
	listResult := listed.(ScheduledTaskToolResult)
	if listResult.Count != 1 || len(listResult.Tasks) != 1 {
		t.Fatalf("expected one scheduled task, got %#v", listResult)
	}

	if err := app.DeleteScheduledTask(createResult.Task.ID); err != nil {
		t.Fatal(err)
	}
	if tasks := app.ListScheduledTasks(); len(tasks) != 0 {
		t.Fatalf("expected task deletion, got %#v", tasks)
	}
}

func TestScheduledTasksClearLegacyPersistenceOnStartup(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "scheduled_tasks.json")
	if err := os.WriteFile(path, []byte(`[{"id":"legacy"}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	app := NewApp()
	app.configPath = filepath.Join(root, "config.json")
	app.config = ConfigState{Workspace: root}
	if err := app.startScheduledTaskManager(); err != nil {
		t.Fatal(err)
	}
	app.stopScheduledTaskManager()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("scheduled task persistence should be removed, stat err=%v", err)
	}
}

func TestScheduledTaskRejectsTooFrequentInterval(t *testing.T) {
	task := ScheduledTask{
		ID: "task_test", Name: "too frequent", Instruction: "check", Workspace: t.TempDir(),
		Schedule: ScheduledTaskSchedule{Type: "interval", Every: "30s"},
	}
	if err := normalizeScheduledTask(&task, time.Now()); err == nil {
		t.Fatal("expected intervals shorter than one minute to be rejected")
	}
}

func TestScheduledTaskToolsIncludeNormalCommandsAndExcludeScheduler(t *testing.T) {
	app := NewApp()
	tools := app.scheduledTaskTools(ConfigState{})
	foundCommand := false
	for _, tool := range tools {
		if tool.Function == nil {
			continue
		}
		if tool.Function.Name == "scheduled_task" {
			t.Fatal("scheduled executions must not recursively manage scheduled tasks")
		}
		if tool.Function.Name == "run_command" {
			foundCommand = true
		}
	}
	if !foundCommand {
		t.Fatal("scheduled executions must receive run_command")
	}
}
