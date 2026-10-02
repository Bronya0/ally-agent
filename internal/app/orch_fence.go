// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
package app

// Section 2: Safety fence (was command_safety.go)
// App-owned home of the fence. Everything the fence decides at this layer lives
// here: the single command entrance (checkCommandSafetyAtCwd), the shared
// location lists every destructive entrance consults (delete / search / command
// target), the credential-store read guard, and the two-form path judgement all
// of them build on. Pure command parsing lives in internal/tools/command, pure
// path rules in internal/tools/pathutil, and the OS sandbox —— which owns its own
// boundary while attached —— in orch_sandbox*.go.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"ally-dev/internal/tools/command"
	"ally-dev/internal/tools/pathutil"
)

// firstWorkspaceRootDeleteTarget reports the first delete target that resolves
// to a writable root itself. The kernel's write filter cannot answer this: the
// roots are exactly what it permits writing to, so removing one is legal there,
// while the delete tool refuses the same target outright (planDeletePath). It
// is deliberately narrow — only a target that resolves to a root is named, so
// `rm -rf build` inside the workspace stays allowed.
func firstWorkspaceRootDeleteTarget(commandLine, workingDir string, roots []string) string {
	for _, target := range command.DeletePathTargets(commandLine) {
		path, ok := command.ResolveCommandLiteralPath(target, workingDir)
		if !ok {
			continue
		}
		for _, root := range roots {
			// 两侧都按真实路径比：macOS 上 $TMPDIR 是 /private/var 的链接，只解
			// 一侧会把同一个目录判成两个。字面形态再比一次做兜底。
			if samePath(path, root) || samePath(evalExistingPrefixOrSelf(path), evalExistingPrefixOrSelf(root)) {
				return filepath.ToSlash(path)
			}
		}
	}
	return ""
}

// evalExistingPrefixOrSelf resolves symlinks in the existing prefix of p and
// falls back to p itself when that cannot be done, so a comparison never loses
// a match just because one side could not be resolved.
func evalExistingPrefixOrSelf(p string) string {
	if resolved, err := evalExistingPrefix(p); err == nil {
		return resolved
	}
	return p
}

// checkCommandSafety resolves a request cwd before inspecting relative mutation
// targets. It remains a compatibility wrapper for tests and callers that do not
// already have the resolved cwd.
func checkCommandSafety(req CommandRequest, roots []string) error {
	workingDir := ""
	if len(roots) > 0 {
		workingDir = roots[0]
	}
	if strings.TrimSpace(req.Cwd) != "" {
		resolved, err := resolveCommandCwd(roots, req.Cwd)
		if err != nil {
			return err
		}
		workingDir = resolved
	}
	return checkCommandSafetyAtCwd(req, roots, workingDir)
}

