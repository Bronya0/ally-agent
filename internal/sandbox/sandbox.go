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
// builds actually need — with Ally's own credential store masked out of its
// view and the network left open for package managers. There is no user setting:
// ResolvedMode reports the platform's own policy (macOS mandates the sandbox,
// every other platform never applies it), and the host without a backend (Windows,
// the BSDs) keeps the command tool working exactly as it did before sandboxing
// existed.
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
	"os/exec"
	"path/filepath"
	"strings"

	"ally-dev/internal/tools/pathutil"
)

// Mode is the resolved product setting for command confinement.
type Mode string

const (
	// ModeOff runs commands unwrapped: the resolved mode on every platform that
	// does not mandate confinement, so the command tool there behaves exactly as
	// it did before sandboxing existed.
	ModeOff Mode = "off"
	// ModeEnforce wraps every command in the OS sandbox. Only a platform that
	// mandates confinement resolves to it, and such a platform degrades to the
	// safety fence (loudly, see Warning) when the backend cannot wrap — refusing
	// every command would brick the tool with no way out.
	ModeEnforce Mode = "enforce"
)

// Spec describes how to confine one command. The zero value (Mode == "") does
// not enforce, so an unconfigured caller runs commands unchanged.
type Spec struct {
	// Mode is ModeEnforce to wrap the command, ModeOff (or "") to run it as-is.
	Mode Mode
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
	// Network allows network egress from inside the sandbox. Callers pass true
	// for commands — package managers and builds need it, and blocking it makes
	// the command tool unusable in practice — and false for the file-mutation
	// helpers, which run a coreutil and nothing else.
	Network bool
	// OnlyWriteRoots drops the locations writableDirs adds beside the caller's
	// own roots: the process temp directory, the per-user toolchain caches and
	// the platform device extras. Commands need them (builds and package
	// managers break without them, and the boundary relies on the kernel being
	// exact rather than lexical). A file mutation runs one coreutil against one
	// path and must not inherit them: those places are not the workspace, so a
	// symlink inside the workspace pointing at any of them would otherwise be
	// an escape the kernel itself allows.
	OnlyWriteRoots bool
}

// Enforce reports whether the spec asks for confinement.
func (s Spec) Enforce() bool { return s.Mode == ModeEnforce }

// AllowsWrite reports whether a symlink-resolved absolute path would be
// writable under spec. It answers from the same plan the profile is built from,
// so the verdict can not drift from what the kernel actually allows.
//
// A spec that is not enforced — or a host whose backend can not confine — has
// no kernel answering for it, so the verdict is false and the caller must keep
// its own judgement (internal/app calls this only while the kernel owns the
// boundary, see kernelOwnsBoundary).
func AllowsWrite(spec Spec, resolved string) bool {
	if !spec.Enforce() || !Available() {
		return false
	}
	plan := buildPlan(spec)
	target := filepath.Clean(resolved)
	if !insideAny(target, plan.writable) {
		return false
	}
	// 可写根之内还可以被重新禁写（知识库 sources/ 就是可读不可写的）：
	// 那一层同样是内核在拒，判定不能漏。
	for _, denied := range plan.denyWrite {
		if isWithin(target, denied.path, true) {
			return false
		}
	}
	return true
}

// ResolvedMode is the confinement policy for the running platform: a platform
// that mandates the sandbox enforces it, every other platform runs commands
// unwrapped. The mode is a property of the host rather than a user setting, so
// no config value can turn confinement on or off by accident.
func ResolvedMode() Mode {
	if forced, ok := platformForcedMode(); ok {
		return forced
	}
	return ModeOff
}

// ModeForced reports whether confinement is mandated on this platform: there is
// no switch for it. A forced platform must degrade to the safety fence when the
// backend cannot wrap a command — refusing everything fail-closed would brick
// the tool with no way out (see wrapSandboxedCommand).
func ModeForced() bool {
	_, forced := platformForcedMode()
	return forced
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

// Warning returns the user-facing notice for this host, or "" when there is
// nothing to say: the mandated sandbox cannot be applied here, so commands run
// under the safety fence instead. The settings page shows the sentence verbatim,
// which is why it names what still guards the command rather than pointing at a
// switch that no longer exists.
func Warning() string {
	if !ModeForced() || Available() {
		return ""
	}
	return "macOS 已强制开启命令沙箱，但" + UnavailableReason() + "；命令已降级为安全围栏保护（工作区路径边界、高危命令拦截、删除必须走 delete 工具）。"
}

// WriteDeniedHint is the model-facing note for a write the OS sandbox refused —
// a command, a background service, or a file-tool mutation. Enforcement cannot
// explain itself: the kernel reports a bare permission error, and a model that
// reads it as a bug in its own command retries the same thing until the run
// gives up. Callers attach it once the failure is known to be a sandbox write
// denial — a local filesystem permission error, never an HTTP status or an
// application-level refusal.
//
// The wording is the single source for every surface that reports this event:
// the tool card's alert line and the error-code label spell the same sentence
// (i18n app.tools.sandboxDenied / tools.error.E_SANDBOX_WRITE_DENIED), so one
// refusal cannot read three different ways.
//
// allowedRoots are the directories the model may act on: the write roots. The
// temp directory and the toolchain caches are writable too, so the note names
// them as the wrong place for a result instead of leaving them out: "only the
// following directories are writable" was untrue, and a model that found the
// one it tried really is writable had no explanation left for the refusal.
func WriteDeniedHint(allowedRoots []string) string {
	var b strings.Builder
	b.WriteString("沙箱已拦截：这次操作试图写入可写范围之外的路径，目标没有被修改。\n")
	b.WriteString("原因：这次操作运行在操作系统级沙箱里，可写范围只有下列目录（系统临时与缓存目录同样可写，但不是放结果的地方）：\n")
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
