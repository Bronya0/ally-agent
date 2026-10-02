// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.

package app

// 沙箱接入的唯一一个文件：命令侧（sandboxSpec / wrapSandboxedCommand /
// kernelOwnsBoundary）与写工具落盘侧（fileMutationSpec / runFileMutation /
// sandboxed* 包装）都在这里，因为两侧共用同一份边界归属判据。

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

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
// of the lexical fence stays on a command (the conditional half of
// checkCommandSafetyAtCwd): the two questions are one question, and a host that
// answers them differently would stand a check down for a kernel that is not
// there.
//
// Whether the OS sandbox is attached at all is that package's own switch
// (sandbox.attached / sandbox.Attached), so this judgement follows it for free:
// detached, the resolution is ModeOff everywhere and the fence owns the boundary
// on every platform.
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
// behind: Seatbelt reports EPERM, bubblewrap reports EROFS on a read-only bind
// or a masked directory. A bare "Permission denied" is deliberately absent — an
// ordinary permission problem (writing a file owned by someone else) produces it
// too, and blaming the sandbox for one of those sends the model looking in the
// wrong place.
//
// 大小写不是判据的一部分（比较处两侧都转小写）：内核与 bash 报 "Operation not
// permitted"，而 Go 自己的 errno 表是小写 "operation not permitted" —— 只比对一种
// 写法会把整类拒写漏掉，而构建工具正是那一类。
var sandboxDeniedWriteMarkers = []string{
	"operation not permitted",
	"read-only file system",
}

// annotateSandboxDeniedWrite records the model-facing next step when a command
// failed because the OS sandbox refused a write. Enforcement cannot explain
// itself: the kernel reports a bare permission error, and a model that reads it
// as a bug in its own command retries the same thing until the run gives up.
// The note rides in its own field, not in Output: the command card previews the
// output tail, so inline text would both crowd out the real error and repeat the
// card's own alert line. The refused targets are recovered from the output as
// best it allows (sandboxDeniedWriteTargets) and left out when it allows nothing.
func annotateSandboxDeniedWrite(spec sandbox.Spec, result *CommandResult, roots []string) {
	if result.ExitCode == 0 || !sandboxRefusedWrite(spec.Enforce(), result.Output) {
		return
	}
	result.DeniedWriteHint = sandbox.WriteDeniedHint(roots, sandboxDeniedWriteTargets(spec, result.Output)...)
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
	// 与文件落盘侧同一个判据（sandboxDeniedWriteMarkerIndex）：大小写不是判据的
	// 一部分，两条路径对「什么算内核拒写」只能有一套答案。
	return sandboxDeniedWriteMarkerIndex(output) >= 0
}

// maxDeniedWriteTargets bounds how many refused paths one hint names. The note
// exists to point the model at the operand it has to move; a wall of paths would
// bury the error it is appended to.
const maxDeniedWriteTargets = 3

// sandboxDeniedWriteTargets names the paths a refused write left in the output,
// keeping only the ones this run's own policy refuses.
//
// The kernel reports errno and nothing else, so the parse is best-effort — and
// the policy is the arbiter: a candidate is reported only when the very plan
// that confined the run says the path is not writable (sandbox.AllowsWrite). A
// bad guess therefore loses a path; it can never claim that a writable location
// was refused.
func sandboxDeniedWriteTargets(spec sandbox.Spec, output string) []string {
	if !spec.Enforce() || !sandbox.Available() {
		return nil
	}
	var targets []string
	seen := make(map[string]bool, maxDeniedWriteTargets)
	for _, line := range strings.Split(output, "\n") {
		candidate := sandboxDeniedWriteCandidate(line)
		if candidate == "" || seen[candidate] {
			continue
		}
		seen[candidate] = true
		if sandbox.AllowsWrite(spec, resolveDeniedWriteTarget(candidate)) {
			continue
		}
		targets = append(targets, candidate)
		if len(targets) >= maxDeniedWriteTargets {
			break
		}
	}
	return targets
}

