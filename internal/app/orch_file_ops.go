// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
package app

// Section: File ops orchestration (was in app.go)
// App-owned create/delete/run-command orchestration plus the shell detection
// helpers and destructive-path safety guards that those operations share.
//
// 多路径删除的共用规则也在这里：本地 delete 与 remote_delete_path 共用的
// path/paths 折叠、条数上限、重复与包含判定（单条路径的落盘判定仍留在各自信任域）。

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"sync"
	"time"

	"ally-dev/internal/sandbox"
	"ally-dev/internal/tools/pathutil"
	toolshared "ally-dev/internal/tools/shared"
)

func (a *App) createFileWithConfig(cfg ConfigState, req CreateFileRequest) (EditResult, error) {
	if strings.TrimSpace(req.Path) == "" {
		return EditResult{}, codedToolError("E_BAD_PATH", errors.New("create requires a non-empty path"))
	}
	roots, err := workspaceRoots(cfg)
	if err != nil {
		return EditResult{}, err
	}
	spec := fileMutationSpec(cfg, roots)
	path, err := resolveWritableFilePath(roots, req.Path)
	if err != nil {
		return EditResult{}, err
	}
	// Record which parent directories MkdirAll actually needs to create so the
	// result can report them without a follow-up list_files. The walk must run
	// before MkdirAll, while the missing ancestors still exist as such.
	createdDirs := newlyCreatedDirs(filepath.Dir(path))
	if err := sandboxedMkdirAll(spec, roots, filepath.Dir(path)); err != nil {
		return EditResult{}, err
	}
	path, err = resolveWritableFilePath(roots, req.Path)
	if err != nil {
		return EditResult{}, err
	}

	before := []byte{}
	beforeHash := ""
	beforeVersion := ""
	perm := os.FileMode(0o644)
	exists := false
	if info, err := os.Lstat(path); err == nil {
		exists = true
		if info.Mode()&os.ModeSymlink != 0 {
			return EditResult{}, symlinkWriteError(req.Path)
		}
		if info.IsDir() {
			return EditResult{}, codedToolError("E_TARGET_IS_DIRECTORY", fmt.Errorf("path is a directory: %s", req.Path))
		}
		if !req.Overwrite {
			return EditResult{}, codedToolError("E_EXISTS", fmt.Errorf("file already exists: %s", req.Path))
		}
		before, _, err = readTextFile(path)
		if err != nil {
			return EditResult{}, codedToolError("E_TEXT_OVERWRITE", fmt.Errorf("refusing to overwrite non-text or unreadable file %s: %w", req.Path, err))
		}
		beforeHash, beforeVersion = hashBytesAndVersion(before)
		perm = info.Mode().Perm()
	} else if !errors.Is(err, os.ErrNotExist) {
		return EditResult{}, err
	}

	content, ending, hadBOM := normalizeText([]byte(req.Content))
	encoded := encodeText(content, ending, hadBOM)
	if req.Overwrite {
		if err := sandboxedWriteFile(spec, roots, path, encoded, perm); err != nil {
			return EditResult{}, err
		}
	} else {
		if err := sandboxedWriteNewFile(spec, roots, path, encoded, perm); err != nil {
			return EditResult{}, err
		}
	}
	// The after content is exactly what we just wrote (encoded). Re-reading
	// the file would only repeat the IO and normalization work we already
	// did; for overwrite paths the bytes on disk are byte-identical to
	// encoded because safeWriteFileWithDir writes encoded verbatim, and for
	// new files safeWriteNewFile does the same. Using encoded directly also
	// avoids a second hash pass on the same content.
	result := makeEditResult(req.Path, beforeHash, beforeVersion, before, encoded, ending, 1, string(before), content)
	created := !exists
	result.Created = &created
	if len(createdDirs) > 0 {
		result.CreatedDirs = createdDirsToDisplay(roots, createdDirs)
	}
	if created {
		result.Summary = fmt.Sprintf("%s created: %d bytes", filepath.ToSlash(req.Path), len(encoded))
	}
	return result, nil
}

// createDirectoryWithConfig creates a directory (with any missing parents)
// inside the writable roots. The path boundary checks mirror createFileWithConfig:
// safeJoin confines the target to a root, symlink components are rejected, and
// an existing path at the target fails with E_EXISTS instead of being reused.
func (a *App) createDirectoryWithConfig(cfg ConfigState, req CreateDirectoryRequest) error {
	if strings.TrimSpace(req.Path) == "" {
		return codedToolError("E_BAD_PATH", errors.New("create directory requires a non-empty path"))
	}
	roots, err := workspaceRoots(cfg)
	if err != nil {
		return err
	}
	spec := fileMutationSpec(cfg, roots)
	path, err := resolveWritableFilePath(roots, req.Path)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(path); err == nil {
		// resolveWritableFilePath already rejected symlink targets; anything
		// else that exists collides with the new directory.
		return codedToolError("E_EXISTS", fmt.Errorf("path already exists: %s", req.Path))
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return sandboxedMkdirAll(spec, roots, path)
}

// cleanRenameSegment validates a rename target name. A rename keeps the entry in
// its parent directory, so the name must be a bare segment: no separators, no
// "." / "..", no NUL or other control character. The Windows-only rules are
// added there because the OS would silently rewrite such a name (trailing dots
// and spaces are dropped, the reserved characters are rejected), leaving the
// dialog's name and the name on disk out of sync.
func cleanRenameSegment(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	if name == "" {
		return "", codedToolError("E_BAD_PATH", errors.New("rename requires a non-empty name"))
	}
	if name == "." || name == ".." || strings.ContainsAny(name, `/\`) || strings.ContainsRune(name, 0) {
		return "", codedToolError("E_BAD_PATH", fmt.Errorf("rename target must be a bare file name: %q", raw))
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			return "", codedToolError("E_BAD_PATH", fmt.Errorf("rename target must not contain control characters: %q", raw))
		}
	}
	if pathutil.IsWindows {
		if strings.ContainsAny(name, `<>:"|?*`) {
			return "", codedToolError("E_BAD_PATH", fmt.Errorf("rename target contains characters Windows rejects: %q", raw))
		}
		if strings.TrimRight(name, ". ") != name {
			return "", codedToolError("E_BAD_PATH", fmt.Errorf("rename target must not end with a dot or space: %q", raw))
		}
	}
	return name, nil
}

