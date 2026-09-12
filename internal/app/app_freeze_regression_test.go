// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
package app

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	openai "github.com/sashabaranov/go-openai"
)

// These tests pin the per-session freeze contracts that keep the request
// prefix (system prompt + toolset + workspace map + history) byte-stable for
// the lifetime of a session. Provider prompt caches ride on that stability:
// any regression that lets config drift (skill toggles, MCP changes, custom
// prompt or workspace edits) leak into an existing session's frozen context
// silently invalidates the whole cached history. If one of these tests fails
// after a refactor, the refactor changes what ongoing conversations send.

// The system prompt must freeze at the session's first request and ignore
// later drift in every input that feeds it (skills, custom prompt, extra
// roots, git bash path). Stateless callers must keep seeing live values.
func TestSessionSystemPromptFreezeSurvivesConfigDrift(t *testing.T) {
	app := &App{}
	skillsV1 := []SkillDefinition{{Name: "alpha", Description: "v1", Source: "user"}}
	skillsV2 := []SkillDefinition{
		{Name: "alpha", Description: "v1-edited", Source: "user"},
		{Name: "beta", Description: "newly enabled", Source: "project"},
	}
	cfgV1 := ConfigState{Workspace: "/tmp/ws-a", CustomPrompt: "persona v1"}
	cfgV2 := ConfigState{
		Workspace:    "/tmp/ws-b",
		CustomPrompt: "persona v2",
		ExtraRoots:   []string{"/tmp/extra"},
		GitBashPath:  "",
	}

	frozen := app.sessionSystemPromptParts("sess", cfgV1, skillsV1)
	if len(frozen) == 0 {
		t.Fatal("expected non-empty prompt parts on first request")
	}
	frozenBytes := app.sessionSystemPrompt("sess", cfgV1, skillsV1)

	after := app.sessionSystemPromptParts("sess", cfgV2, skillsV2)
	if !reflect.DeepEqual(frozen, after) {
		t.Fatal("system prompt parts drifted within a session: skill/customPrompt/workspace changes must stay inert after the first request")
	}
	if app.sessionSystemPrompt("sess", cfgV2, skillsV2) != frozenBytes {
		t.Fatal("joined system prompt bytes drifted within a session")
	}

	// Stateless callers (no session id) must still see live values, proving
	// the freeze is session-scoped rather than a globally stale cache.
	live := app.sessionSystemPromptParts("", cfgV2, skillsV2)
	if reflect.DeepEqual(frozen, live) {
		t.Fatal("stateless prompt unexpectedly equals the frozen session prompt; live path must reflect current config")
	}
}

// The toolset must freeze at the session's first request: MCP servers added
// afterwards must not inject their tools into the session, and callers must
// receive deep clones so mutating a returned toolset cannot corrupt the
// frozen copy. New sessions must see the live set.
func TestSessionToolsetFreezeSurvivesMcpDrift(t *testing.T) {
	app := &App{} // mcpManager nil: first freeze captures the built-in set
	cfg := ConfigState{Workspace: "/tmp/ws-a"}

	frozen := app.buildToolsForSession("sess", cfg)
	if len(frozen) == 0 {
		t.Fatal("expected built-in tools on first request")
	}
	frozenNames := toolFunctionNames(frozen)

	// Simulate a later MCP server connecting with a discovered tool.
	app.mcpManager = &McpManager{
		clients: map[string]*McpClientHandle{
			"srv": {
				ServerName: "srv",
				Status:     "connected",
				ToolDefs: []McpDiscoveredTool{{
					ServerName:   "srv",
					Name:         "probe",
					FunctionName: mcpToolFunctionName("srv", "probe"),
					Description:  "injected later",
					Schema:       map[string]any{"type": "object", "properties": map[string]any{}},
				}},
			},
		},
	}

	after := app.buildToolsForSession("sess", cfg)
	got := toolFunctionNames(after)
	if !reflect.DeepEqual(frozenNames, got) {
		t.Fatalf("session toolset drifted after MCP change:\nfrozen: %v\ngot:    %v", frozenNames, got)
	}
	for _, name := range frozenNames {
		if strings.Contains(name, "probe") {
			t.Fatalf("later-connected MCP tool leaked into frozen session toolset: %v", got)
		}
	}

	// A fresh session must see the live set (freeze is per-session).
	if fresh := toolFunctionNames(app.buildToolsForSession("other", cfg)); !containsString(fresh, mcpToolFunctionName("srv", "probe")) {
		t.Fatalf("new session must see the live MCP tool, got %v", fresh)
	}

	// Deep-clone guarantee: mutating a returned toolset (sloppy caller) must
	// not corrupt what subsequent requests of the session receive.
	if len(after) > 0 && after[0].Function != nil {
		if params, ok := after[0].Function.Parameters.(map[string]any); ok {
			params["__mutated"] = true
		}
		after[0].Function.Description = "__mutated__"
	}
	again := app.buildToolsForSession("sess", cfg)
	if len(again) > 0 && again[0].Function != nil {
		if again[0].Function.Description == "__mutated__" {
			t.Fatal("mutating a returned toolset leaked into the frozen session toolset; clones must be deep")
		}
		if params, ok := again[0].Function.Parameters.(map[string]any); ok {
			if _, leaked := params["__mutated"]; leaked {
				t.Fatal("mutating returned parameters map leaked into the frozen session toolset")
			}
		}
	}
}

// The workspace map must freeze at the session's first request: a rebuilt
// (changed) live map must not alter what the session's requests embed.
func TestSessionWorkspaceMapFreezeSurvivesMapDrift(t *testing.T) {
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "a.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	app := &App{workspaceCaches: newWorkspaceCacheHolder()}
	cfg := ConfigState{Workspace: tmp}

	frozen := app.sessionWorkspaceMap("sess", cfg)
	if !strings.Contains(frozen, "a.txt") {
		t.Fatalf("frozen workspace map must list the workspace file, got: %q", frozen)
	}

	// Simulate the live map changing (rebuild produced different bytes).
	key := workspaceMapCacheKey(tmp)
	app.workspaceCaches.mapMu.Lock()
	app.workspaceCaches.mapCache[key] = workspaceMapCacheEntry{content: "__poisoned_rebuild__", generatedAt: time.Now()}
	app.workspaceCaches.mapMu.Unlock()

	if got := app.sessionWorkspaceMap("sess", cfg); got != frozen {
		t.Fatal("session workspace map drifted after a live map rebuild; frozen sessions must keep their first-request bytes")
	}
	// Stateless callers see the (changed) live map, proving the drift is
	// visible outside the freeze and only sessions are protected.
	if got := app.sessionWorkspaceMap("", cfg); got != "__poisoned_rebuild__" {
		t.Fatalf("stateless workspace map must reflect the live cache, got %q", got)
	}
}

func toolFunctionNames(tools []openai.Tool) []string {
	out := make([]string, 0, len(tools))
	for _, tool := range tools {
		if tool.Function != nil {
			out = append(out, tool.Function.Name)
		}
	}
	return out
}
