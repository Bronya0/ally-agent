// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
package app

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"ally-dev/internal/sandbox"
)

// isolateSandboxHome 把 HOME 指向测试自己的临时目录：沙箱策略会读用户主目录
// （凭证目录的遮蔽目标），测试绝不能读到真实路径。Windows 上 HOME 不生效，
// 必须同时设 USERPROFILE。
func isolateSandboxHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	return home
}

// requireConfinement 只在真正会包命令的宿主机上跑后续断言：档位是平台自己的策略，
// 已经没有“配置里打开沙箱”这条路径可用了。
func requireConfinement(t *testing.T) {
	t.Helper()
	if !kernelOwnsBoundary() {
		t.Skip("this host does not confine commands: no mandated sandbox, or no usable backend")
	}
}

// pinBoundaryOwnership 钉住一个用例的「谁管文件系统边界」判据：true = 内核接管
// （Ally 的越界检查让位），false = 围栏在位。它只换判据、不提供内核，所以任何真
// 会落盘的用例必须自己先 requireConfinement——否则在这个宿主上沙箱包不上，越界
// 写会真的写到工作区外面去。
func pinBoundaryOwnership(t *testing.T, kernelOwns bool) {
	t.Helper()
	previous := kernelBoundaryOverride.Load()
	next := kernelBoundaryForced
	if !kernelOwns {
		next = kernelBoundaryFenced
	}
	kernelBoundaryOverride.Store(next)
	t.Cleanup(func() { kernelBoundaryOverride.Store(previous) })
}

// confinedFence 跑统一围栏入口的「内核接管边界」那一侧：先钉住判据（不提供内核），
// 只验证围栏自己留下的那几道。沙箱接不接，入口都是同一条。
func confinedFence(t *testing.T, commandLine, workingDir string, roots []string) error {
	t.Helper()
	pinBoundaryOwnership(t, true)
	return checkCommandSafetyAtCwd(CommandRequest{Command: commandLine, Cwd: workingDir}, roots, workingDir)
}

// 判据只问沙箱，不问平台：钉住哪一侧就返回哪一侧。
func TestKernelOwnsBoundaryFollowsTheKernel(t *testing.T) {
	pinBoundaryOwnership(t, true)
	if !kernelOwnsBoundary() {
		t.Fatal("kernel ownership must be visible to the judgement")
	}
	pinBoundaryOwnership(t, false)
	if kernelOwnsBoundary() {
		t.Fatal("the fence must be visible to the judgement")
	}
}

