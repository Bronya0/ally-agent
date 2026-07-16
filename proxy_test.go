package main

import (
	"net/http"
	"strings"
	"testing"
)

func TestManualProxyIsFailClosedAndRedacted(t *testing.T) {
	cfg := ConfigState{ProxyMode: proxyModeManual, ProxyURL: "http://user:secret@127.0.0.1:7890"}
	req, _ := http.NewRequest(http.MethodGet, "https://example.com", nil)
	proxyURL, err := proxyForConfig(cfg, req)
	if err != nil {
		t.Fatal(err)
	}
	if proxyURL == nil || proxyURL.Host != "127.0.0.1:7890" {
		t.Fatalf("unexpected proxy: %v", proxyURL)
	}
	if strings.Contains(redactProxyURL(proxyURL.String()), "secret") {
		t.Fatal("proxy password must be redacted")
	}

	_, err = proxyForConfig(ConfigState{ProxyMode: proxyModeManual}, req)
	if err == nil {
		t.Fatal("missing manual proxy must fail instead of falling back to direct")
	}
}

func TestProxyOffIgnoresInheritedEnvironment(t *testing.T) {
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:9999")
	req, _ := http.NewRequest(http.MethodGet, "https://example.com", nil)
	proxyURL, err := proxyForConfig(ConfigState{ProxyMode: proxyModeOff}, req)
	if err != nil || proxyURL != nil {
		t.Fatalf("proxy off must force direct mode, proxy=%v err=%v", proxyURL, err)
	}
}

func TestProxyEnvironmentReplacesInheritedValues(t *testing.T) {
	env := proxyEnvironment(ConfigState{
		ProxyMode:    proxyModeManual,
		ProxyURL:     "socks5://127.0.0.1:7891",
		ProxyNoProxy: "example.test",
	}, []string{"PATH=test", "HTTP_PROXY=http://old", "NO_PROXY=old"})
	joined := strings.Join(env, "\n")
	for _, expected := range []string{"PATH=test", "HTTP_PROXY=socks5://127.0.0.1:7891", "NO_PROXY=localhost,127.0.0.1,::1,example.test"} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("proxy environment missing %q: %s", expected, joined)
		}
	}
	if strings.Contains(joined, "http://old") {
		t.Fatalf("old proxy should be removed: %s", joined)
	}
}

func TestEnvironmentProxyDetection(t *testing.T) {
	for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY", "http_proxy", "https_proxy", "all_proxy", "no_proxy"} {
		t.Setenv(key, "")
	}
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:8888")
	t.Setenv("NO_PROXY", "localhost")
	status := detectEnvironmentProxy()
	if !status.Enabled || status.HTTPSProxy != "http://127.0.0.1:8888" || status.NoProxy != "localhost" {
		t.Fatalf("unexpected environment status: %#v", status)
	}
}
