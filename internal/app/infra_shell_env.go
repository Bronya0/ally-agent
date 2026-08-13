// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
package app

// Section: shared command environment
// Enriches command subprocesses with PATH entries from the user's POSIX
// login shell. The probe runs once per process while keeping Ally's actual
// command shell and safety checks unchanged.

import (
	"context"
	"io"
	"os"
	"os/exec"
	"os/user"
	goruntime "runtime"
	"strings"
	"sync"
	"time"
)

const (
	loginShellEnvTimeout    = 5 * time.Second
	loginShellLookupTimeout = 1 * time.Second
	maxLoginShellEnvOutput  = 1 * 1024 * 1024
)

type loginShellPathProbeDeps struct {
	platform     string
	env          []string
	userShell    func() string
	execFileText func(file string, args []string, timeout time.Duration) (string, error)
}

// probeLoginShellPath runs the user's login shell once and extracts the last
// PATH= line from its environment output. Profile noise is allowed because
// users' shell startup files sometimes print banners or diagnostics.
func probeLoginShellPath(deps loginShellPathProbeDeps) string {
	if deps.platform == "windows" {
		return ""
	}

	shell, _ := environmentValue(deps.env, "SHELL")
	shell = strings.TrimSpace(shell)
	if shell == "" && deps.userShell != nil {
		shell = strings.TrimSpace(deps.userShell())
	}
	if shell == "" || deps.execFileText == nil {
		return ""
	}

	output, err := deps.execFileText(shell, []string{"-l", "-c", "/usr/bin/env"}, loginShellEnvTimeout)
	if err != nil {
		return ""
	}

	var loginPath string
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSuffix(line, "\r")
		if strings.HasPrefix(line, "PATH=") {
			loginPath = strings.TrimSpace(strings.TrimPrefix(line, "PATH="))
		}
	}
	return loginPath
}

// mergeLoginShellPath appends missing absolute login-shell entries while
// preserving the current PATH byte-for-byte. A nil currentPath represents an
// unset PATH; a non-nil pointer to an empty string represents a real empty
// PATH, whose cwd lookup component must be preserved.
func mergeLoginShellPath(currentPath *string, loginShellPath string) string {
	current := ""
	if currentPath != nil {
		current = *currentPath
	}

	seen := make(map[string]struct{})
	for _, entry := range strings.Split(current, ":") {
		if entry != "" {
			seen[entry] = struct{}{}
		}
	}

	additions := make([]string, 0)
	for _, entry := range strings.Split(loginShellPath, ":") {
		// Relative and empty entries are cwd-dependent. Importing one from a
		// login shell would widen command lookup for arbitrary workspace cwd's.
		if !strings.HasPrefix(entry, "/") {
			continue
		}
		if _, ok := seen[entry]; ok {
			continue
		}
		seen[entry] = struct{}{}
		additions = append(additions, entry)
	}
	if len(additions) == 0 {
		return current
	}
	if currentPath == nil {
		return strings.Join(additions, ":")
	}
	return current + ":" + strings.Join(additions, ":")
}

// enrichLoginShellPathEnvironment returns a new environment with only PATH
// enriched. It never imports GOPATH, NVM_DIR, aliases, functions, or other
// profile state into the command process.
func enrichLoginShellPathEnvironment(
	base []string,
	platform string,
	userShell func() string,
	execFileText func(file string, args []string, timeout time.Duration) (string, error),
) []string {
	loginPath := probeLoginShellPath(loginShellPathProbeDeps{
		platform:     platform,
		env:          base,
		userShell:    userShell,
		execFileText: execFileText,
	})
	if loginPath == "" {
		return cloneEnvironment(base)
	}

	current, hasCurrent := environmentValue(base, "PATH")
	var currentPtr *string
	if hasCurrent {
		currentPtr = &current
	}
	merged := mergeLoginShellPath(currentPtr, loginPath)
	if (!hasCurrent && merged == "") || (hasCurrent && merged == current) {
		return cloneEnvironment(base)
	}
	return replaceEnvironmentValue(base, "PATH", merged)
}

