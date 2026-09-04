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
	"strings"
	"testing"
)

// TestSSHCredentialCacheSetGetDelete covers the in-memory cache lifecycle:
// set → get, overwrite, TTL-independent delete, and list DTO never exposing
// the password.
func TestSSHCredentialCacheSetGetDelete(t *testing.T) {
	cache := newSSHCredentialCache()
	if _, ok := cache.get("root@h1"); ok {
		t.Fatal("empty cache returned a credential")
	}
	cache.set("root@h1", "p@ss word")
	if got, ok := cache.get("root@h1"); !ok || got != "p@ss word" {
		t.Fatalf("get after set: ok=%v got=%q", ok, got)
	}
	// overwrite replaces
	cache.set("root@h1", "second")
	if got, _ := cache.get("root@h1"); got != "second" {
		t.Fatalf("overwrite failed: %q", got)
	}
	// list never carries the password and reports exactly one host
	statuses := cache.list()
	if len(statuses) != 1 || statuses[0].Host != "root@h1" || !statuses[0].HasPassword {
		t.Fatalf("unexpected list: %+v", statuses)
	}
	for _, s := range statuses {
		if strings.Contains(s.Host, "second") {
			t.Fatalf("list leaked password via host field: %+v", s)
		}
	}
	// delete removes
	if !cache.delete("root@h1") {
		t.Fatal("delete on existing key returned false")
	}
	if cache.delete("root@h1") {
		t.Fatal("delete on missing key returned true")
	}
	if _, ok := cache.get("root@h1"); ok {
		t.Fatal("credential survived delete")
	}
}

// TestNormalizeSSHCredentialKey verifies the user@host reduction across both
// target forms and that an invalid target fails.
func TestNormalizeSSHCredentialKey(t *testing.T) {
	cases := []struct {
		target, want string
	}{
		{"root@47.1.2.3:/tmp/app", "root@47.1.2.3"},
		{"ssh://deploy@Example.COM:2222/srv/app", "deploy@example.com"},
		{"USER@Host:/a", "user@host"},
	}
	for _, tc := range cases {
		got, err := normalizeSSHCredentialKey(tc.target)
		if err != nil || got != tc.want {
			t.Errorf("normalizeSSHCredentialKey(%q) = %q, %v; want %q", tc.target, got, err, tc.want)
		}
	}
	if _, err := normalizeSSHCredentialKey("bad-target"); err == nil {
		t.Error("invalid target should fail")
	}
}

// TestPrepareRemoteSSHInvocation verifies that a stored credential switches
// the ssh invocation from BatchMode (refuses passwords) to an askpass env,
// and that the cleanup removes the helper directory. Without a credential
// the invocation stays BatchMode with a nil env.
func TestPrepareRemoteSSHInvocation(t *testing.T) {
	a := &App{sshCredentials: newSSHCredentialCache()}
	rt := remoteTarget{Raw: "root@h:/tmp/app", Host: "root@h", WorkspaceRoot: "/tmp/app"}

	// no credential: BatchMode, nil env
	args, env, cleanup, err := a.prepareRemoteSSHInvocation(context.Background(), rt, "")
	if err != nil {
		t.Fatalf("no-credential prepare: %v", err)
	}
	cleanup()
	if env != nil {
		t.Errorf("expected nil env without credential, got %d entries", len(env))
	}
	foundBatch := false
	for _, arg := range args {
		if arg == "BatchMode=yes" {
			foundBatch = true
		}
	}
	if !foundBatch {
		t.Errorf("expected BatchMode=yes without credential, args=%v", args)
	}

	// with credential: no BatchMode, askpass env present, cleanup works
	a.sshCredentials.set("root@h", "sekret")
	args, env, cleanup, err = a.prepareRemoteSSHInvocation(context.Background(), rt, "2222")
	if err != nil {
		t.Fatalf("credential prepare: %v", err)
	}
	defer cleanup()
	for _, arg := range args {
		if arg == "BatchMode=yes" {
			t.Errorf("BatchMode must be dropped when a credential is stored, args=%v", args)
		}
		if arg == "-p" {
			continue
		}
	}
	foundPort := false
	for i, arg := range args {
		if arg == "-p" && i+1 < len(args) && args[i+1] == "2222" {
			foundPort = true
		}
	}
	if !foundPort {
		t.Errorf("port 2222 missing from args: %v", args)
	}
	if env == nil {
		t.Fatal("expected non-nil env with credential")
	}
	helperPath := ""
	for _, kv := range env {
		if after, ok := strings.CutPrefix(kv, "SSH_ASKPASS="); ok {
			helperPath = after
		}
		if kv == "SSH_ASKPASS_REQUIRE=force" {
			continue
		}
		if strings.HasPrefix(kv, "ALLY_SSH_PASSWORD=") && kv != "ALLY_SSH_PASSWORD=sekret" {
			t.Errorf("password env wrong: %q", kv)
		}
	}
	if helperPath == "" {
		t.Fatal("SSH_ASKPASS missing from env")
	}
	cleanup()
	if _, err := os.Stat(helperPath); err == nil {
		t.Errorf("askpass helper still exists after cleanup: %s", helperPath)
	}
}

// TestRedactSSHCredentials covers event/history redaction across all stored
// passwords and the no-op path when nothing is stored.
func TestRedactSSHCredentials(t *testing.T) {
	a := &App{sshCredentials: newSSHCredentialCache()}
	in := `{"name":"ssh_credential","args":"{\"password\":\"s3cret!\"}"}`
	if got := a.redactSSHCredentials(in); got != in {
		t.Fatalf("no-credential redaction changed input: %q", got)
	}
	a.sshCredentials.set("root@h", "s3cret!")
	if got := a.redactSSHCredentials(in); strings.Contains(got, "s3cret!") {
		t.Fatalf("password survived redaction: %q", got)
	}
	if got := a.redactSSHCredentialMessages(nil); got != nil {
		t.Fatalf("nil messages should stay nil, got %v", got)
	}
}
