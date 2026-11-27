// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.

package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gofrs/flock"

	"ally-dev/internal/tools/shared"
)

// 临时工作空间（temp workspace）：一个随 Tab 创建/销毁的沙箱目录，
// 用于偶发的脚本试验。目录由 Go 标准库的临时目录机制（os.MkdirTemp）
// 创建，生命周期契约：
//
//   - 前端在用户点击「临时工作空间」时调用 CreateTempWorkspace 建目录并
//     打开一个 kind:'temp' 的 Tab；
//   - Tab 关闭（且后台 run 已结束）时调用 DeleteTempWorkspace 删除目录；
//     会话记录留在磁盘上，重开时由桌面端为已消失的目录重建一个。
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
	// tempWorkspaceLockName 是工作区内的持有者锁文件。点前缀让它不进
	// 文件树与工作区结构图；创建进程对它保持独占 flock，作为存活凭据。
	tempWorkspaceLockName = ".ally-owner.lock"
	// staleTempWorkspaceAge 是启动清理的陈旧阈值：仅回收修改时间早于
	// 该阈值的残留目录，同时挡住「刚建好还没加上锁」的窗口期竞态。
	staleTempWorkspaceAge = 24 * time.Hour
)

// tempWorkspaceErrorCode 是临时工作空间删除护栏拒绝时使用的稳定错误码。
const tempWorkspaceErrorCode = "E_TEMP_WORKSPACE_INVALID"

// tempWorkspaceInUseErrorCode 是另一实例仍持有工作区锁时的拒绝错误码。
const tempWorkspaceInUseErrorCode = "E_TEMP_WORKSPACE_IN_USE"

// tempWorkspaceGuard 是一个临时工作空间的存活凭据：创建进程对锁文件保持
// 独占 flock，目录扫描据此区分「持有者已退出的残留」与「别的实例仍在使用」，
// 不再把目录修改时间当作进程存活证据。
type tempWorkspaceGuard struct {
	file *flock.Flock
}

// tempWorkspaces 记录本进程创建的临时工作空间（绝对路径 -> 持有锁）。
// 进程退出时统一删除；DeleteTempWorkspace 对成员与非成员一视同仁，
// 只要路径通过护栏且无人持有锁即允许删除。
var tempWorkspaces = struct {
	sync.Mutex
	paths map[string]*tempWorkspaceGuard
}{paths: map[string]*tempWorkspaceGuard{}}

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
	guard := flock.New(filepath.Join(dir, tempWorkspaceLockName))
	locked, lockErr := guard.TryLock()
	if lockErr != nil {
		_ = os.RemoveAll(dir)
		return "", lockErr
	}
	if !locked {
		_ = os.RemoveAll(dir)
		return "", fmt.Errorf("temp workspace lock unavailable: %s", dir)
	}
	tempWorkspaces.Lock()
	tempWorkspaces.paths[dir] = &tempWorkspaceGuard{file: guard}
	tempWorkspaces.Unlock()
	return dir, nil
}

// DeleteTempWorkspace 删除一个临时工作空间目录。护栏：目标必须是系统
// 临时根的直接 ally-temp-* 子目录，否则返回 E_TEMP_WORKSPACE_INVALID；
// 别的实例仍持有锁时返回 E_TEMP_WORKSPACE_IN_USE，绝不删除活目录。
func (a *App) DeleteTempWorkspace(path string) error {
	if !isValidTempWorkspacePath(path) {
		return shared.Newf(tempWorkspaceErrorCode, "refusing to delete %q: not an ally temp workspace under the system temp root", path)
	}
	cleaned := filepath.Clean(strings.TrimSpace(path))
	tempWorkspaces.Lock()
	own := tempWorkspaces.paths[cleaned]
	tempWorkspaces.Unlock()
	if _, statErr := os.Stat(cleaned); statErr != nil {
		if os.IsNotExist(statErr) {
			// 幂等：目录已不存在则连同注册项一并清掉。
			tempWorkspaces.Lock()
			delete(tempWorkspaces.paths, cleaned)
			tempWorkspaces.Unlock()
			return nil
		}
		return statErr
	}
	if own != nil {
		// 本进程持有锁：先释放再删树，锁文件随目录一起移除。
		_ = own.file.Unlock()
		_ = own.file.Close()
	} else {
		// 非本进程目录：锁可以取得说明持有者已退出；取不到说明
		// 另一实例仍在此工作，拒绝删除。
		other := flock.New(filepath.Join(cleaned, tempWorkspaceLockName))
		locked, lockErr := other.TryLock()
		if lockErr != nil {
			return lockErr
		}
		if !locked {
			return shared.Newf(tempWorkspaceInUseErrorCode, "refusing to delete %q: another instance is still using this temp workspace", cleaned)
		}
		_ = other.Unlock()
		_ = other.Close()
	}
	// 即使上面的名字/层级校验通过，删除仍然走基目录断言：递归删除在全仓只有
	// removeAllWithinBase 一条出口（目标必须是基目录的严格子目录）。
	if err := removeTempTree(cleaned); err != nil {
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
	guards := make([]*tempWorkspaceGuard, 0, len(tempWorkspaces.paths))
	for dir, guard := range tempWorkspaces.paths {
		paths = append(paths, dir)
		guards = append(guards, guard)
	}
	tempWorkspaces.paths = map[string]*tempWorkspaceGuard{}
	tempWorkspaces.Unlock()
	for _, guard := range guards {
		_ = guard.file.Unlock()
		_ = guard.file.Close()
	}
	for _, dir := range paths {
		if err := removeTempTree(dir); err != nil {
			a.logAppError("temp workspace cleanup on exit failed", "error", err, "path", dir)
		}
	}
}

// cleanupStaleTempWorkspaces 扫描系统临时根并回收陈旧的 ally-temp-* 残留
// 目录（上次进程崩溃/强杀未清理的）。时限挡住刚建好还没加上锁的窗口期；
// 有锁文件的目录以锁为存活凭据：锁被占用绝不删除，锁空闲才回收；
// 没有锁文件的旧版本目录沿用纯时限规则。在 ServiceStartup 中异步调用，
// 绝不阻塞启动。
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
		tempWorkspaces.Lock()
		own := tempWorkspaces.paths[full] != nil
		tempWorkspaces.Unlock()
		if own {
			continue
		}
		lockPath := filepath.Join(full, tempWorkspaceLockName)
		if _, statErr := os.Stat(lockPath); statErr == nil {
			guard := flock.New(lockPath)
			locked, lockErr := guard.TryLock()
			if lockErr != nil {
				a.logAppError("stale temp workspace lock check failed", "error", lockErr, "path", full)
				continue
			}
			if !locked {
				continue
			}
			_ = guard.Unlock()
			_ = guard.Close()
		}
		if err := removeTempTree(full); err != nil {
			a.logAppError("stale temp workspace cleanup failed", "error", err, "path", full)
		}
	}
}