// renamePathWithConfig renames a file or directory in place and returns the new
// workspace-relative slash path. A rename never moves an entry to another
// directory: the name is validated as a bare segment, so the destination is
// always the source's parent plus that name. The source goes through
// resolveDeletablePath (must exist, refuses VCS metadata) and the destination
// through resolveWritableFilePath (the same guards create uses), so renaming can
// not become a way around either side's guard.
func (a *App) renamePathWithConfig(cfg ConfigState, req RenamePathRequest) (string, error) {
	name, err := cleanRenameSegment(req.NewName)
	if err != nil {
		return "", err
	}
	// 树节点路径固定是工作区内相对路径（/ 分隔）；Windows 上把客户端可能给出的
	// 反斜杠一并归一（Linux 上 \ 是合法文件名字符，filepath.ToSlash 不会动它）。
	rel := path.Clean(filepath.ToSlash(strings.TrimSpace(req.Path)))
	if rel == "" || rel == "." || rel == ".." || path.IsAbs(rel) || strings.HasPrefix(rel, "../") {
		return "", codedToolError("E_BAD_PATH", errors.New("rename requires a workspace-relative path"))
	}
	roots, err := workspaceRoots(cfg)
	if err != nil {
		return "", err
	}
	source, err := resolveDeletablePath(roots, rel)
	if err != nil {
		return "", err
	}
	targetRel := path.Join(path.Dir(rel), name)
	target, err := resolveWritableFilePath(roots, targetRel)
	if err != nil {
		return "", err
	}
	// 名称与大小写都没变：直接当成功空操作，不去让 os.Rename 处理“改成自己”
	// 这种平台相关的边界情况。
	if pathutil.SamePath(source, target) && filepath.Base(source) == filepath.Base(target) {
		return targetRel, nil
	}
	sourceInfo, err := os.Lstat(source)
	if err != nil {
		return "", err
	}
	if targetInfo, lstatErr := os.Lstat(target); lstatErr == nil {
		// 目标已存在即拒绝：改名不覆盖（与 create 的 overwrite=false 同语义）。
		// 例外是同一个文件本身——Windows/macOS 上只改大小写时 Lstat 到的目标
		// 就是源文件，必须放行，否则文件名大小写永远改不了。
		if !os.SameFile(sourceInfo, targetInfo) {
			return "", codedToolError("E_EXISTS", fmt.Errorf("path already exists: %s", targetRel))
		}
	} else if !errors.Is(lstatErr, os.ErrNotExist) {
		return "", lstatErr
	}
	if err := sandboxedRename(fileMutationSpec(cfg, roots), roots, source, target); err != nil {
		return "", err
	}
	return targetRel, nil
}

// newlyCreatedDirs walks up from dir until it finds an existing ancestor and
// returns the chain of directories that MkdirAll would create, ordered from the
// outermost to the innermost. Returns nil when dir already exists.
func newlyCreatedDirs(dir string) []string {
	var missing []string
	cur := filepath.Clean(dir)
	for {
		if _, err := os.Lstat(cur); err == nil {
			break
		}
		missing = append(missing, cur)
		parent := filepath.Dir(cur)
		if parent == cur {
			break
		}
		cur = parent
	}
	for i, j := 0, len(missing)-1; i < j; i, j = i+1, j-1 {
		missing[i], missing[j] = missing[j], missing[i]
	}
	return missing
}

// createdDirsToDisplay converts an absolute created-dir chain (outermost
// first) to workspace-relative paths when it lies inside the primary workspace
// (roots[0]), falling back to absolute slash paths for extra-root writes.
func createdDirsToDisplay(roots []string, dirs []string) []string {
	out := make([]string, len(dirs))
	abs := func() {
		for i, d := range dirs {
			out[i] = filepath.ToSlash(d)
		}
	}
	if len(roots) == 0 {
		abs()
		return out
	}
	if rel, err := filepath.Rel(roots[0], dirs[0]); err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		abs()
		return out
	}
	for i, d := range dirs {
		if r, err := filepath.Rel(roots[0], d); err == nil {
			out[i] = filepath.ToSlash(r)
		} else {
			out[i] = filepath.ToSlash(d)
		}
	}
	return out
}

// copyFilesIntoWorkspaceWithConfig copies absolute source paths dropped from
// the system file manager into a workspace directory. The destination is
// confined to the writable roots via the same resolveWritableFilePath check
// the other file ops use. Name conflicts never overwrite: the copy gets a
// "name (N)" suffix like desktop file managers. Each source is independent —
// one failure is reported per-source and does not abort the rest.
func (a *App) copyFilesIntoWorkspaceWithConfig(cfg ConfigState, req CopyFilesIntoWorkspaceRequest) (CopyFilesIntoWorkspaceResult, error) {
	roots, err := workspaceRoots(cfg)
	if err != nil {
		return CopyFilesIntoWorkspaceResult{}, err
	}
	targetDir := strings.TrimSpace(req.TargetDir)
	targetAbs := roots[0]
	if targetDir != "" {
		abs, err := resolveWritableFilePath(roots, targetDir)
		if err != nil {
			return CopyFilesIntoWorkspaceResult{}, err
		}
		info, err := os.Stat(abs)
		if err != nil {
			return CopyFilesIntoWorkspaceResult{}, codedToolError("E_PATH_NOT_FOUND", err)
		}
		if !info.IsDir() {
			return CopyFilesIntoWorkspaceResult{}, codedToolError("E_BAD_PATH", fmt.Errorf("drop target is not a directory: %s", targetDir))
		}
		targetAbs = abs
	}
	result := CopyFilesIntoWorkspaceResult{TargetDir: filepath.ToSlash(targetDir)}
	for _, src := range req.Sources {
		src = strings.TrimSpace(src)
		if src == "" {
			continue
		}
		destRel, err := copyDroppedSource(src, targetAbs, roots[0])
		if err != nil {
			result.Failed = append(result.Failed, CopyFileFailure{Source: src, Error: err.Error()})
			continue
		}
		result.Copied = append(result.Copied, destRel)
	}
	return result, nil
}

// copyDroppedSource copies one dropped file or directory tree into targetAbs
// and returns the workspace-relative destination for the tree refresh.
func copyDroppedSource(src, targetAbs, primaryRoot string) (string, error) {
	if !filepath.IsAbs(src) {
		return "", codedToolError("E_BAD_PATH", fmt.Errorf("source path must be absolute: %s", src))
	}
	srcAbs := filepath.Clean(src)
	info, err := os.Lstat(srcAbs)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", codedToolError("E_PATH_NOT_FOUND", err)
		}
		return "", err
	}
	if !info.Mode().IsRegular() && !info.IsDir() {
		return "", codedToolError("E_BAD_PATH", fmt.Errorf("source is not a regular file or directory: %s", src))
	}
	// Copying a directory into itself (or into its own subtree) would walk a
	// tree that grows while it is being read; reject like desktop managers do.
	if info.IsDir() && insideRoot(srcAbs, targetAbs) {
		return "", codedToolError("E_BAD_PATH", fmt.Errorf("cannot copy a directory into itself: %s", src))
	}
	destAbs := nonConflictingDestPath(targetAbs, filepath.Base(srcAbs))
	if info.IsDir() {
		if err := copyDroppedTree(srcAbs, destAbs); err != nil {
			return "", err
		}
	} else if err := copyDroppedFile(srcAbs, destAbs, info.Mode().Perm()); err != nil {
		return "", err
	}
	if rel, err := filepath.Rel(primaryRoot, destAbs); err == nil {
		return filepath.ToSlash(rel), nil
	}
	return filepath.ToSlash(destAbs), nil
}

