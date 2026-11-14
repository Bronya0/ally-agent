package app

import (
	"os"
	"path/filepath"
	"testing"
)

func TestScheduledDeletePersistenceFailureKeepsTask(t *testing.T) {
	parent := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(parent, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	task := &ScheduledTask{ID: "task", Name: "keep"}
	manager := &scheduledTaskManager{path: filepath.Join(parent, "tasks.json"), tasks: map[string]*ScheduledTask{task.ID: task}}
	if err := manager.delete(task.ID); err == nil {
		t.Fatal("expected persistence failure")
	}
	if manager.tasks[task.ID] != task {
		t.Fatal("failed deletion removed task from memory")
	}
}
