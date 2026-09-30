// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.

//go:build linux

package sandbox

import (
	"context"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

// bwrapExecutable is bubblewrap, the unprivileged sandbox Flatpak and most
// Linux coding agents build on. It is not installed by default on most
// distributions, so availability is a real question — see usableBwrap.
const bwrapExecutable = "bwrap"

// platformHasBackend reports that this build has a confinement backend.
func platformHasBackend() bool { return true }

// platformWriteExtras are the writable locations the Linux profile adds beside
// the caller's roots. Linux adds none: the temp directory and the caches come
// from writableDirs.
var platformWriteExtras []string

// bwrapProbeTimeout bounds the functional probe, which runs at most once per
// resolved executable path.
const bwrapProbeTimeout = 2 * time.Second

// bwrapUsability caches the functional probe per resolved executable path.
var bwrapUsability sync.Map

// usableBwrap distinguishes an installed binary from a usable sandbox backend.
// Hardened hosts (and some CI runners) expose bwrap on PATH but deny the user
// namespace it needs; treating that as available would make an enforcing spec
// fail later with a misleading launch error and overstate the isolation. The
// verdict is cached for the process lifetime, so installing bubblewrap while
// Ally is running takes effect on restart — the remediation text says so.
func usableBwrap() (string, bool) {
	path, err := exec.LookPath(bwrapExecutable)
	if err != nil {
		return "", false
	}
	if cached, ok := bwrapUsability.Load(path); ok {
		return path, cached.(bool)
	}
	ctx, cancel := context.WithTimeout(context.Background(), bwrapProbeTimeout)
	defer cancel()
	// The probe builds the same mount profile the real profile starts from and
	// runs `true` inside it: only a sandbox that actually starts counts.
	probe := exec.CommandContext(ctx, path, "--ro-bind", "/", "/", "--dev", "/dev", "--proc", "/proc", "--", "true")
	usable := probe.Run() == nil
	verdict, _ := bwrapUsability.LoadOrStore(path, usable)
	return path, verdict.(bool)
}

// Available reports whether bubblewrap can actually confine a process here.
func Available() bool {
	_, ok := usableBwrap()
	return ok
}

// UnavailableReason names why the backend cannot run.
func UnavailableReason() string {
	if _, err := exec.LookPath(bwrapExecutable); err != nil {
		return "PATH 上找不到 bubblewrap（bwrap）"
	}
	return "bubblewrap（bwrap）无法在此主机上创建它需要的命名空间"
}

// UnavailableRemediation names the two ways out.
func UnavailableRemediation() string {
	return "安装 bubblewrap 后重启 Ally（Debian/Ubuntu: sudo apt install bubblewrap；Fedora: sudo dnf install bubblewrap；Arch: sudo pacman -S bubblewrap），或在设置里把命令沙箱改回「关闭」，" + safetyFenceNotice()
}

// Wrap prefixes argv with the bubblewrap invocation when the spec enforces and
// the backend is usable. The second return reports whether wrapping happened.
func Wrap(spec Spec, argv []string) ([]string, bool) {
	if len(argv) == 0 || !spec.Enforce() {
		return argv, false
	}
	bwrap, ok := usableBwrap()
	if !ok {
		return argv, false
	}
	if !spec.ReadOnly {
		// The toolchain caches the writable surface counts on have to exist
		// before the profile binds them: inside the sandbox the command cannot
		// create them, and their parents are not writable.
		_ = EnsureWritableDirs()
	}
	plan := buildPlan(spec)
	args := bwrapArgs(plan.writable, plan.denyWrite, plan.masks, spec.Network)
	// `--` ends bubblewrap's own options, so a command can never be mistaken for
	// one of them.
	args = append(args, "--", resolveProgram(argv[0]))
	args = append(args, argv[1:]...)
	return append([]string{bwrap}, args...), true
}

// PrepareCommand puts the confined command in its own process group. The runner
// is the direct child now, so the process-group signal the caller sends on
// cancel (`kill(-pid)`) only reaches the runner and its descendants when the
// child is a group leader; without this the fallback would kill the runner and
// leave the commands it spawned running.
func PrepareCommand(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}