// checkCommandSafetyAtCwd is THE command safety fence: every check the fence owns
// runs from here, in one place, and exactly one part of it depends on who owns
// the filesystem boundary — asked once, below, through the single judgement
// (kernelOwnsBoundary).
//
// Three checks are kernel-blind and always on: high-risk command semantics
// (curl|sh, privilege escalation) are not filesystem shapes any write filter can
// see; .git metadata sits inside the writable root, because git itself must write
// it; and deleting a whole workspace is legal to a boundary whose writable surface
// is exactly those roots.
//
// Two are lexical guesses about where a write lands, and only the fence owns them
// while no kernel does: routing an explicit delete through the delete tool, and
// refusing a literal target that already exists outside the workspace. A kernel
// confines those by resolved path, and those guesses were the false-positive
// machine the sandbox existed to retire — so they stand down while it is in
// charge. Attaching or detaching the sandbox (internal/sandbox) moves nothing but
// this judgement.
//
// roots[0] 是主工作区（命令的默认 cwd），其余为会话级附加根目录。
func checkCommandSafetyAtCwd(req CommandRequest, roots []string, workingDir string) error {
	cmd := req.Command
	kernelOwns := kernelOwnsBoundary()
	if kernelOwns {
		if root := firstWorkspaceRootDeleteTarget(cmd, workingDir, roots); root != "" {
			return codedToolError("E_DELETE_BLOCKED", fmt.Errorf("安全围栏已拦截：不允许删除工作区根目录。\n原因：删掉工作区根等于删掉整个项目，而 delete 工具明确拒绝同一个目标，所以这一档必须由围栏自己说。\n检测到的目标：%s\n处理方式：要删工作区里的具体条目请用 delete 工具（它逐条检查目标与递归范围）；确实要清空整个工作区，请手动在终端执行。\n被拦截的命令：%s", root, cmd))
		}
	} else if command.ContainsExplicitDeleteCommand(cmd) && !command.IsAllowedDeleteContext(cmd) {
		return codedToolError("E_COMMAND_BLOCKED", fmt.Errorf("安全围栏已拦截：command 不允许直接执行文件删除命令。\n原因：shell 删除命令可能绕过工作区边界、系统目录和 .git 保护。\n处理方式：请改用 delete 工具，由专用工具检查目标路径和递归范围。\n被拦截的命令：%s", cmd))
	}
	if risk := firstVCSMetadataMutationTarget(cmd, workingDir); risk != nil {
		return codedToolError("E_PROTECTED_PATH", fmt.Errorf("安全围栏已拦截：命令目标是版本控制元数据内的路径。\n原因：创建、覆盖或删除 .git/.svn/.hg 的元数据会损坏仓库，写入 hooks 更会在下次 git 命令时执行代码；目标在工作区内，所以「工作区外写入」那道检查不会拦它。\n检测到的目标：%s\n处理方式：版本控制状态请手动在终端变更。\n被拦截的命令：%s", risk.Path, cmd))
	}
	// 密钥/凭据位置一律禁读：这一道不分边界归属，围栏自己管还是内核管都要拦。
	if path, reason := firstSensitiveReadTarget(cmd, workingDir); path != "" {
		return codedToolError("E_PROTECTED_PATH", fmt.Errorf("安全围栏已拦截：命令要读的是密钥/凭据位置。\n原因：%s 属于凭据存储，读出来等于把私钥或令牌交进模型上下文（被注入的指令可以把它带走）。\n检测到的目标：%s\n处理方式：确需这些凭据请你自己手动执行；让 Ally 干活时用专门的工具（例如 ssh_cluster），不要读密钥文件。\n被拦截的命令：%s", reason, path, cmd))
	}
	if !kernelOwns {
		if risk := firstExistingOutsideMutationTarget(cmd, roots, workingDir); risk != nil {
			return codedToolError("E_PATH_OUTSIDE", fmt.Errorf("安全围栏已拦截：命令可能修改工作区外的受保护目标。\n原因：%s。\n检测到的目标：%s\n允许的操作：读取工作区外路径、写入 /dev/null 等空设备、创建不存在的新路径。\n禁止的操作：覆盖、追加、移动、改权限或以其他方式修改已经存在的工作区外文件或目录。\n可写范围：\n%s\n被拦截的命令：%s", risk.Reason, risk.Path, formatAllowedRoots(roots), cmd))
		}
	}
	if risk := command.MatchRiskPattern(cmd); risk != nil {
		return codedToolError("E_COMMAND_BLOCKED", fmt.Errorf("高危命令拒绝: 检测到%s - 命令已被安全围栏拦截。\n如需执行此操作，请手动在终端中执行。\n被拦截的命令: %s", risk.Reason, cmd))
	}
	return nil
}

// vcsMetadataMutationHit reports the form that makes a mutation target version
// control metadata — the path as written, or the path it resolves to — together
// with the reason to show; an empty reason means the target is allowed.
//
// Judging both forms in one place is the point: a symlink inside the workspace
// can point at .git (`ln -s .git gh` plus a write to `gh/hooks/pre-commit`), and
// the written form alone cannot show it. Every local entrance — write, delete,
// command target — comes through this function, so a new entrance cannot quietly
// skip the resolved form, which used to be a repository-corruption hole.
// resolved is what the caller already resolved for its containment check: the
// target itself for a write, and the parent when a symlink's *link* is what gets
// deleted. Remote entrances stay lexical on purpose — nothing resolves locally on
// the far side, so the helper re-checks with remote facts (contains_vcs).
//
// Callers that only refuse use the reason (it names the path themselves); the one
// that explains the refusal shows hit, because “written path resolves into .git”
// is the part the model needs to see.
func vcsMetadataMutationHit(abs, resolved string) (hit string, reason string) {
	if blocked, reason := pathutil.VCSMetadataReason(abs); blocked {
		return abs, reason
	}
	if samePath(resolved, abs) {
		return "", ""
	}
	if blocked, reason := pathutil.VCSMetadataReason(resolved); blocked {
		return resolved, reason
	}
	return "", ""
}