// nonConflictingDestPath joins dir/name and, when that path already exists,
// appends " (N)" before the extension (mirroring desktop file managers) until
// it finds a free name. The result is always a fresh path, so the O_EXCL
// creates in copyDroppedFile never collide.
func nonConflictingDestPath(dir, name string) string {
	candidate := filepath.Join(dir, name)
	if _, err := os.Lstat(candidate); err != nil {
		return candidate
	}
	ext := filepath.Ext(name)
	base := strings.TrimSuffix(name, ext)
	for i := 2; ; i++ {
		candidate = filepath.Join(dir, fmt.Sprintf("%s (%d)%s", base, i, ext))
		if _, err := os.Lstat(candidate); err != nil {
			return candidate
		}
	}
}

// copyDroppedTree recursively copies a dropped directory. Symlinks and other
// non-regular entries inside the tree are skipped: following a link out of
// the dropped tree would copy unexpected content.
func copyDroppedTree(srcDir, dstDir string) error {
	return filepath.WalkDir(srcDir, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(srcDir, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dstDir, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		return copyDroppedFile(p, target, info.Mode().Perm())
	})
}

// copyDroppedFile streams one regular file. The destination was just chosen
// as a non-existing path, so O_EXCL fails loudly if anything raced us.
func copyDroppedFile(src, dst string, perm os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, perm)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// deletePathsWithConfig 删除一次调用里的全部路径，语义与远端 remote_delete_path
// 逐条对齐：第一遍把每条路径都判定完（含目录树统计），任何一条不通过就整批不动手；
// 第二遍才逐个删，单条失败只污染它自己的结果槽（与批量 read 的隔离契约一致）。
//
// paths 由调用方经 resolveDeletePathList 归一：两种写法与条数上限只在那里判一次，
// 所以本地与远端、工具调用与 Wails API 看到的是同一条候选列表。
func (a *App) deletePathsWithConfig(cfg ConfigState, paths []string, recursive bool) (DeletePathsResult, error) {
	if len(paths) == 0 {
		return DeletePathsResult{}, codedToolError("E_BAD_ARGS", errors.New("delete requires a path or a non-empty paths list"))
	}
	roots, err := workspaceRoots(cfg)
	if err != nil {
		return DeletePathsResult{}, err
	}
	spec := fileMutationSpec(cfg, roots)
	planned := make([]plannedDeletePath, 0, len(paths))
	targets := make([]fileMutationTarget, 0, len(paths))
	for _, requestPath := range paths {
		item, err := planDeletePath(spec, roots, requestPath, recursive)
		if err != nil {
			return DeletePathsResult{}, err
		}
		planned = append(planned, item)
		targets = append(targets, fileMutationTarget{localMutationKey(item.absPath), filepath.ToSlash(requestPath)})
	}
	// 与前缀表（app 与 app-backup）不同，这里的包含关系真的会让结果取决于顺序，
	// 所以整批拒掉，让模型自己决定删哪个。
	if err := checkDeletePathList(targets); err != nil {
		return DeletePathsResult{}, err
	}

	result := DeletePathsResult{Paths: make([]DeleteResult, 0, len(planned))}
	for _, item := range planned {
		item.result.OK = true
		if item.absent {
			// 本来就不存在：没东西可删，也没出事。单独记一笔（既不算成功也不
			// 算失败），同批其它路径照删。
			result.AbsentCount++
			result.Paths = append(result.Paths, item.result)
			continue
		}
		removeErr := sandboxedRemove(spec, roots, item.absPath, recursive && item.isDir)
		if removeErr != nil {
			// 失败时清零统计：RemoveAll 可能已经删掉一部分，报出的数字只会误导
			// 模型；resolvedPath 留在结果里供它自己复查剩余状态。
			item.result.OK = false
			item.result.Error = removeErr.Error()
			item.result.ErrorCode = toolErrorCode(removeErr)
			item.result.RemovedFiles = 0
			item.result.RemovedDirs = 0
			item.result.RemovedBytes = 0
			result.FailedCount++
		} else {
			result.DeletedCount++
		}
		result.Paths = append(result.Paths, item.result)
	}
	return result, nil
}

// countDeleteStats 判断这次删除的目标要不要先把整棵树统计一遍。统计只服务成功
// 报告，而边界归内核时写根之外的目标不可能是成功的：对一棵大目录（家目录、
// /var 之类）先 WalkDir 完再等内核拒绝，等于白等几分钟。写根内、以及内核确实
// 放行的位置（临时目录、工具链缓存）照旧精确计数。
func countDeleteStats(spec sandbox.Spec, roots []string, path string) bool {
	if !kernelOwnsBoundary() {
		return true
	}
	resolved, err := evalExistingPrefix(path)
	if err != nil {
		return false
	}
	return insideWriteRoot(roots, resolved) || sandbox.AllowsWrite(spec, resolved)
}

// plannedDeletePath 是「已判定、尚未动手」的一条删除目标：判定阶段的产物全部
// 留在这里，执行阶段只读它，路径不会在删除前被第二次解析。
type plannedDeletePath struct {
	absPath string
	isDir   bool
	// absent 表示判定时这条路径就不存在：执行阶段跳过它，结果槽按删完了报出
	// （见 DeleteResult.Absent），不当失败。
	absent bool
	result DeleteResult
}

// planDeletePath 判定单条路径能否删除，并把它将产生的结果准备好（不含是否真的
// 删成功——那是执行阶段才知道的事）。目标本身不存在不算判定失败：它按 absent
// 返回，同批其它路径照删。
func planDeletePath(spec sandbox.Spec, roots []string, requestPath string, recursive bool) (plannedDeletePath, error) {
	if strings.TrimSpace(requestPath) == "" {
		return plannedDeletePath{}, codedToolError("E_BAD_PATH", errors.New("delete requires a non-empty path"))
	}
	path, info, err := resolveDeleteTarget(roots, requestPath)
	if err != nil {
		return plannedDeletePath{}, err
	}
	for _, root := range roots {
		if samePath(path, root) {
			return plannedDeletePath{}, codedToolError("E_DELETE_BLOCKED", errors.New("refusing to delete workspace root"))
		}
	}

	// Safety: block dangerous delete targets
	if blocked, reason := isDangerousDeletePath(path); blocked {
		return plannedDeletePath{}, codedToolError("E_DELETE_BLOCKED", fmt.Errorf("%s\n\nThis operation has been blocked for safety. If you really need to delete this path, do it manually outside the agent.", reason))
	}

	if info == nil {
		// 要删的东西已经不在：删除的目的达成了。不报错、也不算失败，只把这件事
		// 放进结果槽（DeleteResult.Absent），模型与 UI 各自看得懂。
		return plannedDeletePath{absPath: path, absent: true, result: DeleteResult{
			Deleted:      filepath.ToSlash(requestPath),
			Path:         filepath.ToSlash(requestPath),
			ResolvedPath: filepath.ToSlash(path),
			Recursive:    recursive,
			OK:           true,
			Absent:       true,
		}}, nil
	}
	if info.IsDir() && !recursive {
		return plannedDeletePath{}, codedToolError("E_DIR_REQUIRES_RECURSIVE", errors.New("path is a directory; set recursive=true"))
	}
	result, err := inspectDeleteTarget(requestPath, path, recursive, info, countDeleteStats(spec, roots, path))
	if err != nil {
		return plannedDeletePath{}, err
	}
	return plannedDeletePath{absPath: path, isDir: info.IsDir(), result: result}, nil
}

