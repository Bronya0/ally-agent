// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
package app

// In-memory SSH credential cache for authentication. Credentials come from the
// SSH cluster inventory (ssh_clusters.json next to config.json, mode 0600,
// edited in the SSH cluster manager); a remote tool targeting a registered
// alias loads the node's password/key path here for the TTL window. Nothing
// model-facing carries them: the cluster list handed to the model omits both
// fields. Key paths name a local private key file and are passed to ssh via
// -i. remote_* tools look credentials up by the target's user@host[:port]:
// passwords go through SSH_ASKPASS so password login works without BatchMode
// failing, key paths ride along as an explicit identity file.

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

const sshCredentialTTL = 12 * time.Hour

type sshCredentialEntry struct {
	password  string
	keyPath   string
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

// sshCredentialKey reduces a **resolved** ssh endpoint to the cache slot it
// uses: lowercase user@host plus the effective port (empty means 22). Callers
// pass the resolved host/port, never the raw target string — the raw target may
// name a cluster alias, and keying on it would put the credential in a slot
// nothing else can find. host:2222 and host:22 are different machines
// (container port maps, forwarded services) and keep separate slots, while
// every workspace root on one endpoint shares a slot.
func sshCredentialKey(host, port string) string {
	port = strings.TrimSpace(port)
	if port == "" {
		port = "22"
	}
	return strings.ToLower(strings.TrimSpace(host)) + ":" + port
}

// purgeHost drops every slot of a resolved endpoint host (any port). Node edits
// and deletions call it so a password the user removed cannot keep
// authenticating until the TTL expires.
func (c *sshCredentialCache) purgeHost(host string) {
	if c == nil {
		return
	}
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" {
		return
	}
	prefix := host + ":"
	c.mu.Lock()
	defer c.mu.Unlock()
	for key := range c.items {
		if strings.HasPrefix(key, prefix) {
			delete(c.items, key)
		}
	}
}

// store records the credential for key. An empty password or keyPath keeps
// the value already stored for that host, so a password set and a key set for
// the same host combine instead of clobbering each other.
func (c *sshCredentialCache) store(key, password, keyPath string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	if entry, ok := c.items[key]; ok && now.Before(entry.expiresAt) {
		if password == "" {
			password = entry.password
		}
		if keyPath == "" {
			keyPath = entry.keyPath
		}
	}
	c.items[key] = sshCredentialEntry{password: password, keyPath: keyPath, setAt: now, expiresAt: now.Add(sshCredentialTTL)}
}

// lookup returns the live credential entry for key, if any.
func (c *sshCredentialCache) lookup(key string) (sshCredentialEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.items[key]
	if !ok {
		return sshCredentialEntry{}, false
	}
	if time.Now().After(entry.expiresAt) {
		delete(c.items, key)
		return sshCredentialEntry{}, false
	}
	return entry, true
}

