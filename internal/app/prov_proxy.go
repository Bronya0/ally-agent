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
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/http/httpproxy"
)

const (
	proxyModeOff    = "off"
	proxyModeSystem = "system"
	proxyModeManual = "manual"
)

type ProxyStatus struct {
	Mode           string `json:"mode"`
	Enabled        bool   `json:"enabled"`
	Source         string `json:"source,omitempty"`
	HTTPProxy      string `json:"httpProxy,omitempty"`
	HTTPSProxy     string `json:"httpsProxy,omitempty"`
	NoProxy        string `json:"noProxy,omitempty"`
	PACURL         string `json:"pacUrl,omitempty"`
	PACUnsupported bool   `json:"pacUnsupported,omitempty"`
	Error          string `json:"error,omitempty"`
}

type ProxyTestRequest struct {
	Mode      string `json:"mode"`
	URL       string `json:"url,omitempty"`
	NoProxy   string `json:"noProxy,omitempty"`
	TargetURL string `json:"targetUrl,omitempty"`
}

type ProxyTestResult struct {
	OK         bool   `json:"ok"`
	TargetURL  string `json:"targetUrl"`
	Proxy      string `json:"proxy,omitempty"`
	StatusCode int    `json:"statusCode,omitempty"`
	DurationMS int64  `json:"durationMs"`
	Error      string `json:"error,omitempty"`
}

type resolvedProxy struct {
	status ProxyStatus
}

func normalizeProxyMode(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case proxyModeSystem:
		return proxyModeSystem
	case proxyModeManual:
		return proxyModeManual
	default:
		return proxyModeOff
	}
}

func (a *App) DetectSystemProxy() ProxyStatus {
	status := detectPlatformProxy()
	if !status.Enabled && status.PACURL == "" && !status.PACUnsupported {
		status = detectEnvironmentProxy()
	}
	status.Mode = proxyModeSystem
	return sanitizeProxyStatus(status)
}

func (a *App) TestProxy(req ProxyTestRequest) ProxyTestResult {
	cfg := a.effectiveConfig(ConfigState{})
	cfg.ProxyMode = normalizeProxyMode(req.Mode)
	cfg.ProxyURL = strings.TrimSpace(req.URL)
	cfg.ProxyNoProxy = strings.TrimSpace(req.NoProxy)
	target := strings.TrimSpace(req.TargetURL)
	if target == "" {
		target = strings.TrimSpace(cfg.BaseURL)
	}
	if target == "" {
		target = "https://api.openai.com"
	}
	parsed, err := url.Parse(target)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return ProxyTestResult{TargetURL: target, Error: "invalid target URL"}
	}
	client := proxyHTTPClient(cfg, true, 15*time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	httpReq, _ := http.NewRequestWithContext(ctx, http.MethodHead, parsed.String(), nil)
	started := time.Now()
	resp, err := client.Do(httpReq)
	result := ProxyTestResult{TargetURL: parsed.String(), DurationMS: time.Since(started).Milliseconds()}
	if proxyURL, proxyErr := proxyForConfig(cfg, httpReq); proxyErr == nil && proxyURL != nil {
		result.Proxy = redactProxyURL(proxyURL.String())
	}
	if err != nil {
		result.Error = err.Error()
		return result
	}
	defer resp.Body.Close()
	result.OK = true
	result.StatusCode = resp.StatusCode
	return result
}

func resolveProxy(cfg ConfigState) resolvedProxy {
	mode := normalizeProxyMode(cfg.ProxyMode)
	status := ProxyStatus{Mode: mode}
	switch mode {
	case proxyModeManual:
		proxyURL := normalizeProxyURL(cfg.ProxyURL, "http")
		if proxyURL == "" {
			status.Error = "manual proxy URL is required"
			return resolvedProxy{status: status}
		}
		status.Enabled = true
		status.Source = "manual"
		status.HTTPProxy = proxyURL
		status.HTTPSProxy = proxyURL
		status.NoProxy = cfg.ProxyNoProxy
	case proxyModeSystem:
		status = detectPlatformProxy()
		if !status.Enabled && status.PACURL == "" && !status.PACUnsupported {
			status = detectEnvironmentProxy()
		}
		status.Mode = mode
		if !status.Enabled {
			if status.PACURL != "" || status.PACUnsupported {
				status.Error = "PAC/WPAD proxy detected but automatic PAC evaluation is not supported yet"
			} else if status.Error == "" {
				status.Error = "no fixed system proxy was detected"
			}
		}
	}
	return resolvedProxy{status: status}
}

func proxyForConfig(cfg ConfigState, req *http.Request) (*url.URL, error) {
	resolved := resolveProxy(cfg)
	if resolved.status.Mode == proxyModeOff {
		return nil, nil
	}
	if resolved.status.Error != "" || !resolved.status.Enabled {
		return nil, errors.New(resolved.status.Error)
	}
	proxyCfg := &httpproxy.Config{
		HTTPProxy:  resolved.status.HTTPProxy,
		HTTPSProxy: resolved.status.HTTPSProxy,
		NoProxy:    mergeNoProxy(resolved.status.NoProxy),
	}
	return proxyCfg.ProxyFunc()(req.URL)
}