func (a *App) runCommandWithConfig(parent context.Context, cfg ConfigState, req CommandRequest) (CommandResult, error) {
	if strings.TrimSpace(req.Command) == "" {
		return CommandResult{}, codedToolError("E_BAD_COMMAND", errors.New("command is required"))
	}
	roots, err := workspaceRoots(cfg)
	if err != nil {
		return CommandResult{}, err
	}
	root := roots[0]
	cwd := root
	if strings.TrimSpace(req.Cwd) != "" {
		cwd, err = resolveCommandCwd(roots, req.Cwd)
		if err != nil {
			return CommandResult{}, err
		}
	}
	// 围栏是一个入口：它自己问「谁管边界」（kernelOwnsBoundary），沙箱接不接都走这里。
	if err := checkCommandSafetyAtCwd(req, roots, cwd); err != nil {
		return CommandResult{}, err
	}
	// Knowledge-base runs additionally block literal write targets under the
	// read-only sources/ subtree (deny roots ride in on parent ctx). 内核管边界时
	// 这一层由沙箱的 DenyWriteRoots 兑住，所以只在围栏自己管的时候跑。
	if !kernelOwnsBoundary() {
		if err := checkKBDenyTargets(req.Command, cwd, kbDenyRoots(parent)); err != nil {
			return CommandResult{}, err
		}
	}
	timeout := req.Timeout
	if timeout <= 0 {
		timeout = defaultShellLimit
	}
	if timeout > 600 {
		timeout = 600
	}
	// 超时不再用 context.WithTimeout 杀进程：命令超时后会被收编为后台服务
	// 继续运行（promoteTimedOutCommand），重启 dev server 会撞端口，收编
	// 原进程才能无缝接管。取消（ESC/关闭运行）仍然走 parent ctx 杀树。
	runCtx, cancel := context.WithCancel(parent)
	defer cancel()
	timer := time.NewTimer(time.Duration(timeout) * time.Second)
	defer timer.Stop()

	shell := commandShell(req.Command, cfg.GitBashPath)
	// 沙箱在 shell 外面包一层：命令自己怎么拼都绕不过去，[argv] 的其余参数
	// （-c 与整条命令串）原样透传给 shell。
	spec := sandboxSpec(roots, kbDenyRoots(parent))
	argv := wrapSandboxedCommand(spec, append([]string{shell.path}, shell.args...))
	cmd := exec.CommandContext(runCtx, argv[0], argv[1:]...)
	cmd.Dir = cwd
	cmd.Env = commandEnvironment(cfg)
	buf := &limitedBuffer{limit: maxToolOutput}
	// 完整输出延迟落盘：仅在内存缓冲首次溢出时创建工作区 .tmp 下的 spill 文件，
	// 把已缓冲前缀写入后继续边跑边写；未截断的小输出全程不碰磁盘。
	tw := &teeWriter{primary: buf, spillDir: filepath.Join(root, ".tmp")}
	buf.onTruncate = tw.startSpill
	cmd.Stdout = tw
	cmd.Stderr = tw
	job := prepareServiceCommand(cmd)
	// ESC 取消运行时，杀掉整棵进程树而不是只杀外壳 bash/powershell，
	// 否则 npm/vite/devserver 等子进程会变成孤儿继续占用端口。
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return stopProcessTree(cmd.Process.Pid)
	}
	started := time.Now()
	outputDone := make(chan struct{})
	var outputWG sync.WaitGroup
	if meta, ok := parent.Value(toolExecutionMetaContextKey{}).(toolExecutionMeta); ok && meta.runID != "" && meta.sessionID != "" {
		outputWG.Add(1)
		go func() {
			defer outputWG.Done()
			ticker := time.NewTicker(commandOutputSampleInterval)
			defer ticker.Stop()
			// Track the last emitted length so we can skip the full String()
			// copy when the buffer hasn't grown since the last tick. The
			// previous code called buf.String() (which copies the entire
			// buffered output under the lock) on every 120ms tick even when
			// the command hadn't produced any new output — for a long-running
			// build emitting one line per second, ~99% of ticks were no-ops
			// that still held the write lock during a multi-MB copy.
			lastLen := -1
			emit := func() {
				curLen := buf.Len()
				if curLen == 0 || curLen == lastLen {
					return
				}
				lastLen = curLen
				// During streaming, emit only the tail of the buffer instead
				// of the full content. A long build can fill the 128KB buffer;
				// emitting the full content on every tick means ~1MB/s
				// of JSON marshal + IPC transmission + frontend re-split, most
				// of which the user cannot read at 8 FPS anyway. The complete
				// output is delivered in the final CommandResult once the
				// command exits. The tail size is enough to show the last ~100
				// lines of typical build output. 采样间隔与尾巴长度收口在
				// eventCadenceTable（commandOutput* 档）。
				tail := decodeConsoleOutput(buf.TailString(commandOutputStreamingTailBytes))
				payload := map[string]any{
					"runId":         meta.runID,
					"sessionId":     meta.sessionID,
					"toolBatchId":   meta.toolBatchID,
					"toolCallIndex": meta.toolCallIndex,
					"toolCallId":    meta.toolCallID,
					"name":          meta.toolName,
					"args":          meta.toolArgs,
					"output":        tail,
					"streaming":     true,
				}
				if curLen > commandOutputStreamingTailBytes {
					payload["outputTruncated"] = true
					payload["outputTotalBytes"] = curLen
				}
				a.emit(toolUpdateEvent, payload)
			}
			for {
				select {
				case <-ticker.C:
					emit()
				case <-outputDone:
					emit()
					return
				case <-parent.Done():
					return
				}
			}
		}()
	}
	waitDone := make(chan error, 1)
	if err = cmd.Start(); err == nil {
		// Job Object 注册失败（例如进程已被其他 job 接管）时忽略，
		// 取消时回退到 taskkill /T。
		_ = registerProcessJob(cmd.Process.Pid, job)
		go func() {
			waitDone <- cmd.Wait()
			// 进程退出后关闭 job handle；若取消时 TerminateJobObject 因
			// 竞态失败导致仍有残留孙进程，KILL_ON_JOB_CLOSE 会兜底杀掉。
			unregisterProcessJob(cmd.Process.Pid)
		}()
	} else {
		discardProcessJob(job)
	}
	timedOut := false
	if err == nil {
		select {
		case err = <-waitDone:
		case <-timer.C:
			// 超时收编：进程还活着（waitDone 未关），把原进程连同输出管道
			// 一起移交给服务注册表，避免杀掉重跑撞 EADDRINUSE。
			info, promoteErr := a.promoteTimedOutCommand(promoteCommandParams{
				cmd:        cmd,
				command:    req.Command,
				cwd:        cwd,
				startedAt:  started,
				tee:        tw,
				timeout:    timeout,
				sandboxed:  spec.Enforce(),
				writeRoots: roots,
				waitDone:   waitDone,
			})
			if promoteErr == nil {
				timedOut = true
				close(outputDone)
				outputWG.Wait()
				// 收编路径同样要收尾 spill：超时前的输出可能已超过内存上限，
				// 不关句柄会泄漏 fd、不回报落盘文件会让模型误以为拿到的是全量输出。
				outputFilePath, outputFileSize := finalizeCommandSpill(tw, buf)
				return a.promotedCommandResult(req, shell, cwd, buf, timeout, info, outputFilePath, outputFileSize), nil
			}
			if errors.Is(promoteErr, errProcessAlreadyExited) {
				// 竞态：进程恰好在收编瞬间自行退出，drain 拿真实退出结果。
				err = <-waitDone
			} else {
				// 晋升失败（如服务配额已满，进程已被杀）：按超时错误上报。
				err = promoteErr
			}
		case <-runCtx.Done():
			// parent 取消（ESC）：cmd.Cancel 已杀树；drain waitDone。
			err = <-waitDone
		}
	}
	close(outputDone)
	outputWG.Wait()
	outputFilePath, outputFileSize := finalizeCommandSpill(tw, buf)
	duration := time.Since(started).Milliseconds()
	result := CommandResult{
		Command:         req.Command,
		Cwd:             filepath.ToSlash(cwd),
		Shell:           shell.name,
		ShellPath:       shell.path,
		Output:          decodeConsoleOutput(buf.String()),
		ExitCode:        0,
		TimedOut:        timedOut,
		Cancelled:       errors.Is(runCtx.Err(), context.Canceled),
		DurationMS:      duration,
		Truncated:       buf.truncated,
		Sandboxed:       spec.Enforce(),
		OutputFilePath:  outputFilePath,
		OutputFileBytes: outputFileSize,
	}
	if outputFilePath != "" {
		result.Output += commandTruncationNotice(outputFilePath, outputFileSize)
	}
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			result.ExitCode = exitErr.ExitCode()
		} else if timedOut {
			result.ExitCode = -1
		} else {
			return result, err
		}
	}
	// 内核只回一句裸权限错误，读起来像命令自己写错了：补上能照做的下一步。
	annotateSandboxDeniedWrite(spec, &result, roots)
	return result, nil
}

