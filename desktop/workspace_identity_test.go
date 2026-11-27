package desktop

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	backend "ally-dev/internal/app"
)

func TestSameWorkspaceFollowsFilesystemCaseSemantics(t *testing.T) {
	base := filepath.Join("projects", "cam")
	variant := filepath.Join("projects", "CAM")
	if got := sameWorkspace(base, variant); got != (os.PathSeparator == '\\') {
		t.Fatalf("case-variant roots merged=%v on %s", got, runtime.GOOS)
	}
	if !sameWorkspace(base, base+string(os.PathSeparator)) {
		t.Fatal("a trailing separator broke workspace identity")
	}
	if !sameWorkspace("", "") || sameWorkspace("", base) {
		t.Fatal("empty roots must only match empty roots")
	}
}

// Deleting a workspace's history walks the whole index; on a case-sensitive
// volume `cam` and `CAM` are two projects and deleting one must leave the
// other's sessions alone.
func TestWorkspaceHistoryDeleteKeepsCaseVariantProjects(t *testing.T) {
	a := &desktop{sessions: []backend.SessionIndexEntry{
		{ID: "lower", Workspace: filepath.Join("projects", "cam")},
		{ID: "upper", Workspace: filepath.Join("projects", "CAM")},
	}}
	workspaceCaseFold = false
	defer func() { workspaceCaseFold = os.PathSeparator == '\\' }()

	if ids := a.workspaceSessionIDs(filepath.Join("projects", "cam")); len(ids) != 1 || ids[0] != "lower" {
		t.Fatalf("a case-sensitive volume merged two projects for deletion: %v", ids)
	}

	workspaceCaseFold = true
	if ids := a.workspaceSessionIDs(filepath.Join("projects", "cam")); len(ids) != 2 {
		t.Fatalf("a case-insensitive volume must treat both spellings as one project: %v", ids)
	}
}

func TestWorkspaceKeyFollowsFilesystemCaseSemantics(t *testing.T) {
	if os.PathSeparator != '\\' && workspaceKey(filepath.Join("projects", "cam")) == workspaceKey(filepath.Join("projects", "CAM")) {
		t.Fatal("a case-sensitive volume folded two projects into one cache key")
	}
}
