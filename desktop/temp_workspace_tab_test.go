// SPDX-License-Identifier: GPL-3.0-only
package desktop

import "testing"

func TestClosingOneTabKeepsTheTemporaryWorkspaceOfItsSiblings(t *testing.T) {
	a := &desktop{
		ready: true,
		page:  "plugin:test",
		prefs: defaultPrefs(),
		tabs: []*conversation{
			{ID: "tab-1", Workspace: "ally-temp-shared"},
			{ID: "tab-2", Workspace: "ally-temp-shared"},
			{ID: "tab-3", Workspace: "ally-temp-other"},
		},
	}

	if root := a.detachWorkspaceTab(0); root != "" {
		t.Fatalf("closing a shared tab scheduled deletion of %q", root)
	}
	if len(a.tabs) != 2 || a.tabs[0].ID != "tab-2" {
		t.Fatalf("closing a tab should keep its sibling, tabs = %v", a.tabs)
	}
	if root := a.detachWorkspaceTab(0); root != "ally-temp-shared" {
		t.Fatalf("closing the last shared tab scheduled deletion of %q", root)
	}
	if len(a.tabs) != 1 || a.tabs[0].ID != "tab-3" {
		t.Fatalf("closing the last shared tab should keep the other workspace, tabs = %v", a.tabs)
	}
}