// prepareRemoteSSHInvocation assembles the ssh invocation for one remote
// helper call. Without a stored credential it returns the usual BatchMode
// argv and a nil env (inherit). With a stored key path it appends
// -i <keyPath> so explicit identity files work without an ssh-agent or
// ~/.ssh/config entry. With a stored password it drops BatchMode (its whole
// point is to refuse password prompts) and returns an env that points ssh at
// a short-lived askpass helper holding the password; a key path and a
// password can coexist (-i plus askpass fallback).
// The returned cleanup removes the helper temp dir and must be deferred.
func (a *App) prepareRemoteSSHInvocation(ctx context.Context, rt remoteTarget, port string) (args []string, env []string, cleanup func(), err error) {
	// StrictHostKeyChecking=accept-new automatically records unseen host keys into
	// ~/.ssh/known_hosts without prompting (preventing Host key verification failed
	// before auth in non-interactive batch sessions), while still rejecting changed
	// host keys to guard against man-in-the-middle attacks.
	args = []string{
		"-o", "ConnectTimeout=10",
		"-o", "ServerAliveInterval=15",
		"-o", "ServerAliveCountMax=3",
		"-o", "StrictHostKeyChecking=accept-new",
	}
	if port != "" {
		args = append(args, "-p", port)
	}

	entry, ok := a.sshCredentials.lookup(sshCredentialKey(rt.Host, port))
	hasPassword := ok && strings.TrimSpace(entry.password) != ""
	hasKey := ok && strings.TrimSpace(entry.keyPath) != ""
	if hasKey {
		// IdentitiesOnly 避免先逐一尝试默认密钥/agent 身份，防止某些服务器
		// 因 MaxAuthTries 提前断连；用户显式指定的密钥即为唯一身份。
		args = append(args, "-i", entry.keyPath, "-o", "IdentitiesOnly=yes")
	}
	if hasPassword {
		// 命令行 -o 优先于用户 ~/.ssh/config：显式 BatchMode=no 抵消配置文
		// 件里可能写死的 BatchMode yes（否则 ssh 拒绝一切提示、askpass 永不
		// 触发，密码登录必败且报错与密码错误无法区分）；密码只尝试一次，
		// 输错立即失败，不在启用 fail2ban 的服务器上连试三次导致封禁。
		args = append(args, "-o", "BatchMode=no", "-o", "NumberOfPasswordPrompts=1")
	}
	// 远端按 python3 → python2 → python 依次探测，经 sh -c 探测后用 exec 替换
	// 进程执行：只有 python3 的现代系统、只有 python2 的发行版（Ubuntu 20.04
	// 的 python2 包只提供 python2 这个名字，没有 python 软链）、以及只有 python
	// 的老系统（Python 2.7）都能直接跑，不写死解释器名字。
	args = append(args, rt.Host, "sh -c 'command -v python3 >/dev/null 2>&1 && exec python3 - ; command -v python2 >/dev/null 2>&1 && exec python2 - ; exec python -'")
	if !hasPassword {
		return append([]string{"-o", "BatchMode=yes"}, args...), nil, func() {}, nil
	}

	dir, mkErr := os.MkdirTemp("", "ally-askpass-")
	if mkErr != nil {
		return nil, nil, func() {}, mkErr
	}
	// Windows 原生 OpenSSH 用 CreateProcessW 直接启动 askpass 程序，跑不了
	// 带 shebang 的 shell 脚本（CreateProcessW error:193 “不是有效的 Win32
	// 应用程序”），必须给一个 .bat。.bat 内部绝不能用 %VAR% 展开或把密码
	// 明文写进文件：cmd 在解析阶段就处理 % 与 &|<>^" 等元字符，密码里含
	// 它们会被拆断甚至注入命令。改用 setlocal EnableDelayedExpansion 的
	// !VAR! 延迟展开——变量值在解析完成后才代入，任何字符都原样输出；
	// echo( 是永不误触 on/off//? 开关的安全形式。cmd 的 echo 输出 CRLF，
	// OpenSSH 读取 askpass 输出时用 strcspn(buf, "\r\n") 截断，回车不会
	// 混进密码。
	helper := filepath.Join(dir, "askpass.sh")
	script := "#!/bin/sh\nprintf '%s\\n' \"$ALLY_SSH_PASSWORD\"\n"
	if runtime.GOOS == "windows" {
		helper = filepath.Join(dir, "askpass.bat")
		script = "@echo off\r\nsetlocal EnableDelayedExpansion\r\necho(!ALLY_SSH_PASSWORD!\r\n"
	}
	if wErr := os.WriteFile(helper, []byte(script), 0o700); wErr != nil {
		_ = os.RemoveAll(dir)
		return nil, nil, func() {}, wErr
	}
	env = append(filteredEnv(),
		"ALLY_SSH_PASSWORD="+entry.password,
		"SSH_ASKPASS="+helper,
		// force makes ssh use the askpass program even with a TTY; supported
		// by OpenSSH >= 8.4 (Windows bundled OpenSSH and Git Bash both are).
		"SSH_ASKPASS_REQUIRE=force",
		// 假 DISPLAY 兜底不认 force 的老客户端（< 8.4，如 CentOS 7 自带
		// 的 7.4）：它们只在 DISPLAY 存在且无 TTY 时才用 askpass。本应用
		// 从不使用 X11 转发，该变量仅影响本机 ssh 客户端的提示策略，
		// 不会进入远端会话环境。
		"DISPLAY=dummy:0",
	)
	cleanup = func() { _ = os.RemoveAll(dir) }
	return args, env, cleanup, nil
}

// filteredEnv 返回去除了 DISPLAY / SSH_ASKPASS* / ALLY_SSH_PASSWORD 的
// 环境副本，再由调用方追加自己的值，避免环境块出现重复键（子进程对重复
// 键取哪个值是实现定义行为，真实 DISPLAY 也不应覆盖假的兜底值）。
func filteredEnv() []string {
	inherited := os.Environ()
	filtered := make([]string, 0, len(inherited)+4)
	for _, kv := range inherited {
		key, _, _ := strings.Cut(kv, "=")
		switch key {
		case "DISPLAY", "SSH_ASKPASS", "SSH_ASKPASS_REQUIRE", "ALLY_SSH_PASSWORD":
			continue
		}
		filtered = append(filtered, kv)
	}
	return filtered
}
