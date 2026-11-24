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
	"strings"
	"testing"
)

// TestEditArgTypeErrorNamesTheFieldNotTheGoStruct: a numeric source snippet is a
// schema violation. The model has to get E_BAD_ARGS and the path it can fix
// ("changes[0].oldText"), not the Go decode error, which names an
// internal struct and carries no code at all.
func TestEditArgTypeErrorNamesTheFieldNotTheGoStruct(t *testing.T) {
	t.Parallel()
	args := []byte(`{"path":"a.go","changes":[{"oldText":2,"newText":"y"}]}`)

	err, _ := decodeToolArguments("edit", args, &FileTextEdits{})
	if code := toolErrorCode(err); code != "E_BAD_ARGS" {
		t.Fatalf("error code = %q, want E_BAD_ARGS (%v)", code, err)
	}
	message := err.Error()
	if !strings.Contains(message, "oldText") || !strings.Contains(message, "string") {
		t.Fatalf("the rejection must name the field and its declared type, got %q", message)
	}
	for _, leak := range []string{"FileTextEdits", "cannot unmarshal", "of type string"} {
		if strings.Contains(message, leak) {
			t.Fatalf("the rejection leaks the Go decode error (%q): %q", leak, message)
		}
	}
}

// TestEditMissingFileReturnsCodedNotFound: an absent path is a tool error with a
// code and a next step, not a bare os error naming an OS API.
func TestEditMissingFileReturnsCodedNotFound(t *testing.T) {
	t.Parallel()
	app := newIsolatedTestApp(t)
	res := app.executeTool(context.Background(), ConfigState{Workspace: t.TempDir()}, "s-1", "edit",
		[]byte(`{"path":"missing.txt","changes":[{"oldText":"a","newText":"b"}]}`))
	if res.OK || res.ErrorCode != "E_PATH_NOT_FOUND" {
		t.Fatalf("a missing file must fail with E_PATH_NOT_FOUND, got ok=%v code=%q err=%q", res.OK, res.ErrorCode, res.Error)
	}
	if !strings.Contains(res.Error, "missing.txt") || strings.Contains(res.Error, "GetFileAttributesEx") {
		t.Fatalf("the error must name the path and no OS API, got %q", res.Error)
	}
}

// TestToolSearchAllFailuresAreAnError: a batch where nothing resolved is a
// failed call. ToolExec reports the same class of MCP failure with status=error,
// so the search envelope cannot keep claiming success.
func TestToolSearchAllFailuresAreAnError(t *testing.T) {
	manager := NewMcpManager("", nil)
	app := &App{mcpManager: manager}
	manager.clients["demo"] = &McpClientHandle{ServerName: "demo", Status: "connected", ToolDefs: []McpDiscoveredTool{
		{ServerName: "demo", Name: "add", FunctionName: "mcp__demo__add", Schema: map[string]any{"type": "object"}},
	}}
	cfg := ConfigState{}
	ctx := withBuiltinToolAvailability(t.Context(), app.buildToolsForConfig(cfg))

	res := app.executeTool(ctx, cfg, "", "ToolSearch", []byte(`{"query":["demo.missing"]}`))
	if res.OK || res.ErrorCode != "E_MCP_NOT_FOUND" {
		t.Fatalf("an unresolved search must fail, got ok=%v code=%q err=%q", res.OK, res.ErrorCode, res.Error)
	}
	if !strings.Contains(res.Error, "demo.missing") {
		t.Fatalf("the failure must name the unresolved id, got %q", res.Error)
	}
}
