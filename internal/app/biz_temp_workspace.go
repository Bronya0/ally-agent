// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.

package app

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"ally-dev/internal/tools/shared"
)

// 临时工作空间（temp workspace）：一个随 Tab 创建/销毁的沙箱目录，
// 用于偶发的脚本试验。目录由 Go 标准库的临时目录机制（os.MkdirTemp）
// 创建，生命周期契约：
//
//   - 前端在用户点击「临时工作空间」时调用 CreateTempWorkspace 建目录并
//     打开一个 kind:'temp' 的 Tab；
//   - Tab 关闭（且后台 run 已结束）时调用 DeleteTempWorkspace 删除目录，
//     同时删除关联会话；
//   - Ally 进程退出（ctx 取消）时删除本进程创建的全部临时目录；
//   - 崩溃/强杀残留的目录由下次启动的陈旧清理回收（默认 24 小时阈值，
//     dev 与打包版可能并行运行，绝不误删活跃实例的目录）。
//
// 删除 API 带双重护栏：目标必须是「系统临时根的直接子目录」且名字以
// ally-temp- 前缀开头，路径经过 filepath.Clean/EqualFold 归一比较，
// 任何越界路径一律 E_TEMP_WORKSPACE_INVALID 拒绝。删除不经由 delete 工具
// 或命令安全层，护栏即唯一边界。

const (
	// tempWorkspacePrefix 是 Ally 创建的临时工作空间目录名前缀。
	tempWorkspacePrefix = "ally-temp-"
	// staleTempWorkspaceAge 是启动清理的陈旧阈值：仅删除修改时间早于
	// 该阈值的残留目录，避免误删并行运行的另一个 Ally 实例的活跃目录。
	staleTempWorkspaceAge = 24 * time.Hour
)

// tempWorkspaceErrorCode 是临时工作空间删除护栏拒绝时使用的稳定错误码。
const tempWorkspaceErrorCode = "E_TEMP_WORKSPACE_INVALID"

// tempWorkspaces 记录本进程创建的临时工作空间（绝对路径 -> true）。
// 进程退出时统一删除；DeleteTempWorkspace 对成员与非成员一视同仁，
// 只要路径通过护栏即允许删除。
var tempWorkspaces = struct {
	sync.Mutex
	paths map[string]struct{}
}{paths: map[string]struct{}{}}

// isValidTempWorkspacePath 校验 path 是否是「临时根下的 ally-temp-* 直接
// 子目录」。目录分隔符归一后按平台大小写语义比较（Windows 不区分大小写），
// 防 .. 穿越与链接拼接。
func isValidTempWorkspacePath(path string) bool {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return false
	}
	cleaned := filepath.Clean(trimmed)
	base := filepath.Base(cleaned)
	if !strings.HasPrefix(base, tempWorkspacePrefix) {
		return false
	}
	tempRoot := filepath.Clean(os.TempDir())
	dirName := filepath.Dir(cleaned)
	// 目录部分必须精确等于临时根。Windows 盘符大小写不敏感，用 EqualFold
	// 近似；其余平台要求字面相等。归一比较发生在 filepath.Clean 之后，
	// .. 穿越与多余分隔符都会被先归一再拒绻。
	if os.PathSeparator == '\\' {
		return strings.EqualFold(dirName, tempRoot)
	}
	return dirName == tempRoot
}

// CreateTempWorkspace 创建一个临时工作空间目录并返回其绝对路径。
// 目录记入进程集合，随 Ally 退出或 Tab 关闭删除。
func (a *App) CreateTempWorkspace() (string, error) {
	dir, err := os.MkdirTemp("", tempWorkspacePrefix+"*")
	if err != nil {
		return "", err
	}
	if abs, absErr := filepath.Abs(dir); absErr == nil {
		dir = abs
	}
	tempWorkspaces.Lock()
	tempWorkspaces.paths[dir] = struct{}{}
	tempWorkspaces.Unlock()
	return dir, nil
}

// DeleteTempWorkspace 删除一个临时工作空间目录。护栏：目标必须是系统
// 临时根的直接 ally-temp-* 子目录，否则返回 E_TEMP_WORKSPACE_INVALID。
func (a *App) DeleteTempWorkspace(path string) error {
	if !isValidTempWorkspacePath(path) {
		return shared.Newf(tempWorkspaceErrorCode, "refusing to delete %q: not an ally temp workspace under the system temp root", path)
	}
	cleaned := filepath.Clean(strings.TrimSpace(path))
	if err := os.RemoveAll(cleaned); err != nil {
		return err
	}
	tempWorkspaces.Lock()
	delete(tempWorkspaces.paths, cleaned)
	tempWorkspaces.Unlock()
	return nil
}

// cleanupTempWorkspacesOnExit 在进程生命周期结束时删除本进程创建的全部
// 临时工作空间。由 ServiceStartup 的 ctx.Done 监听调用；失败仅记日志。
func (a *App) cleanupTempWorkspacesOnExit() {
	tempWorkspaces.Lock()
	paths := make([]string, 0, len(tempWorkspaces.paths))
	for dir := range tempWorkspaces.paths {
		paths = append(paths, dir)
	}
	tempWorkspaces.paths = map[string]struct{}{}
	tempWorkspaces.Unlock()
	for _, dir := range paths {
		if err := os.RemoveAll(dir); err != nil {
			a.logAppError("temp workspace cleanup on exit failed", "error", err, "path", dir)
		}
	}
}

// cleanupStaleTempWorkspaces 扫描系统临时根并删除陈旧的 ally-temp-* 残留
// 目录（上次进程崩溃/强杀未清理的）。只删除修改时间早于阈值的目录。
// 在 ServiceStartup 中异步调用，绝不阻塞启动。
func (a *App) cleanupStaleTempWorkspaces() {
	entries, err := os.ReadDir(os.TempDir())
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-staleTempWorkspaceAge)
	for _, entry := range entries {
		name := entry.Name()
		if !entry.IsDir() || !strings.HasPrefix(name, tempWorkspacePrefix) {
			continue
		}
		full := filepath.Join(os.TempDir(), name)
		info, statErr := entry.Info()
		if statErr != nil {
			continue
		}
		// ModTime 取目录自身修改时间：目录内文件增删会刷新它，活跃
		// 工作区几乎不可能连续 24h 无任何顶层变化。
		if info.ModTime().After(cutoff) {
			continue
		}
		if err := os.RemoveAll(full); err != nil {
			a.logAppError("stale temp workspace cleanup failed", "error", err, "path", full)
		}
	}
}
