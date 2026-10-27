package desktop

import (
	"testing"

	backend "ally-dev/internal/app"
	"github.com/egoist/mygo/ui"
)

// The SSH auth list scrolls inside its own box: switching group tabs must not
// move the tab strip, and clicking a row toggles its checkbox like main.
func TestSSHAuthTabsStayPut(t *testing.T) {
	a := demoDesktop()
	a.sshServers = []backend.SSHServerNode{
		{ID: "s1", Alias: "a1", Group: "g1", Host: "h1", Username: "u"},
		{ID: "s2", Alias: "a2", Group: "g2", Host: "h2", Username: "u"},
	}
	a.sshF.AuthOpen = true
	a.sshF.AuthReady = true
	a.sshF.AuthDraft = map[string]bool{}
	tt := ui.NewTester(a.view, 1266, 843)
	tt.Frame()
	// Measure the SAME label before/after: Find on the first tab can hit the
	// whole Tabs control (its accessibility label is labels[0]), whose box
	// top is not the label baseline — comparing g1-vs-g2 is apples to oranges.
	g2Before, ok := tt.Find("g2")
	if !ok {
		t.Fatal("group tab g2 missing")
	}
	if err := tt.Click("g2"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Frame()
	g2After, ok := tt.Find("g2")
	if !ok {
		t.Fatal("group tab g2 missing after switch")
	}
	if g2After.Y != g2Before.Y {
		t.Fatalf("tab strip moved when switching groups: %.1f -> %.1f", g2Before.Y, g2After.Y)
	}
	if _, ok := tt.Find("a2"); !ok {
		t.Fatal("g2 server row should show after switching")
	}
	if err := tt.Click("a2"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !a.sshF.AuthDraft["a2"] {
		t.Fatal("clicking a row should toggle its checkbox")
	}
}