func proxyHTTPClient(cfg ConfigState, allowPrivate bool, timeout time.Duration) *http.Client {
	return &http.Client{Transport: proxyHTTPTransport(cfg, allowPrivate), Timeout: timeout}
}

// httpClientWithUserAgent returns an HTTP client that injects the configured
// User-Agent on every outbound request when the request does not already
// specify one. Used by LLM SDK clients (go-openai / openai-go / anthropic-sdk-go)
// that do not expose UA configuration directly. When no custom UA is configured
// the underlying SDK default is preserved.
func httpClientWithUserAgent(cfg ConfigState, allowPrivate bool, timeout time.Duration) *http.Client {
	base := proxyHTTPClient(cfg, allowPrivate, timeout)
	ua := strings.TrimSpace(cfg.UserAgent)
	if ua == "" {
		return base
	}
	base.Transport = &userAgentTransport{base: base.Transport, ua: ua}
	return base
}

// userAgentTransport wraps an http.RoundTripper and sets the User-Agent header
// on every request that does not already carry one.
type userAgentTransport struct {
	base http.RoundTripper
	ua   string
}

func (t *userAgentTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if _, ok := req.Header["User-Agent"]; ok {
		return t.base.RoundTrip(req)
	}
	// Clone the request so the caller's headers are not mutated. Body is
	// shared by reference, which is safe because the base Transport consumes
	// it exactly once, same as a direct call.
	clone := req.Clone(req.Context())
	clone.Header.Set("User-Agent", t.ua)
	return t.base.RoundTrip(clone)
}

// proxyTransportCache memoizes *http.Transport instances by (cfg, allowPrivate)
// so consecutive LLM requests reuse the same HTTP/2 connection pool instead
// of paying a fresh TLS handshake (100-500ms) on every request. The key is
// derived only from the proxy-related config fields, so changes to unrelated
// ConfigState fields (model, temperature, etc.) do not invalidate the cache.
//
// The cache has small, bounded cardinality: at most 3 modes × 2 allowPrivate
// = 6 entries per process, so it never grows unboundedly across a session.
// Callers must not mutate the returned Transport; it is shared. If the user
// changes proxy settings, a new Transport is constructed for the new key and
// the old one becomes idle and eventually GC'd once its connections close.
var proxyTransportCache = struct {
	sync.Mutex
	entries map[proxyTransportKey]*http.Transport
}{entries: map[proxyTransportKey]*http.Transport{}}

type proxyTransportKey struct {
	mode         string
	proxyURL     string
	proxyNoProxy string
	allowPrivate bool
}

func proxyHTTPTransport(cfg ConfigState, allowPrivate bool) *http.Transport {
	key := proxyTransportKey{
		mode:         normalizeProxyMode(cfg.ProxyMode),
		proxyURL:     strings.TrimSpace(cfg.ProxyURL),
		proxyNoProxy: strings.TrimSpace(cfg.ProxyNoProxy),
		allowPrivate: allowPrivate,
	}
	proxyTransportCache.Lock()
	if t, ok := proxyTransportCache.entries[key]; ok {
		proxyTransportCache.Unlock()
		return t
	}
	t := newProxyHTTPTransport(cfg, allowPrivate)
	proxyTransportCache.entries[key] = t
	proxyTransportCache.Unlock()
	return t
}

// invalidateProxyTransportCache closes and drops every cached *http.Transport.
// Called by SaveConfig when the user changes proxy-related config fields so
// that idle connections through the old proxy are released immediately
// instead of lingering up to IdleConnTimeout (90s). Safe to call from any
// goroutine; callers that already hold a Transport reference may continue
// using it — the Transport remains valid until its last reference is gone.
func invalidateProxyTransportCache() {
	proxyTransportCache.Lock()
	old := proxyTransportCache.entries
	proxyTransportCache.entries = map[proxyTransportKey]*http.Transport{}
	proxyTransportCache.Unlock()
	for _, t := range old {
		t.CloseIdleConnections()
	}
}

func newProxyHTTPTransport(cfg ConfigState, allowPrivate bool) *http.Transport {
	dialer := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}
	transport := &http.Transport{
		Proxy:                 func(req *http.Request) (*url.URL, error) { return proxyForConfig(cfg, req) },
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   10,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: time.Second,
	}
	// Apply the private-network guard on every dial path, including the
	// NO_PROXY direct-connection path when a proxy is configured. Without
	// this, hosts matched by NO_PROXY were dialed directly with a bare
	// Dialer, bypassing the allowPrivateNetwork=false restriction (SSRF).
	// The user-configured proxy server itself is always reachable.
	transport.DialContext = guardedDialContext(dialer, allowPrivate, proxyHosts(cfg))
	return transport
}