// sandboxDeniedWriteCandidate pulls the path one output line blames, or "" when
// the line holds no refusal or no path. Producers spell the same event
// differently — `mkdir: /p: …` (coreutils), `open /p: …` (Go's own errors),
// `sh: /p: …` (bash) — but they agree on the shape "<what>: <path>: <errno>",
// so the path is the last token before the trailing separator, with the quoting
// the tools add stripped off.
func sandboxDeniedWriteCandidate(line string) string {
	marker := sandboxDeniedWriteMarkerIndex(line)
	if marker < 0 {
		return ""
	}
	head := strings.TrimRight(line[:marker], " \t:")
	if i := strings.LastIndex(head, ":"); i >= 0 {
		head = head[i+1:]
	}
	head = strings.TrimSpace(head)
	if i := strings.LastIndexAny(head, " \t"); i >= 0 {
		if tail := strings.Trim(head[i+1:], "\"'`“”‘’"); filepath.IsAbs(tail) {
			return tail
		}
	}
	// 末尾那个词不是绝对路径：工具把带空格的路径原样打了出来（macOS 上很常见：
	// `mkdir: /Users/…/Application Support/…: Operation not permitted`），路径
	// 被空格切碎。只有整行没有引号时才敢按「第一个斜杠、前面是空白」取回去：带引号
	// 说明工具引用过路径，多操作数的行（mv/cp 的 `'/src' to '/dst'`）也在其中，
	// 按第一个斜杠取会把两个路径连成一条假路径——那比不说是更糟的误导。
	if strings.ContainsAny(head, "\"'`“”‘’") {
		return ""
	}
	for i := 0; i < len(head); i++ {
		if head[i] == '/' && (i == 0 || head[i-1] == ' ' || head[i-1] == '\t') {
			return strings.TrimRight(head[i:], " \t:")
		}
	}
	return ""
}

// resolveDeniedWriteTarget resolves a path taken from command output the way the
// policy resolves a write target: absolute, then symlink-free where that is
// possible — a path that does not exist keeps its lexical form.
func resolveDeniedWriteTarget(candidate string) string {
	abs, err := filepath.Abs(candidate)
	if err != nil {
		return candidate
	}
	if real, err := filepath.EvalSymlinks(abs); err == nil {
		return real
	}
	return abs
}

