// SPDX-License-Identifier: GPL-3.0-only
package desktop

import "testing"

// A workspace entry of the header menu has to show something on every click: a
// Tab of another workspace comes forward, and the workspace the current Tab
// already runs in opens a new Tab instead of switching to itself.
func TestWorkspaceEntryTab(t *testing.T) {
	tabs := func(roots ...string) []*conversation {
		out := make([]*conversation, 0, len(roots))
		for _, root := range roots {
			out = append(out, &conversation{Workspace: root})
		}
		return out
	}
	cases := []struct {
		name      string
		tabs      []*conversation
		active    int
		workspace string
		want      int
	}{
		{name: "no tab yet opens a new one", tabs: tabs(`C:\work\cam`), active: 0, workspace: `C:\work\ally`, want: -1},
		{name: "the current workspace opens a new one", tabs: tabs(`C:\work\cam`), active: 0, workspace: `C:\work\cam`, want: -1},
		{name: "another tab comes forward", tabs: tabs(`C:\work\cam`, `C:\work\ally`), active: 1, workspace: `C:\work\cam`, want: 0},
		{name: "the first tab of a workspace comes forward", tabs: tabs(`C:\work\cam`, `C:\work\ally`, `C:\work\cam`), active: 1, workspace: `C:\work\cam`, want: 0},
		{name: "the path matches whatever its case", tabs: tabs(`C:\work\CAM`), active: 1, workspace: `c:\work\cam`, want: 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := workspaceEntryTab(c.tabs, c.active, c.workspace); got != c.want {
				t.Fatalf("workspaceEntryTab(%v, %d, %q) = %d, want %d", len(c.tabs), c.active, c.workspace, got, c.want)
			}
		})
	}
}
