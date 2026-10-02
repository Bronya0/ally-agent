// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.

package app

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

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"ally-dev/internal/sandbox"
)

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
