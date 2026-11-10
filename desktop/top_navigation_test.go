package desktop

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestTopNavigationPreferenceRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prefs.json")
	prefs := defaultPrefs()
	if prefs.TopNavigation {
		t.Fatal("existing installations should retain the sidebar")
	}
	prefs.TopNavigation = true
	data, err := json.Marshal(prefs)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if !readPrefs(path).TopNavigation {
		t.Fatal("top navigation preference was not restored")
	}
}

func TestTopNavigationMenuAndHeaderLayout(t *testing.T) {
	for _, bar := range []ui.TitleBar{{}, {Left: 80, Height: 44}} {
		a := &desktop{ready: true, page: "chat", prefs: defaultPrefs(), tabs: []*conversation{{ID: "nav-test", Workspace: "mygo"}}}
		a.prefs.TopNavigation = true
		tt := ui.NewTester(func(c *ui.Context) { c.DisableAnimations(); c.SetTheme(a.theme()); a.headerBar(c) }, 1000, 500)
		tt.SetTitleBar(bar)
		tt.Frame()
		logo, ok := tt.Find("应用 Logo")
		if !ok {
			t.Fatal("logo missing")
		}
		menu, ok := tt.Find("导航菜单")
		if !ok || menu.X < logo.X+logo.W {
			t.Fatalf("menu overlaps logo: logo=%v menu=%v", logo, menu)
		}
		tab, ok := tt.Find("工作区标签 nav-test")
		if !ok || tab.X < menu.X+menu.W {
			t.Fatalf("menu overlaps tabs: menu=%v tab=%v", menu, tab)
		}
		if err := tt.Click("导航菜单"); err != nil {
			t.Fatal(err)
		}
		if !tt.HasText("Agent") {
			t.Fatal("navigation menu did not open")
		}
		tt.Key(0, ui.KeyEscape)
		tt.Frame()
		if tt.HasText("Agent") {
			t.Fatal("Escape did not close navigation menu")
		}
	}
}
