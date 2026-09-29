// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.

package app

import (
	"errors"
	"fmt"
	"strings"

	toolshared "ally-dev/internal/tools/shared"
)

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
