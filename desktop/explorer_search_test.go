package desktop

import (
	backend "ally-dev/internal/app"
	"context"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestExplorerSearchTreeAndReset(t *testing.T) {
	files, children, opened := explorerSearchTree([]backend.FileEntry{
		{Path: "src/deep/one.go"}, {Path: "src/two.go"}, {Path: "README.md"}, {Path: "src/deep/one.go"},
	})
	if len(files) != 2 || files[0].Path != "src" || !files[0].Dir || len(children["src"]) != 2 || len(children["src/deep"]) != 1 || !opened["src/deep"] {
		t.Fatalf("incorrect tree: %v %v %v", files, children, opened)
	}
	for _, action := range []string{"close", "workspace", "clear"} {
		t.Run(action, func(t *testing.T) {
			root := t.TempDir()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			a := &desktop{explorer: true, fileRoot: workspaceKey(root), files: files, fileChildren: children, fileOpen: opened}
			a.filesF.Search = explorerSearch{Input: "one", Query: "one", Busy: true, Cancel: cancel, Note: "old"}
			a.filesF.HelpOpen = true
			epoch := a.fileEpoch
			switch action {
			case "close":
				a.setExplorerOpen(false)
				a.setExplorerOpen(true)
			case "workspace":
				a.prepareFileTree(t.TempDir())
			case "clear":
				a.searchFiles("")
			}
			if ctx.Err() == nil || a.fileEpoch <= epoch || a.filesF.Search.Input != "" || a.filesF.Search.Query != "" || a.filesF.Search.Busy || a.filesF.Search.Note != "" || len(a.files) != 0 || len(a.fileChildren) != 0 || len(a.fileOpen) != 0 {
				t.Fatalf("search survived %s: %+v", action, a.filesF.Search)
			}
		})
	}
}

func TestExplorerSearchTypingAndPanelReopen(t *testing.T) {
	a := &desktop{explorer: true, prefs: defaultPrefs(), files: []backend.FileEntry{{Path: "normal.txt", Name: "normal.txt"}}}
	tt := ui.NewTester(func(c *ui.Context) {
		if a.explorer {
			a.fileTree(c)
		}
	}, 300, 500)
	if err := tt.Click("搜索工作区文件"); err != nil {
		t.Fatal(err)
	}
	tt.Type("needle")
	tt.Frame()
	if a.filesF.Search.Input != "needle" || a.filesF.Search.Query != "" || a.files[0].Path != "normal.txt" {
		t.Fatal("typing triggered a search or lost text")
	}
	search, _ := tt.Find("搜索工作区文件")
	help, ok := tt.Find("文件浏览器帮助")
	if !ok || help.Y <= search.Y+search.H {
		t.Fatal("help is not below the tree")
	}
	a.setExplorerOpen(false)
	tt.Frame()
	a.setExplorerOpen(true)
	tt.Frame()
	if a.filesF.Search.Input != "" {
		t.Fatal("reopened input retained text")
	}
	if err := tt.Click("搜索工作区文件"); err != nil {
		t.Fatal(err)
	}
	tt.Type("next")
	tt.Frame()
	if a.filesF.Search.Input != "next" {
		t.Fatalf("reopened input retained native edit state: %q", a.filesF.Search.Input)
	}
	// An empty submission must go through OnSubmit and reset result state.
	a.filesF.Search.Query, a.filesF.Search.Note = "old", "old result"
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Key(0, ui.KeyBackspace)
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if a.filesF.Search.Query != "" || a.filesF.Search.Note != "" {
		t.Fatal("Enter did not clear the search")
	}
	a.filesF.Search.Input = "previous workspace"
	a.prepareFileTree(t.TempDir())
	tt.Frame()
	if err := tt.Click("搜索工作区文件"); err != nil {
		t.Fatal(err)
	}
	tt.Type("new workspace")
	tt.Frame()
	if a.filesF.Search.Input != "new workspace" {
		t.Fatal("workspace switch retained native input state")
	}
}
