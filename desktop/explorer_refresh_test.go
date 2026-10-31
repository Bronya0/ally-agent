package desktop

import (
	backend "ally-dev/internal/app"
	"testing"
)

func TestExplorerRefreshPreservesExpandedDirectories(t *testing.T) {
	root := t.TempDir()
	a := &desktop{}
	a.prepareFileTree(root)
	a.files = []backend.FileEntry{{Path: "src", Dir: true}, {Path: "closed", Dir: true}}
	a.fileOpen["src"] = true
	a.fileOpen["src/nested"] = true
	a.fileOpen["closed"] = false
	a.fileChildren["src"] = []backend.FileEntry{{Path: "src/nested", Dir: true}}
	a.fileChildren["src/nested"] = []backend.FileEntry{{Path: "src/nested/old.go"}}
	a.fileChildren["closed"] = []backend.FileEntry{{Path: "closed/stale.go"}}
	a.fileLoading["src"] = true

	a.prepareFileTree(root)
	if !a.fileOpen["src"] || !a.fileOpen["src/nested"] || len(a.files) != 2 || len(a.fileChildren["src/nested"]) != 1 {
		t.Fatal("refresh collapsed or blanked the visible tree")
	}
	if _, cached := a.fileChildren["closed"]; cached || len(a.fileLoading) != 0 {
		t.Fatal("refresh retained a stale collapsed listing or pending read")
	}
	a.applyFileTreeEntries("", []backend.FileEntry{{Path: "src", Dir: true}, {Path: "new.txt"}})
	a.applyFileTreeEntries("src", []backend.FileEntry{{Path: "src/nested", Dir: true}})
	a.applyFileTreeEntries("src/nested", []backend.FileEntry{{Path: "src/nested/new.go"}})
	if !a.fileOpen["src/nested"] || a.fileChildren["src/nested"][0].Path != "src/nested/new.go" {
		t.Fatal("refresh failed to update nested contents while keeping them expanded")
	}
	// A collapse made while the refresh is pending must win over the response.
	a.fileOpen["src"] = false
	a.applyFileTreeEntries("", a.files)
	if a.fileOpen["src"] {
		t.Fatal("refresh undid a user collapse")
	}
	if a.fileBranchOpen("src/nested") {
		t.Fatal("remembered expansion below a collapsed ancestor would trigger IO")
	}
	a.fileOpen["src"] = true
	if !a.fileBranchOpen("src/nested") || a.fileBranchOpen("closed") {
		t.Fatal("refresh must read only visible expanded branches")
	}
}

func TestExplorerRefreshRemovesDeletedBranchesAndResetsWorkspace(t *testing.T) {
	a := &desktop{}
	a.prepareFileTree(t.TempDir())
	a.files = []backend.FileEntry{{Path: "gone", Dir: true}, {Path: "gone-other", Dir: true}}
	for _, path := range []string{"gone", "gone/nested", "gone-other"} {
		a.fileOpen[path] = true
		a.fileChildren[path] = []backend.FileEntry{{Path: path + "/old.txt"}}
		a.fileLoading[path] = true
	}
	// Replacing a directory with a file removes its old subtree as well.
	a.applyFileTreeEntries("", []backend.FileEntry{{Path: "gone"}, {Path: "gone-other", Dir: true}})
	for _, path := range []string{"gone", "gone/nested"} {
		if a.fileOpen[path] || a.fileChildren[path] != nil || a.fileLoading[path] {
			t.Fatalf("deleted branch retained: %s", path)
		}
	}
	if !a.fileOpen["gone-other"] {
		t.Fatal("removed an unrelated directory with the same prefix")
	}
	a.prepareFileTree(t.TempDir())
	if len(a.files)+len(a.fileOpen)+len(a.fileChildren)+len(a.fileLoading) != 0 {
		t.Fatal("workspace switch retained another workspace's tree")
	}
}
