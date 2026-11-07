package desktop

import (
	backend "ally-dev/internal/app"
	"context"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func TestWorkspaceHistoryMergesVisitsAndLegacySessions(t *testing.T) {
	root := t.TempDir()
	visited := filepath.Join(root, "visited")
	newerSession := filepath.Join(root, "newer-session")
	legacy := filepath.Join(root, "legacy")
	temporary := filepath.Join(root, "ally-temp-test")
	sessions := []backend.SessionIndexEntry{
		{Workspace: newerSession, UpdatedAt: 200},
		{Workspace: strings.ToUpper(visited), UpdatedAt: 100},
		{Workspace: legacy, UpdatedAt: 50},
		{Workspace: temporary, UpdatedAt: 20},
		{Workspace: filepath.Join(legacy, "."), UpdatedAt: 10},
	}
	recent := []string{visited, temporary, "", strings.ToUpper(visited)}
	want := []string{visited, newerSession, legacy}
	if got := workspaceHistoryRoots(recent, sessions); !reflect.DeepEqual(got, want) {
		t.Fatalf("history=%v, want %v", got, want)
	}
	if got := startupWorkspace(recent, sessions, "fallback"); got != visited {
		t.Fatalf("startup ignored recent visit: %q", got)
	}
}

func TestWorkspaceMenuIncludesEmptyOpenedWorkspaceAndUpdatesOrder(t *testing.T) {
	first, second := t.TempDir(), t.TempDir()
	a := &desktop{prefs: defaultPrefs()}
	a.newConversation(first)
	a.newConversation(second)
	if len(a.sessions) != 0 || !reflect.DeepEqual(a.workspaceHistoryRoots(), []string{second, first}) {
		t.Fatal("opening empty workspaces did not immediately update history")
	}
	a.activateTab(0)
	if !reflect.DeepEqual(a.workspaceHistoryRoots(), []string{first, second}) {
		t.Fatal("switching to an existing tab did not invalidate cached history order")
	}
	revision := a.workspaceVisitsRev
	a.activateTab(0)
	if a.workspaceVisitsRev != revision {
		t.Fatal("reselecting the latest workspace requested another history save")
	}
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Box(c).Children(func() {
			a.workspaceHistoryMenu(c.Key("add"), func(b ui.Element) { b.Size(30, 30).Label("add") })
		})
	}, 700, 500)
	if err := tt.Click("add"); err != nil {
		t.Fatal(err)
	}
	top, ok := tt.Find("打开工作区 " + first)
	next, found := tt.Find("打开工作区 " + second)
	if !ok || !found || top.Y >= next.Y {
		t.Fatal("workspace menu did not display empty workspaces in visit order")
	}
	// A later session-index refresh must not override explicit visit order.
	a.sessions = []backend.SessionIndexEntry{{Workspace: second, UpdatedAt: time.Now().UnixMilli()}}
	a.sessRev++
	if !reflect.DeepEqual(a.workspaceHistoryRoots(), []string{first, second}) {
		t.Fatal("session save overrode workspace visit order")
	}
}

func TestWorkspaceVisitsPersistWithoutConversationAndCanBeForgotten(t *testing.T) {
	data := t.TempDir()
	t.Setenv("ALLY_DATA_DIR", data)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())
	first, second := t.TempDir(), t.TempDir()
	a := &desktop{core: backend.NewApp(), prefs: defaultPrefs()}
	a.newConversation(first)
	a.newConversation(second)
	a.activateTab(0)
	waitPrefs := func() {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := waitWindowWrites(ctx, []<-chan struct{}{a.prefsSaveTail}, &a.writeFailures); err != nil {
			t.Fatal(err)
		}
	}
	waitPrefs()
	reloaded := readPrefs(a.prefsPath())
	if !reflect.DeepEqual(reloaded.RecentWorkspaces, []string{first, second}) {
		t.Fatalf("persisted visits=%v", reloaded.RecentWorkspaces)
	}
	if len(a.sessions) != 0 || startupWorkspace(reloaded.RecentWorkspaces, nil, "fallback") != first {
		t.Fatal("empty workspace could not be restored without conversation records")
	}
	// The existing rail delete entry must also work for a history-only workspace.
	a.removeWorkspaceHistory(first)
	if !reflect.DeepEqual(a.workspaceHistoryRoots(), []string{second}) {
		t.Fatal("forgetting a workspace did not invalidate the history cache")
	}
	waitPrefs()
	if got := readPrefs(a.prefsPath()).RecentWorkspaces; !reflect.DeepEqual(got, []string{second}) {
		t.Fatalf("forgotten workspace returned after restart: %v", got)
	}
}

func TestWorkspaceVisitsAreBoundedAndExcludeTemporary(t *testing.T) {
	a := &desktop{}
	root := t.TempDir()
	for i := 0; i < maxRecentWorkspaces+5; i++ {
		a.rememberWorkspace(filepath.Join(root, fmt.Sprint(i)))
	}
	if len(a.prefs.RecentWorkspaces) != maxRecentWorkspaces {
		t.Fatal("workspace visits grew past their retained limit")
	}
	revision := a.workspaceVisitsRev
	a.rememberWorkspace(filepath.Join(root, "ally-temp-test"))
	a.rememberWorkspace("")
	if a.workspaceVisitsRev != revision {
		t.Fatal("temporary workspace was recorded in permanent history")
	}
}
