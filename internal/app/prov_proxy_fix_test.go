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
	"net"
	"strings"
	"testing"
	"time"
)

func TestProxyHostsAllowsConfiguredProxyServer(t *testing.T) {
	cfg := ConfigState{ProxyMode: proxyModeManual, ProxyURL: "http://proxy.local:7890"}
	hosts := proxyHosts(cfg)
	if len(hosts) != 1 {
		t.Fatalf("expected one proxy host, got %#v", hosts)
	}
	if _, ok := hosts["proxy.local"]; !ok {
		t.Fatalf("expected proxy.local in allow set, got %#v", hosts)
	}
}

// TestGuardedDialAllowsProxyHostBypass verifies that the configured proxy
// server itself is exempt from the private-network guard: 127.0.0.1 is
// private, but as the proxy host it must be reachable (the dial fails only
// because the port is closed, not because of the guard).
func TestGuardedDialAllowsProxyHostBypass(t *testing.T) {
	dialer := &net.Dialer{Timeout: time.Second}
	guard := guardedDialContext(dialer, false, map[string]struct{}{"127.0.0.1": {}})
	_, err := guard(context.Background(), "tcp", "127.0.0.1:1")
	if err == nil {
		t.Fatal("expected dial error for closed port")
	}
	if strings.Contains(err.Error(), "refusing private") {
		t.Fatalf("proxy host must not be blocked by private guard: %v", err)
	}
}
