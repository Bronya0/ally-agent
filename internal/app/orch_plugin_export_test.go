// SPDX-License-Identifier: GPL-3.0-only
package app

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCopyFilePreservesDestinationOnFailure(t *testing.T) {
	root := t.TempDir()
	dest := filepath.Join(root, "export.zip")
	if err := os.WriteFile(dest, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Opening a directory succeeds on Unix, but copying its contents fails.
	if err := copyFile(t.TempDir(), dest); err == nil {
		t.Fatal("accepted a directory source")
	}
	if data, err := os.ReadFile(dest); err != nil || string(data) != "original" {
		t.Fatalf("failed export changed original: %q, %v", data, err)
	}
	app := &App{}
	if err := app.writePluginExport("demo", filepath.Join(t.TempDir(), "missing"), false, dest); err == nil {
		t.Fatal("accepted packaging a missing plugin directory")
	}
	if data, err := os.ReadFile(dest); err != nil || string(data) != "original" {
		t.Fatalf("failed packaging changed original: %q, %v", data, err)
	}
	source := filepath.Join(root, "source.zip")
	if err := os.WriteFile(source, []byte("replacement"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := copyFile(source, dest); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(dest); err != nil || string(data) != "replacement" {
		t.Fatalf("successful export content: %q, %v", data, err)
	}
	if err := copyFile(dest, dest); err == nil {
		t.Fatal("accepted copying onto itself")
	}
	if data, err := os.ReadFile(dest); err != nil || string(data) != "replacement" {
		t.Fatalf("self-copy changed original: %q, %v", data, err)
	}
	matches, err := filepath.Glob(filepath.Join(root, ".ally-export-*"))
	if err != nil || len(matches) != 0 {
		t.Fatalf("temporary exports remain: %v, %v", matches, err)
	}
}
