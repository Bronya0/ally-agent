// SPDX-License-Identifier: GPL-3.0-only
package app

import (
	"net"
	"strconv"
	"strings"
)

// Windows ProxyOverride uses case-insensitive host globs and <local>, not
// NO_PROXY's domain-suffix/CIDR syntax. Match hostnames without resolving DNS.
func matchesWindowsProxyBypass(host, rules string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	for _, rule := range strings.FieldsFunc(rules, func(r rune) bool {
		return r == ';' || r == ',' || r == ' ' || r == '\t' || r == '\r' || r == '\n'
	}) {
		rule = strings.ToLower(rule)
		if rule == "<local>" {
			if host != "" && !strings.Contains(host, ".") && net.ParseIP(host) == nil {
				return true
			}
			continue
		}
		if matchProxyHostGlob(host, rule) {
			return true
		}
	}
	return false
}

// Only '*' is special in a Windows bypass pattern. Consume literals in order
// with anchored first/last fragments, without regex compilation or backtracking.
func matchProxyHostGlob(host, pattern string) bool {
	parts := strings.Split(pattern, "*")
	if len(parts) == 1 {
		return host == pattern
	}
	if !strings.HasPrefix(host, parts[0]) {
		return false
	}
	host = host[len(parts[0]):]
	for _, part := range parts[1 : len(parts)-1] {
		index := strings.Index(host, part)
		if index < 0 {
			return false
		}
		host = host[index+len(part):]
	}
	return strings.HasSuffix(host, parts[len(parts)-1])
}

// NO_PROXY consumers commonly support CIDRs but not 10.* / 192.168.*.
// Convert only complete trailing octet wildcards; other globs stay untouched.
func proxyIPv4WildcardCIDR(rule string) string {
	if !strings.HasSuffix(rule, ".*") {
		return rule
	}
	parts := strings.Split(rule, ".")
	if len(parts) > 4 {
		return rule
	}
	prefix := -1
	for i, part := range parts {
		if part == "*" {
			if prefix < 0 {
				prefix = i
			}
			parts[i] = "0"
		} else if prefix >= 0 {
			return rule
		}
	}
	if prefix <= 0 {
		return rule
	}
	for len(parts) < 4 {
		parts = append(parts, "0")
	}
	ip := strings.Join(parts, ".")
	if net.ParseIP(ip) == nil {
		return rule
	}
	return ip + "/" + strconv.Itoa(prefix*8)
}
