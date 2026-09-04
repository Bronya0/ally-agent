// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
package app

// In-memory SSH credential cache for password authentication. Passwords arrive
// through the chat (the model calls ssh_credential after the user types them)
// and live only in App memory: they are never written to config.json, session
// histories, or tool-call events. remote_* tools look credentials up by
// target's user@host and feed them to ssh via SSH_ASKPASS so password login
// works without BatchMode failing.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const sshCredentialTTL = 12 * time.Hour

type sshCredentialEntry struct {
	password  string
	setAt     time.Time
	expiresAt time.Time
}

type sshCredentialCache struct {
	mu    sync.Mutex
	items map[string]sshCredentialEntry
}

func newSSHCredentialCache() *sshCredentialCache {
	return &sshCredentialCache{items: make(map[string]sshCredentialEntry)}
}

// normalizeSSHCredentialKey reduces a remote target to user@host so that the
// same credential covers every workspace root on that host. The port stays in
// the ssh args (via -p); a password is per host+user, so it is dropped here.
func normalizeSSHCredentialKey(target string) (string, error) {
	rt, err := parseRemoteTarget(target)
	if err != nil {
		return "", err
	}
	return strings.ToLower(strings.TrimSpace(rt.Host)), nil
}

func (c *sshCredentialCache) set(key, password string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	c.items[key] = sshCredentialEntry{password: password, setAt: now, expiresAt: now.Add(sshCredentialTTL)}
}

func (c *sshCredentialCache) get(key string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.items[key]
	if !ok {
		return "", false
	}
	if time.Now().After(entry.expiresAt) {
		delete(c.items, key)
		return "", false
	}
	return entry.password, true
}

func (c *sshCredentialCache) delete(key string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, ok := c.items[key]
	if ok {
		delete(c.items, key)
	}
	return ok
}

// sshCredentialStatus is the list-form DTO: it never carries the password.
type sshCredentialStatus struct {
	Host        string `json:"host"`
	HasPassword bool   `json:"hasPassword"`
	SetAtMS     int64  `json:"setAtMs"`
	ExpiresMS   int64  `json:"expiresMs"`
}

func (c *sshCredentialCache) list() []sshCredentialStatus {
	c.mu.Lock()
	defer c.mu.Unlock()
	statuses := make([]sshCredentialStatus, 0, len(c.items))
	now := time.Now()
	for key, entry := range c.items {
		if now.After(entry.expiresAt) {
			delete(c.items, key)
			continue
		}
		statuses = append(statuses, sshCredentialStatus{
			Host:        key,
			HasPassword: true,
			SetAtMS:     entry.setAt.UnixMilli(),
			ExpiresMS:   entry.expiresAt.UnixMilli(),
		})
	}
	return statuses
}

// SSHCredentialRequest is the ssh_credential tool's flat argument shape.
type SSHCredentialRequest struct {
	Action   string `json:"action"`
	Target   string `json:"target,omitempty"`
	Password string `json:"password,omitempty"`
}

// executeSSHCredentialTool implements the ssh_credential tool: set (store a
// password in memory), clear (drop it), or list (show which hosts have one).
// The password is accepted as a tool argument because it is typed by the user
// into the chat and forwarded verbatim; responses never echo it back, and
// tool-call events plus persisted history are redacted via redactSSHCredentials.
func (a *App) executeSSHCredentialTool(req SSHCredentialRequest) (map[string]any, error) {
	action := strings.ToLower(strings.TrimSpace(req.Action))
	if action == "" {
		action = "set"
	}
	switch action {
	case "set":
		if strings.TrimSpace(req.Target) == "" {
			return nil, codedToolError("E_BAD_SSH_CREDENTIAL", errors.New("target is required for action=set"))
		}
		if strings.TrimSpace(req.Password) == "" {
			return nil, codedToolError("E_BAD_SSH_CREDENTIAL", errors.New("password is required for action=set"))
		}
		key, err := normalizeSSHCredentialKey(req.Target)
		if err != nil {
			return nil, codedToolError("E_BAD_SSH_CREDENTIAL", err)
		}
		a.sshCredentials.set(key, req.Password)
		statuses := a.sshCredentials.list()
		var status sshCredentialStatus
		for _, s := range statuses {
			if s.Host == key {
				status = s
				break
			}
		}
		return map[string]any{
			"ok":       true,
			"host":     status.Host,
			"set":      true,
			"expires":  status.ExpiresMS,
			"ttlHours": int(sshCredentialTTL.Hours()),
			"note":     "stored in memory only; remote tools with the same target will use it automatically",
		}, nil
	case "clear":
		if strings.TrimSpace(req.Target) == "" {
			return nil, codedToolError("E_BAD_SSH_CREDENTIAL", errors.New("target is required for action=clear"))
		}
		key, err := normalizeSSHCredentialKey(req.Target)
		if err != nil {
			return nil, codedToolError("E_BAD_SSH_CREDENTIAL", err)
		}
		removed := a.sshCredentials.delete(key)
		return map[string]any{"ok": true, "host": key, "cleared": removed}, nil
	case "list":
		return map[string]any{"ok": true, "credentials": a.sshCredentials.list()}, nil
	default:
		return nil, codedToolError("E_BAD_SSH_CREDENTIAL", fmt.Errorf("action must be set, clear, or list"))
	}
}

