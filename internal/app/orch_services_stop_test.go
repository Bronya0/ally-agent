package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRuntimeServiceStopFailureStillFlushesStats(t *testing.T) {
	a := newApiTestApp(t)
	t.Setenv("ALLY_DATA_DIR", t.TempDir())
	if err := a.acquireRuntimeLock(); err != nil {
		t.Fatal(err)
	}
	// Cleanup must preserve directories a surviving service could still write.
	a.runtime.mcpCwd = t.TempDir()
	marker := filepath.Join(a.runtime.mcpCwd, "still-in-use")
	if err := os.WriteFile(marker, []byte("data"), 0600); err != nil {
		t.Fatal(err)
	}
	svc := &managedService{info: ServiceInfo{ID: "failing-stop", Status: "running"}, output: newRollingBuffer(serviceOutputLimit), waitDone: make(chan struct{}), stopTree: func() error { return errors.New("termination refused") }}
	a.services[svc.info.ID] = svc
	a.stats = newStatsRecorder()
	a.stats.storageDir = t.TempDir()
	a.stats.start(context.Background())
	t.Cleanup(func() {
		_ = a.stats.stop(time.Second)
		if a.runtime.lock != nil {
			_ = a.runtime.lock.Unlock()
		}
	})
	now := time.Now()
	a.stats.record(statsRecord{Model: "shutdown-test", Ts: now.UnixMilli(), InputTokens: 42, Requests: 1})
	if err := a.stopRuntime(2 * time.Second); err == nil || !strings.Contains(err.Error(), "termination refused") {
		t.Fatalf("stop error: %v", err)
	}
	date := statsDateKey(now.UnixMilli())
	records, err := readStatsDayFile(filepath.Join(a.stats.storageDir, date+".json"), date)
	if err != nil || len(records) != 1 || records[0].InputTokens != 42 {
		t.Fatalf("statistics not flushed: %+v %v", records, err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("deleted directory still owned by live service")
	}
	if a.runtime.lock == nil {
		t.Fatal("released data lock while service could still write")
	}
	// A subsequent confirmed exit allows the remaining cleanup and lock release.
	svc.mu.Lock()
	svc.info.Status = "exited"
	svc.mu.Unlock()
	if err := a.stopRuntime(2 * time.Second); err != nil {
		t.Fatal(err)
	}
	if a.runtime.lock != nil {
		t.Fatal("data lock retained after safe shutdown")
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("temporary directory not cleaned: %v", err)
	}
}

func TestServiceConfirmedStopDoesNotWaitForOutputDrain(t *testing.T) {
	redirectAppStateDir(t)
	a := NewApp()
	svc := &managedService{info: ServiceInfo{ID: "slow-output", Status: "running"}, output: newRollingBuffer(serviceOutputLimit), waitDone: make(chan struct{}), stopTree: func() error { return nil }}
	a.services[svc.info.ID] = svc
	started := time.Now()
	info, err := a.StopService(StopServiceRequest{ID: svc.info.ID})
	if err != nil || info.Status != "stopped" {
		t.Fatalf("confirmed termination reported as failure: %+v %v", info, err)
	}
	if time.Since(started) > time.Second {
		t.Fatal("output drain blocked confirmed stop")
	}
	if len(a.finishedQueue) != 1 {
		t.Fatal("confirmed stop not retained as finished")
	}
}

func TestServiceExitClearsOnlyTransientStopError(t *testing.T) {
	for _, exitErr := range []error{nil, errors.New("real process wait failure")} {
		t.Run(func() string {
			if exitErr == nil {
				return "normal-exit"
			}
			return "execution-error"
		}(), func(t *testing.T) {
			redirectAppStateDir(t)
			a := NewApp()
			svc := &managedService{info: ServiceInfo{ID: "recover", Status: "running"}, output: newRollingBuffer(serviceOutputLimit), waitDone: make(chan struct{}), stopTree: func() error { return errors.New("temporary termination failure") }}
			a.services[svc.info.ID] = svc
			if _, err := a.StopService(StopServiceRequest{ID: svc.info.ID}); err == nil {
				t.Fatal("stop failure hidden")
			}
			wait := make(chan error, 1)
			a.watchPromotedCommandExit(svc, wait)
			wait <- exitErr
			select {
			case <-svc.waitDone:
			case <-time.After(time.Second):
				t.Fatal("waiter stalled")
			}
			info := svc.snapshot()
			if info.Status != "exited" {
				t.Fatalf("status: %+v", info)
			}
			want := ""
			if exitErr != nil {
				want = exitErr.Error()
			}
			if info.Error != want {
				t.Fatalf("stale or lost error: got %q want %q", info.Error, want)
			}
		})
	}
}
