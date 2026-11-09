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

// The history panel keeps its right edge on the button's: the button closes the
// right cluster, so the panel takes the room the window has instead of running
// past it.
func TestWorkspaceMenuOpensInwardAndDistinguishesSameNames(t *testing.T) {
	a := &desktop{sessions: []backend.SessionIndexEntry{{Workspace: "C:/work/cam"}, {Workspace: "C:/other/cam"}, {Workspace: "C:/work/cam"}}}
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Box(c).Absolute().Left(500).Top(10).Children(func() {
			a.workspaceHistoryMenu(c.Key("add"), func(b ui.Element) { b.Size(30, 30).Label("add") })
		})
	}, 600, 500)
	if err := tt.Click("add"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
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
	if tt.HasText("工作区历史菜单") || len(a.tabs) != 1 || a.tabs[0].Workspace != "C:/other/cam" {
		t.Fatal("history selected wrong path or did not close")
	}
}

// With nothing to list, the history says where the add went: the header's button
// lists what was open, and the plus beside the tabs is the only way in.
func TestWorkspaceHistoryEmptyStatePointsAtThePlus(t *testing.T) {
	a := &desktop{prefs: defaultPrefs()}
	tt := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(a.theme())
		c.DisableAnimations()
		a.workspaceHistoryMenu(c.Key("add"), func(b ui.Element) { b.Size(30, 30).Label("add") })
	}, 600, 400)
	if err := tt.Click("add"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	for _, want := range []string{"无历史记录", "点标签栏的 ＋ 添加工作区"} {
		if _, ok := tt.Find(want); !ok {
			t.Fatalf("the empty history is missing %q: %v", want, tt.Texts())
		}
	}
	if _, ok := tt.Find("＋ 添加工作区"); ok {
		t.Fatalf("the history panel kept the add actions: %v", tt.Texts())
	}
}

// The plus beside the last tab and the header's button open one panel each: the
// two ways to start a workspace beside the tabs, the history under the header. A
// press that switches entries has to leave one panel up: built from inside each
// entry, the panel of the first one came out closing while the second went up in
// the same frame, two panels and two nodes of one key.
func TestWorkspaceMenuSwitchesEntryWithoutTwoPanels(t *testing.T) {
	a := &desktop{
		ready:    true,
		page:     "chat",
		prefs:    defaultPrefs(),
		tabs:     []*conversation{{ID: "first", Workspace: "work"}},
		sessions: []backend.SessionIndexEntry{{Workspace: "work"}},
	}
	tt := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(a.theme())
		c.DisableAnimations()
		a.headerBar(c)
	}, 900, 120)

	if err := tt.Click("添加工作区"); err != nil {
		t.Fatalf("the plus beside the last tab: %v", err)
	}
	tt.Frame()
	if _, ok := tt.Find("添加工作区菜单"); !ok {
		t.Fatalf("no add panel under the plus: %v", tt.Texts())
	}
	// The plus offers only the ways to start a workspace: the history list belongs
	// to the header's button, so the same rows never show up twice.
	for _, want := range []string{"＋ 添加工作区", "临时工作区"} {
		if _, ok := tt.Find(want); !ok {
			t.Fatalf("the add panel is missing %q: %v", want, tt.Texts())
		}
	}
	if tt.HasText("打开工作区 work") {
		t.Fatalf("the add panel listed history: %v", tt.Texts())
	}
	// Narrower than the history panel on purpose: two short rows never need more,
	// and the plus ends the strip, so its panel grows right from the plus instead
	// of covering the tabs before it.
	addPanel, panelOK := tt.Find("添加工作区菜单")
	plus, plusOK := tt.Find("添加工作区")
	if !panelOK || !plusOK {
		t.Fatalf("no plus or add panel to measure: %v", tt.Texts())
	}
	if addPanel.W != workspaceAddPanelWidth || addPanel.W >= workspaceHistoryPanelWidth {
		t.Fatalf("the add panel is %.1f wide, want %.0f", addPanel.W, workspaceAddPanelWidth)
	}
	if d := addPanel.X - plus.X; d < -1 || d > 1 {
		t.Fatalf("the add panel starts %.1f from the plus, want flush left", d)
	}
	if addPanel.Y < plus.Y+plus.H {
		t.Fatalf("the add panel sits at y=%.1f, want below the plus ending at %.1f", addPanel.Y, plus.Y+plus.H)
	}

	// Straight onto the other entry: the panel moves there, one panel and no
	// duplicate key (a strict Tester panics on one).
	if err := tt.Click("工作区历史"); err != nil {
		t.Fatalf("the header's history button: %v", err)
	}
	tt.Frame()
	panels := 0
	for _, text := range tt.Texts() {
		if text == "添加工作区菜单" || text == "工作区历史菜单" {
			panels++
		}
	}
	if panels != 1 {
		t.Fatalf("switching entries left %d panels visible: %v", panels, tt.Texts())
	}
	if _, ok := tt.Find("工作区历史菜单"); !ok {
		t.Fatalf("no history panel under the header's button: %v", tt.Texts())
	}
	if _, ok := tt.Find("临时工作区"); ok {
		t.Fatalf("the history panel kept the add actions: %v", tt.Texts())
	}
	if _, ok := tt.Find("打开工作区 work"); !ok {
		t.Fatalf("the history panel lost its rows: %v", tt.Texts())
	}
	// The history opens leftward from the button, flush with its right edge: the
	// header's button closes the right cluster, so the panel takes the room the
	// window has instead of running past it.
	history, historyOK := tt.Find("工作区历史菜单")
	button, buttonOK := tt.Find("工作区历史")
	if !historyOK || !buttonOK {
		t.Fatalf("no button or history panel to measure: %v", tt.Texts())
	}
	if d := (history.X + history.W) - (button.X + button.W); d < -1 || d > 1 {
		t.Fatalf("the history panel ends %.1f from the button's right edge, want flush", d)
	}
	if history.X < 0 {
		t.Fatalf("the history panel runs off the window's left edge: %v", history)
	}
}
