// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.

package app

import (
	"log"
	"strings"
	"sync"
	"sync/atomic"

	"ally-dev/internal/sandbox"
)

// 命令沙箱的编排层：把本机档位、本次运行的工作区与知识库禁写面拼成一次启动策略，
// 再把"沙箱没能包上"与"沙箱拦下了这次写入"两种结果翻译成模型能照做的说法。
// 策略与 profile 生成本身在 internal/sandbox（纯算法，不依赖 App 状态）。沙箱没有
// 任何可配项：档位是宿主的属性，策略其余部分写死在这里。

// sandboxSpec resolves the confinement policy for one local command or service.
// Nothing here is configurable: the mode is the host's own policy
// (sandbox.ResolvedMode) and the rest is fixed — the write roots of this run, the
// knowledge-base sources/ subtree re-denied inside them, Ally's own data
// directory masked out of the command's view, and the network left open because
// package managers and builds need it. denyRoots is the KB sources/ subtree:
// readable, never writable.
func sandboxSpec(roots, denyRoots []string) sandbox.Spec {
	return sandbox.Spec{
		Mode:            sandbox.ResolvedMode(),
		WriteRoots:      roots,
		DenyWriteRoots:  denyRoots,
		ForbidReadRoots: sandboxForbiddenReadRoots(),
		Network:         true,
	}
}

// sandboxForbiddenReadRoots is what a model-driven shell must not see: Ally's own
// data directory, which holds the model API keys, the SSH cluster credentials and
// the session histories. The list is built rather than configured, so no config
// edit can lose the mask. Write roots still win (internal/sandbox drops an
// overlapping mask), so a workspace that somehow sits inside the data directory
// stays writable and the conflict is reported by the per-command diagnostics
// instead of silently unprotecting it.
func sandboxForbiddenReadRoots() []string {
	dir := strings.TrimSpace(appDataDir())
	if dir == "" {
		return nil
	}
	return []string{dir}
}

// wrapSandboxedCommand confines argv when the host mandates it. There is one
// failure mode and it is handled squarely: a mandated platform has no switch to
// turn the sandbox off, so refusing every command would brick the tool with no
// way out — there the command degrades to unwrapped execution under the full
// safety fence, with a standing log line saying so.
func wrapSandboxedCommand(spec sandbox.Spec, argv []string) []string {
	if !spec.Enforce() {
		return argv
	}
	// 先建后报：诊断读的是同一个策略，缓存目录还没创时先建好，否则首次运行必然
	// 报一串“缓存不存在”（它们下一刻就会被建出来）。建不出来时诊断会如实报告。
	sandbox.EnsureWritableDirsOnce()
	logSandboxProblems(spec)
	if wrapped, ok := sandbox.Wrap(spec, argv); ok {
		return wrapped
	}
	if _, warned := sandboxProblemsSeen.LoadOrStore("degraded:"+sandbox.UnavailableReason(), true); !warned {
		log.Printf("command sandbox: forced mode but backend unavailable (%s); commands run under the safety fence only", sandbox.UnavailableReason())
	}
	return argv
}

// kernelBoundaryOverride pins boundary ownership for tests: 0 follows the host,
// the other two force one side. Only tests set it (pinKernelBoundary), and
// claiming kernel ownership on a host that cannot actually confine is refused
// there — that claim would let an "outside write is refused" assertion through
// to a real write.
var kernelBoundaryOverride atomic.Int32

const (
	kernelBoundaryFollowHost int32 = 0
	kernelBoundaryForced     int32 = 1
	kernelBoundaryFenced     int32 = 2
)

// kernelOwnsBoundary is the single judgement for who owns the filesystem
// boundary — the workspace outline the write tools, the command tool's cwd and
// every outside-write check draw. It is also the single judgement for how much
// of the lexical fence stays on a command (checkConfinedCommandSafety): the two
// questions are one question, and a host that answers them differently would
// stand a check down for a kernel that is not there.
//
// While the OS sandbox really confines this host's commands and mutations, an
// outside write is refused by the kernel itself, so the lexical checks in front
// of it stand down; when the kernel does not confine (no mandated sandbox, or a
// backend that cannot wrap), those checks are the only thing standing and stay
// at full strength.
//
// The judgement asks the sandbox, never the platform: which OS this is does not
// answer whether the confinement is in force right now.
func kernelOwnsBoundary() bool {
	switch kernelBoundaryOverride.Load() {
	case kernelBoundaryForced:
		return true
	case kernelBoundaryFenced:
		return false
	}
	return sandbox.ResolvedMode() == sandbox.ModeEnforce && sandbox.Available()
}

