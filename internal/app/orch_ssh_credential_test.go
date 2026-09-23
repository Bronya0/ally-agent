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
	"time"
)

// TestSSHCredentialCacheStoreLookup covers the cache lifecycle production uses:
// store, overwrite, same-mode field retention, mode changes, and TTL expiry.
func TestSSHCredentialCacheStoreLookup(t *testing.T) {
	cache := newSSHCredentialCache()
	if _, ok := cache.lookup("root@h1"); ok {
		t.Fatal("empty cache returned a credential")
	}
	cache.store("root@h1", sshAuthTypePassword, "p@ss word", "")
	entry, ok := cache.lookup("root@h1")
	if !ok || entry.password != "p@ss word" || entry.authType != sshAuthTypePassword {
		t.Fatalf("lookup after store: ok=%v entry=%+v", ok, entry)
	}
	cache.store("root@h1", sshAuthTypePassword, "second", "")
	if entry, _ := cache.lookup("root@h1"); entry.password != "second" {
		t.Fatalf("overwrite failed: %+v", entry)
	}

	cache.store("root@h1", sshAuthTypeKey, "key-passphrase", "")
	cache.store("root@h1", sshAuthTypeKey, "", "/keys/id_test.pem")
	entry, ok = cache.lookup("root@h1")
	if !ok || entry.authType != sshAuthTypeKey || entry.password != "key-passphrase" || entry.keyPath != "/keys/id_test.pem" {
		t.Fatalf("same-mode key fields were not retained: ok=%v entry=%+v", ok, entry)
	}

	cache.store("root@h1", sshAuthTypePassword, "account-password", "")
	entry, ok = cache.lookup("root@h1")
	if !ok || entry.authType != sshAuthTypePassword || entry.password != "account-password" || entry.keyPath != "" {
		t.Fatalf("mode change retained an incompatible credential: ok=%v entry=%+v", ok, entry)
	}

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
	cache.store(sshCredentialKey("root@h1", ""), sshAuthTypePassword, "pw", "")
	cache.store(sshCredentialKey("root@h1", "2222"), sshAuthTypePassword, "pw2222", "")
	cache.store(sshCredentialKey("root@h10", ""), sshAuthTypePassword, "neighbour", "")

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
