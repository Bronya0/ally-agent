package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ally-dev/internal/tools/grep"
)

func TestExplorerFilenameSearch(t *testing.T) {
	t.Parallel()
	if _, err := grep.Find(); err != nil {
		t.Skip("rg unavailable")
	}
	root := t.TempDir()
	files := []string{"nested/中文 Needle.go", ".hidden/needle.txt", "ignored/needle.json", "needle-dir/other.txt", "unrelated.txt", ".gitignore", "dist", "src/vendor-source/kept.txt"}
	for _, dir := range grep.ExcludedDirs() {
		// Directory exclusions apply at every depth and regardless of case.
		// "dist" itself is a normal file, so put that directory under src.
		if dir != "dist" {
			files = append(files, dir+"/deep/needle.txt")
		}
		files = append(files, "src/"+strings.ToUpper(dir)+"/deep/needle.txt")
	}
	for _, name := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("ignored/\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	a := newIsolatedTestApp(t)
	result, err := a.SearchExplorerFiles(context.Background(), root, "NEEDLE")
	if err != nil || result.Truncated || len(result.Entries) != 3 {
		t.Fatalf("search: %+v %v", result, err)
	}
	result, err = a.SearchExplorerFiles(context.Background(), root, "needle-dir/other")
	if err != nil || len(result.Entries) != 1 {
		t.Fatalf("path query: %+v %v", result, err)
	}
	result, err = a.SearchExplorerFiles(context.Background(), root, "dist")
	if err != nil || len(result.Entries) != 1 || result.Entries[0].Path != "dist" {
		t.Fatalf("directory exclusion hid a normal file: %+v %v", result, err)
	}
	result, err = a.SearchExplorerFiles(context.Background(), root, "kept")
	if err != nil || len(result.Entries) != 1 {
		t.Fatalf("directory exclusion hid a similar directory name: %+v %v", result, err)
	}
	result, err = a.SearchExplorerFiles(context.Background(), t.TempDir(), "missing")
	if err != nil || len(result.Entries) != 0 {
		t.Fatalf("empty directory: %+v %v", result, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = a.SearchExplorerFiles(ctx, root, "needle"); err == nil {
		t.Fatal("canceled search succeeded")
	}
	for i := 0; i < 1002; i++ {
		if err := os.WriteFile(filepath.Join(root, fmt.Sprintf("needle-%04d", i)), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	result, err = a.SearchExplorerFiles(context.Background(), root, "needle")
	if err != nil || !result.Truncated || len(result.Entries) != 1000 {
		t.Fatalf("limit: %d %v %v", len(result.Entries), result.Truncated, err)
	}
}
