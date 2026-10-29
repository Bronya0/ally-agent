package desktop

import (
	backend "ally-dev/internal/app"
	"testing"
)

func TestStartupWorkspace(t *testing.T) {
	entry := func(path string, updated int64) backend.SessionIndexEntry {
		return backend.SessionIndexEntry{Workspace: path, UpdatedAt: updated}
	}
	const fallback = "configured-workspace"
	for _, tc := range []struct {
		name     string
		sessions []backend.SessionIndexEntry
		want     string
	}{
		{"sidebar order beats session count", []backend.SessionIndexEntry{entry("ally-agent-v2", 100), entry("ally-agent", 90), entry("ally-agent", 80), entry("ally-agent", 70)}, "ally-agent-v2"},
		{"deduplicate workspaces", []backend.SessionIndexEntry{entry("ally-agent-v2", 100), entry("ally-agent-v2", 90), entry("ally-agent", 80)}, "ally-agent-v2"},
		{"skip temporary", []backend.SessionIndexEntry{entry("ally-temp-startup", 100), entry("ally-agent", 90)}, "ally-agent"},
		{"fallback", []backend.SessionIndexEntry{entry("ally-temp-startup", 100), entry("", 102)}, fallback},
		{"first launch", nil, fallback},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := startupWorkspace(tc.sessions, fallback); got != tc.want {
				t.Fatalf("startupWorkspace = %q, want %q", got, tc.want)
			}
		})
	}
}
