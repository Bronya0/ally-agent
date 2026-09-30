// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.

// Package sandbox confines a shell command with the host operating system's own
// primitive: Seatbelt (`sandbox-exec`) on macOS and bubblewrap (`bwrap`) on
// Linux. It is the *enforcement* layer under the permission rules (the policy
// layer in internal/app): a command that passes every safety check still cannot
// escape the box it runs in.
//
// The shape of the boundary on both platforms is the same: the command reads
// almost anything (compilers read GOROOT, git reads ~/.gitconfig) but writes
// only inside the writable roots — the workspace plus the toolchain caches
// builds actually need — optionally with a set of forbid-read roots masked and
// network egress either open or closed. Windows and the BSDs have no backend in
// this build: ResolveMode collapses the product setting to off there so the
// command tool keeps working exactly as it did before sandboxing existed.
//
// Nothing here depends on App state, ConfigState, or any *App receiver; the
// package takes plain paths and returns plain argv. That keeps it callable from
// the orchestration layer and testable from any OS (the profile builders are
// platform-neutral on purpose, so a Windows dev machine still exercises them).
//
// Two deliberate differences from the reference implementation this is modelled
// on (DeepSeek-Reasonix, also Go):
//
//   - The Linux profile does not replace /tmp with a private tmpfs. Keeping the
//     host temp directory visible avoids shadowing a workspace that itself lives
//     under /tmp (Ally creates temporary workspaces), which a `--tmpfs /tmp`
//     would hide even after a later --bind re-exposes the subtree.
//   - Confined commands are put in their own process group (see PrepareCommand)
//     so cancelling a run reaches the whole tree instead of only the runner.
package sandbox

import (
	"errors"
	"os/exec"
	"path/filepath"
	"strings"

	"ally-dev/internal/tools/pathutil"
)

// Mode is the resolved product setting for command confinement.
type Mode string

const (
	// ModeOff runs commands unwrapped. It is the default: a config that never
	// mentions the sandbox must behave exactly as it did before sandboxing
	// existed.
	ModeOff Mode = "off"
	// ModeEnforce wraps every command in the OS sandbox. When no usable backend
	// exists the command is refused rather than run unwrapped — silently
	// dropping the confinement the user asked for would be worse than failing.
	ModeEnforce Mode = "enforce"
)

// Spec describes how to confine one command. The zero value (Mode == "") does
// not enforce, so an unconfigured caller runs commands unchanged.
type Spec struct {
	// Mode is ModeEnforce to wrap the command, ModeOff (or "") to run it as-is.
	Mode Mode
	// ReadOnly removes every ordinary writable location: the write roots below,
	// the process temp directory and the toolchain caches are all left out, and
	// only the platform's throwaway device mounts remain writable. It is distinct
	// from an empty WriteRoots slice, whose meaning is "unconfigured".
	ReadOnly bool
	// WriteRoots are the directories the command may write to: the workspace
	// roots. Platforms add the temp directory and the toolchain caches on top
	// (see writableDirs) so builds and package managers keep working without
	// broad writes.
	WriteRoots []string
	// DenyWriteRoots are re-denied inside the write roots (the knowledge-base
	// sources/ subtree is readable but read-only). Paths outside every write
	// root are dropped: they are already unwritable.
	DenyWriteRoots []string
	// ForbidReadRoots are paths masked out of the command's view even though the
	// rest of the host stays readable — this is what keeps Ally's own credential
	// store (model API keys, SSH secrets) out of a model-driven shell. A path
	// that a write root contains or is contained by is dropped, because write
	// roots always win.
	ForbidReadRoots []string
	// Network allows network egress from inside the sandbox. It defaults to on
	// at the config layer: package managers and builds need it, and blocking it
	// by default makes the command tool unusable in practice.
	Network bool
}

// Enforce reports whether the spec asks for confinement.
func (s Spec) Enforce() bool { return s.Mode == ModeEnforce }

// ParseMode maps a configured string to a canonical mode. Anything that is not
// "enforce" (including empty, a legacy config without the field, or a typo) is
// off, so an unknown value can never turn confinement on by accident.
func ParseMode(configured string) Mode {
	if strings.EqualFold(strings.TrimSpace(configured), string(ModeEnforce)) {
		return ModeEnforce
	}
	return ModeOff
}

