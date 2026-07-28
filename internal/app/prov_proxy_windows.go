//go:build windows

package app

import (
	"strings"

	"golang.org/x/sys/windows/registry"
)

func detectPlatformProxy() ProxyStatus {
	key, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Internet Settings`, registry.QUERY_VALUE)
	if err != nil {
		return ProxyStatus{Source: "windows", Error: err.Error()}
	}
	defer key.Close()
	enabled, _, _ := key.GetIntegerValue("ProxyEnable")
	server, _, _ := key.GetStringValue("ProxyServer")
	override, _, _ := key.GetStringValue("ProxyOverride")
	pacURL, _, _ := key.GetStringValue("AutoConfigURL")
	status := ProxyStatus{Source: "windows", NoProxy: override, PACURL: pacURL, PACUnsupported: pacURL != ""}
	if enabled == 0 || strings.TrimSpace(server) == "" {
		return status
	}
	for _, item := range strings.Split(server, ";") {
		name, value, found := strings.Cut(strings.TrimSpace(item), "=")
		if !found {
			status.HTTPProxy = normalizeProxyURL(item, "http")
			status.HTTPSProxy = status.HTTPProxy
			break
		}
		switch strings.ToLower(strings.TrimSpace(name)) {
		case "http":
			status.HTTPProxy = normalizeProxyURL(value, "http")
		case "https":
			status.HTTPSProxy = normalizeProxyURL(value, "http")
		case "socks", "socks5":
			proxyURL := normalizeProxyURL(value, "socks5")
			if status.HTTPProxy == "" {
				status.HTTPProxy = proxyURL
			}
			if status.HTTPSProxy == "" {
				status.HTTPSProxy = proxyURL
			}
		}
	}
	if status.HTTPSProxy == "" {
		status.HTTPSProxy = status.HTTPProxy
	}
	if status.HTTPProxy == "" {
		status.HTTPProxy = status.HTTPSProxy
	}
	status.Enabled = status.HTTPProxy != "" || status.HTTPSProxy != ""
	return status
}
