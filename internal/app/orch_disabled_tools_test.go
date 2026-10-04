// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
package app

import (
	"testing"
)

func TestBuildToolsForConfigHonorsDisabledTools(t *testing.T) {
	app := NewApp()
	cfg := ConfigState{Workspace: t.TempDir(), DisabledTools: []string{"wait", "calculate", "read"}}
	tools := app.buildToolsForConfig(cfg)
	names := map[string]bool{}
	for _, tool := range tools {
		if tool.Function != nil {
			names[tool.Function.Name] = true
		}
	}
	if names["wait"] || names["calculate"] {
		t.Fatal("disabled tools must not be injected")
	}
	for _, want := range []string{"read", "edit", "command"} {
		if !names[want] {
			t.Fatalf("protected tool %q must survive", want)
		}
	}
	if !names["list_files"] {
		t.Fatal("non-disabled tools must stay")
	}
}

func TestBuildToolsForSessionFreezesDisabledTools(t *testing.T) {
	app := NewApp()
	off := ConfigState{Workspace: t.TempDir(), DisabledTools: []string{"wait"}}
	on := ConfigState{Workspace: t.TempDir()}

	// A session first run with the disabled set freezes that set...
	frozen := app.buildToolsForSession("sess-tools-1", off)
	found := map[string]bool{}
	for _, tool := range frozen {
		if tool.Function != nil {
			found[tool.Function.Name] = true
		}
	}
	if found["wait"] {
		t.Fatal("frozen set must respect the disabled list")
	}
	// ...and later config changes do not retroactively alter it: the frozen
	// set keeps `wait` out even when the config re-enables it, and a session
	// frozen with the full set keeps every tool even after disabling.
	full := app.buildToolsForSession("sess-tools-2", on)
	app2WaitSeen := false
	for _, tool := range full {
		if tool.Function != nil && tool.Function.Name == "wait" {
			app2WaitSeen = true
		}
	}
	if !app2WaitSeen {
		t.Fatal("session frozen with the full set must keep every tool")
	}
	_ = app.buildToolsForSession("sess-tools-1", on)
	still := app.buildToolsForSession("sess-tools-1", on)
	for _, tool := range still {
		if tool.Function != nil && tool.Function.Name == "wait" {
			t.Fatal("re-enabled config must not leak into an already-frozen session")
		}
	}
}

func TestMergeConfigSanitizesDisabledTools(t *testing.T) {
	// mergeConfig 是请求级 overlay 的入口：受保护工具与未知名字在这里就被
	// 拒绝，不落盘、不触真实路径（单测隔离铁律）。
	got := mergeConfig(ConfigState{}, ConfigState{DisabledTools: []string{"read", "WAIT", "bogus_tool", "wait"}}).DisabledTools
	set := map[string]bool{}
	for _, name := range got {
		set[name] = true
	}
	if len(got) != 1 || !set["wait"] || set["read"] || set["bogus_tool"] {
		t.Fatalf("mergeConfig.DisabledTools = %v, want only [wait]", got)
	}
}
