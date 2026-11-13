package desktop

import (
	"context"
	"fmt"
	"math"
	"testing"

	backend "ally-dev/internal/app"
	"github.com/egoist/mygo/ui"
)

func explorerTabs(t *testing.T) *desktop {
	t.Helper()
	a := &desktop{page: "chat", explorer: true, prefs: defaultPrefs(), tabs: []*conversation{
		{ID: "A", Workspace: t.TempDir()}, {ID: "B", Workspace: t.TempDir()},
	}}
	a.activateTab(0)
	return a
}

func TestExplorerTabSwitchRestoresTreeAndScroll(t *testing.T) {
	a := explorerTabs(t)
	a.files = []backend.FileEntry{{Path: "src", Name: "src", Dir: true}}
	a.fileOutline.Open.Add("src")
	a.fileOutline.Open.Add("src/deep")
	a.fileChildren["src"] = []backend.FileEntry{{Path: "src/deep", Name: "deep", Dir: true}}
	for i := range 100 {
		a.fileChildren["src/deep"] = append(a.fileChildren["src/deep"], backend.FileEntry{Path: fmt.Sprintf("src/deep/a-%03d.go", i), Name: fmt.Sprintf("a-%03d.go", i)})
	}
	a.reindexFileTree()
	tester := ui.NewTester(func(c *ui.Context) {
		c.DisableAnimations()
		a.fileTree(c)
	}, 300, 400)
	a.fileScroll.Y = 460
	tester.Frame()
	firstScroll := a.fileScroll
	if firstScroll.Y != 460 || !tester.HasText("a-025.go") {
		t.Fatal("first workspace tree was not expanded and scrolled")
	}
	a.fileLoading = map[string]bool{"src/deep": true}
	epoch := a.fileEpoch
	a.activateTab(1)
	if len(a.files)+a.fileOutline.Open.Len()+len(a.fileChildren) != 0 || a.fileScroll == firstScroll || a.fileEpoch <= epoch || len(a.fileLoading) != 0 {
		t.Fatal("switch carried another workspace's rows, scrolling or pending reads")
	}
	a.files = []backend.FileEntry{{Path: "src", Name: "src", Dir: true}}
	a.fileOutline.Open.Add("src")
	for i := range 60 {
		a.fileChildren["src"] = append(a.fileChildren["src"], backend.FileEntry{Path: fmt.Sprintf("src/b-%03d.go", i), Name: fmt.Sprintf("b-%03d.go", i)})
	}
	a.reindexFileTree()
	tester.Frame()
	a.fileScroll.Y = 115
	tester.Frame()
	secondScroll := a.fileScroll
	for range 3 {
		a.activateTab(0)
		a.prepareFileTree(a.current().Workspace)
		tester.Frame()
		if !a.fileOutline.Open.Has("src/deep") || len(a.fileChildren["src/deep"]) != 100 || a.fileScroll != firstScroll || math.Abs(float64(a.fileScroll.Y-460)) > 0.1 || !tester.HasText("a-025.go") {
			t.Fatal("returning to A lost its nested expansion, rows or scroll position")
		}
		a.activateTab(1)
		a.prepareFileTree(a.current().Workspace)
		tester.Frame()
		if a.fileOutline.Open.Has("src/deep") || len(a.fileChildren["src"]) != 60 || a.fileScroll != secondScroll || math.Abs(float64(a.fileScroll.Y-115)) > 0.1 {
			t.Fatal("returning to B lost its own tree or inherited A's state")
		}
	}
}

func TestExplorerSearchSwitchKeepsOrdinaryTree(t *testing.T) {
	a := explorerTabs(t)
	a.files = []backend.FileEntry{{Path: "normal", Dir: true}}
	a.fileOutline.Open.Add("normal")
	a.fileChildren["normal"] = []backend.FileEntry{{Path: "normal/file.go"}}
	a.reindexFileTree()
	a.fileScroll.Y = 120
	a.rememberFileTree() // The search entry point saves this before replacing rows.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a.filesF.Search = explorerSearch{Query: "needle", Busy: true, Cancel: cancel}
	entries, children, opened := explorerSearchTree([]backend.FileEntry{{Path: "search/needle.go"}})
	a.files, a.fileChildren = entries, children
	a.fileOutline = ui.OutlineState[string]{}
	for path := range opened {
		a.fileOutline.Open.Add(path)
	}
	a.reindexFileTree()
	a.fileScroll = &ui.ScrollState{Y: 20}
	a.activateTab(1)
	if ctx.Err() == nil || a.filesF.Search.Query != "" || a.filesF.Search.Busy {
		t.Fatal("workspace switch retained a search or its pending callback")
	}
	a.activateTab(0)
	if len(a.files) != 1 || a.files[0].Path != "normal" || !a.fileOutline.Open.Has("normal") || a.fileOutline.Open.Has("search") || a.fileScroll.Y != 120 {
		t.Fatal("search results replaced the remembered ordinary tree")
	}
}

func TestExplorerTreeReleasedOnlyAfterLastWorkspaceTab(t *testing.T) {
	for _, index := range []int{0, 1} {
		t.Run(fmt.Sprint(index), func(t *testing.T) {
			a := explorerTabs(t)
			root := a.tabs[index].Workspace
			a.activateTab(index)
			a.files = []backend.FileEntry{{Path: "src", Dir: true}}
			a.fileOutline.Open.Add("src")
			a.rememberFileTree()
			a.activateTab(0)
			a.page = "plugin:test"
			a.closeWorkspaceTab(index)
			if a.workspaceTrees[workspaceKey(root)] != nil {
				t.Fatal("closing the last workspace tab retained its tree")
			}
			a.tabs = append(a.tabs, &conversation{ID: "reopened", Workspace: root})
			a.activateTab(len(a.tabs) - 1)
			if a.fileOutline.Open.Has("src") || len(a.files) != 0 {
				t.Fatal("closed workspace was re-cached during tab activation")
			}
		})
	}
	a := explorerTabs(t)
	root := a.tabs[0].Workspace
	a.files = []backend.FileEntry{{Path: "src", Dir: true}}
	a.fileOutline.Open.Add("src")
	a.tabs = append(a.tabs, &conversation{ID: "sibling", Workspace: root})
	a.activateTab(2)
	if !a.fileOutline.Open.Has("src") {
		t.Fatal("same-workspace tab switch reset the tree")
	}
	a.page = "plugin:test"
	a.closeWorkspaceTab(0)
	if !a.fileOutline.Open.Has("src") {
		t.Fatal("closing a sibling tab cleared a workspace still open")
	}
}
