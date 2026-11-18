package desktop

import (
	backend "ally-dev/internal/app"
	"path/filepath"
	"testing"
)

func TestStartupWorkspace(t *testing.T) {
	entry := func(path string, updated int64) backend.SessionIndexEntry {
		return backend.SessionIndexEntry{Workspace: path, UpdatedAt: updated}
	}
	fallback := t.TempDir()
	project := t.TempDir()
	second := t.TempDir()
	missing := filepath.Join(t.TempDir(), "missing")
	temporary := filepath.Join(t.TempDir(), "ally-temp-startup")
	for _, tc := range []struct {
		name     string
		sessions []backend.SessionIndexEntry
		want     string
	}{
		{"sidebar order beats session count", []backend.SessionIndexEntry{entry(second, 100), entry(project, 90), entry(project, 80), entry(project, 70)}, second},
		{"deduplicate workspaces", []backend.SessionIndexEntry{entry(second, 100), entry(second, 90), entry(project, 80)}, second},
		{"skip temporary", []backend.SessionIndexEntry{entry(temporary, 100), entry(project, 90)}, project},
		{"skip missing workspace", []backend.SessionIndexEntry{entry(missing, 100), entry(project, 90)}, project},
		{"fallback when missing", []backend.SessionIndexEntry{entry(missing, 100)}, fallback},
		{"fallback", []backend.SessionIndexEntry{entry(temporary, 100), entry("", 102)}, fallback},
		{"first launch", nil, fallback},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := startupWorkspace(nil, tc.sessions, fallback); got != tc.want {
				t.Fatalf("startupWorkspace = %q, want %q", got, tc.want)
			}
		})
	}
}
