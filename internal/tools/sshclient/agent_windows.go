// SPDX-License-Identifier: GPL-3.0-only
//go:build windows

package sshclient

import (
	"context"
	"net"
	"os"
	"strings"
	"time"

	"github.com/Microsoft/go-winio"
)

const defaultWindowsAgentPipe = `\\.\pipe\openssh-ssh-agent`

func dialAgent(ctx context.Context) (net.Conn, error) {
	path := strings.TrimSpace(os.Getenv("SSH_AUTH_SOCK"))
	if path != "" && strings.HasPrefix(strings.ToLower(path), `\\.\pipe\`) && path != defaultWindowsAgentPipe {
		attemptCtx, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
		conn, err := winio.DialPipeContext(attemptCtx, path)
		cancel()
		if err == nil {
			return conn, nil
		}
	}
	return winio.DialPipeContext(ctx, defaultWindowsAgentPipe)
}
