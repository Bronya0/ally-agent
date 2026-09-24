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
	"testing"
)

// TestLoadUpdateCacheDropsInvalidTag: the cached tag becomes a path component
// (updateVersionDir) and the update flows delete that path recursively, so a
// corrupt or hand-edited cache must never hand a non-tag to the filesystem.
func TestLoadUpdateCacheDropsInvalidTag(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	path := updateCachePath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}

	cases := []struct{ body, want string }{
		{`{"lastTag":"v1.2.3"}`, "v1.2.3"},
		{`{"lastTag":"1.2.3-beta.1"}`, "1.2.3-beta.1"},
		{`{"lastTag":"../../.."}`, ""},
		{`{"lastTag":"/etc"}`, ""},
		{`{"lastTag":"v1.2.3-../.."}`, ""},
		{`{"lastTag":"v1"},`, ""},
		{`not json at all`, ""},
	}
	for _, tc := range cases {
		if err := os.WriteFile(path, []byte(tc.body), 0o600); err != nil {
			t.Fatal(err)
		}
		if got := loadUpdateCache().LastTag; got != tc.want {
			t.Fatalf("loadUpdateCache(%s).LastTag = %q, want %q", tc.body, got, tc.want)
		}
	}
}

// TestSaveUpdateCacheRoundTrip: the cache is written through the atomic helper,
// so a valid tag must read back unchanged and no temp file may be left behind.
func TestSaveUpdateCacheRoundTrip(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	saveUpdateCache(updateReleaseCache{LastTag: "v9.9.9"})
	if got := loadUpdateCache().LastTag; got != "v9.9.9" {
		t.Fatalf("round trip lost the tag: %q", got)
	}
	entries, err := os.ReadDir(filepath.Dir(updateCachePath()))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() != "update_cache.json" {
			t.Fatalf("atomic write left an unexpected file behind: %s", entry.Name())
		}
	}
}
