// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"fmt"
	"os"
	"testing"
	"time"

	backend "ally-dev/internal/app"
)

// tempWorkspaceDeletionWindow covers the asynchronous deletion a regression
// schedules; the passing case never reaches the delete at all.
const tempWorkspaceDeletionWindow = 100 * time.Millisecond

func newTempWorkspace(t *testing.T, core *backend.App) string {
	t.Helper()
	dir, err := core.CreateTempWorkspace()
	if err != nil {
		t.Fatalf("CreateTempWorkspace: %v", err)
	}
	t.Cleanup(func() { _ = core.DeleteTempWorkspace(dir) })
	return dir
}

// tempWorkspaceTabDesktop builds a windowless desktop over sandboxes the
// backend created: only a directory under the system temp root is deletable,
// so t.TempDir() cannot stand in for one.
func tempWorkspaceTabDesktop(t *testing.T, core *backend.App, sandboxes ...string) *desktop {
	t.Helper()
	a := &desktop{ready: true, page: "plugin:test", core: core, explorer: true, workspaceFiles: map[string]*workspaceFile{}, prefs: defaultPrefs()}
	for i, root := range sandboxes {
		a.tabs = append(a.tabs, &conversation{ID: fmt.Sprintf("tab-%d", i), Workspace: root})
	}
	return a
}

func withImmediateTempWorkspaceRelease(t *testing.T) {
	t.Helper()
	restore := tempWorkspaceReleaseDelay
	tempWorkspaceReleaseDelay = 0
	t.Cleanup(func() { tempWorkspaceReleaseDelay = restore })
}

func TestClosingOneTabKeepsTheTemporaryWorkspaceOfItsSiblings(t *testing.T) {
	withImmediateTempWorkspaceRelease(t)
	core := backend.NewApp()
	shared := newTempWorkspace(t, core)
	other := newTempWorkspace(t, core)
	a := tempWorkspaceTabDesktop(t, core, shared, shared, other)

	a.closeWorkspaceTab(0)
	time.Sleep(tempWorkspaceDeletionWindow)

	if _, err := os.Stat(shared); err != nil {
		t.Fatalf("closing one of two tabs removed their shared temporary workspace: %v", err)
	}
	if len(a.tabs) != 2 {
		t.Fatalf("tabs after close = %d, want 2", len(a.tabs))
	}
}

func TestClosingTheLastTabRemovesOnlyItsTemporaryWorkspace(t *testing.T) {
	withImmediateTempWorkspaceRelease(t)
	core := backend.NewApp()
	shared := newTempWorkspace(t, core)
	other := newTempWorkspace(t, core)
	a := tempWorkspaceTabDesktop(t, core, shared, shared, other)

	a.closeWorkspaceTab(0) // a sibling still shows the sandbox
	a.closeWorkspaceTab(0) // the last tab of that sandbox closes

	deadline := time.Now().Add(2 * time.Second)
	for {
		_, err := os.Stat(shared)
		if os.IsNotExist(err) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the last tab of a temporary workspace left its sandbox on disk")
		}
		time.Sleep(2 * time.Millisecond)
	}
	if _, err := os.Stat(other); err != nil {
		t.Fatalf("removing one sandbox touched another workspace: %v", err)
	}
}
