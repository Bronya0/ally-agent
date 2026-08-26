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
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// unmarshalTypeErrorFor runs the same unmarshal the tool decoder performs and
// returns the reported *json.UnmarshalTypeError, so tests assert repair
// behavior against the real field paths encoding/json produces.
func unmarshalTypeErrorFor(t *testing.T, args []byte, v any) *json.UnmarshalTypeError {
	t.Helper()
	err := json.Unmarshal(args, v)
	if err == nil {
		t.Fatalf("expected unmarshal error for %s", args)
	}
	var typeErr *json.UnmarshalTypeError
	if !errors.As(err, &typeErr) {
		t.Fatalf("expected *json.UnmarshalTypeError, got %T: %v", err, err)
	}
	return typeErr
}

func TestRepairToolArgJSONTopLevelStringEncodedArray(t *testing.T) {
	args := []byte(`{"files":"[{\"path\":\"app.go\",\"startLine\":1}]"}`)
	typeErr := unmarshalTypeErrorFor(t, args, &BatchReadRequest{})

	fixed, ok := repairToolArgJSON(args, typeErr)
	if !ok {
		t.Fatal("expected repair to succeed")
	}
	var req BatchReadRequest
	if err := json.Unmarshal(fixed, &req); err != nil {
		t.Fatalf("repaired JSON still invalid: %v\n%s", err, fixed)
	}
	if len(req.Files) != 1 || req.Files[0].Path != "app.go" {
		t.Fatalf("unexpected repaired request: %#v", req)
	}
}

func TestRepairToolArgJSONNestedStringEncodedChanges(t *testing.T) {
	args := []byte(`{"files":[{"path":"app.go","version":"9k3m7x","changes":"[{\"oldText\":\"a\",\"newText\":\"b\"}]"}]}`)
	typeErr := unmarshalTypeErrorFor(t, args, &ModelEditToolRequest{})

	fixed, ok := repairToolArgJSON(args, typeErr)
	if !ok {
		t.Fatalf("expected repair to succeed, field path was %q", typeErr.Field)
	}
	var req ModelEditToolRequest
	if err := json.Unmarshal(fixed, &req); err != nil {
		t.Fatalf("repaired JSON still invalid: %v\n%s", err, fixed)
	}
	if len(req.Files) != 1 || len(req.Files[0].Changes) != 1 || req.Files[0].Changes[0].OldText != "a" {
		t.Fatalf("unexpected repaired request: %#v", req)
	}
}

func TestRepairToolArgJSONSingleObjectForSlice(t *testing.T) {
	// A bare object where an array is expected gets wrapped, no string
	// encoding involved.
	args := []byte(`{"files":{"path":"app.go"}}`)
	typeErr := unmarshalTypeErrorFor(t, args, &BatchReadRequest{})

	fixed, ok := repairToolArgJSON(args, typeErr)
	if !ok {
		t.Fatal("expected repair to succeed")
	}
	var req BatchReadRequest
	if err := json.Unmarshal(fixed, &req); err != nil {
		t.Fatalf("repaired JSON still invalid: %v\n%s", err, fixed)
	}
	if len(req.Files) != 1 || req.Files[0].Path != "app.go" {
		t.Fatalf("unexpected repaired request: %#v", req)
	}
}

func TestRepairToolArgJSONLeavesPlainStringsAlone(t *testing.T) {
	// A string value that is not JSON array/object text must never be
	// rewritten, even when a type error names its field.
	args := []byte(`{"files":"just a path"}`)
	typeErr := unmarshalTypeErrorFor(t, args, &BatchReadRequest{})
	if _, ok := repairToolArgJSON(args, typeErr); ok {
		t.Fatal("plain string must not be repaired")
	}
}

func TestExecuteToolReadRepairsStringEncodedFiles(t *testing.T) {
	app := NewApp()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "sample.txt"), []byte("alpha\nbeta\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	res := app.executeTool(context.Background(), ConfigState{Workspace: dir}, "s-1", "read",
		[]byte(`{"files":"[{\"path\":\"sample.txt\"}]"}`))
	if !res.OK {
		t.Fatalf("read failed: %v", res.Error)
	}
	data, ok := res.Data.(*BatchReadResult)
	if !ok || len(data.Files) != 1 || data.Files[0].Path != "sample.txt" {
		t.Fatalf("unexpected read result: %#v", res.Data)
	}
	if len(res.Warnings) == 0 || !strings.Contains(res.Warnings[0], "files") {
		t.Fatalf("expected a repair warning mentioning files, got %v", res.Warnings)
	}
}

func TestExecuteToolEditRepairsStringEncodedFiles(t *testing.T) {
	app := NewApp()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "sample.txt"), []byte("alpha\nbeta\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := ConfigState{Workspace: dir}

	readRes := app.executeTool(context.Background(), cfg, "s-1", "read", []byte(`{"files":[{"path":"sample.txt"}]}`))
	if !readRes.OK {
		t.Fatalf("read failed: %v", readRes.Error)
	}
	version := readRes.Data.(*BatchReadResult).Files[0].Version

	// The whole files array arrives double-encoded as a string.
	editArgs := []byte(`{"files":"[{\"path\":\"sample.txt\",\"version\":\"` + version + `\",\"changes\":[{\"oldText\":\"alpha\",\"newText\":\"ALPHA\"}]}]"}`)
	res := app.executeTool(context.Background(), cfg, "s-1", "edit", editArgs)
	if !res.OK {
		t.Fatalf("edit failed: %v", res.Error)
	}
	if len(res.Warnings) == 0 || !strings.Contains(res.Warnings[0], "files") {
		t.Fatalf("expected a repair warning mentioning files, got %v", res.Warnings)
	}
	got, err := os.ReadFile(filepath.Join(dir, "sample.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "ALPHA\nbeta\n" {
		t.Fatalf("unexpected file content after edit: %q", got)
	}
}

func TestExecuteToolEditRepairsNestedStringEncodedChanges(t *testing.T) {
	app := NewApp()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "sample.txt"), []byte("alpha\nbeta\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := ConfigState{Workspace: dir}

	readRes := app.executeTool(context.Background(), cfg, "s-1", "read", []byte(`{"files":[{"path":"sample.txt"}]}`))
	if !readRes.OK {
		t.Fatalf("read failed: %v", readRes.Error)
	}
	version := readRes.Data.(*BatchReadResult).Files[0].Version

	// files is a proper array, but each item's changes is double-encoded.
	editArgs := []byte(`{"files":[{"path":"sample.txt","version":"` + version + `","changes":"[{\"oldText\":\"beta\",\"newText\":\"BETA\"}]"}]}`)
	res := app.executeTool(context.Background(), cfg, "s-1", "edit", editArgs)
	if !res.OK {
		t.Fatalf("edit failed: %v", res.Error)
	}
	got, err := os.ReadFile(filepath.Join(dir, "sample.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "alpha\nBETA\n" {
		t.Fatalf("unexpected file content after edit: %q", got)
	}
}
