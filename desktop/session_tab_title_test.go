// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"path/filepath"
	"testing"
)

// TestNewTabIsNamedAfterItsWorkspace: blank conversations in different projects
// all read "新对话", so the tab strip could not tell them apart until each had a
// first message. The workspace name is what the backend already uses.
func TestNewTabIsNamedAfterItsWorkspace(t *testing.T) {
	root := filepath.Join(t.TempDir(), "ally-agent")
	a := &desktop{}

	if got := a.freshConversation(root).Title; got != "ally-agent" {
		t.Fatalf("new tab title = %q, want the workspace directory name", got)
	}
	// A temporary workspace is named for what it is, not for its random suffix.
	temp := filepath.Join(t.TempDir(), "ally-temp-123456")
	if got := a.freshConversation(temp).Title; got == "ally-temp-123456" || got == "" {
		t.Fatalf("temporary workspace tab title = %q, want a readable name", got)
	}
	// Nothing chosen yet still needs a title.
	if got := a.freshConversation("").Title; got == "" {
		t.Fatal("a conversation without a workspace needs a title")
	}
}

// The title stays the workspace name until the first message names the session,
// so a tab that already talked keeps its own title.
func TestFirstMessageStillNamesTheSession(t *testing.T) {
	root := filepath.Join(t.TempDir(), "cam")
	t2 := &conversation{ID: "s1", Workspace: root, Title: sessionTabTitle(root)}
	if t2.Title != "cam" {
		t.Fatalf("start title = %q", t2.Title)
	}
	t2.Title = short("修一下登录页的报错", 36)
	if t2.Title != "修一下登录页的报错" {
		t.Fatalf("message did not name the session: %q", t2.Title)
	}
}
