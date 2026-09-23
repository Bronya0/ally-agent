// SPDX-License-Identifier: GPL-3.0-only
//go:build !windows

package sshclient

import (
	"context"
	"errors"
	"net"
	"os"
	"strings"
)

func dialAgent(ctx context.Context) (net.Conn, error) {
	path := strings.TrimSpace(os.Getenv("SSH_AUTH_SOCK"))
	if path == "" {
		return nil, errors.New("SSH_AUTH_SOCK is not set")
	}
	return (&net.Dialer{}).DialContext(ctx, "unix", path)
}
