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

func TestSanitizeHiddenModesKeepsOnlyHideable(t *testing.T) {
	got := sanitizeHiddenModes([]string{"Skills", " kb ", "Chat", "SETTINGS", "bogus", "skills", "games", ""})
	want := map[string]bool{"skills": true, "kb": true, "games": true}
	if len(got) != len(want) {
		t.Fatalf("sanitizeHiddenModes = %v, want %v", got, want)
	}
	for _, name := range got {
		if !want[name] {
			t.Fatalf("sanitizeHiddenModes = %v, want %v", got, want)
		}
	}
	if sanitizeHiddenModes(nil) != nil {
		t.Fatal("nil input must stay nil so mergeConfig keeps the field-absent semantics")
	}
}

func TestMergeConfigHiddenModesReplacesAndCleans(t *testing.T) {
	// 非 nil overlay（含空切片）整体替换并清洗：chat/settings/未知键丢弃。
	got := mergeConfig(ConfigState{}, ConfigState{HiddenModes: []string{"kb", "Chat", "bogus"}}).HiddenModes
	if len(got) != 1 || got[0] != "kb" {
		t.Fatalf("mergeConfig.HiddenModes = %v, want [kb]", got)
	}
	got = mergeConfig(ConfigState{HiddenModes: []string{"kb"}}, ConfigState{HiddenModes: []string{}}).HiddenModes
	if len(got) != 0 {
		t.Fatalf("empty overlay must clear the list, got %v", got)
	}
	// nil overlay 表示字段没携带：保留 base。
	got = mergeConfig(ConfigState{HiddenModes: []string{"kb"}}, ConfigState{}).HiddenModes
	if len(got) != 1 || got[0] != "kb" {
		t.Fatalf("nil overlay must keep base, got %v", got)
	}
}
