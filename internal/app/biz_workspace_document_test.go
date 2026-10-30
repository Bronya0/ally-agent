package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestWorkspacePropertiesAreCheapAndDetailsExplicit(t *testing.T) {
	dir := t.TempDir()
	name := filepath.Join(dir, "archive.bin")
	if err := os.WriteFile(name, []byte{0, 1, 2, 3}, 0600); err != nil {
		t.Fatal(err)
	}
	a := newWorkspaceEditorTestApp(dir)
	req := WorkspacePathRequest{Workspace: dir, Path: "archive.bin"}
	basic, err := a.InspectWorkspaceFile(context.Background(), req, false)
	if err != nil || basic.Size != 4 || basic.SHA256 != "" {
		t.Fatalf("basic properties: %+v %v", basic, err)
	}
	full, err := a.InspectWorkspaceFile(context.Background(), req, true)
	if err != nil || full.SHA256 == "" {
		t.Fatalf("explicit hashes: %+v %v", full, err)
	}
	_, err = a.ReadWorkspaceFileAt(req)
	if toolErrorCode(err) != "E_BINARY_FILE" || WorkspaceFilePreviewNote(err) == "" {
		t.Fatalf("binary fallback changed model error envelope: %v", err)
	}
	if WorkspaceFilePreviewNote(os.ErrPermission) != "" {
		t.Fatal("actual permission failures were silenced")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := a.InspectWorkspaceFile(ctx, req, true); !errors.Is(err, context.Canceled) {
		t.Fatalf("inspect did not cancel: %v", err)
	}
}
