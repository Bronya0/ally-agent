// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.

//go:build darwin

package app

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"time"
)

const macOSProxyDetectionTimeout = 2 * time.Second

// detectPlatformProxy reads the proxy configuration that macOS currently uses.
// scutil is part of macOS and reports the active SystemConfiguration proxy
// dictionary, so this works for GUI-launched apps without relying on shell
// environment variables.
func detectPlatformProxy() ProxyStatus {
	const source = "macos-systemconfiguration"
	ctx, cancel := context.WithTimeout(context.Background(), macOSProxyDetectionTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "/usr/sbin/scutil", "--proxy")
	output, err := cmd.Output()
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return ProxyStatus{Source: source, Error: "scutil --proxy timed out"}
		}
		return ProxyStatus{Source: source, Error: fmt.Sprintf("scutil --proxy failed: %v", err)}
	}

	status := parseScutilProxyOutput(string(output))
	status.Source = source
	return status
}
