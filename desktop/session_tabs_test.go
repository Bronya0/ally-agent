package desktop

import (
	backend "ally-dev/internal/app"
	"testing"
)

func TestShowSessionKeepsMultipleSessionsFromSameWorkspace(t *testing.T) {
	workspace := t.TempDir()
	first := &conversation{ID: "first", Workspace: workspace, Model: "model-a", Messages: []map[string]any{{"role": "user", "content": "hello"}}}
	second := &conversation{ID: "second", Workspace: workspace, Title: "second session", Messages: []map[string]any{{"role": "user", "content": "hi"}}}
	a := &desktop{page: "chat", tabs: []*conversation{first}}

	a.showSession(second)
	if len(a.tabs) != 2 || a.tabs[0] != first || a.tabs[1] != second {
		t.Fatalf("opening a same-workspace session replaced an existing tab: %#v", a.tabs)
	}
	if a.active != 1 || a.current() != second {
		t.Fatalf("newly opened session is not active: active=%d", a.active)
	}
	if second.Model != first.Model {
		t.Fatalf("newly opened session did not inherit workspace model: got %q want %q", second.Model, first.Model)
	}

	a.showSession(second)
	if len(a.tabs) != 2 || a.current() != second {
		t.Fatalf("reopening an already-open session duplicated or failed to activate it: tabs=%d active=%d", len(a.tabs), a.active)
	}
}

// Opening history used to add a tab beside the blank one the workspace had open,
// so one project showed a workspace tab and a session tab at once.
func TestOpeningASavedSessionTakesOverTheBlankTab(t *testing.T) {
	workspace := t.TempDir()
	blank := &conversation{ID: "blank", Workspace: workspace}
	a := &desktop{page: "chat", tabs: []*conversation{blank}, openSessions: map[string]*conversation{blank.ID: blank}}
	saved := &conversation{ID: "saved", Workspace: workspace, Title: "old chat", Messages: []map[string]any{{"role": "user", "content": "hi"}}}

	a.showSession(saved)
	if len(a.tabs) != 1 || a.tabs[0] != saved || a.current() != saved {
		t.Fatalf("opening a saved session did not reuse the blank tab: tabs=%d active=%d", len(a.tabs), a.active)
	}
	if _, ok := a.openSessions[blank.ID]; ok {
		t.Fatal("replaced blank tab retained its old session")
	}
}

// The blank tab is only spare while nothing depends on it: a draft, a pending
// attachment, a running turn or another project keeps its own tab.
func TestBlankTabKeepsItsPlaceWhenItHoldsWork(t *testing.T) {
	workspace := t.TempDir()
	for name, tab := range map[string]*conversation{
		"draft":           {ID: "draft", Workspace: workspace, Draft: "typing"},
		"messages":        {ID: "chatting", Workspace: workspace, Messages: []map[string]any{{"role": "user", "content": "hi"}}},
		"running":         {ID: "running", Workspace: workspace, Busy: true},
		"pending":         {ID: "pending", Workspace: workspace, PendingAttachments: []backend.AttachmentInput{{ID: "image", Name: "a.png", Kind: "image"}}},
		"other-workspace": {ID: "elsewhere", Workspace: t.TempDir()},
	} {
		t.Run(name, func(t *testing.T) {
			a := &desktop{page: "chat", tabs: []*conversation{tab}}
			saved := &conversation{ID: "saved-" + name, Workspace: workspace}
			a.showSession(saved)
			if len(a.tabs) != 2 || a.tabs[0] != tab || a.current() != saved {
				t.Fatalf("%s: the tab was taken over: tabs=%d active=%d", name, len(a.tabs), a.active)
			}
		})
	}
}