// proxyHosts returns the lowercase hostnames of the configured proxy servers
// so the dial guard can always allow connections to them.
func proxyHosts(cfg ConfigState) map[string]struct{} {
	hosts := map[string]struct{}{}
	resolved := resolveProxy(cfg)
	if !resolved.status.Enabled || resolved.status.Error != "" {
		return hosts
	}
	for _, raw := range []string{resolved.status.HTTPProxy, resolved.status.HTTPSProxy} {
		if u, err := url.Parse(raw); err == nil && u.Hostname() != "" {
			hosts[strings.ToLower(u.Hostname())] = struct{}{}
		}
	}
	return hosts
}

func guardedDialContext(dialer *net.Dialer, allowPrivate bool, allowHosts map[string]struct{}) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		if _, ok := allowHosts[strings.ToLower(host)]; ok {
			return dialer.DialContext(ctx, network, address)
		}
		ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, err
		}
		var lastErr error
		for _, ipAddr := range ips {
			if !allowPrivate && isPrivateHTTPAddress(ipAddr.IP) {
				lastErr = fmt.Errorf("refusing private or local network address %s for host %s", ipAddr.IP, host)
				continue
			}
			if conn, dialErr := dialer.DialContext(ctx, network, net.JoinHostPort(ipAddr.IP.String(), port)); dialErr == nil {
				return conn, nil
			} else {
				lastErr = dialErr
			}
		}
		return nil, lastErr
	}
}

func proxyEnvironment(cfg ConfigState, base []string) []string {
	env := removeEnvKeys(base, "HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY", "http_proxy", "https_proxy", "all_proxy", "no_proxy")
	resolved := resolveProxy(cfg)
	if !resolved.status.Enabled || resolved.status.Error != "" {
		return env
	}
	if resolved.status.HTTPProxy != "" {
		env = append(env, "HTTP_PROXY="+resolved.status.HTTPProxy, "http_proxy="+resolved.status.HTTPProxy)
	}
	if resolved.status.HTTPSProxy != "" {
		env = append(env, "HTTPS_PROXY="+resolved.status.HTTPSProxy, "https_proxy="+resolved.status.HTTPSProxy)
	}
	if strings.HasPrefix(strings.ToLower(resolved.status.HTTPProxy), "socks") {
		env = append(env, "ALL_PROXY="+resolved.status.HTTPProxy, "all_proxy="+resolved.status.HTTPProxy)
	}
	noProxy := mergeNoProxy(resolved.status.NoProxy)
	env = append(env, "NO_PROXY="+noProxy, "no_proxy="+noProxy)
	return env
}

func removeEnvKeys(env []string, keys ...string) []string {
	blocked := map[string]bool{}
	for _, key := range keys {
		blocked[strings.ToLower(key)] = true
	}
	out := make([]string, 0, len(env))
	for _, item := range env {
		key, _, _ := strings.Cut(item, "=")
		if !blocked[strings.ToLower(key)] {
			out = append(out, item)
		}
	}
	return out
}

func detectEnvironmentProxy() ProxyStatus {
	httpProxy := normalizeProxyURL(firstEnv("HTTP_PROXY", "http_proxy", "ALL_PROXY", "all_proxy"), "http")
	httpsProxy := normalizeProxyURL(firstEnv("HTTPS_PROXY", "https_proxy", "ALL_PROXY", "all_proxy"), "http")
	status := ProxyStatus{Source: "environment", HTTPProxy: httpProxy, HTTPSProxy: httpsProxy, NoProxy: firstEnv("NO_PROXY", "no_proxy")}
	status.Enabled = httpProxy != "" || httpsProxy != ""
	return status
}

func firstEnv(keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return value
		}
	}
	return ""
}

func normalizeProxyURL(value, defaultScheme string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if !strings.Contains(value, "://") {
		value = defaultScheme + "://" + value
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" {
		return ""
	}
	switch strings.ToLower(parsed.Scheme) {
	case "http", "https", "socks5", "socks5h":
	default:
		return ""
	}
	return parsed.String()
}

func mergeNoProxy(value string) string {
	parts := []string{"localhost", "127.0.0.1", "::1"}
	for _, part := range strings.FieldsFunc(value, func(r rune) bool { return r == ',' || r == ';' }) {
		part = strings.TrimSpace(part)
		if part != "" && part != "<local>" {
			parts = append(parts, part)
		}
	}
	return strings.Join(parts, ",")
}

func sanitizeProxyStatus(status ProxyStatus) ProxyStatus {
	status.HTTPProxy = redactProxyURL(status.HTTPProxy)
	status.HTTPSProxy = redactProxyURL(status.HTTPSProxy)
	return status
}

func redactProxyURL(value string) string {
	parsed, err := url.Parse(value)
	if err != nil || parsed.User == nil {
		return value
	}
	parsed.User = url.UserPassword(parsed.User.Username(), "***")
	return parsed.String()
}