// sandboxProblemsSeen de-duplicates the diagnostics log. A dropped write root or
// a mask that lost to a write root is a standing policy problem: it would
// otherwise repeat on every single command and bury everything else.
var sandboxProblemsSeen sync.Map

// logSandboxProblems records the protections this run's policy asked for that
// will not apply. An enforcement layer that silently drops a protection looks
// exactly like one that works, so a lost mask has to end up somewhere readable.
func logSandboxProblems(spec sandbox.Spec) {
	problems := spec.Diagnostics()
	if len(problems) == 0 {
		return
	}
	key := strings.Join(problems, "\n")
	if _, seen := sandboxProblemsSeen.LoadOrStore(key, true); seen {
		return
	}
	log.Printf("command sandbox: %d requested protection(s) do not apply:\n%s", len(problems), key)
}

// sandboxDeniedWriteMarkers are the kernel-level strings a refused write leaves
// behind: Seatbelt reports EPERM ("Operation not permitted"), bubblewrap reports
// EROFS on a read-only bind or a masked directory ("Read-only file system").
// A bare "Permission denied" is deliberately absent — an ordinary permission
// problem (writing a file owned by someone else) produces it too, and blaming
// the sandbox for one of those sends the model looking in the wrong place.
var sandboxDeniedWriteMarkers = []string{
	"Operation not permitted",
	"Read-only file system",
}

// annotateSandboxDeniedWrite records the model-facing next step when a command
// failed because the OS sandbox refused a write. Enforcement cannot explain
// itself: the kernel reports a bare permission error, and a model that reads it
// as a bug in its own command retries the same thing until the run gives up.
// The note rides in its own field, not in Output: the command card previews the
// output tail, so inline text would both crowd out the real error and repeat the
// card's own alert line.
func annotateSandboxDeniedWrite(spec sandbox.Spec, result *CommandResult, roots []string) {
	if result.ExitCode == 0 || !sandboxRefusedWrite(spec.Enforce(), result.Output) {
		return
	}
	result.DeniedWriteHint = sandbox.WriteDeniedHint(roots)
	// 同一件事的结构化标记：卡片据此固定显示一行醒目报错。提示既不留在正文里
	// （会挤掉真正的报错），也不靠正文渲染：模型侧由 renderCommandResultForModel
	// 追加，服务那侧走同一对字段（ServiceInfo / ServiceReadResult）。
	result.SandboxDenied = true
}

// sandboxRefusedWrite 判断这段输出是不是内核拒写留下的那句。confined 是调用方
// 记录的「这条命令 / 这个服务真的跑在沙箱里」：降级运行（强制档 + 后端不可用）
// 或根本没包沙箱时没有内核兜底，同一句 “Operation not permitted” 只是普通权限
// 问题，安到沙箱头上会让模型找错方向。判据收口在这里，命令与服务共用。
func sandboxRefusedWrite(confined bool, output string) bool {
	if !confined || !sandbox.Available() {
		return false
	}
	for _, marker := range sandboxDeniedWriteMarkers {
		if strings.Contains(output, marker) {
			return true
		}
	}
	return false
}

// sandboxModeString renders a resolved mode the way the status payload spells it:
// "" for off, "enforce" for a host that confines commands.
func sandboxModeString(mode sandbox.Mode) string {
	if mode == sandbox.ModeEnforce {
		return string(sandbox.ModeEnforce)
	}
	return ""
}

// sandboxStatusFor reports what this host actually does, so the settings page can
// state the truth instead of echoing a mode anyone chose — the sandbox is not a
// setting any more.
func sandboxStatusFor() SandboxStatus {
	return SandboxStatus{
		Effective: sandboxModeString(sandbox.ResolvedMode()),
		Forced:    sandbox.ModeForced(),
		Available: sandbox.Available(),
		Warning:   sandbox.Warning(),
	}
}

// GetSandboxStatus lets the settings page tell the user whether the sandbox can
// run on this machine (and, when this platform mandates it and the backend is
// unusable, that the safety fence is what is guarding commands).
func (a *App) GetSandboxStatus() (SandboxStatus, error) {
	if err := a.ensureInitialized(); err != nil {
		return SandboxStatus{}, err
	}
	return sandboxStatusFor(), nil
}
