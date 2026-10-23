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
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
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

func TestProxyTransportCacheBoundedAndReusable(t *testing.T) {
	invalidateProxyTransportCache()
	t.Cleanup(invalidateProxyTransportCache)
	cfg := ConfigState{ProxyMode: proxyModeManual, ProxyURL: "http://127.0.0.1:7890"}
	first := proxyHTTPTransport(cfg, false)
	if proxyHTTPTransport(cfg, false) != first {
		t.Fatal("transport was not reused")
	}
	for i := 0; i < maxProxyTransports+3; i++ {
		cfg.ProxyNoProxy = fmt.Sprintf("host%d.test", i)
		proxyHTTPTransport(cfg, false)
	}
	proxyTransportCache.Lock()
	count := len(proxyTransportCache.entries)
	proxyTransportCache.Unlock()
	if count > maxProxyTransports {
		t.Fatalf("unbounded cache: %d", count)
	}
}

func TestResolvedProxyTransportKeepsSnapshot(t *testing.T) {
	resolved := resolvedProxy{status: ProxyStatus{Mode: proxyModeManual, Enabled: true, HTTPProxy: "http://127.0.0.1:7890", HTTPSProxy: "http://127.0.0.1:7890"}}
	transport := newResolvedProxyHTTPTransport(resolved, false)
	defer transport.CloseIdleConnections()
	resolved.status.HTTPSProxy = "http://127.0.0.2:7891"
	req, _ := http.NewRequest("GET", "https://example.com", nil)
	proxy, err := transport.Proxy(req)
	if err != nil || proxy.Host != "127.0.0.1:7890" {
		t.Fatalf("snapshot changed: %v %v", proxy, err)
	}
	if transport.ResponseHeaderTimeout <= 0 {
		t.Fatal("missing response header timeout")
	}
}

func TestGuardedDialRejectsPrivateAndHonorsCancellation(t *testing.T) {
	guard := guardedDialContext(&net.Dialer{Timeout: time.Second}, false, nil)
	if conn, err := guard(context.Background(), "tcp", "127.0.0.1:80"); err == nil || !strings.Contains(err.Error(), "refusing private") {
		if conn != nil {
			conn.Close()
		}
		t.Fatalf("private address was not rejected: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	guard = guardedDialContext(&net.Dialer{Timeout: time.Second}, true, nil)
	if conn, err := guard(ctx, "tcp", "127.0.0.1:80"); err == nil {
		conn.Close()
		t.Fatal("canceled dial succeeded")
	}
}

func TestLongLivedClientRefreshesProxyTransport(t *testing.T) {
	// Proxy servers answer directly, so this regression test never uses public DNS.
	proxy := func(name string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, name)
		}))
	}
	first, second := proxy("first"), proxy("second")
	defer first.Close()
	defer second.Close()
	transport := func(address string) *http.Transport {
		return newProxyHTTPTransport(ConfigState{ProxyMode: proxyModeManual, ProxyURL: address}, true)
	}
	old, next := transport(first.URL), transport(second.URL)
	defer old.CloseIdleConnections()
	defer next.CloseIdleConnections()
	current := old
	client := &http.Client{Transport: &refreshingProxyTransport{current: func() *http.Transport { return current }}, Timeout: time.Second}
	for _, want := range []string{"first", "second"} {
		resp, err := client.Get("http://destination.invalid/resource")
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil || string(body) != want {
			t.Fatalf("proxy body=%q err=%v, want %q", body, err, want)
		}
		current = next
	}
}
