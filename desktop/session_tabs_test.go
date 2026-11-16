package desktop

import "testing"

func TestShowSessionKeepsMultipleSessionsFromSameWorkspace(t *testing.T) {
	workspace := t.TempDir()
	first := &conversation{ID: "first", Workspace: workspace, Model: "model-a"}
	second := &conversation{ID: "second", Workspace: workspace, Title: "second session"}
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

	third := &conversation{ID: "third", Workspace: workspace}
	a.showSession(third)
	if len(a.tabs) != 3 || a.tabs[2] != third || a.current() != third {
		t.Fatalf("third same-workspace session was not added independently: tabs=%d active=%d", len(a.tabs), a.active)
	}

	a.showSession(second)
	if len(a.tabs) != 3 || a.current() != second {
		t.Fatalf("reopening an already-open session duplicated or failed to activate it: tabs=%d active=%d", len(a.tabs), a.active)
	}
}
