// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
package app

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCorruptConfigIsQuarantined: an unparseable config used to be left in place,
// so the app kept running on defaults and the next save wrote those defaults back
// over the file — the user's settings were gone for good. It must be moved aside
// instead, with the original bytes intact.
func TestCorruptConfigIsQuarantined(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	dir := filepath.Join(home, ".ally_agent")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.json")
	corrupt := []byte(`{"workspace": "/tmp/x",`) // truncated on purpose
	if err := os.WriteFile(path, corrupt, 0o600); err != nil {
		t.Fatal(err)
	}

	app := NewApp()
	if err := app.ensureInitialized(); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("the corrupt config must be moved out of the way, stat err = %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	quarantined := ""
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "config.json.corrupt-") {
			quarantined = entry.Name()
		}
	}
	if quarantined == "" {
		t.Fatal("the corrupt config must be kept as config.json.corrupt-<timestamp>")
	}
	got, err := os.ReadFile(filepath.Join(dir, quarantined))
	if err != nil || string(got) != string(corrupt) {
		t.Fatalf("the quarantined copy must keep the original bytes: %q (err=%v)", got, err)
	}
	if want := defaultConfigState(); app.config.Workspace != want.Workspace {
		t.Fatalf("a corrupt config must fall back to defaults, got workspace %q", app.config.Workspace)
	}
}

// TestConfigLoadKeepsHealthyFile: the quarantine path must not fire on a valid
// config — that would silently reset a working installation.
func TestConfigLoadKeepsHealthyFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	dir := filepath.Join(home, ".ally_agent")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.json")
	healthy := []byte("{\n  \"customPrompt\": \"keep me\"\n}\n")
	if err := os.WriteFile(path, healthy, 0o600); err != nil {
		t.Fatal(err)
	}

	app := NewApp()
	if err := app.ensureInitialized(); err != nil {
		t.Fatal(err)
	}
	if app.config.CustomPrompt != "keep me" {
		t.Fatalf("a healthy config must be loaded, got customPrompt %q", app.config.CustomPrompt)
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != string(healthy) {
		t.Fatalf("a healthy config must stay untouched: %q (err=%v)", got, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.Contains(entry.Name(), ".corrupt-") {
			t.Fatalf("no file may be quarantined on a healthy load: %s", entry.Name())
		}
	}
}

// TestUnreadableConfigDecision: only unparseable content may be moved aside. A
// transient read error (EACCES, EMFILE) says nothing about the content, and
// quarantining then would silently degrade a healthy config to defaults.
func TestUnreadableConfigDecision(t *testing.T) {
	dir := t.TempDir()
	healthy := filepath.Join(dir, "config.json")
	body := []byte("{\n  \"customPrompt\": \"keep me\"\n}\n")
	if err := os.WriteFile(healthy, body, 0o600); err != nil {
		t.Fatal(err)
	}
	handleUnreadableConfig(healthy, os.ErrPermission)
	if got, err := os.ReadFile(healthy); err != nil || string(got) != string(body) {
		t.Fatalf("a parsable config must stay in place: %q (err=%v)", got, err)
	}

	broken := filepath.Join(dir, "broken.json")
	if err := os.WriteFile(broken, []byte(`{"workspace": "/tmp/x",`), 0o600); err != nil {
		t.Fatal(err)
	}
	handleUnreadableConfig(broken, errors.New("unexpected end of JSON input"))
	if _, err := os.Stat(broken); !os.IsNotExist(err) {
		t.Fatalf("unparseable content must be moved aside, stat err = %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	kept := false
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "broken.json.corrupt-") {
			kept = true
		}
	}
	if !kept {
		t.Fatal("the invalid file must be kept as broken.json.corrupt-<timestamp>")
	}
}

// TestAllowPrivateNetworkSurvivesReload: the SSRF guard switch is persisted, so a
// user who turned it off must still be off after a restart. mergeConfig does not
// carry the field (a request-level overlay must never widen it), so the load path
// has to adopt the disk value explicitly — otherwise the permissive default
// silently wins on every start.
func TestAllowPrivateNetworkSurvivesReload(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	dir := filepath.Join(home, ".ally_agent")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"allowPrivateNetwork": false}`), 0o600); err != nil {
		t.Fatal(err)
	}

	app := NewApp()
	if err := app.ensureInitialized(); err != nil {
		t.Fatal(err)
	}
	if app.config.allowPrivateNetworkEnabled() {
		t.Fatal("a persisted allowPrivateNetwork=false must survive the load")
	}

	// A legacy config without the field keeps the permissive default instead of
	// being read as "off".
	if err := os.WriteFile(path, []byte(`{"workspace": "/tmp/x"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	legacy := NewApp()
	if err := legacy.ensureInitialized(); err != nil {
		t.Fatal(err)
	}
	if !legacy.config.allowPrivateNetworkEnabled() {
		t.Fatal("a legacy config without the field must keep the permissive default")
	}
}
