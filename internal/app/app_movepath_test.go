package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMovePathWithinWorkspace(t *testing.T) {
	app := &App{workspaceCaches: newWorkspaceCacheHolder()}
	dir := t.TempDir()
	app.config.Workspace = dir

	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}

	// 正常移动
	res, err := app.MovePath(MovePathRequest{Source: "a.txt", Destination: "sub/a.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Moved {
		t.Fatal("expected Moved=true")
	}
	if _, err := os.Stat(filepath.Join(dir, "sub", "a.txt")); err != nil {
		t.Fatal("file should exist at destination")
	}
	if _, err := os.Stat(filepath.Join(dir, "a.txt")); !os.IsNotExist(err) {
		t.Fatal("file should no longer exist at source")
	}

	// 目标已存在且不覆盖 → E_EXISTS
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := app.MovePath(MovePathRequest{Source: "a.txt", Destination: "sub/a.txt"}); err == nil || !strings.Contains(err.Error(), "E_EXISTS") {
		t.Fatalf("expected E_EXISTS error, got %v", err)
	}

	// 目标已存在且覆盖
	if _, err := app.MovePath(MovePathRequest{Source: "a.txt", Destination: "sub/a.txt", Overwrite: true}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "sub", "a.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "new" {
		t.Fatalf("expected overwritten content, got %q", string(data))
	}

	// 移动到自身路径 → no-op
	res, err = app.MovePath(MovePathRequest{Source: "sub/a.txt", Destination: "sub/a.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Moved {
		t.Fatal("expected Moved=false for same path")
	}
}
