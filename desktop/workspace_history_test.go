package desktop

import (
	backend "ally-dev/internal/app"
	"context"
	"errors"
	"fmt"
	"os"
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
	if err := os.Mkdir(visited, 0700); err != nil {
		t.Fatal(err)
	}
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

func TestHiddenWorkspaceStaysHiddenAfterSessionIndexChanges(t *testing.T) {
	root := t.TempDir()
	other := t.TempDir()
	a := &desktop{prefs: defaultPrefs()}
	a.sessions = []backend.SessionIndexEntry{{ID: "first", Workspace: root}, {ID: "other", Workspace: other}}
	a.sessRev++
	if got := a.workspaceHistoryRoots(); len(got) != 2 {
		t.Fatalf("initial roots = %v", got)
	}
	a.hideWorkspaceVisit(root)
	if got := a.workspaceHistoryRoots(); !reflect.DeepEqual(got, []string{other}) {
		t.Fatalf("hidden roots = %v", got)
	}
	a.sessions = append(a.sessions, backend.SessionIndexEntry{ID: "new", Workspace: root})
	a.sessRev++
	if got := a.workspaceHistoryRoots(); !reflect.DeepEqual(got, []string{other}) {
		t.Fatalf("index update revealed hidden workspace: %v", got)
	}
	a.showWorkspaceVisit(root)
	if got := a.workspaceHistoryRoots(); !reflect.DeepEqual(got, []string{root, other}) {
		t.Fatalf("reopened roots = %v", got)
	}
}

func TestRailWorkspaceClickAlwaysAddsTab(t *testing.T) {
	root := filepath.Join(t.TempDir(), "project")
	other := filepath.Join(t.TempDir(), "other")
	a := &desktop{prefs: defaultPrefs(), page: "chat", tabs: []*conversation{
		{ID: "first", Workspace: root}, {ID: "second", Workspace: other},
	}, active: 1}
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Width(240).Children(func() { a.railWorkspaceRow(c, root, "project", root) })
	}, 300, 100)
	for i := 0; i < 2; i++ {
		if err := tt.Click("project"); err != nil {
			t.Fatal(err)
		}
		if len(a.tabs) != 3+i || a.active != len(a.tabs)-1 || a.current().Workspace != root {
			t.Fatalf("click %d did not add a workspace tab: tabs=%d active=%d", i, len(a.tabs), a.active)
		}
		if a.tabs[0].ID != "first" || a.tabs[1].ID != "second" || a.current().ID == a.tabs[0].ID {
			t.Fatal("opening a workspace changed existing tab order or reused a session")
		}
	}
}

func TestRailWorkspaceOrderDoesNotFollowVisits(t *testing.T) {
	root := t.TempDir()
	alpha, zeta := filepath.Join(root, "alpha"), filepath.Join(root, "zeta")
	a := &desktop{prefs: defaultPrefs(), tabs: []*conversation{{ID: "a", Workspace: alpha}, {ID: "z", Workspace: zeta}}}
	a.activateTab(0)
	a.activateTab(1)
	want := []string{alpha, zeta}
	if got := railWorkspacesMatching(a.workspaceHistoryRoots(), ""); !reflect.DeepEqual(got, want) {
		t.Fatalf("initial order=%v, want %v", got, want)
	}
	a.activateTab(0)
	if got := railWorkspacesMatching(a.workspaceHistoryRoots(), ""); !reflect.DeepEqual(got, want) {
		t.Fatalf("switching workspace reordered rows: %v", got)
	}
	if got := railWorkspacesMatching(a.workspaceHistoryRoots(), "ZET"); !reflect.DeepEqual(got, []string{zeta}) {
		t.Fatalf("filtered rows=%v", got)
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
	// Removing a history-only workspace goes through the confirmation dialog, which
	// cannot run here, so the step it guards is exercised on its own: the visit is
	// forgotten, the cache is invalidated and the removal survives a restart.
	a.hideWorkspaceVisit(first)
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

// A session the backend refuses to delete is running or compacting. Forgetting it
// anyway blanked a tab whose transcript still exists on disk, and the run's later
// events had no tab left to report into.
func TestWorkspaceRemovalKeepsSessionsItCouldNotDelete(t *testing.T) {
	root := `C:\work\cam`
	running := &conversation{ID: "running", Workspace: root, Messages: []map[string]any{{"role": "user", "content": "还在跑"}}}
	saved := &conversation{ID: "saved", Workspace: root, Messages: []map[string]any{{"role": "user", "content": "旧的"}}}
	a := &desktop{
		tabs:         []*conversation{running, saved},
		openSessions: map[string]*conversation{"running": running, "saved": saved},
	}

	deleted, failures := a.deleteWorkspaceSessions([]string{"running", "saved"}, func(id string) error {
		if id == "running" {
			return errors.New("session is running")
		}
		return nil
	})
	if !reflect.DeepEqual(deleted, []string{"saved"}) || len(failures) != 1 {
		t.Fatalf("deleted=%v failures=%v", deleted, failures)
	}

	a.applyWorkspaceRemoval(root, deleted, failures)

	if a.tabs[0] != running || len(a.tabs[0].Messages) != 1 {
		t.Fatal("a session that could not be deleted lost its tab")
	}
	if a.openSessions["running"] == nil {
		t.Fatal("a session that could not be deleted left the open set")
	}
	if a.tabs[1].ID == "saved" || len(a.tabs[1].Messages) != 0 {
		t.Fatal("the deleted session kept its transcript")
	}
	// The workspace still holds a session, so its entry has to stay.
	if a.workspaceHidden(root) {
		t.Fatal("a partly deleted workspace was dropped from the history")
	}
}

// Deleting the history of the workspace in use has to stick: activating its tab
// records the visit again, which used to put the entry straight back.
func TestDeletedWorkspaceStaysOutWhileItsTabIsOpen(t *testing.T) {
	t.Setenv("ALLY_DATA_DIR", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())
	a := &desktop{core: backend.NewApp(), prefs: defaultPrefs()}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := waitWindowWrites(ctx, []<-chan struct{}{a.prefsSaveTail}, &a.writeFailures); err != nil {
			t.Error(err)
		}
	})
	root := t.TempDir()
	a.newConversation(root)
	if got := a.workspaceHistoryRoots(); len(got) != 1 {
		t.Fatalf("opening a workspace recorded %v, want one entry", got)
	}

	a.hideWorkspaceVisit(root)
	a.activateTab(a.active)
	if got := a.workspaceHistoryRoots(); len(got) != 0 {
		t.Fatalf("the deleted workspace came back as %v", got)
	}

	// Adding it again is what brings it back.
	a.showWorkspaceVisit(root)
	a.activateTab(a.active)
	if got := a.workspaceHistoryRoots(); len(got) != 1 {
		t.Fatalf("adding the workspace again left %v", got)
	}
}
