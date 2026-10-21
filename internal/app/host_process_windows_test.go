//go:build windows

package app

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"ally-dev/internal/sandbox"

	"golang.org/x/sys/windows"
)

// Each helper writes only beneath its parent's t.TempDir sandbox.
func TestOwnedProcessHelper(t *testing.T) {
	stage := os.Getenv("ALLY_PROCESS_TEST_STAGE")
	if stage == "" {
		return
	}
	dir := os.Getenv("ALLY_PROCESS_TEST_DIR")
	if stage == "root" || stage == "root-exit" {
		exe, err := os.Executable()
		if err != nil {
			os.Exit(2)
		}
		child := exec.Command(exe, "-test.run=^TestOwnedProcessHelper$")
		child.Dir = dir
		child.Stdout = os.Stdout
		child.Stderr = os.Stderr
		child.Env = append(os.Environ(), "ALLY_PROCESS_TEST_STAGE=child")
		hideCommandWindow(child)
		if err := child.Start(); err != nil {
			os.Exit(3)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, stage+".pid"), []byte(strconv.Itoa(os.Getpid())), 0600); err != nil {
		os.Exit(4)
	}
	if stage == "root-exit" {
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if _, err := os.Stat(filepath.Join(dir, "child.pid")); err == nil {
				os.Exit(0)
			}
			time.Sleep(10 * time.Millisecond)
		}
		os.Exit(5)
	}
	for {
		time.Sleep(time.Second)
	}
}

func TestOwnedProcessTreeStopsChildrenAndCannotTouchAnotherJob(t *testing.T) {
	root := validationSandbox(t)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	launch := func(dir, stage string) (*exec.Cmd, func() error, <-chan struct{}) {
		t.Helper()
		cmd := exec.Command(exe, "-test.run=^TestOwnedProcessHelper$")
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "ALLY_PROCESS_TEST_STAGE="+stage, "ALLY_PROCESS_TEST_DIR="+dir)
		if err := startTrackedCommand(cmd); err != nil {
			t.Fatal(err)
		}
		stop, err := serviceProcessStop(cmd.Process.Pid)
		if err != nil {
			t.Fatal(err)
		}
		done := make(chan struct{})
		go func() { _ = cmd.Wait(); unregisterProcessJob(cmd.Process.Pid); close(done) }()
		t.Cleanup(func() {
			_ = stop()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Error("helper leaked")
			}
		})
		return cmd, stop, done
	}
	otherDir := t.TempDir()
	other, _, _ := launch(otherDir, "control")
	cmd, stop, done := launch(root, "root")
	var childPID int
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		b, err := os.ReadFile(filepath.Join(root, "child.pid"))
		if err == nil {
			childPID, _ = strconv.Atoi(string(b))
			if childPID > 0 {
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if childPID == 0 {
		t.Fatal("child did not start")
	}
	// Pin the original kernel objects before termination. PID lookups after
	// Wait can observe a reused PID, and job accounting may reach zero before
	// the process object has finished signaling its exit.
	openProcess := func(pid int) windows.Handle {
		t.Helper()
		h, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
		if err != nil {
			t.Fatalf("open original process %d: %v", pid, err)
		}
		t.Cleanup(func() { _ = windows.CloseHandle(h) })
		return h
	}
	rootHandle := openProcess(cmd.Process.Pid)
	childHandle := openProcess(childPID)
	otherHandle := openProcess(other.Process.Pid)
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := stop(); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("root did not exit")
	}
	for _, target := range []struct {
		name   string
		handle windows.Handle
	}{
		{"root", rootHandle}, {"child", childHandle},
	} {
		state, err := windows.WaitForSingleObject(target.handle, 3000)
		if err != nil || state != windows.WAIT_OBJECT_0 {
			t.Fatalf("original %s process did not exit: wait=%#x error=%v", target.name, state, err)
		}
	}
	if err := stop(); err != nil {
		t.Fatal(err)
	} // stale identity is a harmless no-op
	if state, err := windows.WaitForSingleObject(otherHandle, 0); err != nil || state != uint32(windows.WAIT_TIMEOUT) {
		t.Fatalf("unrelated original process is no longer running: wait=%#x error=%v", state, err)
	}
	if err := stopProcessTree(os.Getpid()); err == nil {
		t.Fatal("allowed killing Ally itself")
	}
	if _, err := serviceProcessStop(os.Getpid()); err == nil {
		t.Fatal("unowned PID accepted")
	}
}

func TestServiceForcedStopFailureRemainsRetryable(t *testing.T) {
	redirectAppStateDir(t)
	a := NewApp()
	svc := &managedService{info: ServiceInfo{ID: "retry", Status: "running"}, output: newRollingBuffer(serviceOutputLimit), waitDone: make(chan struct{}), stopTree: func() error { return fmt.Errorf("test termination failure") }}
	a.services["retry"] = svc
	if _, err := a.StopService(StopServiceRequest{ID: "retry"}); err == nil {
		t.Fatal("failure hidden")
	}
	if svc.snapshot().Status != "running" {
		t.Fatal("failed stop was marked finished")
	}
	if len(a.finishedQueue) != 0 {
		t.Fatal("live service was queued for eviction")
	}
	svc.stopTree = func() error { close(svc.waitDone); return nil }
	info, err := a.StopService(StopServiceRequest{ID: "retry", GraceSeconds: 30})
	if err != nil || info.Status != "stopped" || info.Error != "" {
		t.Fatalf("retry: %+v %v", info, err)
	}
	a.mu.Lock()
	a.stopping = true
	a.mu.Unlock()
	_, err = a.startServiceWithConfig(context.Background(), ConfigState{Workspace: t.TempDir()}, StartServiceRequest{Description: "must not launch", Command: "echo unexpected"})
	if err == nil {
		t.Fatal("started during shutdown")
	}
}