// finalizeCommandSpill closes a command's lazily created spill file (created on
// first overflow) and reports it for the model-facing result: the path and byte
// size of the retained full-output file, or empty values when the output never
// exceeded the in-memory cap. Callers must invoke it only once the command path
// stopped writing through the teeWriter — process exited, ESC-killed, or the
// command was promoted to a service (promotion re-routes writes away from the
// spill, so its content is final). Without this the promoted path would leak the
// handle and leave the model thinking the capped buffer is all the output.
func finalizeCommandSpill(tw *teeWriter, buf *limitedBuffer) (string, int64) {
	f := tw.spill
	if f == nil {
		return "", 0
	}
	name := f.Name()
	_ = f.Close()
	if !buf.truncated {
		_ = os.Remove(name)
		return "", 0
	}
	// On codepage-936 systems the full output is commonly GBK while the
	// model-facing cap keeps UTF-8 in memory; rewrite the spill as UTF-8 so
	// `read` can open it, then stat the final size.
	transcodeSpillFileForRead(name)
	info, err := os.Stat(name)
	if err != nil {
		return name, 0
	}
	return name, info.Size()
}

// commandTruncationNotice is the trailing hint appended to a command result
// whose full output was spilled to disk, telling the model the buffered text is
// not everything the command produced. Shared by the normal exit path and the
// promoted-to-service path so both report the same thing.
func commandTruncationNotice(outputFilePath string, outputFileSize int64) string {
	return fmt.Sprintf("\n\n[输出已截断：仅保留前 %d KB。完整输出已保存到 %s（共 %s），可用 read 工具读取该文件查看全部内容；大文件建议分段或按需检索，避免整读]", maxToolOutput/1024, outputFilePath, formatMapFileSize(outputFileSize))
}

// teeWriter mirrors command output to the capped buffer and, once the buffer
// first overflows, to a lazily created spill file under spillDir so the full
// output survives truncation. The buffer owns both sinks and writes them under
// one mutex, so the spill file stays a byte-exact in-order copy of the complete
// output (no chunk duplicated or lost) even though stdout and stderr feed this
// same writer concurrently.
type teeWriter struct {
	primary  *limitedBuffer
	spillDir string
	spill    *os.File // nil until the first truncation
	// promoted 在 promoteTo 之后非 nil：新写入只进该服务的滚动缓冲，不再进
	// primary/spill（命令已移交给服务注册表，截断报告与 spill 文件不再有意义）。
	promoted io.Writer
	// mu 串行化「判断 promoted 并写入目标」的整段，而不只是判断本身：持锁写入
	// primary 才能让 promoteTo 的切换点没有缝隙。否则并发写会先判断、解锁，
	// 再落到 primary，正好落在 promoteTo 的种子快照之后——这段字节只进旧缓冲、
	// 不进服务滚动缓冲（丢的往往正是 dev server 的 ready 行）。
	// 锁序固定为 mu → limitedBuffer.mu：startSpill 是在 limitedBuffer.mu 内被
	// 回调的，绝不可在 startSpill 里反向获取 mu（否则死锁）。
	mu sync.Mutex
}

func (w *teeWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.promoted != nil {
		_, _ = w.promoted.Write(p)
		return len(p), nil
	}
	return w.primary.Write(p)
}

// promoteTo 把输出原子改道到 sink（收编后的服务滚动缓冲）：在同一个临界区内
// 先把 primary 已捕获的输出作为种子写进 sink，再置位 promoted，之后所有写入都
// 只进 sink。种子与切换同锁完成，字节既不丢也不重排——这是收编后任务中心预览
// 与 service read 能看到超时前启动日志的前提。已改道过则无操作。
func (w *teeWriter) promoteTo(sink io.Writer) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.promoted != nil || sink == nil {
		return
	}
	_, _ = sink.Write([]byte(w.primary.String()))
	w.promoted = sink
}

// startSpill is invoked by limitedBuffer exactly once, on first overflow, with
// the buffered prefix (under the buffer lock). It creates the spill file under
// spillDir, copies the prefix, and returns the sink so the buffer can append
// the overflowing chunk and every later one verbatim; failures degrade
// gracefully to buffer-only capture.
func (w *teeWriter) startSpill(prefix []byte) io.Writer {
	if w.spill != nil || w.spillDir == "" {
		return nil
	}
	if err := os.MkdirAll(w.spillDir, 0o755); err != nil {
		return nil
	}
	f, err := os.CreateTemp(w.spillDir, "ally-run-*.log")
	if err != nil {
		return nil
	}
	w.spill = f
	_, _ = f.Write(prefix)
	return f
}