// ResolveMode is ParseMode for the running platform: a platform without a
// backend (Windows, the BSDs) collapses the setting to off instead of failing
// closed, so the command tool keeps working there. Warning reports the collapse
// to the user at settings-save time.
func ResolveMode(configured string) Mode {
	if ParseMode(configured) != ModeEnforce {
		return ModeOff
	}
	if platformHasBackend() {
		return ModeEnforce
	}
	return ModeOff
}

// Diagnostics names the confinement the spec asked for that will not apply, and
// the writable locations it asked for that do not exist. An enforcement layer
// that silently drops a protection is worse than one that refuses: callers log
// these (and may surface them) so a lost mask never looks like a working one.
// Platforms without a backend have nothing to report — the sandbox is not what
// is guarding commands there.
func (s Spec) Diagnostics() []string {
	if !platformHasBackend() {
		return nil
	}
	return buildPlan(s).problems
}

// Warning returns the user-facing notice for a configured mode this host cannot
// honour, or "" when there is nothing to say. The settings save path surfaces it
// so the toggle never silently does nothing.
func Warning(configured string) string {
	if ParseMode(configured) != ModeEnforce {
		return ""
	}
	if ResolveMode(configured) != ModeEnforce {
		// A platform without a backend folds the setting to off and keeps running
		// commands, so the refusal wording — and its "turn the switch back off"
		// advice — would describe something that never happened. Name what is
		// actually guarding the command instead.
		return "命令沙箱在当前平台不可用（" + UnavailableReason() + "），已按「关闭」处理，" + safetyFenceNotice()
	}
	if !Available() {
		return "命令沙箱已开启，但" + UnavailableReason() + "；命令会被拒绝执行（fail-closed）。" + UnavailableRemediation()
	}
	return ""
}

// UnavailableError is the error reported when a command asked for confinement
// and no backend could apply it. The caller pairs it with the
// E_SANDBOX_UNAVAILABLE code.
func UnavailableError() error {
	return errors.New("命令沙箱已开启，但" + UnavailableReason() + "。已拒绝执行该命令（fail-closed）。" + UnavailableRemediation())
}

// safetyFenceNotice names what still guards a command when the OS sandbox is
// not in play. On a platform without a backend this sentence is the entire
// answer the user gets, and turning the sandbox off is the other half of every
// remediation line, so it is written once and reused.
func safetyFenceNotice() string {
	return "命令仍受安全围栏保护（工作区路径边界、高危命令拦截、删除必须走 delete 工具）。"
}

// WriteDeniedHint is the model-facing note for a command whose write the OS
// sandbox refused. Enforcement cannot explain itself: the kernel reports a bare
// permission error, and a model that reads it as a bug in its own command
// retries the same thing until the run gives up. Callers append this to the
// command result once the failure is known to be a sandbox write denial — a
// local filesystem permission error, never an HTTP status or an
// application-level refusal.
//
// allowedRoots are the directories the model may act on: the command's write
// roots. The temp directory and the toolchain caches are writable too but are
// deliberately not listed — they are not places to send an agent.
func WriteDeniedHint(allowedRoots []string) string {
	var b strings.Builder
	b.WriteString("命令沙箱已拦截：这次命令试图写入允许范围之外的路径，那一处没有被修改。\n")
	b.WriteString("原因：命令运行在操作系统级沙箱里，只有下列目录可写：\n")
	b.WriteString(pathutil.FormatAllowedRoots(allowedRoots))
	b.WriteString("\n处理方式：把改动放进上面的目录后重试；确实需要写入其它位置时，先向用户说明用途，由用户手动在终端执行。")
	return b.String()
}

// resolveProgram turns a bare program name into the absolute path the runner
// will exec. The runner does its own PATH lookup, but resolving here keeps the
// program identity independent of whatever PATH the confined process ends up
// with (and of the runner's own lookup rules).
func resolveProgram(name string) string {
	if name == "" || filepath.IsAbs(name) {
		return name
	}
	if resolved, err := exec.LookPath(name); err == nil {
		return resolved
	}
	return name
}
