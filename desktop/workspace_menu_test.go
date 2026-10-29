package desktop

import (
	backend "ally-dev/internal/app"
	"github.com/egoist/mygo/ui"
	"testing"
)

func TestHistoryWorkspaceAlwaysCreatesNewTab(t *testing.T) {
	a := &desktop{tabs: []*conversation{{ID: "existing", Workspace: `C:\work\cam`}, {ID: "other", Workspace: `C:\other\cam`}}, active: 1}
	for _, root := range []string{`C:\work\cam`, `C:\work\cam`, `C:\other\cam`} {
		before := len(a.tabs)
		a.openWorkspaceEntry(root)
		if len(a.tabs) != before+1 || a.active != before {
			t.Fatal("history reused an existing tab")
		}
		added := a.tabs[a.active]
		if added.Workspace != root {
			t.Fatal("history opened wrong workspace")
		}
		for _, old := range a.tabs[:before] {
			if old.ID == added.ID {
				t.Fatal("history reused an existing session")
			}
		}
	}
	if a.tabs[0].ID != "existing" || a.tabs[1].ID != "other" {
		t.Fatal("existing tabs changed")
	}
}

func TestWorkspaceMenuOpensToLeftAndDistinguishesSameNames(t *testing.T) {
	a := &desktop{workspaceMenuOpen: true, sessions: []backend.SessionIndexEntry{{Workspace: "C:/work/cam"}, {Workspace: "C:/other/cam"}, {Workspace: "C:/work/cam"}}}
	tt := ui.NewTester(func(c *ui.Context) {
		anchor := ui.ButtonBase(c.Key("add")).Absolute().Left(500).Top(10).Size(30, 30).Label("add")
		a.workspaceMenu(c, anchor)
	}, 600, 500)
	panel, ok := tt.Find("工作区历史菜单")
	if !ok {
		t.Fatal("missing menu")
	}
	if panel.X >= 500 || panel.X+panel.W > 531 {
		t.Fatalf("menu should open left of anchor: %v", panel)
	}
	for _, root := range []string{"C:/work/cam", "C:/other/cam"} {
		if _, ok := tt.Find("打开工作区 " + root); !ok {
			t.Fatal("same-name workspace missing: " + root)
		}
	}
	if err := tt.Click("打开工作区 C:/other/cam"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.workspaceMenuOpen || len(a.tabs) != 1 || a.tabs[0].Workspace != "C:/other/cam" {
		t.Fatal("history selected wrong path or did not close")
	}
}