// cleanupCommandSpillFiles removes stale command full-output temp files
// older than 24 hours from the workspace .tmp directory (and the legacy
// .ally/tmp location), once at startup, to avoid unbounded accumulation.
func cleanupCommandSpillFiles(workspaceRoot string) {
	if strings.TrimSpace(workspaceRoot) == "" {
		return
	}
	abs, err := filepath.Abs(workspaceRoot)
	if err != nil {
		return
	}
	dirs := []string{
		filepath.Join(abs, ".tmp"),
		// Legacy location used before the switch to workspace .tmp; sweep
		// leftovers so they do not linger forever.
		filepath.Join(abs, ".ally", "tmp"),
	}
	cutoff := time.Now().Add(-24 * time.Hour)
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			name := entry.Name()
			if !strings.HasPrefix(name, "ally-run-") || !strings.HasSuffix(name, ".log") {
				continue
			}
			if info, err := entry.Info(); err == nil && info.ModTime().Before(cutoff) {
				_ = os.Remove(filepath.Join(dir, name))
			}
		}
	}
}

type shellInvocation struct {
	name string
	path string
	args []string
}

// commandShell determines which shell to use for executing a command.
//
// On Windows it prefers Git Bash (bash.exe) so command syntax is unified with
// Linux/macOS. Detection order:
//  1. The gitBashPath setting (manual user override, passed as configuredPath)
//  2. Git for Windows common installation paths
//  3. A Git for Windows installation found through git.exe on PATH
//  4. A Git Bash executable found on PATH
//  5. Fallback to PowerShell (pwsh.exe → powershell.exe), which is always
//     available on Windows (5.1 is built-in, no installation required).
//
// On Linux/macOS it uses bash -c directly and ignores configuredPath.
func commandShell(command, configuredPath string) shellInvocation {
	if goruntime.GOOS == "windows" {
		if bashPath, bashName := findWindowsBash(configuredPath); bashPath != "" {
			return shellInvocation{name: bashName, path: bashPath, args: []string{"-c", command}}
		}
		shell := windowsPowerShell()
		return shellInvocation{
			name: shell.name,
			path: shell.path,
			args: []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", wrapPowerShellCommand(command)},
		}
	}
	return shellInvocation{name: "bash", path: "bash", args: []string{"-c", command}}
}

// shellBinary holds a resolved shell name and executable path.
type shellBinary struct {
	name string
	path string
}

// findWindowsBash searches for a usable bash.exe on Windows.
// configuredPath is an explicit user override from the gitBashPath setting;
// when set and valid it takes priority. Returns the path and a display name
// ("bash"), or empty strings if no bash was found.
func findWindowsBash(configuredPath string) (string, string) {
	// 1. User-configured path (manual override).
	if p := existingGitBashPath(configuredPath); p != "" {
		return p, "bash"
	}

	// 2. Git for Windows common installation paths.
	gitBashPaths := []string{}
	if progFiles := os.Getenv("ProgramFiles"); progFiles != "" {
		gitBashPaths = append(gitBashPaths,
			filepath.Join(progFiles, "Git", "bin", "bash.exe"),
			filepath.Join(progFiles, "Git", "usr", "bin", "bash.exe"),
		)
	}
	if progFilesX86 := os.Getenv("ProgramFiles(x86)"); progFilesX86 != "" {
		gitBashPaths = append(gitBashPaths,
			filepath.Join(progFilesX86, "Git", "bin", "bash.exe"),
			filepath.Join(progFilesX86, "Git", "usr", "bin", "bash.exe"),
		)
	}
	if localAppData := os.Getenv("LOCALAPPDATA"); localAppData != "" {
		gitBashPaths = append(gitBashPaths,
			filepath.Join(localAppData, "Programs", "Git", "bin", "bash.exe"),
			filepath.Join(localAppData, "Programs", "Git", "usr", "bin", "bash.exe"),
		)
	}
	for _, p := range gitBashPaths {
		if p = existingGitBashPath(p); p != "" {
			return p, "bash"
		}
	}

	// 3. Derive Git Bash from git.exe on PATH. This supports portable and
	// non-default Git for Windows installations without accidentally selecting
	// C:\Windows\System32\bash.exe, which is the legacy WSL launcher.
	for _, gitName := range []string{"git.exe", "git"} {
		gitPath, err := exec.LookPath(gitName)
		if err != nil {
			continue
		}
		for _, candidate := range gitBashCandidatesFromGitExecutable(gitPath) {
			if p := existingGitBashPath(candidate); p != "" {
				return p, "bash"
			}
		}
	}

	// 4. bash.exe on PATH, but only when it belongs to a Git for Windows
	// installation. Accepting an arbitrary bash.exe here can select WSL, whose
	// command-line forwarding and Linux PATH semantics break Windows tools and
	// can cause shell input such as $BASH_VERSION or $(...) to be parsed twice.
	if p, err := exec.LookPath("bash.exe"); err == nil {
		if p = existingGitBashPath(p); p != "" {
			return p, "bash"
		}
	}

	return "", ""
}

func gitBashCandidatesFromGitExecutable(gitPath string) []string {
	dir := filepath.Dir(filepath.Clean(strings.TrimSpace(gitPath)))
	base := strings.ToLower(filepath.Base(dir))
	var root string
	switch base {
	case "cmd", "bin":
		root = filepath.Dir(dir)
	default:
		return nil
	}
	return []string{
		filepath.Join(root, "bin", "bash.exe"),
		filepath.Join(root, "usr", "bin", "bash.exe"),
	}
}

func existingGitBashPath(candidate string) string {
	candidate = strings.TrimSpace(candidate)
	if candidate == "" {
		return ""
	}
	info, err := os.Stat(candidate)
	if err != nil || info.IsDir() {
		return ""
	}

	dir := filepath.Dir(filepath.Clean(candidate))
	if !strings.EqualFold(filepath.Base(dir), "bin") {
		return ""
	}
	root := filepath.Dir(dir)
	if strings.EqualFold(filepath.Base(root), "usr") {
		root = filepath.Dir(root)
	}
	for _, gitPath := range []string{
		filepath.Join(root, "cmd", "git.exe"),
		filepath.Join(root, "bin", "git.exe"),
	} {
		if gitInfo, statErr := os.Stat(gitPath); statErr == nil && !gitInfo.IsDir() {
			return candidate
		}
	}
	return ""
}

// windowsPowerShell resolves the best available PowerShell on Windows.
func windowsPowerShell() shellBinary {
	for _, candidate := range []string{"pwsh.exe", "pwsh", "powershell.exe", "powershell"} {
		if p, err := exec.LookPath(candidate); err == nil {
			name := strings.TrimSuffix(strings.ToLower(filepath.Base(p)), ".exe")
			return shellBinary{name: name, path: p}
		}
	}
	return shellBinary{name: "powershell", path: "powershell.exe"}
}

// windowsShellInfo returns the display name and path of the shell that
// commandShell will use on Windows. On non-Windows it returns ("bash", "bash").
func windowsShellInfo(configuredPath string) shellBinary {
	if goruntime.GOOS != "windows" {
		return shellBinary{name: "bash", path: "bash"}
	}
	if bashPath, _ := findWindowsBash(configuredPath); bashPath != "" {
		return shellBinary{name: "bash", path: bashPath}
	}
	return windowsPowerShell()
}

func wrapPowerShellCommand(command string) string {
	return "$ErrorActionPreference = 'Stop'; try { " + command + "; if ($global:LASTEXITCODE -is [int]) { exit $global:LASTEXITCODE } } catch { Write-Error $_; exit 1 }"
}

