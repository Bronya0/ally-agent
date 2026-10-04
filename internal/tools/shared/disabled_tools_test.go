// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
package shared

import (
	"slices"
	"testing"
)

func TestFilterToolsDropsDisabledButNeverProtected(t *testing.T) {
	tools := Builtins()
	all := make([]string, 0, len(tools))
	for _, tool := range tools {
		if tool.Function != nil {
			all = append(all, tool.Function.Name)
		}
	}

	// Every builtin except the protected five can be filtered out.
	disabled := make([]string, 0, len(all))
	for _, name := range all {
		if !IsProtectedTool(name) {
			disabled = append(disabled, name)
		}
	}
	filtered := FilterTools(tools, disabled)
	if len(filtered) != 5 {
		t.Fatalf("filtering everything toggleable must leave the 5 protected tools, got %d", len(filtered))
	}
	for _, tool := range filtered {
		if !IsProtectedTool(tool.Function.Name) {
			t.Fatalf("non-protected tool %q survived", tool.Function.Name)
		}
	}

	// Naming the protected tools is refused: the core five survive any config.
	filtered = FilterTools(tools, []string{"read", "EDIT", " command "})
	for _, want := range []string{"read", "edit", "command"} {
		found := false
		for _, tool := range filtered {
			if tool.Function != nil && tool.Function.Name == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("protected tool %q must never be filtered", want)
		}
	}

	// Unknown names are inert.
	if got := FilterTools(tools, []string{"not_a_tool", ""}); len(got) != len(tools) {
		t.Fatalf("unknown disabled names must not filter anything: %d vs %d", len(got), len(tools))
	}
}

func TestSanitizeDisabledTools(t *testing.T) {
	got := SanitizeDisabledTools([]string{
		" WAIT ", "wait", // dedupe, normalize
		"read", "command", // protected: dropped
		"not_a_tool", "", // unknown/blank: dropped
		"screenshot", // kept
	})
	want := []string{"wait", "screenshot"}
	if !slices.Equal(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestBuiltinToolNamesCoverEveryDeclaration(t *testing.T) {
	names := BuiltinToolNames()
	if len(names) == 0 {
		t.Fatal("no builtin tool names")
	}
	seen := map[string]bool{}
	for _, name := range names {
		if seen[name] {
			t.Fatalf("duplicate tool name %q", name)
		}
		seen[name] = true
		if !IsBuiltinTool(name) {
			t.Fatalf("BuiltinToolNames entry %q is not recognized by IsBuiltinTool", name)
		}
	}
	// Legacy aliases (document_read and friends) normalize onto their
	// canonical name's schema; the alias spelling itself is not a canonical
	// entry and must not survive sanitization.
	if IsBuiltinTool("document_read") {
		t.Fatal("legacy alias must not be treated as a canonical builtin name")
	}
}
