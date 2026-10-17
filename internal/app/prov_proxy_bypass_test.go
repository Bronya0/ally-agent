// SPDX-License-Identifier: GPL-3.0-only
package app

import (
	"net/url"
	"strings"
	"testing"
)

func TestWindowsProxyBypassRouting(t *testing.T) {
	for _, tc := range []struct {
		name, rules, host string
		bypass            bool
	}{
		{"private class A", "10.*", "10.1.2.3", true},
		{"private class C", "192.168.*", "192.168.1.2", true},
		{"outside subnet", "192.168.*", "192.169.1.2", false},
		{"separators and case", "ignored; 10.*\t*.EXAMPLE.TEST", "api.example.test", true},
		{"prefix wildcard", "ms*", "msserver.example.test", true},
		{"middle wildcard", "*int*", "printer.internal.test", true},
		{"anchored wildcard", "ms*test", "msserver.test.other", false},
		{"no overlapping literals", "a*a", "a", false},
		{"local name", "<local>", "intranet", true},
		{"local excludes FQDN", "<local>", "intranet.example.test", false},
		{"local excludes IPv6", "<local>", "[2001:db8::1]", false},
		{"exact Windows host", "example.test", "example.test", true},
		{"Windows host is not suffix", "example.test", "api.example.test", false},
		{"external API", "10.*;192.168.*;<local>", "api.openai.com", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resolved := resolveDetectedSystemProxy(ProxyStatus{
				Source: "windows", Enabled: true,
				HTTPProxy: "http://proxy.example.test:7890", HTTPSProxy: "http://proxy.example.test:7890",
				NoProxy: tc.rules,
			}, "")
			target, err := url.Parse("https://" + tc.host)
			if err != nil {
				t.Fatal(err)
			}
			proxy, err := proxyForResolved(resolved, target)
			if err != nil {
				t.Fatal(err)
			}
			if (proxy == nil) != tc.bypass {
				t.Fatalf("rules=%q host=%q proxy=%v, want bypass=%v", tc.rules, tc.host, proxy, tc.bypass)
			}
		})
	}
}

func TestSystemProxyExtraBypass(t *testing.T) {
	for _, source := range []string{"windows", "environment", "macos-systemconfiguration"} {
		t.Run(source, func(t *testing.T) {
			resolved := resolveDetectedSystemProxy(ProxyStatus{
				Source: source, Enabled: true, HTTPSProxy: "http://proxy.example.test:7890", NoProxy: "system.example.test",
			}, "custom.example.test,10.0.0.0/8")
			for _, host := range []string{"system.example.test", "custom.example.test", "sub.custom.example.test", "10.1.2.3"} {
				target, _ := url.Parse("https://" + host)
				if proxy, err := proxyForResolved(resolved, target); err != nil || proxy != nil {
					t.Fatalf("system/custom bypass lost for %s: proxy=%v err=%v", host, proxy, err)
				}
			}
			target, _ := url.Parse("https://api.openai.com")
			if proxy, err := proxyForResolved(resolved, target); err != nil || proxy == nil {
				t.Fatalf("external request lost proxy: proxy=%v err=%v", proxy, err)
			}
		})
	}
}

func TestSystemProxyMissingDoesNotBypassErrors(t *testing.T) {
	resolved := resolveDetectedSystemProxy(ProxyStatus{Source: "windows", NoProxy: "*"}, "*")
	target, _ := url.Parse("https://api.openai.com")
	if _, err := proxyForResolved(resolved, target); err == nil {
		t.Fatal("missing system proxy must still report an error")
	}
}

func TestProxyNoProxyExportIPv4Wildcards(t *testing.T) {
	value := mergeNoProxy("10.*;192.168.*;172.16.*.*;<local>;*.example.test;custom.test")
	for _, rule := range []string{"10.0.0.0/8", "192.168.0.0/16", "172.16.0.0/16", "*.example.test", "custom.test"} {
		if !strings.Contains(","+value+",", ","+rule+",") {
			t.Fatalf("export missing %q: %q", rule, value)
		}
	}
	if strings.Contains(value, "<local>") {
		t.Fatalf("export contains unsupported <local> token: %q", value)
	}
	for _, invalid := range []string{"999.*", "10.*.1.*", "*.168.*", "1.2.3.4.*"} {
		if got := proxyIPv4WildcardCIDR(invalid); got != invalid {
			t.Fatalf("invalid wildcard %q converted to %q", invalid, got)
		}
	}
}
