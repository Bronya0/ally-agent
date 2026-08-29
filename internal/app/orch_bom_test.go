// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestBOMEndToEndReadEditPreservesBOM verifies the full user-visible path:
// read of a BOM file must not expose the BOM, edit must match on the
// BOM-less text, the file on disk must keep its BOM after the edit, and the
// version returned by edit must equal the next read's version (no drift).
func TestBOMEndToEndReadEditPreservesBOM(t *testing.T) {
	app := NewApp()
	dir := t.TempDir()
	rel := "sample.txt"
	original := "\uFEFFalpha\nbeta\n"
	if err := os.WriteFile(filepath.Join(dir, rel), []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := ConfigState{Workspace: dir}

	// 1. Read: version is based on the original bytes (with BOM), displayed
	// content must not contain the BOM.
	readRes := app.executeTool(context.Background(), cfg, "s-1", "read", []byte(`{"files":[{"path":"sample.txt"}]}`))
	if !readRes.OK {
		t.Fatalf("read failed: %v", readRes.Error)
	}
	readData := readRes.Data.(*BatchReadResult)
	if len(readData.Files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(readData.Files))
	}
	f := readData.Files[0]
	if strings.Contains(f.Content, "\uFEFF") {
		t.Fatalf("read content must not expose the BOM: %q", f.Content)
	}
	readVersion := f.Version
	if readVersion == "" {
		t.Fatal("read returned empty version")
	}

	// 2. Edit using that version: change the first line.
	editArgs := []byte(`{"path":"sample.txt","version":"` + readVersion + `","changes":[{"oldText":"alpha","newText":"ALPHA"}]}`)
	editRes := app.executeTool(context.Background(), cfg, "s-1", "edit", editArgs)
	if !editRes.OK {
		t.Fatalf("edit failed: %v", editRes.Error)
	}
	editData := editRes.Data.(MultiEditResult)
	if editData.FileCount != 1 || len(editData.Files) != 1 {
		t.Fatalf("unexpected edit result: %#v", editData)
	}
	afterVersion := editData.Files[0].Version

	// 3. File on disk must still start with the BOM and contain the edit.
	got, err := os.ReadFile(filepath.Join(dir, rel))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(got), "\uFEFF") {
		t.Fatalf("BOM was lost after edit: %q", got)
	}
	if string(got) != "\uFEFFALPHA\nbeta\n" {
		t.Fatalf("unexpected edited content: %q", got)
	}

	// 4. Next read returns the same version the edit reported (no drift).
	readRes2 := app.executeTool(context.Background(), cfg, "s-1", "read", []byte(`{"files":[{"path":"sample.txt"}]}`))
	if !readRes2.OK {
		t.Fatalf("second read failed: %v", readRes2.Error)
	}
	readData2 := readRes2.Data.(*BatchReadResult)
	if readData2.Files[0].Version != afterVersion {
		t.Fatalf("version drift: edit returned %q, next read returned %q", afterVersion, readData2.Files[0].Version)
	}
}

// TestBOMEndToEndEditFailsWithStaleVersion ensures the optimistic-concurrency
// guard still works for BOM files: editing with a version computed from
// BOM-less bytes must fail.
func TestBOMEndToEndEditFailsWithStaleVersion(t *testing.T) {
	app := NewApp()
	dir := t.TempDir()
	rel := "sample.txt"
	if err := os.WriteFile(filepath.Join(dir, rel), []byte("\uFEFFalpha\nbeta\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := ConfigState{Workspace: dir}

	// Version computed from BOM-less text differs from the on-disk version
	// (which includes the BOM), so the edit must be rejected.
	staleVersion := hashVersion([]byte("alpha\nbeta\n"))
	editArgs := []byte(`{"path":"sample.txt","version":"` + staleVersion + `","changes":[{"oldText":"alpha","newText":"ALPHA"}]}`)
	editRes := app.executeTool(context.Background(), cfg, "s-1", "edit", editArgs)
	if editRes.OK {
		t.Fatal("edit with a version computed from BOM-less bytes must fail")
	}
	if !strings.Contains(editRes.Error, "E_VERSION_MISMATCH") {
		t.Fatalf("expected E_VERSION_MISMATCH, got: %v", editRes.Error)
	}
}
