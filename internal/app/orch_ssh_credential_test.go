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
	"time"
)

// TestSSHCredentialCacheStoreLookup covers the cache lifecycle production uses:
// store → lookup, overwrite, password and key path merging for the same host,
// and TTL expiry evicting the entry.
func TestSSHCredentialCacheStoreLookup(t *testing.T) {
	cache := newSSHCredentialCache()
	if _, ok := cache.lookup("root@h1"); ok {
		t.Fatal("empty cache returned a credential")
	}
	cache.store("root@h1", "p@ss word", "")
	entry, ok := cache.lookup("root@h1")
	if !ok || entry.password != "p@ss word" {
		t.Fatalf("lookup after store: ok=%v entry=%+v", ok, entry)
	}
	// overwrite replaces
	cache.store("root@h1", "second", "")
	if entry, _ := cache.lookup("root@h1"); entry.password != "second" {
		t.Fatalf("overwrite failed: %+v", entry)
	}
	// an empty field keeps what is already stored, so a password and a key path
	// for the same host combine instead of clobbering each other
	cache.store("root@h1", "", "/keys/id_test.pem")
	entry, ok = cache.lookup("root@h1")
	if !ok || entry.password != "second" || entry.keyPath != "/keys/id_test.pem" {
		t.Fatalf("password+key merge failed: ok=%v entry=%+v", ok, entry)
	}
	// TTL expiry: lookup drops the entry instead of returning it
	cache.mu.Lock()
	cache.items["root@h1"] = sshCredentialEntry{password: "stale", expiresAt: time.Now().Add(-time.Minute)}
	cache.mu.Unlock()
	if _, ok := cache.lookup("root@h1"); ok {
		t.Fatal("expired credential must not be returned")
	}
	cache.mu.Lock()
	remaining := len(cache.items)
	cache.mu.Unlock()
	if remaining != 0 {
		t.Fatalf("expired entry should be evicted, cache still holds %d", remaining)
	}
}

// TestSSHCredentialKey locks the resolved-endpoint key contract: lowercase
// user@host plus the effective port (empty means 22), so one machine keeps one
// slot while host:2222 and host:22 stay apart.
func TestSSHCredentialKey(t *testing.T) {
	cases := []struct {
		host, port, want string
	}{
		{"root@47.1.2.3", "", "root@47.1.2.3:22"},
		{"Deploy@Example.COM", "2222", "deploy@example.com:2222"},
		{" user@host ", " 2222 ", "user@host:2222"},
	}
	for _, tc := range cases {
		if got := sshCredentialKey(tc.host, tc.port); got != tc.want {
			t.Errorf("sshCredentialKey(%q, %q) = %q; want %q", tc.host, tc.port, got, tc.want)
		}
	}
}

// TestSSHCredentialPurgeHost covers the invalidation production relies on:
// editing or deleting a cluster node must drop every port slot of that endpoint
// so a password the user removed cannot keep authenticating until the TTL
// expires, while neighbouring hosts (and prefixes of them) stay untouched.
func TestSSHCredentialPurgeHost(t *testing.T) {
	cache := newSSHCredentialCache()
	cache.store(sshCredentialKey("root@h1", ""), "pw", "")
	cache.store(sshCredentialKey("root@h1", "2222"), "pw2222", "")
	cache.store(sshCredentialKey("root@h10", ""), "neighbour", "")

	cache.purgeHost("root@h1")
	if _, ok := cache.lookup(sshCredentialKey("root@h1", "")); ok {
		t.Error("purged host still returns a credential on the default port")
	}
	if _, ok := cache.lookup(sshCredentialKey("root@h1", "2222")); ok {
		t.Error("purged host still returns a credential on the nonstandard port")
	}
	if _, ok := cache.lookup(sshCredentialKey("root@h10", "")); !ok {
		t.Error("a host whose name merely starts with the purged name must survive")
	}
}

