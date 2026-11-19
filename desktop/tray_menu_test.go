package desktop

import (
	"strings"
	"testing"
)

func TestAllyTrayMenuUsesSharedItems(t *testing.T) {
	items := (&desktop{}).trayMenuItems()
	if len(items) != 4 {
		t.Fatalf("got %d items", len(items))
	}
	for i, item := range items {
		if item.Key == "" || item.Label == "" || item.Icon == nil || (item.OnClick == nil && len(item.Children) == 0) {
			t.Fatalf("item %d is incomplete: %+v", i, item)
		}
	}
	if items[0].SeparatorBefore || len(items[0].Children) != 3 || !items[0].Children[0].Checked || !items[1].SeparatorBefore || items[2].SeparatorBefore || !items[3].SeparatorBefore {
		t.Fatal("tray menu grouping changed")
	}
}

func TestTrayListsTabsWithRunState(t *testing.T) {
	a := &desktop{tabs: []*conversation{
		{ID: "running", Workspace: "project", Title: "First", Busy: true},
		{ID: "done", Workspace: "project", Title: "Second", trayOutcome: "done"},
		{ID: "failed", Workspace: "other", trayOutcome: "error"},
	}, active: 1}
	items := a.trayMenuItems()
	if len(items) != 5 {
		t.Fatalf("got %d items", len(items))
	}
	if items[0].Key != "tabs" || len(items[0].Children) != 3 {
		t.Fatal("tabs submenu missing")
	}
	tabs := items[0].Children
	for i, want := range []struct{ id, icon, status string }{
		{"running", "refresh", "运行中"}, {"done", "check", "已完成"}, {"failed", "alert", "失败"},
	} {
		item := tabs[i]
		if item.Key != "tab:"+want.id || item.Icon != iconImage(want.icon) || item.StatusColor.A == 0 || !strings.Contains(item.AccessibilityLabel, want.status) || item.OnClick == nil {
			t.Fatalf("tab %s: %+v", want.id, item)
		}
	}
	if items[1].Key != "proxy" || len(items[1].Children) != 3 || !items[1].Children[0].Checked || items[1].Children[1].Key != "proxy:system" || items[1].Children[2].Key != "proxy:manual" || items[2].Key != "open" || !items[2].SeparatorBefore || items[3].Key != "update" || items[4].Key != "quit" {
		t.Fatal("fixed actions must follow the tab list")
	}
	if strings.Contains(tabs[0].Label, "运行中") {
		t.Fatal("status should be represented by icon")
	}
	if strings.Contains(tabs[0].Label, "First") || strings.Contains(tabs[0].AccessibilityLabel, "First") {
		t.Fatal("tray exposed conversation title")
	}
}
