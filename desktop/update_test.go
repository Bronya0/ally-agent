package desktop

import (
	"context"
	"errors"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	backend "ally-dev/internal/app"
)

func TestOnlyPackagedReleasesCheckAutomatically(t *testing.T) {
	for _, tc := range []struct {
		current, bundled string
		want             bool
	}{
		{"0.1.0", "", false}, {"dev", "v1.28.0", false}, {"1.28.0", "v1.28.0", true},
		{"1.28.0", "v1.27.0", false}, {"1.28.0-beta.1", "v1.28.0-beta.1", false},
	} {
		if got := packagedReleaseVersion(tc.current, tc.bundled); got != tc.want {
			t.Fatalf("%q/%q: %v", tc.current, tc.bundled, got)
		}
	}
}

func TestSnapshotStoresDraftOutsideModelMessages(t *testing.T) {
	tab := &conversation{ID: "draft", Draft: "未发送的草稿"}
	stored := snapshot(tab)
	if stored.Draft != tab.Draft || len(stored.Messages) != 0 {
		t.Fatal("draft lost or injected into messages")
	}
	tab.Draft = ""
	if stored = snapshot(tab); stored.Draft != "" {
		t.Fatal("cleared draft survived")
	}
}

func TestUpdatePreflightDoesNotProceedWhileFileIsSaving(t *testing.T) {
	a := &desktop{workspaceFiles: map[string]*workspaceFile{"file": {Saving: true}}}
	proceeded, cancelled := false, false
	a.prepareWindowClose(func() { proceeded = true }, func() { cancelled = true })
	if proceeded || !cancelled {
		t.Fatal("updating proceeded before file save finished")
	}
}

func TestUpdateCheckDecisions(t *testing.T) {
	for _, tc := range []struct {
		name                                          string
		can, auto, skipped, dismissed, staged, silent bool
		wantDownload                                  bool
		wantState                                     string
		wantOpen                                      bool
	}{
		{"automatic", true, true, false, false, false, true, true, "available", false},
		{"disabled", true, false, false, false, false, true, false, "available", false},
		{"unsupported-linux", false, true, false, false, false, true, false, "available", false},
		{"skipped", true, true, true, false, false, true, false, "available", false},
		{"legacy-dismissed", true, true, false, true, false, true, false, "available", false},
		{"already-staged", true, true, false, false, true, true, false, "ready", true},
		{"skipped-staged", true, true, true, false, true, true, false, "available", false},
		{"manual-skipped", true, true, true, false, false, false, false, "available", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := &desktop{}
			result := backend.CheckForUpdatesResult{OK: true, Tag: "v1.29.0", CanAutoUpdate: tc.can, AutoUpdateEnabled: tc.auto, Skipped: tc.skipped}
			if tc.dismissed {
				a.prefs.DismissedUpdateTag = result.Tag
			}
			if tc.staged {
				result.StagedVersion = result.Tag
			}
			got := a.acceptUpdateCheck(result, "1.28.0", tc.silent)
			if got != tc.wantDownload || a.upd.State != tc.wantState || a.upd.Open != tc.wantOpen {
				t.Fatalf("download=%v state=%+v", got, a.upd)
			}
		})
	}
	a := &desktop{}
	if a.acceptUpdateCheck(backend.CheckForUpdatesResult{OK: true, Tag: "v1.27.0"}, "1.28.0", true) || a.upd.Tag != "" || a.upd.State != "latest" {
		t.Fatal("offered a downgrade")
	}
}

func TestUpdateEventsKeepOwnershipAndProgress(t *testing.T) {
	a := &desktop{}
	a.upd.Tag, a.upd.State, a.upd.Busy = "v1.29.0", "downloading", true
	a.applyEvent("update:progress", map[string]any{"version": "v1.29.0", "stage": "download", "percent": 42, "bytesDownloaded": 420, "bytesTotal": 1000})
	if a.upd.Progress != 42 || a.upd.BytesNow != 420 || a.upd.BytesTotal != 1000 {
		t.Fatalf("lost progress: %+v", a.upd)
	}
	a.applyEvent("update:progress", map[string]any{"version": "v1.28.0", "percent": 99})
	if a.upd.Progress != 42 {
		t.Fatal("stale version overwrote progress")
	}
	a.upd.Cancelling = true
	a.applyEvent("update:ready", map[string]any{"version": "v1.29.0"})
	if a.upd.State == "ready" {
		t.Fatal("cancelled operation became ready")
	}
	a.applyEvent("update:cancelled", map[string]any{"version": "v1.29.0"})
	if !a.upd.Busy {
		t.Fatal("event released ownership before worker completed")
	}
}

func TestUpdatePreservesSilentErrorsAndWindowsExitMeaning(t *testing.T) {
	a := &desktop{}
	a.upd.Silent = true
	a.updateFailed("network failed")
	if a.upd.Open || !strings.Contains(a.settings.UpdateNote, "network failed") {
		t.Fatal("background error opened a modal or was lost")
	}
	if strings.Contains(updateApplyLabel("windows"), "重启") || !strings.Contains(updateReadyNote("windows"), "重新打开") {
		t.Fatal("Windows promised an automatic relaunch")
	}
	if !strings.Contains(updateReadyNote("darwin"), "自动") {
		t.Fatal("macOS relaunch instructions missing")
	}
}

func TestWindowWriteBarrierWaitsForEveryQueueAndConsumesErrors(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var failures windowWriteFailures
		failures.record("config", errors.New("disk full"))
		slow := make(chan struct{})
		done := make(chan error, 1)
		go func() { done <- waitWindowWrites(context.Background(), []<-chan struct{}{nil, slow}, &failures) }()
		synctest.Wait()
		select {
		case <-done:
			t.Fatal("did not wait for session queue")
		default:
		}
		close(slow)
		synctest.Wait()
		if err := <-done; err == nil || !strings.Contains(err.Error(), "disk full") {
			t.Fatal("save failure lost")
		}
		if err := waitWindowWrites(context.Background(), nil, &failures); err != nil {
			t.Fatal("consumed error poisoned retry")
		}
		failures.record("config", errors.New("old failure"))
		failures.record("config", nil)
		if err := failures.take(); err != nil {
			t.Fatal("successful retry did not clear failure")
		}
	})
}

func TestWindowWriteBarrierTimesOut(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		var failures windowWriteFailures
		if err := waitWindowWrites(ctx, []<-chan struct{}{make(chan struct{})}, &failures); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("missing timeout: %v", err)
		}
	})
}