// firstVCSMetadataMutationTarget blocks commands whose statically resolvable
// operands touch version-control metadata. Targets inside the workspace pass the
// outside-write check by design, and .git used to have a delete guard only, so
// `> .git/hooks/pre-commit` (code execution on the next git command) and
// `> .git/index` (repository corruption) were both accepted.
func firstVCSMetadataMutationTarget(commandLine string, workingDir string) *outsideMutationRisk {
	for _, target := range command.LiteralWriteTargets(commandLine) {
		path, ok := command.ResolveCommandLiteralPath(target.Path, workingDir)
		if !ok {
			continue
		}
		if hit, _ := vcsMetadataMutationHit(path, evalExistingPrefixOrSelf(path)); hit != "" {
			return &outsideMutationRisk{Path: filepath.ToSlash(hit)}
		}
	}
	return nil
}

// firstSensitiveReadTarget reports the first operand of a command that names a
// credential store, with the reason to show (empty path means none did).
//
// Reads are the point here, so it cannot reuse the write-target helpers:
// `cat ~/.ssh/id_rsa` mutates nothing and is exactly what has to be refused.
// The judgement is lexical — the operand has to resolve to a literal path — and
// it follows a symlink when one resolves, so a clean-looking name inside the
// workspace cannot hide a key. Operands that only *use* a key (`ssh -i KEY`,
// `chmod 600 KEY`, `ls`) never reach this loop: the command package leaves them
// out, because using a key is not reading it.
func firstSensitiveReadTarget(commandLine, workingDir string) (string, string) {
	for _, operand := range command.DisclosingPathOperands(commandLine) {
		path, ok := command.ResolveCommandLiteralPath(operand, workingDir)
		if !ok {
			continue
		}
		if blocked, reason := blockedSensitiveRead(path); blocked {
			return filepath.ToSlash(path), reason
		}
	}
	return "", ""
}

// blockedSensitiveRead reports whether a resolved path — or what it points at —
// is a credential store. It is the single judgement shared by the command fence
// and the read / grep entrances, so one list covers every way the model can ask
// for the contents of a key file.
func blockedSensitiveRead(path string) (bool, string) {
	if blocked, reason := pathutil.SensitiveReadReason(pathRuntime, path); blocked {
		return true, reason
	}
	if resolved, err := evalExistingPrefix(path); err == nil && !samePath(resolved, path) {
		return pathutil.SensitiveReadReason(pathRuntime, resolved)
	}
	return false, ""
}

// outsideMutationRisk describes an outside-write risk that the UI can explain
// directly. Creating a new literal outside path is allowed, while changing an
// existing path or using an unresolved redirection target remains blocked.
// Harmless sinks such as /dev/null are ignored.
type outsideMutationRisk struct {
	Path   string
	Reason string
}

func firstExistingOutsideMutationTarget(commandLine string, roots []string, workingDir string) *outsideMutationRisk {
	if len(roots) == 0 {
		return nil
	}
	if strings.TrimSpace(workingDir) == "" {
		workingDir = roots[0]
	}
	for _, target := range command.LiteralWriteTargets(commandLine) {
		reason := "命令的写入目标已经存在于工作区外，继续执行可能修改其内容或元数据"
		if target.Kind == command.WriteTargetRedirection {
			reason = "重定向目标已经存在，继续执行可能覆盖或追加其内容"
		}
		if risk := inspectCommandMutationTarget(target.Path, workingDir, roots, reason); risk != nil {
			return risk
		}
	}
	return nil
}

