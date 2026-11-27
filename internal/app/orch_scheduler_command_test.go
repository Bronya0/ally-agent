// SPDX-License-Identifier: GPL-3.0-only
package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestScheduledCommandTimeoutDoesNotBecomeService(t *testing.T) {
	a := newIsolatedTestApp(t)
	t.Cleanup(a.stopAllServices)
	root := t.TempDir()
	command := "echo ready > ready.txt; sleep 5; echo unexpected > late.txt"
	if commandShell(command, "").name == "powershell" {
		command = "echo ready > ready.txt; Start-Sleep -Seconds 5; echo unexpected > late.txt"
	}
	task := ScheduledTask{ID: "command", Command: command, Workspace: root, TimeoutSeconds: 600, Running: true}
	m := &scheduledTaskManager{app: a, path: filepath.Join(root, "tasks.json"), tasks: map[string]*ScheduledTask{task.ID: &task}}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	timeout := make(chan time.Time, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		m.runCommandTask(task, ctx, ConfigState{Workspace: root}, timeout)
	}()
	poll := time.NewTicker(5 * time.Millisecond)
	defer poll.Stop()
ready:
	for {
		select {
		case <-done:
			t.Fatalf("command finished before test timeout: %+v", task)
		case <-ctx.Done():
			t.Fatal("command did not reach readiness")
		case <-poll.C:
			if _, err := os.Stat(filepath.Join(root, "ready.txt")); err == nil {
				break ready
			}
		}
	}
	timeout <- time.Now()
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("timed-out command did not stop promptly")
	}
	if task.Running || task.LastStatus != "timed_out" || task.RunCount != 1 {
		t.Fatalf("unexpected task result: %+v", task)
	}
	if services := a.listServices().Services; len(services) != 0 {
		t.Fatalf("timed-out scheduled command created a service: %+v", services)
	}
	if _, err := os.Stat(filepath.Join(root, "late.txt")); !os.IsNotExist(err) {
		t.Fatalf("command continued past its deadline: %v", err)
	}
}