// TestNormalizeSSHCredentialKey 已被 TestSSHCredentialKey 取代（键现在由解析后的
// 端点得出，不再从原始 target 字符串里取主机段）。

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
	foundAcceptNew := false
	for _, arg := range args {
		if arg == "BatchMode=yes" {
			foundBatch = true
		}
		if arg == "StrictHostKeyChecking=accept-new" {
			foundAcceptNew = true
		}
	}
	if !foundBatch {
		t.Errorf("expected BatchMode=yes without credential, args=%v", args)
	}
	if !foundAcceptNew {
		t.Errorf("expected StrictHostKeyChecking=accept-new without credential, args=%v", args)
	}

	// with credential: no BatchMode, askpass env present, cleanup works
	a.sshCredentials.store(sshCredentialKey("root@h", "2222"), "sekret", "")
	args, env, cleanup, err = a.prepareRemoteSSHInvocation(context.Background(), rt, "2222")
	if err != nil {
		t.Fatalf("credential prepare: %v", err)
	}
	defer cleanup()
	foundAcceptNew = false
	for _, arg := range args {
		if arg == "BatchMode=yes" {
			t.Errorf("BatchMode must be dropped when a credential is stored, args=%v", args)
		}
		if arg == "StrictHostKeyChecking=accept-new" {
			foundAcceptNew = true
		}
		if arg == "-p" {
			continue
		}
	}
	if !foundAcceptNew {
		t.Errorf("expected StrictHostKeyChecking=accept-new with credential, args=%v", args)
	}
	foundPort := false
	foundFallback := false
	for i, arg := range args {
		if arg == "-p" && i+1 < len(args) && args[i+1] == "2222" {
			foundPort = true
		}
		if strings.Contains(arg, "command -v python3") && strings.Contains(arg, "command -v python2") && strings.Contains(arg, "exec python -") {
			foundFallback = true
		}
	}
	if !foundPort {
		t.Errorf("port 2222 missing from args: %v", args)
	}
	if !foundFallback {
		t.Errorf("expected python3->python2->python fallback command in args: %v", args)
	}
	// 密码模式必须显式 BatchMode=no（抵消用户 ssh_config）并只试一次密码
	foundBatchNo := false
	foundSinglePrompt := false
	for _, arg := range args {
		if arg == "BatchMode=no" {
			foundBatchNo = true
		}
		if arg == "NumberOfPasswordPrompts=1" {
			foundSinglePrompt = true
		}
	}
	if !foundBatchNo || !foundSinglePrompt {
		t.Errorf("password invocation must force BatchMode=no and one prompt, args=%v", args)
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
	hasDisplay := false
	for _, kv := range env {
		if kv == "DISPLAY=dummy:0" {
			hasDisplay = true
		}
		if strings.HasPrefix(kv, "DISPLAY=") && kv != "DISPLAY=dummy:0" {
			t.Errorf("inherited DISPLAY must be filtered, got %q", kv)
		}
	}
	if !hasDisplay {
		t.Errorf("expected DISPLAY=dummy:0 fallback env for legacy clients, env=%v", env)
	}
	cleanup()
	if _, err := os.Stat(helperPath); err == nil {
		t.Errorf("askpass helper still exists after cleanup: %s", helperPath)
	}
}

// TestSSHCredentialKeyPath covers key-based credentials: storing a key path
// without a password leaves the password slot empty, and
// prepareRemoteSSHInvocation appends -i while keeping BatchMode. A stored
// password and key path combine: -i present, BatchMode dropped, askpass env.
func TestSSHCredentialKeyPath(t *testing.T) {
	a := &App{sshCredentials: newSSHCredentialCache()}
	rt := remoteTarget{Raw: "root@h:/tmp/app", Host: "root@h", WorkspaceRoot: "/tmp/app"}

	a.sshCredentials.store(sshCredentialKey("root@h", ""), "", "/keys/id_test.pem")
	if entry, ok := a.sshCredentials.lookup(sshCredentialKey("root@h", "")); !ok || entry.password != "" || entry.keyPath != "/keys/id_test.pem" {
		t.Fatalf("key-only entry must store the key path and no password, got ok=%v entry=%+v", ok, entry)
	}

	args, env, cleanup, err := a.prepareRemoteSSHInvocation(context.Background(), rt, "")
	if err != nil {
		t.Fatalf("key prepare: %v", err)
	}
	cleanup()
	foundKey := false
	foundBatch := false
	for i, arg := range args {
		if arg == "-i" && i+1 < len(args) && args[i+1] == "/keys/id_test.pem" {
			foundKey = true
		}
		if arg == "BatchMode=yes" {
			foundBatch = true
		}
	}
	if !foundKey || !foundBatch {
		t.Fatalf("key invocation missing -i/BatchMode: %v", args)
	}
	if env != nil {
		t.Fatalf("key-only credential must not build an askpass env, got %d entries", len(env))
	}

	// password + key combine: -i stays, BatchMode drops, askpass env is built
	a.sshCredentials.store(sshCredentialKey("root@h", ""), "sekret", "")
	args, env, cleanup, err = a.prepareRemoteSSHInvocation(context.Background(), rt, "")
	if err != nil {
		t.Fatalf("combined prepare: %v", err)
	}
	defer cleanup()
	foundKey = false
	for i, arg := range args {
		if arg == "-i" && i+1 < len(args) && args[i+1] == "/keys/id_test.pem" {
			foundKey = true
		}
		if arg == "BatchMode=yes" {
			t.Fatalf("BatchMode must be dropped with a stored password: %v", args)
		}
	}
	if !foundKey || env == nil {
		t.Fatalf("combined invocation missing -i or askpass env: args=%v env=%v", args, env)
	}
	cleanup()

}
