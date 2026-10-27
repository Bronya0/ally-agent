// SPDX-License-Identifier: GPL-3.0-only
package app

import (
	"bytes"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func TestNativeWorkspaceImage(t *testing.T) {
	dir := t.TempDir()
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "image.png"), encoded.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bad.png"), []byte("invalid"), 0600); err != nil {
		t.Fatal(err)
	}
	app := newWorkspaceEditorTestApp(dir)
	data, err := app.ReadNativeWorkspaceImage(WorkspacePathRequest{Path: "image.png"})
	if err != nil || !bytes.Equal(data, encoded.Bytes()) {
		t.Fatalf("image read: %v", err)
	}
	if _, err := app.ReadNativeWorkspaceImage(WorkspacePathRequest{Path: "bad.png"}); err == nil {
		t.Fatal("invalid image accepted")
	}
}