// 边界归内核时，写 / 删 / cwd 三个解析器不再自己拒越界路径：它们只做归一化，越界
// 由内核在真正落盘时拒。这里不落盘，所以不需要沙箱真的可用。
func TestBoundaryResolversStandDownForTheKernel(t *testing.T) {
	isolateSandboxHome(t)
	roots := []string{t.TempDir()}
	outsideDir := t.TempDir()
	outsideFile := filepath.Join(outsideDir, "escaped.txt")
	if err := os.WriteFile(outsideFile, []byte("keep\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	pinBoundaryOwnership(t, false)
	if _, err := resolveWritableFilePath(roots, outsideFile); toolErrorCode(err) != "E_PATH_OUTSIDE" {
		t.Fatalf("with the fence guarding, an outside write path must be refused here, got %v", err)
	}
	if _, err := resolveDeletablePath(roots, outsideFile); toolErrorCode(err) != "E_PATH_OUTSIDE" {
		t.Fatalf("with the fence guarding, an outside delete path must be refused here, got %v", err)
	}
	if _, err := resolveCommandCwd(roots, outsideDir); toolErrorCode(err) != "E_PATH_OUTSIDE" {
		t.Fatalf("with the fence guarding, an outside cwd must be refused here, got %v", err)
	}

	// 同一个入口、同一份路径，只把判据换成内核：三个都只归一化。
	pinBoundaryOwnership(t, true)
	resolved, err := resolveWritableFilePath(roots, outsideFile)
	if err != nil {
		t.Fatalf("the kernel owns the boundary: the resolver must not refuse, got %v", err)
	}
	if !samePath(resolved, outsideFile) {
		t.Fatalf("the resolver must still normalize the path, got %q want %q", resolved, outsideFile)
	}
	if _, err := resolveDeletablePath(roots, outsideFile); err != nil {
		t.Fatalf("the kernel owns the boundary: a delete path must resolve, got %v", err)
	}
	if _, err := resolveCommandCwd(roots, outsideDir); err != nil {
		t.Fatalf("the kernel owns the boundary: an outside cwd must resolve, got %v", err)
	}
}

// 越界目标不预先统计：写根外的大目录先 WalkDir 一遍再等内核拒绝，纯属白等。
func TestDeleteStatsSkipTargetsTheKernelWillRefuse(t *testing.T) {
	isolateSandboxHome(t)
	workspace := t.TempDir()
	// 区外目标与工作区都在改 TMPDIR 之前建好：os.TempDir() 本身在沙箱可写面里。
	outside := t.TempDir()
	t.Setenv("TMPDIR", t.TempDir())
	roots := []string{workspace}
	spec := fileMutationSpec(ConfigState{Workspace: workspace}, roots)

	inside := filepath.Join(workspace, "notes")
	if err := os.MkdirAll(inside, 0o755); err != nil {
		t.Fatal(err)
	}
	if !countDeleteStats(spec, roots, inside) {
		t.Fatal("a target inside the write roots must keep its exact counts")
	}

	pinBoundaryOwnership(t, true)
	if countDeleteStats(spec, roots, outside) {
		t.Fatal("an outside target must not be walked: the kernel refuses that removal anyway")
	}
	pinBoundaryOwnership(t, false)
	if !countDeleteStats(spec, roots, outside) {
		t.Fatal("with the fence guarding, outside targets keep their exact counts")
	}
}

// 档位不再由配置决定：spec 必须跟随宿主解析出的档位。
func TestSandboxSpecFollowsTheResolvedMode(t *testing.T) {
	isolateSandboxHome(t)
	spec := sandboxSpec([]string{t.TempDir()}, nil)
	if spec.Enforce() != (sandbox.ResolvedMode() == sandbox.ModeEnforce) {
		t.Fatalf("a spec must follow the resolved host mode, got %v", spec.Mode)
	}
}

func TestSandboxSpecCarriesRootsDenyRootsAndCredentialMask(t *testing.T) {
	if sandbox.ResolvedMode() != sandbox.ModeEnforce {
		t.Skip("this host does not mandate the sandbox")
	}
	home := isolateSandboxHome(t)
	spec := sandboxSpec(
		[]string{"/w/space"},
		[]string{"/w/space/sources"},
	)

	if !spec.Enforce() {
		t.Fatal("the resolved mode must survive into the spec")
	}
	if len(spec.WriteRoots) != 1 || spec.WriteRoots[0] != "/w/space" {
		t.Fatalf("write roots = %v", spec.WriteRoots)
	}
	// 知识库的 sources/ 可读不可写：这条保护只能从策略传下去。
	if len(spec.DenyWriteRoots) != 1 || spec.DenyWriteRoots[0] != "/w/space/sources" {
		t.Fatalf("deny-write roots = %v", spec.DenyWriteRoots)
	}
	// 模型驱动 shell 不该读到 API key 与 SSH 凭据：配置目录要被遮蔽掉。
	want := filepath.Join(home, ".ally_agent")
	if len(spec.ForbidReadRoots) != 1 || spec.ForbidReadRoots[0] != want {
		t.Fatalf("forbid-read roots = %v, want %s", spec.ForbidReadRoots, want)
	}
	// 命令需要出网：包管理器与构建没有它就跑不起来。
	if !spec.Network {
		t.Fatal("network must stay open for commands")
	}
}

// 沙箱拦下的写入只留下一句裸权限错误，读起来像命令自己写错了：必须补上能照做的
// 下一步，且只在开启沙箱时补。后端用不了时提示本来就不该加（那时的裸权限错误只是
// 普通权限问题），所以本机包不住命令时这条断言无从验证。
func TestAnnotateSandboxDeniedWrite(t *testing.T) {
	requireConfinement(t)
	roots := []string{t.TempDir()}
	enforced := sandbox.Spec{Mode: sandbox.ModeEnforce}

	const rawOutput = "/bin/sh: /etc/hosts: Operation not permitted"
	refused := &CommandResult{ExitCode: 1, Output: rawOutput}
	annotateSandboxDeniedWrite(enforced, refused, roots)
	if !strings.Contains(refused.DeniedWriteHint, "沙箱已拦截") {
		t.Fatalf("a refused write must be explained, got %q", refused.DeniedWriteHint)
	}
	// 提示不进 Output：命令卡正体只预览尾部若干行，提示埋在里面既挤掉真正的报错，
	// 又和卡片顶部那行固定报错重复；模型侧由 renderCommandResultForModel 追加。
	if refused.Output != rawOutput {
		t.Fatalf("the hint must stay out of the command output, got %q", refused.Output)
	}
	// 标记与提示同源：前端靠它固定显示那一行报错，漏了它就只剩正文里看不到的文字。
	if !refused.SandboxDenied {
		t.Fatal("a refused write must carry the structured flag the card renders from")
	}
	if !strings.Contains(refused.DeniedWriteHint, filepath.ToSlash(roots[0])) {
		t.Fatalf("the hint must name the writable roots, got %q", refused.DeniedWriteHint)
	}

	// 关闭沙箱时同样的输出与沙箱无关，加提示就是误导。
	off := &CommandResult{ExitCode: 1, Output: rawOutput}
	annotateSandboxDeniedWrite(sandbox.Spec{}, off, roots)
	if off.DeniedWriteHint != "" {
		t.Fatalf("a disabled sandbox must not be blamed, got %q", off.DeniedWriteHint)
	}

	// 成功的命令即使输出里带了同名字样也不加提示。
	ok := &CommandResult{ExitCode: 0, Output: "Operation not permitted"}
	annotateSandboxDeniedWrite(enforced, ok, roots)
	if ok.DeniedWriteHint != "" {
		t.Fatalf("a successful command must not be annotated, got %q", ok.DeniedWriteHint)
	}

	// 普通的权限问题（写别人的文件）不能被归因到沙箱。
	perm := &CommandResult{ExitCode: 1, Output: "chmod: /x: Permission denied"}
	annotateSandboxDeniedWrite(enforced, perm, roots)
	if perm.DeniedWriteHint != "" {
		t.Fatalf("an ordinary permission error must not be blamed on the sandbox, got %q", perm.DeniedWriteHint)
	}
	// 没能归因到沙箱的三种结果一个都不许带标记：卡片会照着它把绿√换成拒绝标记。
	for name, res := range map[string]*CommandResult{"disabled": off, "succeeded": ok, "ordinary permission": perm} {
		if res.SandboxDenied {
			t.Fatalf("%s must not be flagged as a sandbox denial", name)
		}
	}
}

func TestWrapSandboxedCommandLeavesUnconfinedCommandsAlone(t *testing.T) {
	isolateSandboxHome(t)
	argv := []string{"bash", "-c", "echo hi"}
	got := wrapSandboxedCommand(sandbox.Spec{}, argv)
	if len(got) != len(argv) || got[0] != argv[0] || got[2] != argv[2] {
		t.Fatalf("argv = %v, want unchanged", got)
	}
}

// 端到端：真实的 command 路径开启沙箱后，工作区内可写、区外被拦，且拦下时模型
// 拿到的是可照做的下一步而不是一句裸权限错误。只在有可用后端的机器上跑。
func TestRunCommandWithSandboxConfinesWrites(t *testing.T) {
	requireConfinement(t)
	isolateSandboxHome(t)
	// 工作区与区外目标必须在本次改写 TMPDIR 之前建好：os.TempDir() 是可写面之一，
	// 而 t.TempDir() 建在 TMPDIR 下——不把两者拆开，"区外"其实落在可写面里。
	workspace := t.TempDir()
	outside := t.TempDir()
	t.Setenv("TMPDIR", t.TempDir())

	a := NewApp()
	if err := a.ensureInitialized(); err != nil {
		t.Fatal(err)
	}
	cfg := ConfigState{Workspace: workspace}
	ctx := context.Background()

	inside, err := a.runCommandWithConfig(ctx, cfg, CommandRequest{
		Command: "echo hi > inside.txt && cat inside.txt",
	})
	if err != nil {
		t.Fatalf("a write inside the workspace must run: %v", err)
	}
	if !inside.Sandboxed {
		t.Fatal("the result must report that the command ran confined")
	}
	if inside.ExitCode != 0 || !strings.Contains(inside.Output, "hi") {
		t.Fatalf("inside write failed: exit=%d output=%q", inside.ExitCode, inside.Output)
	}

	refused, err := a.runCommandWithConfig(ctx, cfg, CommandRequest{
		Command: "echo pwn > " + filepath.ToSlash(filepath.Join(outside, "escaped.txt")),
	})
	if err != nil {
		t.Fatalf("a refused write is a command result, not a tool error: %v", err)
	}
	if refused.ExitCode == 0 {
		t.Fatalf("a write outside the workspace must fail: %q", refused.Output)
	}
	// 提示在 DeniedWriteHint 上，不在 Output 里：命令卡正体只预览尾部若干行，
	// 埋进正文会挤掉真正的报错。模型侧由 renderCommandResultForModel 追加。
	if !strings.Contains(refused.DeniedWriteHint, "沙箱已拦截") {
		t.Fatalf("the model must learn the write was refused by the sandbox, got %q", refused.DeniedWriteHint)
	}
	if !refused.SandboxDenied {
		t.Fatal("the refusal must carry the structured flag the card renders from")
	}
	if _, statErr := os.Stat(filepath.Join(outside, "escaped.txt")); statErr == nil {
		t.Fatal("the file outside the workspace must not exist")
	}
}

// 会话级附加工作区（Tab 上临时加的一个或多个目录）必须一起进沙箱写根：只在文件
// 工具里放行、在命令里不放行，等于用户加的那个目录对 shell 反而变成只读。
func TestRunCommandWithSandboxAllowsEveryWorkspaceRoot(t *testing.T) {
	requireConfinement(t)
	isolateSandboxHome(t)
	workspace := t.TempDir()
	extra := t.TempDir()
	outside := t.TempDir()
	t.Setenv("TMPDIR", t.TempDir())

	a := NewApp()
	if err := a.ensureInitialized(); err != nil {
		t.Fatal(err)
	}
	cfg := ConfigState{
		Workspace:  workspace,
		ExtraRoots: []string{extra},
	}
	ctx := context.Background()
	two := filepath.Join(extra, "two.txt")

	result, err := a.runCommandWithConfig(ctx, cfg, CommandRequest{
		Command: "echo one > one.txt && echo two > " + filepath.ToSlash(two),
	})
	if err != nil {
		t.Fatalf("writing both roots must run: %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("the extra root must be writable from a command: %q", result.Output)
	}
	for _, path := range []string{filepath.Join(workspace, "one.txt"), two} {
		if _, statErr := os.Stat(path); statErr != nil {
			t.Fatalf("expected %s to be written: %v", path, statErr)
		}
	}

	// 两个写根之外的第三个位置仍然被拦：多个写根不是“整个盘都放开了”。
	refused, err := a.runCommandWithConfig(ctx, cfg, CommandRequest{
		Command: "echo pwn > " + filepath.ToSlash(filepath.Join(outside, "escaped.txt")),
	})
	if err != nil {
		t.Fatalf("a refused write is a command result, not a tool error: %v", err)
	}
	if refused.ExitCode == 0 {
		t.Fatalf("a write outside every root must fail: %q", refused.Output)
	}
}

// 附加工作区是随会话变的：策略每条命令现算（不缓存），所以删掉之后下一条命令必须
// 立刻不再可写。哪一天策略被缓存起来，这个测试就是“删了还能写”的告警。
func TestRunCommandWithSandboxDropsRemovedRoot(t *testing.T) {
	requireConfinement(t)
	isolateSandboxHome(t)
	workspace := t.TempDir()
	extra := t.TempDir()
	t.Setenv("TMPDIR", t.TempDir())

	a := NewApp()
	if err := a.ensureInitialized(); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	keep := ConfigState{
		Workspace:  workspace,
		ExtraRoots: []string{extra},
	}
	inside := filepath.Join(extra, "kept.txt")
	if result, err := a.runCommandWithConfig(ctx, keep, CommandRequest{
		Command: "echo ok > " + filepath.ToSlash(inside),
	}); err != nil || result.ExitCode != 0 {
		t.Fatalf("the extra root must be writable while it is listed: %v %q", err, result.Output)
	}

	// 同一个会话、下一条命令：附加工作区已从会话里移除。
	without := ConfigState{Workspace: workspace}
	after := filepath.Join(extra, "after-removal.txt")
	result, err := a.runCommandWithConfig(ctx, without, CommandRequest{
		Command: "echo pwn > " + filepath.ToSlash(after),
	})
	if err != nil {
		t.Fatalf("a refused write is a command result, not a tool error: %v", err)
	}
	if result.ExitCode == 0 {
		t.Fatalf("a removed extra root must stop being writable: %q", result.Output)
	}
	if _, statErr := os.Stat(after); statErr == nil {
		t.Fatal("the file must not exist after the root was removed")
	}
}

// 命令串里的文本围栏只看字面写目标，脚本内部的删除它一点也看不见（`python3 x.py`
// 没有写目标）。所以“脚本里删工作区外的文件/目录”这一整类只能靠内核级沙箱拦，
// 这条测试盯的就是它。
func TestRunCommandWithSandboxConfinesScriptAndDynamicWrites(t *testing.T) {
	requireConfinement(t)
	python, lookErr := exec.LookPath("python3")
	if lookErr != nil {
		t.Skip("python3 is not on PATH")
	}
	isolateSandboxHome(t)
	workspace := t.TempDir()
	outside := t.TempDir()
	t.Setenv("TMPDIR", t.TempDir())

	victimFile := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(victimFile, []byte("keep me\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	victimDir := filepath.Join(outside, "tree")
	if err := os.MkdirAll(filepath.Join(victimDir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}

	// 模型会先把这个脚本写进工作区（write 工具），再让 shell 跑它。
	script := fmt.Sprintf(`import os, shutil
for kind, path in [("file", %q), ("dir", %q)]:
    try:
        os.remove(path) if kind == "file" else shutil.rmtree(path)
        print("REMOVED", kind)
    except Exception as exc:
        print("blocked", kind, type(exc).__name__)
`, victimFile, victimDir)
	if err := os.WriteFile(filepath.Join(workspace, "victim.py"), []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}

	a := NewApp()
	if err := a.ensureInitialized(); err != nil {
		t.Fatal(err)
	}
	cfg := ConfigState{Workspace: workspace}
	ctx := context.Background()

	result, err := a.runCommandWithConfig(ctx, cfg, CommandRequest{Command: filepath.ToSlash(python) + " victim.py"})
	if err != nil {
		t.Fatalf("the script run is a command result, not a tool error: %v", err)
	}
	if !strings.Contains(result.Output, "blocked file") || !strings.Contains(result.Output, "blocked dir") {
		t.Fatalf("the OS must refuse both operations: %q", result.Output)
	}
	if _, statErr := os.Stat(victimFile); statErr != nil {
		t.Fatalf("the file outside the workspace must survive: %v", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(victimDir, "sub")); statErr != nil {
		t.Fatalf("the directory outside the workspace must survive: %v", statErr)
	}

	// 同一类洞的另一副面孔：shell 里的动态写目标（变量）同样是围栏看不见的，
	// 沙箱必须拦住它。
	dynamic := filepath.Join(outside, "dynamic.txt")
	if err := os.WriteFile(dynamic, []byte("original\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := a.runCommandWithConfig(ctx, cfg, CommandRequest{
		Command: "f=" + filepath.ToSlash(dynamic) + `; echo pwn > "$f"`,
	}); err != nil {
		t.Fatalf("a dynamic write is a command result, not a tool error: %v", err)
	}
	body, readErr := os.ReadFile(dynamic)
	if readErr != nil || string(body) != "original\n" {
		t.Fatalf("a dynamic target outside the workspace must stay untouched, got %q (err=%v)", body, readErr)
	}
}

// 状态行只说实话：档位跟随宿主解析结果，Forced / Available / Warning 直报本机情况，
// 再也没有“用户选了但没生效”这一档。
func TestSandboxStatusForFollowsHostCapability(t *testing.T) {
	isolateSandboxHome(t)

	status := sandboxStatusFor()
	if want := sandboxModeString(sandbox.ResolvedMode()); status.Effective != want {
		t.Fatalf("effective = %q, want %q", status.Effective, want)
	}
	if status.Forced != sandbox.ModeForced() || status.Available != sandbox.Available() {
		t.Fatalf("status must mirror the host, got %+v", status)
	}
	if status.Warning != sandbox.Warning() {
		t.Fatalf("warning = %q, want %q", status.Warning, sandbox.Warning())
	}
}

// 沙箱真正包住命令时，围栏只保留内核看不见的两道：VCS 元数据与高危语义。词法
// 猜测的三道（工作区外写入、KB 禁写、删除强制走 delete）让位给内核——它们的
// 误报正是猜测的代价。本测试验证留下的、放走的各归其位。
func TestConfinedCommandSafetyKeepsKernelBlindSpots(t *testing.T) {
	workspace := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workspace, ".git", "hooks"), 0o755); err != nil {
		t.Fatal(err)
	}
	// 删除命令放行：内核把 rm 的爆炸半径锁进写根，围栏不再强制走 delete 工具。
	if err := confinedFence(t, "rm -rf build", workspace, []string{workspace}); err != nil {
		t.Fatalf("a confined rm must not be forced through the delete tool: %v", err)
	}
	// 工作区外写入放行：词法判断“目标已存在且在区外”正是误报大户，内核拦得更准。
	if err := confinedFence(t, "echo hi > /etc/hosts", workspace, []string{workspace}); err != nil {
		t.Fatalf("a confined outside write must be left to the kernel: %v", err)
	}
	// VCS 元数据仍然拦截：.git 在写根之内（git 自己要写），内核管不了它。
	if code := toolErrorCode(confinedFence(t, "echo x > .git/hooks/pre-commit", workspace, []string{workspace})); code != "E_PROTECTED_PATH" {
		t.Fatalf("a confined VCS-metadata write must stay blocked, got %v", code)
	}
	// 高危语义仍然拦截：fork 炸弹不碰文件系统，写过滤器看不见它。
	if code := toolErrorCode(confinedFence(t, ":(){ :|:& };:", workspace, []string{workspace})); code != "E_COMMAND_BLOCKED" {
		t.Fatalf("a confined fork bomb must stay blocked, got %v", code)
	}
}

// 删掉工作区根是内核唯一兜不住的自毁面：写根正是它的可写范围，所以「删自己」
// 在它眼里合法，而 delete 工具明确拒绝同一个目标。围栏必须自己说出这一条。
func TestConfinedCommandSafetyKeepsWorkspaceRootDelete(t *testing.T) {
	workspace := t.TempDir()
	extra := t.TempDir()
	roots := []string{workspace, extra}

	for _, cmd := range []string{"rm -rf .", "rm -rf " + filepath.ToSlash(workspace), "rm -rf ./", "rmdir ."} {
		if code := toolErrorCode(confinedFence(t, cmd, workspace, roots)); code != "E_DELETE_BLOCKED" {
			t.Fatalf("deleting the workspace root must stay blocked, %q got %v", cmd, code)
		}
	}
	// 附加工作区根同样是「自己」：它也是写根之一。
	if code := toolErrorCode(confinedFence(t, "rm -rf "+filepath.ToSlash(extra), workspace, roots)); code != "E_DELETE_BLOCKED" {
		t.Fatalf("deleting an extra workspace root must stay blocked, got %v", code)
	}
	// 窄检查只认「解析后就是写根」：工作区内的子目录照旧放行。
	for _, cmd := range []string{"rm -rf build", "rm -rf ./build", "rm -f notes.txt"} {
		if err := confinedFence(t, cmd, workspace, roots); err != nil {
			t.Fatalf("%q must stay allowed under confinement: %v", cmd, err)
		}
	}
}

// 全链路验证围栏让位：沙箱 enforce 下 rm 命令直接执行成功；越界写由内核拒绝且
// 模型能读到能照做的提示；VCS 元数据写仍然被围栏拦下。
func TestCommandFenceStandsDownUnderConfinement(t *testing.T) {
	requireConfinement(t)
	isolateSandboxHome(t)
	workspace := t.TempDir()
	outside := t.TempDir()
	t.Setenv("TMPDIR", t.TempDir())

	a := NewApp()
	if err := a.ensureInitialized(); err != nil {
		t.Fatal(err)
	}
	cfg := ConfigState{Workspace: workspace}
	ctx := context.Background()

	victim := filepath.Join(workspace, "build.log")
	if err := os.WriteFile(victim, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := a.runCommandWithConfig(ctx, cfg, CommandRequest{Command: "rm build.log"})
	if err != nil {
		t.Fatalf("a confined rm must execute instead of routing to the delete tool: %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("a confined rm inside the workspace must succeed, got %q", result.Output)
	}
	if _, statErr := os.Stat(victim); !os.IsNotExist(statErr) {
		t.Fatal("the confined rm must actually remove the file")
	}

	// 工作区根自毁是内核兜不住的那一档：写根正是它放行写的地方，所以「删掉自己」
	// 在内核眼里合法，只能由围栏说出不行（delete 工具对同一个目标也是拒的）。
	if _, err := a.runCommandWithConfig(ctx, cfg, CommandRequest{Command: "rm -rf ."}); toolErrorCode(err) != "E_DELETE_BLOCKED" {
		t.Fatalf("deleting the workspace root must stay blocked under confinement, got %v (%s)", err, toolErrorCode(err))
	}
	if _, statErr := os.Stat(workspace); statErr != nil {
		t.Fatalf("the workspace root must survive: %v", statErr)
	}

	// 越界写：围栏不预检（误报来源），内核当场拒绝，错误附上模型能照做的提示。
	target := filepath.Join(outside, "existing.txt")
	if err := os.WriteFile(target, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	refused, err := a.runCommandWithConfig(ctx, cfg, CommandRequest{
		Command: "echo pwn > " + filepath.ToSlash(target),
	})
	if err != nil {
		t.Fatalf("a kernel-refused write is a command result, not a tool error: %v", err)
	}
	if refused.ExitCode == 0 {
		t.Fatalf("an outside write must be refused by the kernel, got %q", refused.Output)
	}
	if !strings.Contains(refused.DeniedWriteHint, "沙箱已拦截") {
		t.Fatalf("the refusal must carry the model-facing hint, got %q", refused.DeniedWriteHint)
	}
	if data, readErr := os.ReadFile(target); readErr != nil || string(data) != "keep" {
		t.Fatalf("the refused write must leave the target untouched, got %q %v", data, readErr)
	}

	// VCS 元数据：内核的盲区，围栏照拦。
	if err := os.MkdirAll(filepath.Join(workspace, ".git", "hooks"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := a.runCommandWithConfig(ctx, cfg, CommandRequest{Command: "echo x > .git/hooks/pre-commit"}); toolErrorCode(err) != "E_PROTECTED_PATH" {
		t.Fatalf("a confined VCS-metadata write must stay blocked, got %v (%s)", err, toolErrorCode(err))
	}
}
