package app

import (
	"net"
	"strconv"
	"strings"
)

// parseScutilProxyOutput parses the stable, line-oriented dictionary emitted by
// `scutil --proxy`. It deliberately accepts only the scalar keys Ally needs and
// the ExceptionsList array; unknown keys are ignored so newer macOS fields do
// not break detection.
func parseScutilProxyOutput(output string) ProxyStatus {
	status := ProxyStatus{Source: "macos-systemconfiguration"}
	values := make(map[string]string)
	var exceptions []string
	inExceptions := false

	for _, rawLine := range strings.Split(output, "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" {
			continue
		}
		if inExceptions {
			if line == "}" {
				inExceptions = false
				continue
			}
			key, value, ok := strings.Cut(line, ":")
			if !ok || !isDecimal(strings.TrimSpace(key)) {
				continue
			}
			value = strings.TrimSpace(value)
			if value != "" {
				exceptions = append(exceptions, unquoteScutilValue(value))
			}
			continue
		}

		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "ExceptionsList" {
			if strings.Contains(value, "<array>") {
				inExceptions = true
			}
			continue
		}
		if key != "" && value != "" {
			values[key] = unquoteScutilValue(value)
		}
	}

	httpProxy := scutilProxyURL(values, "HTTPEnable", "HTTPProxy", "HTTPPort", "http")
	httpsProxy := scutilProxyURL(values, "HTTPSEnable", "HTTPSProxy", "HTTPSPort", "http")
	socksProxy := scutilProxyURL(values, "SOCKSEnable", "SOCKSProxy", "SOCKSPort", "socks5")
	if httpProxy == "" {
		httpProxy = httpsProxy
	}
	if httpsProxy == "" {
		httpsProxy = httpProxy
	}
	if httpProxy == "" && httpsProxy == "" {
		httpProxy = socksProxy
		httpsProxy = socksProxy
	}

	status.HTTPProxy = httpProxy
	status.HTTPSProxy = httpsProxy
	status.NoProxy = strings.Join(exceptions, ",")
	status.Enabled = httpProxy != "" || httpsProxy != ""
	status.PACURL = values["ProxyAutoConfigURLString"]
	status.PACUnsupported = status.PACURL != "" ||
		scutilBool(values["ProxyAutoConfigEnable"]) ||
		scutilBool(values["ProxyAutoDiscoveryEnable"])
	return status
}

func scutilProxyURL(values map[string]string, enabledKey, hostKey, portKey, scheme string) string {
	if !scutilBool(values[enabledKey]) {
		return ""
	}
	host := strings.TrimSpace(values[hostKey])
	port, err := strconv.Atoi(strings.TrimSpace(values[portKey]))
	if host == "" || err != nil || port < 1 || port > 65535 {
		return ""
	}
	if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
		host = strings.TrimPrefix(strings.TrimSuffix(host, "]"), "[")
	}
	return normalizeProxyURL(net.JoinHostPort(host, strconv.Itoa(port)), scheme)
}

func scutilBool(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes":
		return true
	default:
		return false
	}
}

func isDecimal(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func unquoteScutilValue(value string) string {
	if len(value) >= 2 && strings.HasPrefix(value, "\"") && strings.HasSuffix(value, "\"") {
		return value[1 : len(value)-1]
	}
	return value
}
