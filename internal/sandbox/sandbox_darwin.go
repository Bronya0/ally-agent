// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.

//go:build darwin

package sandbox

import (
	"context"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

// seatbeltExecutable is the macOS sandbox CLI. Apple marks it deprecated but
// ships it on every release, and it speaks the same SBPL profile language the
// system uses for its own sandboxes — which is why macOS needs no extra
// dependency here.
const seatbeltExecutable = "sandbox-exec"

// devDir is writable inside the profile because commands expect to write the
// device nodes they always could (/dev/null, /dev/stdout, /dev/tty, ...). Raw
// device writes still need root, so the wider rule costs little.
const devDir = "/dev"

// platformHasBackend reports that this build has a confinement backend.
func platformHasBackend() bool { return true }

// platformForcedMode: macOS 强制开启沙箱。设置页没有开关，用户无处把它关掉，
// 因此后端包不上时也不能 fail-closed（那等于把命令工具永久焊死），只能降级为
// 安全围栏兜底并如实告警。
func platformForcedMode() (Mode, bool) { return ModeEnforce, true }

// platformWriteExtras are the writable locations the macOS profile adds beside
// the caller's roots.
var platformWriteExtras = []string{devDir}

// sandboxExecUsability caches the probe verdict per resolved executable path, so
// repeated Available() calls stay O(1) after the first check.
var sandboxExecUsability sync.Map // resolved executable path -> bool

const (
	// sandboxExecProbeTimeout bounds the functional probe.
	sandboxExecProbeTimeout = 2 * time.Second
	// sandboxExecProbeCommand only has to start for the probe to mean anything.
	sandboxExecProbeCommand = "/usr/bin/true"
)

// seatbeltExecPath caches where sandbox-exec resolved to. The lookup walks PATH
// on every call and Available() is consulted on every single path resolution, so
// leaving it uncached makes the whole path layer pay for a directory scan. PATH
// does not change for the life of the process, and the usability verdict is
// already cached for just as long (see usableSandboxExec).
var seatbeltExecPath struct {
	once sync.Once
	path string
}

func lookPathSeatbelt() string {
	seatbeltExecPath.once.Do(func() {
		seatbeltExecPath.path, _ = exec.LookPath(seatbeltExecutable)
	})
	return seatbeltExecPath.path
}

// usableSandboxExec distinguishes an installed sandbox-exec from a usable
// Seatbelt backend. Restricted hosts ship the binary but refuse sandbox_apply,
// so every launch fails with a launch error that reads like a broken command;
// probing that operation directly is what makes the verdict honest.
func usableSandboxExec() bool {
	path := lookPathSeatbelt()
	if path == "" {
		return false
	}
	if cached, ok := sandboxExecUsability.Load(path); ok {
		return cached.(bool)
	}
	ctx, cancel := context.WithTimeout(context.Background(), sandboxExecProbeTimeout)
	defer cancel()
	runErr := exec.CommandContext(ctx, path, "-p", "(version 1)(allow default)", sandboxExecProbeCommand).Run()
	// A slow host must not poison the cache with a transient timeout; a
	// definitive failure stays cached so every command does not pay for it
	// again.
	if ctx.Err() != nil {
		return false
	}
	usable := runErr == nil
	actual, _ := sandboxExecUsability.LoadOrStore(path, usable)
	return actual.(bool)
}

// Available reports whether the OS sandbox can actually confine a process here.
func Available() bool { return usableSandboxExec() }

// UnavailableReason names why the backend cannot run, telling a missing tool
// apart from one the host refuses to apply — the second is the common case on
// managed machines and needs a different answer from the user.
func UnavailableReason() string {
	if lookPathSeatbelt() == "" {
		return "PATH 上找不到 sandbox-exec（macOS 自带该组件，正常位于 /usr/bin/sandbox-exec）"
	}
	return "sandbox-exec 存在但无法应用沙箱（通常是本机安全策略限制了 sandbox_apply）"
}

// Wrap prefixes argv with `sandbox-exec -p <profile>` when the spec enforces and
// the probe says the tool can actually apply a profile. The second return
// reports whether wrapping happened; false means the argv is unwrapped and the
// caller decides whether that is acceptable.
func Wrap(spec Spec, argv []string) ([]string, bool) {
	if len(argv) == 0 || !spec.Enforce() || !Available() {
		return argv, false
	}
	// The toolchain caches the writable surface counts on have to exist before
	// the profile names them: inside the sandbox the command cannot create them,
	// and their parents are not writable. Bounded to one attempt per process —
	// see EnsureWritableDirsOnce.
	EnsureWritableDirsOnce()
	plan := buildPlan(spec)
	profile := seatbeltProfile(plan.writable, plan.denyWrite, plan.masks, spec.Network)
	return append([]string{seatbeltExecutable, "-p", profile, resolveProgram(argv[0])}, argv[1:]...), true
}

// PrepareCommand puts the confined command in its own process group. The runner
// is the direct child now, so the process-group signal the caller sends on
// cancel (`kill(-pid)`) only reaches the runner and its descendants when the
// child is a group leader; without this the fallback would kill the runner and
// leave the commands it spawned running.
func PrepareCommand(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}