// sandboxDeniedWriteMarkerIndex reports where the first kernel-refusal marker
// starts in line, or -1. Both sides of the comparison are lower-cased here, so
// "what a refused write looks like" has exactly one definition: the command path
// and the file-mutation path (isWriteRefusal) ask the same question.
func sandboxDeniedWriteMarkerIndex(line string) int {
	lower := strings.ToLower(line)
	first := -1
	for _, marker := range sandboxDeniedWriteMarkers {
		if i := strings.Index(lower, marker); i >= 0 && (first < 0 || i < first) {
			first = i
		}
	}
	return first
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

// 写工具的沙箱接入层：create / create_directory / edit / edit_files / delete /
// rename 的落盘动作不再由 Ally 进程直接 syscall，而是经 sandbox-exec（macOS）
// / bwrap（Linux）包一层 /bin/mv、/bin/rm、/bin/mkdir、/bin/ln 完成。策略层
// （resolveWritableFilePath / resolveDeletablePath）依然是第一道闸，沙箱是它
// 之下的内核级兜底：校验与落盘之间的 TOCTOU 竞态（例如后台 dev server 在两次
// 调用之间植入符号链接）从此由内核拦住，而不是靠词法判断的运气。
//
// 与命令沙箱（orch_sandbox.go 的 sandboxSpec）的三处刻意差异：
//
//   - 写根多出 ~/.ally_agent：memory / 全局配置写在那里，策略层也一直把它当
//     兜底白名单（pathutil.InsideWriteRoot）。命令沙箱反而永远禁读它（护模型
//     API key），两边不矛盾：mutation 只回传错误文本、不回流文件内容，mv/rm
//     不构成凭据外泄面；所以这里的 spec 不携带任何禁读遮罩。
//   - 这里禁网（Network=false）：mutation 只跑 mv/rm/ln/mkdir，不需要出网。
//   - 写根之外不附带临时目录与工具链缓存（OnlyWriteRoots=true）：命令需要它们
//     （构建与包管理器离了就崩），落盘动作不需要。少了这一条，工作区里指向
//     /tmp 或 ~/.npm 的符号链接就是内核自己会放行的逃逸口子。

// fileMutationTimeout bounds one sandboxed mutation. rm -rf on a huge
// node_modules can take tens of seconds; anything past this cap is treated as
// hung so a stuck sandbox-exec cannot hold the fileOpsMu serialization lock
// (and every other file tool) hostage forever.
const fileMutationTimeout = 120 * time.Second

// fileMutationSpec builds the confinement policy for one write-tool mutation.
// It mirrors sandboxSpec (same host-resolved mode) with the two documented
// differences above: the Ally data directory joins the write roots, and the
// forbid-read masks do not apply. The mode decides whether anything is confined
// at all: ModeOff leaves every caller on its unconfined path.
func fileMutationSpec(cfg ConfigState, roots []string) sandbox.Spec {
	writeRoots := make([]string, 0, len(roots)+1)
	writeRoots = append(writeRoots, roots...)
	if dir := strings.TrimSpace(appDataDir()); dir != "" {
		writeRoots = append(writeRoots, dir)
	}
	return sandbox.Spec{
		Mode:           sandbox.ResolvedMode(),
		WriteRoots:     writeRoots,
		DenyWriteRoots: kbDenyRootsForConfig(cfg),
		Network:        false,
		OnlyWriteRoots: true,
	}
}

// mutationProgram resolves the coreutil the mutation runs as. An absolute
// path keeps the program identity independent of the confined process's PATH;
// /bin and /usr/bin cover the stock locations on both macOS and Linux.
func mutationProgram(name string) string {
	for _, candidate := range []string{filepath.Join("/bin", name), filepath.Join("/usr/bin", name)} {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
	}
	if resolved, err := exec.LookPath(name); err == nil {
		return resolved
	}
	return name
}

// runFileMutation executes one confined mutation argv and translates the
// failure modes a model can act on: a kernel write refusal gets the same
// "沙箱已拦截" hint the command tool appends (the markers and the hint
// text are shared), everything else surfaces with the tool's own output.
func runFileMutation(spec sandbox.Spec, roots []string, argv []string) error {
	ctx, cancel := context.WithTimeout(context.Background(), fileMutationTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	// 独立进程组：超时杀树要能到达 sandbox-exec 之下的 mv/rm，否则孤儿进程可能
	// 在工具调用已经报错之后继续改盘。
	sandbox.PrepareCommand(cmd)
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return stopProcessTree(cmd.Process.Pid)
	}
	out, err := cmd.CombinedOutput()
	if err == nil {
		return nil
	}
	detail := strings.TrimSpace(string(out))
	if ctx.Err() != nil {
		return fmt.Errorf("sandboxed file mutation timed out after %s: %s: %s", fileMutationTimeout, argv[0], detail)
	}
	// 归因走命令侧那一套判据（sandboxRefusedWrite）：降级运行时命令根本没被包
	// 住，同一句 “Operation not permitted” 只是普通权限问题，安到沙箱头上会让
	// 模型找错方向。
	if sandboxRefusedWrite(spec.Enforce(), detail) {
		return codedToolError("E_SANDBOX_WRITE_DENIED", fmt.Errorf("%s\n\n%s", detail, sandbox.WriteDeniedHint(roots, sandboxDeniedWriteTargets(spec, detail)...)))
	}
	if detail == "" {
		return err
	}
	return fmt.Errorf("%s: %s", err, detail)
}

// stageMutationFile writes data to a fresh temp sibling under dir and returns
// its path. The staging file is created by Ally itself (unconfined), but it is
// a random O_EXCL name inside a directory the policy layer already validated
// as writable; the content only lands at the real target through the sandboxed
// move, so a refused mutation leaves nothing behind (the caller removes the
// staged file on every path).
func stageMutationFile(dir string, data []byte, perm os.FileMode) (string, error) {
	f, err := os.CreateTemp(dir, ".ally-stage-*")
	if err != nil {
		return "", err
	}
	name := f.Name()
	_, werr := f.Write(data)
	if werr == nil {
		werr = f.Chmod(perm)
	}
	cerr := f.Close()
	if err := errors.Join(werr, cerr); err != nil {
		_ = os.Remove(name)
		return "", err
	}
	return name, nil
}

// stagingRefusal turns a refused staging create into the explanation the kernel
// path carries. Staging is the one write-tool step that runs unconfined (Ally
// creates the temp sibling itself), so a target the OS refuses — a root-owned
// directory such as /etc — fails here, before the sandboxed move ever runs, and
// would otherwise reach the model as a bare "permission denied" with no error
// code at all: the same event, none of the explanation.
//
// The classification stops inside the write roots: a refusal on a path that is
// writable territory (a directory inside the workspace owned by another user,
// say) is an ordinary permission problem, and blaming the sandbox for it would
// send the model looking in the wrong place.
func stagingRefusal(spec sandbox.Spec, roots []string, dir string, err error) error {
	if !spec.Enforce() || insideWriteRoot(roots, dir) || !isWriteRefusal(err) {
		return err
	}
	return codedToolError("E_SANDBOX_WRITE_DENIED", fmt.Errorf("%s\n\n%s", stagingRefusalDetail(dir, err), sandbox.WriteDeniedHint(roots, dir)))
}

// isWriteRefusal reports whether err is the OS refusing the write itself, as
// opposed to another failure (a missing directory, a full disk). EPERM/EACCES
// arrive as fs.ErrPermission; a read-only filesystem keeps its own wording,
// which is the same one the kernel markers look for.
func isWriteRefusal(err error) bool {
	if errors.Is(err, fs.ErrPermission) {
		return true
	}
	// 与命令侧同一个判据（sandboxDeniedWriteMarkerIndex）：大小写不是判据的一部分，
	// 两条路径对「什么算内核拒写」只能有一套答案。
	return sandboxDeniedWriteMarkerIndex(err.Error()) >= 0
}

// stagingRefusalDetail names the directory the write was staged in rather than
// the randomly named staging file: the directory is what the model has to move
// its target out of, and Ally's temp naming is not its business.
func stagingRefusalDetail(dir string, err error) string {
	reason := err.Error()
	var pathErr *os.PathError
	if errors.As(err, &pathErr) {
		reason = pathErr.Err.Error()
	}
	return fmt.Sprintf("%s: %s", filepath.ToSlash(dir), reason)
}

// sandboxedMkdirAll is the confined form of os.MkdirAll.
func sandboxedMkdirAll(spec sandbox.Spec, roots []string, dir string) error {
	if !spec.Enforce() {
		return os.MkdirAll(dir, 0o755)
	}
	argv := wrapSandboxedCommand(spec, []string{mutationProgram("mkdir"), "-p", dir})
	return runFileMutation(spec, roots, argv)
}

// sandboxedWriteFile atomically overwrites path with data (temp sibling +
// confined mv, the same shape as read.SafeWritePreparedFile). The parent
// directory must already exist, exactly like the unsandboxed original.
func sandboxedWriteFile(spec sandbox.Spec, roots []string, path string, data []byte, perm os.FileMode) error {
	if !spec.Enforce() {
		return safeWriteFile(path, data, perm)
	}
	staged, err := stageMutationFile(filepath.Dir(path), data, perm)
	if err != nil {
		return stagingRefusal(spec, roots, filepath.Dir(path), err)
	}
	defer os.Remove(staged)
	// mv 落在已存在的目录上会把文件挪进目录里，而 rename(2) 会报 EISDIR；
	// 落盘前先缩小这个窗口，行为与直接 syscall 保持一致。
	if info, statErr := os.Lstat(path); statErr == nil && info.IsDir() {
		return codedToolError("E_TARGET_IS_DIRECTORY", fmt.Errorf("path is a directory: %s", path))
	}
	argv := wrapSandboxedCommand(spec, []string{mutationProgram("mv"), "-f", staged, path})
	return runFileMutation(spec, roots, argv)
}

// sandboxedWriteNewFile creates path only when it does not exist yet (temp
// sibling + confined ln, mirroring read.SafeWriteNewFile's O_EXCL contract:
// the link syscall fails loudly when the target is taken).
func sandboxedWriteNewFile(spec sandbox.Spec, roots []string, path string, data []byte, perm os.FileMode) error {
	if !spec.Enforce() {
		return safeWriteNewFile(path, data, perm)
	}
	staged, err := stageMutationFile(filepath.Dir(path), data, perm)
	if err != nil {
		return stagingRefusal(spec, roots, filepath.Dir(path), err)
	}
	defer os.Remove(staged)
	argv := wrapSandboxedCommand(spec, []string{mutationProgram("ln"), staged, path})
	if err := runFileMutation(spec, roots, argv); err != nil {
		if strings.Contains(err.Error(), "File exists") {
			return codedToolError("E_EXISTS", fmt.Errorf("file already exists: %s", path))
		}
		return err
	}
	return nil
}

// sandboxedRename is the confined form of os.Rename for the in-place renames
// the rename tool performs (source and target share one parent directory).
func sandboxedRename(spec sandbox.Spec, roots []string, source, target string) error {
	if !spec.Enforce() {
		return os.Rename(source, target)
	}
	// 与 sandboxedWriteFile 同理：mv 把目录目标当成“挪进去”而不是报错。仅改大小
	// 写的目录改名（docs→DOCS）在大小写不敏感的文件系统上会 Lstat 命中源目录
	// 本身，那是 os.Rename 本就允许的合法操作，得放过去。
	if info, statErr := os.Lstat(target); statErr == nil && info.IsDir() {
		if srcInfo, srcErr := os.Lstat(source); srcErr != nil || !os.SameFile(info, srcInfo) {
			return codedToolError("E_EXISTS", fmt.Errorf("path already exists: %s", target))
		}
	}
	argv := wrapSandboxedCommand(spec, []string{mutationProgram("mv"), source, target})
	return runFileMutation(spec, roots, argv)
}

// sandboxedRemove is the confined form of os.Remove / os.RemoveAll. Like the
// syscalls it replaces, removing a symlink takes the link, never its target.
func sandboxedRemove(spec sandbox.Spec, roots []string, path string, recursive bool) error {
	if !spec.Enforce() {
		if recursive {
			return os.RemoveAll(path)
		}
		return os.Remove(path)
	}
	argv := []string{mutationProgram("rm"), "-f"}
	if recursive {
		argv = append(argv, "-r")
	}
	argv = append(argv, path)
	wrapped := wrapSandboxedCommand(spec, argv)
	return runFileMutation(spec, roots, wrapped)
}