func inspectDeleteTarget(requestPath, absPath string, recursive bool, info os.FileInfo, countTree bool) (DeleteResult, error) {
	result := DeleteResult{
		Deleted:      filepath.ToSlash(requestPath),
		Path:         filepath.ToSlash(requestPath),
		ResolvedPath: filepath.ToSlash(absPath),
		Kind:         deleteTargetKind(info),
		Recursive:    recursive,
		WasSymlink:   info.Mode()&os.ModeSymlink != 0,
	}
	if info.IsDir() {
		// 不统计时（见 countDeleteStats）连形状都只填已有的那些：这次删除会被
		// 内核拒掉，调用方本来就会把统计清零。
		if !countTree {
			return result, nil
		}
		if !recursive {
			result.RemovedDirs = 1
			return result, nil
		}
		err := filepath.WalkDir(absPath, func(_ string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			entryInfo, err := d.Info()
			if err != nil {
				return err
			}
			if d.IsDir() {
				result.RemovedDirs++
			} else {
				result.RemovedFiles++
				result.RemovedBytes += entryInfo.Size()
			}
			return nil
		})
		return result, err
	}
	result.RemovedFiles = 1
	result.RemovedBytes = info.Size()
	return result, nil
}

func deleteTargetKind(info os.FileInfo) string {
	mode := info.Mode()
	if mode&os.ModeSymlink != 0 {
		return "symlink"
	}
	if info.IsDir() {
		return "directory"
	}
	if mode.IsRegular() {
		return "file"
	}
	return "other"
}

func makeEditResult(rel string, beforeHash, beforeVersion string, before, after []byte, ending string, replacements int, beforeText, afterText string) EditResult {
	beforeLines, _ := splitLines(beforeText)
	afterLines, _ := splitLines(afterText)
	added := len(afterLines) - len(beforeLines)
	removed := 0
	if added < 0 {
		removed = -added
		added = 0
	}
	afterHash, afterVersion := hashBytesAndVersion(after)
	return EditResult{
		Path:          filepath.ToSlash(rel),
		BeforeSHA256:  beforeHash,
		AfterSHA256:   afterHash,
		BeforeVersion: beforeVersion,
		Version:       afterVersion,
		BeforeBytes:   len(before),
		AfterBytes:    len(after),
		Replacements:  replacements,
		AddedLines:    added,
		RemovedLines:  removed,
		LineEnding:    ending,
		Summary:       fmt.Sprintf("%s updated: %d -> %d bytes", filepath.ToSlash(rel), len(before), len(after)),
	}
}

// ── Workspace path resolution (thin pathutil wrappers) ───────

func workspaceRoot(cfg ConfigState) (string, error) {
	return pathutil.RootFromConfig(cfg.Workspace)
}

// workspaceRoots 返回主工作区 + 会话级 ExtraRoots 的去重列表。
// 主工作区始终是 roots[0]，且必须存在；ExtraRoots 中不存在或非目录的条目被静默跳过。
// 重复路径（按 OS 风格归一化后）只保留首次出现。
func workspaceRoots(cfg ConfigState) ([]string, error) {
	return pathutil.RootsFromConfig(cfg.Workspace, cfg.ExtraRoots)
}

// insideAnyRoot 判断 target 是否落在任一 root 内（不含 symlink 解析）。
func insideAnyRoot(roots []string, target string) bool {
	return pathutil.InsideAnyRoot(roots, target)
}

func safeJoin(roots []string, p string) (string, error) {
	return pathutil.SafeJoin(pathRuntime, roots, p)
}

// resolveBoundaryPath joins p the way the current boundary owner expects: the
// containment verdict comes from the fence only while the fence is what guards
// the boundary, and the kernel answers for it once confinement is in force.
// Normalization is identical either way (pathutil.JoinPath is the half SafeJoin
// builds on), so no caller can see two spellings of the same path.
func resolveBoundaryPath(roots []string, p string) (string, error) {
	if kernelOwnsBoundary() {
		return pathutil.JoinPath(roots, p)
	}
	return pathutil.SafeJoin(pathRuntime, roots, p)
}

// symlinkWriteError is the single model-facing explanation for refusing to
// write through a symlink, so both entry points that can hit it (create's
// overwrite path and the shared write-path resolver) say the same thing. It
// also names the one asymmetry the model has to know about: a command line is
// judged on the resolved path by the OS sandbox, so it can do what the write
// tool refuses — without that sentence the model just retries the same call.
func symlinkWriteError(p string) error {
	return codedToolError("E_SYMLINK_PATH", fmt.Errorf("安全围栏已拦截：不允许通过符号链接写入。\n原因：%s 是符号链接；写工具不跟随链接，以免改动链接指向的目标（它可能落在允许范围之外）。\n处理方式：改为直接写链接指向的真实路径；要替换链接本身，请先删除它再建文件。命令行写入按真实路径判定——解析后仍在允许范围内时命令行可以做到，所以改用命令行也是一种办法。", p))
}

func resolveWritableFilePath(roots []string, p string) (string, error) {
	abs, err := resolveBoundaryPath(roots, p)
	if err != nil {
		return "", codedToolError("E_PATH_OUTSIDE", err)
	}
	if info, err := os.Lstat(abs); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return "", symlinkWriteError(p)
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	resolved, err := evalExistingPrefix(abs)
	if err != nil {
		return "", err
	}
	// 版本控制元数据对每一条写路径都不可碰（create / edit / 编辑器保存 /
	// http_request saveTo / move 目标）：.git/hooks 会在下次 git 命令时执行，
	// 覆盖 .git/index 或 ref 会损坏仓库。两种形态（字面 + 符号链接解析后）一次
	// 判完，与删除路径、命令目标共用 vcsMetadataMutationHit，免得新入口只判了
	// 字面那一半。
	if _, reason := vcsMetadataMutationHit(abs, resolved); reason != "" {
		return "", codedToolError("E_PROTECTED_PATH", errors.New(reason))
	}
	if !kernelOwnsBoundary() && !insideWriteRoot(roots, resolved) {
		return "", codedToolError("E_PATH_OUTSIDE", fmt.Errorf("path resolves outside workspace or ~/.ally_agent: %s\n可写范围：%s", p, formatAllowedRoots(roots)))
	}
	return abs, nil
}