var commandEnvironmentState struct {
	sync.Once
	env []string
}

// commandEnvironment returns the stable process environment used by local
// command and background-service subprocesses, with proxy settings applied
// for the current request. The login-shell probe is memoized process-wide so
// every command does not rerun user startup files.
func commandEnvironment(cfg ConfigState) []string {
	commandEnvironmentState.Do(func() {
		base := os.Environ()
		commandEnvironmentState.env = enrichLoginShellPathEnvironment(
			base,
			goruntime.GOOS,
			accountLoginShell,
			runLoginShellText,
		)
	})
	return proxyEnvironment(cfg, commandEnvironmentState.env)
}

// warmCommandEnvironment starts the same one-time probe used by commands.
// Startup calls this asynchronously so a slow user profile cannot block the
// window, while the first command still waits for the shared sync.Once if
// probing is not complete yet.
func warmCommandEnvironment() {
	commandEnvironmentState.Do(func() {
		base := os.Environ()
		commandEnvironmentState.env = enrichLoginShellPathEnvironment(
			base,
			goruntime.GOOS,
			accountLoginShell,
			runLoginShellText,
		)
	})
}

func cloneEnvironment(env []string) []string {
	return append([]string(nil), env...)
}

func environmentValue(env []string, key string) (string, bool) {
	for i := len(env) - 1; i >= 0; i-- {
		name, value, ok := strings.Cut(env[i], "=")
		if ok && name == key {
			return value, true
		}
	}
	return "", false
}

func replaceEnvironmentValue(env []string, key, value string) []string {
	out := make([]string, 0, len(env)+1)
	replaced := false
	for _, item := range env {
		name, _, ok := strings.Cut(item, "=")
		if ok && name == key {
			if !replaced {
				out = append(out, key+"="+value)
				replaced = true
			}
			continue
		}
		out = append(out, item)
	}
	if !replaced {
		out = append(out, key+"="+value)
	}
	return out
}

func runLoginShellText(file string, args []string, timeout time.Duration) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, file, args...)
	buf := &limitedBuffer{limit: maxLoginShellEnvOutput}
	cmd.Stdout = buf
	cmd.Stderr = io.Discard
	err := cmd.Run()
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if err != nil {
		return "", err
	}
	return buf.String(), nil
}

// accountLoginShell falls back to the OS account database when GUI/launchd
// does not provide SHELL. macOS normally uses zsh; dscl gives the
// actual account setting when it differs. Linux reads the local passwd entry
// and falls back to bash when no entry is available.
func accountLoginShell() string {
	switch goruntime.GOOS {
	case "darwin":
		if shell := macOSAccountLoginShell(); shell != "" {
			return shell
		}
		return "/bin/zsh"
	case "linux":
		if shell := linuxAccountLoginShell(); shell != "" {
			return shell
		}
		return "/bin/bash"
	default:
		return ""
	}
}

func macOSAccountLoginShell() string {
	current, err := user.Current()
	if err != nil || strings.TrimSpace(current.Username) == "" {
		return ""
	}
	output, err := runLoginShellText(
		"/usr/bin/dscl",
		[]string{".", "-read", "/Users/" + current.Username, "UserShell"},
		loginShellLookupTimeout,
	)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == "UserShell:" {
			return strings.TrimSpace(fields[1])
		}
	}
	return ""
}

func linuxAccountLoginShell() string {
	current, err := user.Current()
	if err != nil {
		return ""
	}
	data, err := os.ReadFile("/etc/passwd")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Split(line, ":")
		if len(fields) < 7 {
			continue
		}
		if fields[0] == current.Username || fields[2] == current.Uid {
			return strings.TrimSpace(fields[6])
		}
	}
	return ""
}
