// SPDX-License-Identifier: GPL-3.0-only
package app

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newWorkspaceEditorTestApp(workspace string) *App {
	app := NewApp()
	app.initialized = true
	app.config = ConfigState{Workspace: workspace}
	return app
}

func TestWorkspaceEditorReadSavePreservesTextShape(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sample.txt")
	original := append([]byte{0xEF, 0xBB, 0xBF}, []byte("alpha\r\nbeta\r\n")...)
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}

	app := newWorkspaceEditorTestApp(dir)
	snapshot, err := app.ReadWorkspaceFile("sample.txt")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Content != "alpha\nbeta\n" || snapshot.LineEnding != "CRLF" {
		t.Fatalf("unexpected normalized snapshot: content=%q ending=%q", snapshot.Content, snapshot.LineEnding)
	}

	result, err := app.SaveWorkspaceFile(SaveWorkspaceFileRequest{
		Path:    "sample.txt",
		Version: snapshot.Version,
		Content: "alpha\nBETA\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Version == snapshot.Version {
		t.Fatal("expected saved content to receive a new version")
	}
	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	expected := append([]byte{0xEF, 0xBB, 0xBF}, []byte("alpha\r\nBETA\r\n")...)
	if !bytes.Equal(written, expected) {
		t.Fatalf("save did not preserve BOM/CRLF: %q", written)
	}
}

func TestWorkspaceEditorRejectsStaleSave(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sample.txt")
	if err := os.WriteFile(path, []byte("before\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	app := newWorkspaceEditorTestApp(dir)
	snapshot, err := app.ReadWorkspaceFile("sample.txt")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("external\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err = app.SaveWorkspaceFile(SaveWorkspaceFileRequest{
		Path:    "sample.txt",
		Version: snapshot.Version,
		Content: "editor\n",
	})
	if err == nil || !strings.Contains(err.Error(), "E_VERSION_MISMATCH") {
		t.Fatalf("expected stale save to fail with E_VERSION_MISMATCH, got %v", err)
	}
	written, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(written) != "external\n" {
		t.Fatalf("stale save overwrote external content: %q", written)
	}
}

func TestWorkspaceEditorRejectsOversizedFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "large.txt")
	if err := os.WriteFile(path, bytes.Repeat([]byte{'x'}, workspaceEditorMaxBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	app := newWorkspaceEditorTestApp(dir)
	_, err := app.ReadWorkspaceFile("large.txt")
	if err == nil || !strings.Contains(err.Error(), "E_FILE_TOO_LARGE") {
		t.Fatalf("expected oversized file to fail with E_FILE_TOO_LARGE, got %v", err)
	}
}