// resolveDeleteTarget 解析删除目标，并把「目标本身在不在」一并答出来：不在不算
// 解析失败（delete 的语义由 planDeletePath 决定），所以 info 为 nil 时照样返回。
// 解析本身失败（越界、VCS 元数据、IO 错误）仍按 error 返回。
func resolveDeleteTarget(roots []string, p string) (string, os.FileInfo, error) {
	abs, err := resolveBoundaryPath(roots, p)
	if err != nil {
		return "", nil, codedToolError("E_PATH_OUTSIDE", err)
	}
	info, err := os.Lstat(abs)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", nil, err
	}
	// 缺失时 info 为 nil：符号链接只对存在的东西成立，按字面路径解析已有前缀。
	checkPath := abs
	if info != nil && info.Mode()&os.ModeSymlink != 0 {
		checkPath = filepath.Dir(abs)
	}
	resolved, err := evalExistingPrefix(checkPath)
	if err != nil {
		return "", nil, err
	}
	// 同 resolveWritableFilePath：两种形态一次判完（vcsMetadataMutationHit），
	// 免得这份复判在每个入口各写一遍。上面对符号链接目标取的是它的父目录
	// （删链接本体不算动目标），所以解析结果与 abs 不同，字面判定同样会跑。
	if _, reason := vcsMetadataMutationHit(abs, resolved); reason != "" {
		return "", nil, codedToolError("E_PROTECTED_PATH", errors.New(reason))
	}
	if !kernelOwnsBoundary() && !insideWriteRoot(roots, resolved) {
		return "", nil, codedToolError("E_PATH_OUTSIDE", fmt.Errorf("path resolves outside workspace or ~/.ally_agent: %s\n可写范围：%s", p, formatAllowedRoots(roots)))
	}
	return abs, info, nil
}

// resolveDeletablePath 是「目标必须存在」的那一面：rename 的源路径这类调用方要
// 的是能用的路径，不是「它本来就不在」这个答案。
func resolveDeletablePath(roots []string, p string) (string, error) {
	abs, info, err := resolveDeleteTarget(roots, p)
	if err != nil {
		return "", err
	}
	if info == nil {
		return "", codedToolError("E_PATH_NOT_FOUND", fmt.Errorf("path does not exist: %s", p))
	}
	return abs, nil
}

func resolveCommandCwd(roots []string, p string) (string, error) {
	abs, err := resolveBoundaryPath(roots, p)
	if err != nil {
		return "", codedToolError("E_PATH_OUTSIDE", err)
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", codedToolError("E_CWD_INVALID", err)
		}
		return "", err
	}
	if !kernelOwnsBoundary() && !insideWriteRoot(roots, resolved) {
		return "", codedToolError("E_PATH_OUTSIDE", fmt.Errorf("cwd resolves outside workspace or ~/.ally_agent: %s\n可写范围：%s", p, formatAllowedRoots(roots)))
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", codedToolError("E_CWD_INVALID", fmt.Errorf("cwd is not a directory: %s", p))
	}
	return filepath.Clean(resolved), nil
}

// formatAllowedRoots 把 roots 列表格式化为换行分隔的字符串，用于错误信息提示。
func formatAllowedRoots(roots []string) string {
	return pathutil.FormatAllowedRoots(roots)
}

// 本地 delete 与远端 remote_delete_path 共用的一块：请求里的「一个路径」和
// 「一串路径」是同一个东西的两种写法，以及「这一串路径彼此之间能不能一起删」。
//
// 单条路径的落盘判定各自留在自己的信任域里（本地在 Go 里问本机文件系统，远端
// 只能在 SSH 另一头问），这里只放与磁盘无关、两端必须一致的那部分：条数上限、
// 重复、包含。三条判定都收口在这一个文件里，改一处两端同时生效。

// resolveDeletePathList 把两种写法折叠成一条有序候选列表，并守住条数上限。
// 闸门已用 oneOf 拒过「两个都传」与「一个都没传」，但 Wails API
// （App.DeletePath，前端资源管理器直接用）不走闸门，所以 handler 侧要有同一
// 套判定 —— 判据只有这一份。
func resolveDeletePathList(single string, list []string) ([]string, error) {
	if strings.TrimSpace(single) != "" {
		if len(list) > 0 {
			return nil, codedToolError("E_BAD_ARGS", errors.New("delete takes either path or paths, not both: send `path` for one entry, `paths` for several"))
		}
		return []string{single}, nil
	}
	candidates := make([]string, 0, len(list))
	for _, candidate := range list {
		if strings.TrimSpace(candidate) == "" {
			return nil, codedToolError("E_BAD_ARGS", errors.New("paths must not contain blank entries; drop the empty one instead of sending it"))
		}
		candidates = append(candidates, candidate)
	}
	if len(candidates) == 0 {
		return nil, codedToolError("E_BAD_ARGS", errors.New("delete requires a path or a non-empty paths list"))
	}
	if len(candidates) > toolshared.DeletePathListLimit {
		return nil, codedToolError("E_BAD_ARGS", fmt.Errorf("too many paths (%d); one delete call takes at most %d — split the list and send the rest in a later call", len(candidates), toolshared.DeletePathListLimit))
	}
	return candidates, nil
}

// deletePathsFailureSummary 把一次删除的失败槽压成一行，给以 error 为契约的调用方
// （Wails API / 资源管理器）用：工具调用看到的是逐条槽位，UI 只问「成不成」。
func deletePathsFailureSummary(result DeletePathsResult) string {
	if result.FailedCount == 0 {
		return ""
	}
	parts := make([]string, 0, result.FailedCount)
	for _, item := range result.Paths {
		if item.OK {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s: %s", item.Path, item.Error))
	}
	return fmt.Sprintf("%d of %d paths could not be deleted — %s", result.FailedCount, len(result.Paths), strings.Join(parts, "; "))
}

// checkDeletePathList 判「这一串路径彼此之间」是否可执行。它建在批次写冲突判定
// 用的同一套目标键上（本地 localMutationKey，远端 remoteMutationKey），所以
// 「一次调用里重复同一个路径」与「同一批里写两次同一个路径」是同一套判据。
//
// 重复与包含都拒绝，而不是悄悄合并：两种写法都会让结果取决于顺序——父目录先
// 删掉，子路径就只剩「不存在」——模型无法从结果里看出实际发生了什么。
func checkDeletePathList(targets []fileMutationTarget) error {
	for i := range targets {
		for j := i + 1; j < len(targets); j++ {
			first, second := targets[i], targets[j]
			if first.key == second.key {
				return codedToolError("E_DUPLICATE_PATH", fmt.Errorf("%s appears twice in this call's paths; list each path once", second.display))
			}
			ancestor, descendant, nested := deletePathAncestorPair(first, second)
			if !nested {
				continue
			}
			return codedToolError("E_PATH_OVERLAP", fmt.Errorf("%s is inside %s; deleting both in one call makes the result depend on which one is deleted first — list only %s, or send the two in separate calls", descendant.display, ancestor.display, ancestor.display))
		}
	}
	return nil
}

// deletePathAncestorPair 报出两者中「谁包含谁」；互不包含时为 false。
func deletePathAncestorPair(a, b fileMutationTarget) (ancestor, descendant fileMutationTarget, nested bool) {
	if deletePathKeyWithin(a.key, b.key) {
		return a, b, true
	}
	if deletePathKeyWithin(b.key, a.key) {
		return b, a, true
	}
	return fileMutationTarget{}, fileMutationTarget{}, false
}

// deletePathKeyWithin 判 descendant 是否落在 ancestor 之内。两个键都由
// localMutationKey / remoteMutationKey 生成，形态已经统一（同样的分隔符、同样的
// 大小写折叠），所以「前缀 + 分隔符」就是唯一的包含关系：工作区里的 app 与
// app-backup 不会因为前缀相同被判成父子。
func deletePathKeyWithin(ancestor, descendant string) bool {
	return strings.HasPrefix(descendant, ancestor+"/")
}