// redactSSHCredentials replaces every stored ssh password occurrence in s
// with the masked form. Apply it to every outbound tool-call event
// (tool:update / tool:result / tool:error / sub:tool:start) and to persisted
// history, so the plaintext lives only in backend memory and the chat text
// the user typed it into.
func (a *App) redactSSHCredentials(s string) string {
	if a == nil || a.sshCredentials == nil || s == "" {
		return s
	}
	a.sshCredentials.mu.Lock()
	passwords := make([]string, 0, len(a.sshCredentials.items))
	for _, entry := range a.sshCredentials.items {
		if entry.password != "" {
			passwords = append(passwords, entry.password)
		}
	}
	a.sshCredentials.mu.Unlock()
	for _, password := range passwords {
		s = strings.ReplaceAll(s, password, "***")
	}
	return s
}

// prepareRemoteSSHInvocation assembles the ssh invocation for one remote
// helper call. Without a stored credential it returns the usual BatchMode
// argv and a nil env (inherit). With a stored credential it drops BatchMode
// (its whole point is to refuse password prompts) and returns an env that
// points ssh at a short-lived askpass helper holding the password.
// The returned cleanup removes the helper temp dir and must be deferred.
func (a *App) prepareRemoteSSHInvocation(ctx context.Context, rt remoteTarget, port string) (args []string, env []string, cleanup func(), err error) {
	args = []string{"-o", "ConnectTimeout=10", "-o", "ServerAliveInterval=15", "-o", "ServerAliveCountMax=3"}
	if port != "" {
		args = append(args, "-p", port)
	}
	args = append(args, rt.Host, "python3", "-")

	key, keyErr := normalizeSSHCredentialKey(rt.Raw)
	if keyErr != nil {
		return nil, nil, func() {}, keyErr
	}
	password, ok := a.sshCredentials.get(key)
	if !ok || strings.TrimSpace(password) == "" {
		return append([]string{"-o", "BatchMode=yes"}, args...), nil, func() {}, nil
	}

	dir, mkErr := os.MkdirTemp("", "ally-askpass-")
	if mkErr != nil {
		return nil, nil, func() {}, mkErr
	}
	helper := filepath.Join(dir, "askpass.sh")
	script := "#!/bin/sh\nprintf '%s\\n' \"$ALLY_SSH_PASSWORD\"\n"
	if wErr := os.WriteFile(helper, []byte(script), 0o700); wErr != nil {
		_ = os.RemoveAll(dir)
		return nil, nil, func() {}, wErr
	}
	env = append(os.Environ()[:len(os.Environ()):len(os.Environ())],
		"ALLY_SSH_PASSWORD="+password,
		"SSH_ASKPASS="+helper,
		// force makes ssh use the askpass program even with a TTY; supported
		// by OpenSSH >= 8.4 (Windows bundled OpenSSH and Git Bash both are).
		"SSH_ASKPASS_REQUIRE=force",
	)
	cleanup = func() { _ = os.RemoveAll(dir) }
	return args, env, cleanup, nil
}