func inspectCommandMutationTarget(target, workingDir string, roots []string, existingReason string) *outsideMutationRisk {
	path, ok := command.ResolveCommandLiteralPath(target, workingDir)
	if !ok {
		// 动态目标（变量/通配符/命令替换/heredoc 内容）无法静态解析：
		// 宽松策略下放行，避免对合法复杂命令误判；字面外部已存在目标仍拦截。
		return nil
	}

	resolvedPath := path
	if resolved, err := evalExistingPrefix(path); err == nil {
		resolvedPath = resolved
	}
	lexicallyAllowed := insideAnyRoot(roots, path) || insideAllyAgentDir(path)
	resolvedAllowed := insideWriteRoot(roots, resolvedPath)
	if lexicallyAllowed && !resolvedAllowed {
		return &outsideMutationRisk{
			Path:   filepath.ToSlash(resolvedPath),
			Reason: "命令目标通过工作区内的符号链接解析到允许根目录之外",
		}
	}
	if resolvedAllowed {
		return nil
	}
	if command.PathExists(resolvedPath) || command.PathExists(path) {
		return &outsideMutationRisk{
			Path:   filepath.ToSlash(resolvedPath),
			Reason: existingReason,
		}
	}
	return nil
}

func validateRemoteCommandSafety(cmd string) error {
	if risk := command.MatchRiskPattern(cmd); risk != nil {
		return codedToolError("E_COMMAND_BLOCKED", fmt.Errorf("高危命令拒绝: 检测到%s - 命令已被安全围栏拦截。\n如需执行此操作，请手动在终端中执行。\n被拦截的命令: %s", risk.Reason, cmd))
	}
	return nil
}

// ── 受保护位置清单（删除 / 搜索 / 命令目标共用） ──

// protectedLocation 是系统里一处受保护的位置：登记一次，两个守卫按标记取用，
// 不再各存一份清单。形态统一为 normalizeSystemPath 的规范化形式（小写、去盘符、
// 正斜杠开头），所以同一张表在三个平台上都成立：C:\Windows 与 \\?\C:\Windows\x
// 命中同一项，macOS 大小写不敏感的 /system 也不会漏掉 /System 那一项。
//
// 两个标记互不蕴含，因为两者的判据本就不同：
//   - remove：删除（delete 工具、命令的删除目标）保护它及其整棵子树。这里只登记
//     「深度守卫兜不住」的目标 —— 第 1/2 层目录由 isDangerousDeletePath 的层级规则
//     负责，所以 /etc、/var、/opt、/root 这类整树不标 remove：深层项目文件
//     （/etc/nginx/conf.d/x.conf、/root/proj/app.py）是允许删的，整树登记会误杀。
//   - scan：工作区之外的 grep / 列目录把它当危险根（树太大、扫全盘）。搜索没有
//     「深度」概念，一级系统树必须逐个点名，所以 /etc、/var、/opt、/root 在这里出现。
type protectedLocation struct {
	path   string
	remove bool
	scan   bool
}