func TestServiceTreeShutdownAndNaturalParentExit(t *testing.T) {
	for _, mode := range []string{"explicit", "shutdown", "parent-exit"} {
		t.Run(mode, func(t *testing.T) {
			root := validationSandbox(t)
			exe, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			quote := func(s string) string { return "'" + strings.ReplaceAll(filepath.ToSlash(s), "'", "'\"'\"'") + "'" }
			stage := "root"
			if mode == "parent-exit" {
				stage = "root-exit"
			}
			command := "ALLY_PROCESS_TEST_STAGE=" + stage + " ALLY_PROCESS_TEST_DIR=" + quote(root) + " " + quote(exe) + " -test.run=^TestOwnedProcessHelper$"
			a := NewApp()
			t.Cleanup(a.stopAllServices)
			info, err := a.startServiceWithConfig(context.Background(), ConfigState{Workspace: root}, StartServiceRequest{Description: "process cleanup probe", Command: command})
			if err != nil {
				t.Fatal(err)
			}
			svc := a.services[info.ID]
			deadline := time.Now().Add(5 * time.Second)
			childPID := 0
			for time.Now().Before(deadline) {
				if b, err := os.ReadFile(filepath.Join(root, "child.pid")); err == nil {
					childPID, _ = strconv.Atoi(string(b))
					if childPID > 0 {
						break
					}
				}
				time.Sleep(10 * time.Millisecond)
			}
			if childPID == 0 {
				t.Fatal("child not ready")
			}
			start := time.Now()
			switch mode {
			case "explicit":
				info, err = a.StopService(StopServiceRequest{ID: info.ID, GraceSeconds: 30})
				if err != nil || info.Status != "stopped" {
					t.Fatalf("stop: %+v %v", info, err)
				}
			case "shutdown":
				a.mu.Lock()
				a.stopping = true
				a.mu.Unlock()
				a.stopAllServices()
			}
			select {
			case <-svc.waitDone:
			case <-time.After(5 * time.Second):
				t.Fatal("service cleanup stalled")
			}
			if time.Since(start) > 2500*time.Millisecond {
				t.Fatal("stop retained a grace wait")
			}
			// kill-on-close is asynchronous; independently confirm the descendant is gone.
			deadline = time.Now().Add(2 * time.Second)
			for isProcessAlive(childPID) && time.Now().Before(deadline) {
				time.Sleep(10 * time.Millisecond)
			}
			if isProcessAlive(childPID) {
				t.Fatalf("child %d survived %s", childPID, mode)
			}
		})
	}
}

func TestTrackedCommandCancellationDuringStartup(t *testing.T) {
	root := validationSandbox(t)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 8; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		cmd := exec.CommandContext(ctx, exe, "-test.run=^TestOwnedProcessHelper$")
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "ALLY_PROCESS_TEST_STAGE=child", "ALLY_PROCESS_TEST_DIR="+root)
		timer := time.AfterFunc(time.Duration(i)*100*time.Microsecond, cancel)
		err := startTrackedCommand(cmd)
		timer.Stop()
		cancel()
		if err != nil {
			continue
		}
		done := make(chan struct{})
		go func() { _ = cmd.Wait(); unregisterProcessJob(cmd.Process.Pid); close(done) }()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			unregisterProcessJob(cmd.Process.Pid)
			t.Fatal("cancellation raced past job assignment")
		}
		if isProcessAlive(cmd.Process.Pid) {
			t.Fatal("cancelled process survived")
		}
	}
}

func TestRuntimeStopsServicesEvenWhenCheckpointTimesOut(t *testing.T) {
	a := newApiTestApp(t)
	t.Setenv("ALLY_DATA_DIR", t.TempDir())
	a.runs["blocked"] = func() {}
	done := make(chan struct{})
	svc := &managedService{info: ServiceInfo{ID: "shutdown-probe", Status: "running"}, output: newRollingBuffer(serviceOutputLimit), waitDone: done, stopTree: func() error { close(done); return nil }}
	a.services[svc.info.ID] = svc
	if err := a.stopRuntime(50 * time.Millisecond); err == nil {
		t.Fatal("expected checkpoint timeout")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("checkpoint timeout skipped service cleanup")
	}
	a.mu.Lock()
	delete(a.runs, "blocked")
	a.mu.Unlock()
	if err := a.stopRuntime(time.Second); err != nil {
		t.Fatal(err)
	}
}

func TestFileMutationCancellationStopsOwnedTree(t *testing.T) {
	root := validationSandbox(t)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("ALLY_PROCESS_TEST_STAGE", "root")
	t.Setenv("ALLY_PROCESS_TEST_DIR", root)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- runFileMutationWithContext(ctx, sandbox.Spec{}, []string{root}, []string{exe, "-test.run=^TestOwnedProcessHelper$"})
	}()
	deadline := time.Now().Add(5 * time.Second)
	childPID := 0
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(filepath.Join(root, "child.pid")); err == nil {
			childPID, _ = strconv.Atoi(string(b))
			if childPID > 0 {
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if childPID == 0 {
		t.Fatal("mutation child never started")
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancelled mutation reported success")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled mutation failed to stop")
	}
	if isProcessAlive(childPID) {
		t.Fatal("mutation child survived cancellation")
	}
}
