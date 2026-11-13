//go:build ally_explorer

// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"os"
	"path/filepath"
	"testing"

	backend "ally-dev/internal/app"
	"github.com/egoist/mygo/ui"
)

// explorerInteraction lays out a tree whose rows are known, with the files the
// copy shortcut reads on disk.
func explorerInteraction(t *testing.T) (*desktop, *ui.Tester) {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "src"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "src", "app.go"), []byte("package src\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	a := &desktop{prefs: defaultPrefs(), page: "chat", tabs: []*conversation{{Workspace: root}}}
	a.files = []backend.FileEntry{
		{Path: "src", Name: "src", Dir: true},
		{Path: "docs", Name: "docs", Dir: true},
		{Path: "README.md", Name: "README.md"},
	}
	a.fileChildren = map[string][]backend.FileEntry{
		"src":  {{Path: "src/app.go", Name: "app.go"}, {Path: "src/main.go", Name: "main.go"}},
		"docs": {{Path: "docs/a.md", Name: "a.md"}},
	}
	a.fileOutline.Open.Add("src")
	a.reindexFileTree()
	tt := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(a.theme())
		c.DisableAnimations()
		ui.Row(c).Fill().AlignItems(ui.Stretch).Children(func() { a.fileTree(c) })
	}, 320, 420)
	tt.Frame()
	return a, tt
}

// clickRow rests the pointer on a row and clicks it, as a user does before any
// click: the row only reports a click while the pointer is over it.
func clickRow(t *testing.T, tt *ui.Tester, label string) {
	t.Helper()
	row, ok := tt.Find(label)
	if !ok {
		t.Fatalf("missing row %q", label)
	}
	tt.Move(row.X+row.W/2, row.Y+row.H/2)
	tt.Frame()
	if err := tt.Click(label); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
}

func TestOnDemandExplorerTreeInteraction(t *testing.T) {
	a, tt := explorerInteraction(t)

	// Clicking a closed directory expands it, and the tree is on it afterwards.
	clickRow(t, tt, "docs")
	if !a.fileOutline.Open.Has("docs") {
		t.Fatal("a click did not expand the directory")
	}
	if got := a.fileChosenPath(); got != "docs" {
		t.Fatalf("the clicked row is %q", got)
	}

	// Home and End go to the ends of the rows, the arrows step through them.
	tt.Key(0, ui.KeyHome)
	tt.Frame()
	if got := a.fileChosenPath(); got != "src" {
		t.Fatalf("Home chose %q", got)
	}
	tt.Key(0, ui.KeyDown)
	tt.Frame()
	if got := a.fileChosenPath(); got != "src/app.go" {
		t.Fatalf("Down chose %q", got)
	}
	tt.Key(0, ui.KeyEnd)
	tt.Frame()
	if got := a.fileChosenPath(); got != "README.md" {
		t.Fatalf("End chose %q", got)
	}

	// Enter and the left and right keys open and close the row the tree is on.
	tt.Key(0, ui.KeyHome)
	tt.Frame()
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if a.fileOutline.Open.Has("src") {
		t.Fatal("Enter did not close the directory")
	}
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if !a.fileOutline.Open.Has("src") {
		t.Fatal("Enter did not open the directory")
	}

	// Ctrl+C copies the file the tree is on, not the first one.
	tt.Key(0, ui.KeyDown)
	tt.Frame()
	if got := a.fileChosenPath(); got != "src/app.go" {
		t.Fatalf("the tree is on %q", got)
	}
	tt.Key(ui.Cmd, ui.KeyC)
	want := filepath.Join(a.current().Workspace, "src", "app.go")
	files, err := tt.ClipboardData().Files()
	if err != nil || len(files) != 1 || files[0] != want {
		t.Fatalf("copied files = %v, %v; want %s", files, err, want)
	}

	// A directory the tree is on copies nothing.
	tt.Key(0, ui.KeyHome)
	tt.Frame()
	tt.SetClipboard("kept")
	tt.Key(ui.Cmd, ui.KeyC)
	if tt.Clipboard() != "kept" {
		t.Fatal("Ctrl+C copied a directory")
	}
}

// TestOnDemandExplorerTreeLazyLoadsOpenedOnly checks the rule behind the
// children callback: a directory in view that is not open has no listing and
// keeps its arrow, so no frame reads one behind the user's back.
func TestOnDemandExplorerTreeLazyLoadsOpenedOnly(t *testing.T) {
	a := &desktop{prefs: defaultPrefs(), page: "chat", tabs: []*conversation{{Workspace: "/work/lazy"}}}
	a.files = []backend.FileEntry{
		{Path: "src", Name: "src", Dir: true},
		{Path: "docs", Name: "docs", Dir: true},
		{Path: "README.md", Name: "README.md"},
	}
	a.fileChildren = map[string][]backend.FileEntry{
		"src": {{Path: "src/app.go", Name: "app.go"}},
	}
	a.fileOutline.Open.Add("src")
	a.reindexFileTree()
	if kids := a.fileTreeKids("docs"); kids == nil || len(kids) != 0 {
		t.Fatalf("a directory waiting for its listing lost its arrow: %v", kids)
	}
	if _, loaded := a.fileIndex.kids["docs"]; loaded {
		t.Fatal("the closed directory was listed before it opened")
	}
	if kids := a.fileTreeKids("README.md"); kids != nil {
		t.Fatalf("a file reported children: %v", kids)
	}
	if kids := a.fileTreeKids("src"); len(kids) != 1 {
		t.Fatalf("the open directory listed %v", kids)
	}
	a.toggleFolder("docs")
	if _, loaded := a.fileIndex.kids["docs"]; loaded {
		t.Fatal("the listing arrived without a backend")
	}
	if !a.fileOutline.Open.Has("docs") {
		t.Fatal("the directory did not open")
	}
}