// 删除保护机制（单一来源，全平台与本地/远程统一）：
// 机制 1：基于系统/全盘绝对视角的层级深度守卫（禁止删除盘根、1级和2级骨干目录）；
// 机制 2：跨平台通用的敏感文件与深层系统目录黑名单（包含设备、内核接口、二进制目录、敏感认证文件等）。
// 两个机制都建立在 normalizeSystemPath 之上：卷标必须在转正斜杠之前按原生形式裁掉，否则
// 形如 \\?\C:\Users\alice 的写法会整段留下、把深度多算一层而绕开机制 1。
var protectedLocations = []protectedLocation{
	// 1. Linux & macOS 设备接口、内核虚拟文件系统与底层
	{path: "/dev", remove: true, scan: true},
	{path: "/proc", remove: true, scan: true},
	{path: "/sys", remove: true, scan: true},
	{path: "/boot", remove: true, scan: true},
	{path: "/lost+found", remove: true},

	// 2. 关键系统二进制与动态库
	{path: "/bin", remove: true, scan: true},
	{path: "/sbin", remove: true, scan: true},
	{path: "/lib", remove: true, scan: true},
	{path: "/lib32", remove: true},
	{path: "/lib64", remove: true, scan: true},
	{path: "/libx32", remove: true},
	{path: "/usr/bin", remove: true},
	{path: "/usr/sbin", remove: true},
	{path: "/usr/lib", remove: true},
	{path: "/usr/lib64", remove: true},
	// /usr/share 与 /usr/local/lib 不是二进制目录，但同属发行版/第三方安装内容：
	// 删掉会把 man、locale、启动脚本与已装库一起连根拔掉。
	{path: "/usr/share", remove: true},
	{path: "/usr/local/lib", remove: true},
	{path: "/usr/local/bin", remove: true},
	{path: "/usr/local/sbin", remove: true},
	{path: "/var/local/libs", remove: true},

	// 3. 核心敏感凭据与认证配置（精确保护关键文件，避免一刀切整树封死 /etc）
	{path: "/etc/shadow", remove: true},
	{path: "/etc/sudoers", remove: true},
	{path: "/etc/passwd", remove: true},
	{path: "/etc/group", remove: true},
	{path: "/etc/fstab", remove: true},
	{path: "/etc/crypttab", remove: true},
	{path: "/etc/ssh", remove: true},
	{path: "/etc/pam.d", remove: true},
	{path: "/etc/security", remove: true},

	// 3.1 /etc 直属文件：机制 1 只拦第 2 层的“目录”，这些文件本身落在它的覆盖
	//     之外，必须逐个点名（旧实现靠 /etc 整树拦截，改成“层级 + 名单”后漏掉了
	//     它们，见 TestUnifiedDeleteProtectionRules）。
	{path: "/etc/hosts", remove: true},
	{path: "/etc/resolv.conf", remove: true},
	{path: "/etc/nsswitch.conf", remove: true},
	{path: "/etc/hostname", remove: true},
	{path: "/etc/environment", remove: true},
	{path: "/etc/profile", remove: true},
	{path: "/etc/bash.bashrc", remove: true},
	{path: "/etc/ld.so.conf", remove: true},
	{path: "/etc/shells", remove: true},
	{path: "/etc/machine-id", remove: true},

	// 4. macOS 关键系统目录
	{path: "/system", remove: true, scan: true},
	{path: "/library", remove: true, scan: true},
	{path: "/applications", remove: true, scan: true},
	{path: "/cores", remove: true},
	{path: "/private/etc", remove: true},
	{path: "/private/var", remove: true},

	// 5. Windows 关键系统目录与引导文件（去除盘符后的相对根形式）
	{path: "/windows", remove: true, scan: true},
	{path: "/windows.old", remove: true},
	{path: "/program files", remove: true, scan: true},
	{path: "/program files (x86)", remove: true, scan: true},
	{path: "/programdata", remove: true},
	{path: "/system volume information", remove: true},
	{path: "/$recycle.bin", remove: true},
	{path: "/recovery", remove: true},
	{path: "/perflogs", remove: true},
	{path: "/documents and settings", remove: true},
	{path: "/config.msi", remove: true},
	{path: "/$windows.~bt", remove: true},
	{path: "/$windows.~ws", remove: true},
	{path: "/$winreagent", remove: true},
	{path: "/$sysreset", remove: true},
	{path: "/bootmgr", remove: true},
	{path: "/bootsect.bak", remove: true},
	{path: "/msocache", remove: true},
	{path: "/inetpub", remove: true},

	// 6. 只参与搜索判断的一级系统树。删除侧由层级规则（根、1/2 级目录）加精确
	//    名单负责，整树登记会把深层项目文件一起误杀。
	{path: "/etc", scan: true},
	{path: "/usr", scan: true},
	{path: "/var", scan: true},
	{path: "/opt", scan: true},
	{path: "/root", scan: true},
}

// removableProtectedTargets 是登记了删除保护的位置，删除守卫与远端 helper 共用
// 这一份（远端 helper 自己实现层级规则与临时目录豁免，只吃这份名单）。
func removableProtectedTargets() []string {
	targets := make([]string, 0, len(protectedLocations))
	for _, loc := range protectedLocations {
		if loc.remove {
			targets = append(targets, loc.path)
		}
	}
	return targets
}

// driveVolume 返回路径里的“盘符型”卷标（C:、\\?\C:、\\.\C:），统一小写；
// 没有（POSIX 路径、UNC 共享）则返回空串。
// 必须按 Windows 原生反斜杠形式取得：filepath.VolumeName 返回的就是这种形态，
// 若先转正斜杠再裁剪，\\?\C:\... 、\\.\C:\... 这类写法整段裁不掉，路径会被多算
// 一到两层深度，从而绕开 1/2 级守卫（实测 \\?\C:\Users\alice 曾被整段放行）。
// UNC（\\server\share）刻意不裁：共享根不是本机系统根，把它当盘根会让网络工作区里
// 正常的二级目录（\\nas\work\proj 的子目录）被误拒。
func driveVolume(nativePath string) string {
	volume := strings.ToLower(filepath.VolumeName(nativePath))
	if volume == "" {
		return ""
	}
	tail := volume
	for _, prefix := range []string{`\\?\`, `\\.\`} {
		tail = strings.TrimPrefix(tail, prefix)
	}
	if len(tail) == 2 && tail[1] == ':' {
		return volume
	}
	return ""
}

func normalizeSystemPath(p string) string {
	cleaned := strings.ToLower(filepath.Clean(p))
	if volume := driveVolume(cleaned); volume != "" {
		cleaned = strings.TrimPrefix(cleaned, volume)
	}
	lower := strings.TrimRight(filepath.ToSlash(cleaned), "/")
	if lower == "" {
		return "/"
	}
	return lower
}

func isInsideTempDir(abs string) bool {
	norm := normalizeSystemPath(abs)
	// "/tmp"、"/var/tmp" 是 POSIX 临时根的字面量。盘符路径（Windows 的 C:\tmp）只是
	// 恰好同名，不能按它们整体豁免 —— Windows 真正的临时根是 os.TempDir()，由下面
	// 那段负责；否则 C:\tmp\x 会把整条保护链一次性让开。
	for _, tmpPrefix := range []string{"/tmp", "/var/tmp"} {
		if driveVolume(abs) != "" {
			break
		}
		if norm == tmpPrefix {
			return false // /tmp 本身不可删
		}
		if strings.HasPrefix(norm, tmpPrefix+"/") {
			return true
		}
	}
	if tmp := os.TempDir(); tmp != "" {
		// 同卷才比较：C:\...\Temp 与 D:\...\Temp 规范化后是同一个字符串，跨盘比较
		// 会把另一个盘上的同名目录误判成临时目录。
		if tmpVolume := driveVolume(tmp); tmpVolume == "" || tmpVolume == driveVolume(abs) {
			cleanTmp := normalizeSystemPath(tmp)
			if norm == cleanTmp {
				return false // 临时根目录本身不可删
			}
			if cleanTmp != "/" && strings.HasPrefix(norm, cleanTmp+"/") {
				return true
			}
		}
	}
	return false
}

func isLikelyDirectory(abs string) bool {
	if info, err := os.Lstat(abs); err == nil {
		return info.IsDir()
	}
	// 对于磁盘上不存在的路径（如静态判定或单测中的路径字符串）：无扩展名者按目录处理（默认保守防护）
	return filepath.Ext(abs) == ""
}

// isDangerousDeletePath returns (blocked, reason). Blocks paths that are
// OS-protected locations, home roots, VCS metadata, workspace root, and any
// directory inside the Ally data directory.
func isDangerousDeletePath(absPath string) (bool, string) {
	abs := filepath.Clean(absPath)

	// 1. VCS metadata — never delete .git or similar, nor anything below it.
	if blocked, reason := pathutil.VCSMetadataReason(abs); blocked {
		return true, reason
	}

	// 2. Ally Agent data directory：目录本身永不可删；其子树只允许「单文件」
	// 删除（记忆笔记、缓存文件），目录一律禁止——delete 工具对目录本就强制
	// recursive=true（IsDir && !Recursive 会被拒），所以用 isLikelyDirectory
	// 判定就等价于用调用方的 recursive 标志，无需把标志透传到每一层。
	//
	// 词法与解析后两种形态都要判：调用方传进来的是词法路径，而工作区内的符号
	// 链接可以让它指向数据目录（`ln -s ~/.ally_agent link` 之后
	// `delete link/histories recursive`）。围栏只管「解析后是否还在允许根内」，
	// 而数据目录本身就在写白名单里，所以这道守卫是唯一能拦它的地方。
	if allyDir, err := filepath.Abs(appDataDir()); err == nil {
		dirs := []string{allyDir}
		if resolvedDir, rErr := filepath.EvalSymlinks(allyDir); rErr == nil && !samePath(resolvedDir, allyDir) {
			dirs = append(dirs, resolvedDir)
		}
		candidates := []string{abs}
		if resolved, rErr := evalExistingPrefix(abs); rErr == nil && !samePath(resolved, abs) {
			candidates = append(candidates, resolved)
		}
		for _, candidate := range candidates {
			for _, dir := range dirs {
				if samePath(candidate, dir) {
					return true, fmt.Sprintf("refusing to delete Ally data directory %q", candidate)
				}
				if insideRoot(dir, candidate) && isLikelyDirectory(candidate) {
					return true, fmt.Sprintf("refusing to delete directory %q inside the Ally data directory; delete individual files instead, or remove it manually outside the agent", candidate)
				}
			}
		}
	}

	// 3. 临时目录豁免：临时工作区与编译构建目录允许清理（但临时根目录本身如 /tmp 仍受保护）
	if isInsideTempDir(abs) {
		return false, ""
	}

	norm := normalizeSystemPath(abs)
	if norm == "" || norm == "/" {
		return true, fmt.Sprintf("refusing to delete filesystem root %q", abs)
	}

	// ── 机制一：全盘视角 1 级、2 级目录绝对禁止删除 ──
	parts := strings.FieldsFunc(strings.Trim(norm, "/"), func(r rune) bool { return r == '/' })
	depth := len(parts)
	isDir := isLikelyDirectory(abs)

	if depth == 1 {
		return true, fmt.Sprintf("refusing to delete top-level filesystem directory %q", abs)
	}
	if depth == 2 && isDir {
		return true, fmt.Sprintf("refusing to delete second-level filesystem directory %q", abs)
	}

	// ── 机制二：跨平台通用敏感文件与深层目录黑名单 ──
	for _, target := range removableProtectedTargets() {
		if insideRoot(target, norm) {
			return true, fmt.Sprintf("refusing to delete protected system target %q (%s)", abs, target)
		}
	}

	return false, ""
}

// isDangerousSearchRoot returns (blocked, reason). Blocks grep/list operations
// that would traverse system directories, home directories, or other high-risk paths.
func isDangerousSearchRoot(absPath string) (bool, string) {
	abs := filepath.Clean(absPath)
	lower := strings.ToLower(abs)

	if insideAllyAgentDir(abs) {
		return false, ""
	}

	// 1. Root paths — too broad
	if abs == "/" || lower == `c:\` || lower == `c:` {
		return true, fmt.Sprintf("refusing to search from root %q; this would scan the entire filesystem. Specify a project subdirectory instead", abs)
	}

	// Test and temporary workspaces commonly live below /var on macOS.
	if tmp := os.TempDir(); tmp != "" && insideRoot(tmp, abs) {
		return false, ""
	}

	// 2. 系统位置：与删除守卫共用一张表，这里只取 scan 那一列。判据走规范化形态，
	//    所以大小写不同的写法（macOS 上的 /system）和带盘符/长路径前缀的 Windows
	//    写法（\\?\C:\Windows\x）都能命中同一项。
	norm := normalizeSystemPath(abs)
	for _, loc := range protectedLocations {
		if !loc.scan {
			continue
		}
		if insideRoot(loc.path, norm) {
			return true, fmt.Sprintf("refusing to search system directory %q; this path is outside the project scope", abs)
		}
	}

	// 4. Home directories — too broad
	if homeDir, err := os.UserHomeDir(); err == nil {
		cleanHome := filepath.Clean(homeDir)
		if abs == cleanHome {
			return true, fmt.Sprintf("refusing to search from home directory %q; this would scan personal files. Specify a project subdirectory", abs)
		}
	}

	return false, ""
}
