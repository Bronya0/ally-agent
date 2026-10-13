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
	"path/filepath"
	"strings"
)

// SetSessionExtraRoots publishes the session's current extra write roots.
//
// The composer panel calls this the moment a root is added or removed, because
// the session-index write that normally carries the list is skipped while the
// session is busy (a running or compacting session never persists). Without
// this channel a root approved during a run — e.g. while the model waits in ask
// — reached the backend only with the next StartChat, so every command of the
// current run was still fenced against the old list.
func (a *App) SetSessionExtraRoots(sessionID string, roots []string) error {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return errors.New("session id is required")
	}
	a.publishSessionExtraRoots(sessionID, roots)
	return nil
}

// publishSessionExtraRoots writes the session's live root list. An empty list is
// a real state ("no extra roots"), not a missing one, so it is stored as such:
// the run path must be able to tell "the user removed every root" from "nothing
// was ever published" (the latter keeps the request's list).
func (a *App) publishSessionExtraRoots(sessionID string, roots []string) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return
	}
	normalized := normalizeExtraRoots(roots)
	a.mu.Lock()
	// 懒加载兜底：NewApp 已经初始化，但持锁写 nil map 会 panic 并永久占住互斥锁。
	if a.sessionExtraRoots == nil {
		a.sessionExtraRoots = map[string][]string{}
	}
	a.sessionExtraRoots[sessionID] = normalized
	a.mu.Unlock()
}

// cfgWithLiveExtraRoots returns cfg carrying the session's live extra roots.
// Sessions that never published a list keep whatever the request carried.
func (a *App) cfgWithLiveExtraRoots(sessionID string, cfg ConfigState) ConfigState {
	if strings.TrimSpace(sessionID) == "" {
		return cfg
	}
	a.mu.Lock()
	roots, ok := a.sessionExtraRoots[sessionID]
	roots = cloneStringSlice(roots)
	a.mu.Unlock()
	if !ok {
		return cfg
	}
	cfg.ExtraRoots = roots
	return cfg
}

// normalizeExtraRoots is the canonical form of an extra-roots list: the fence's
// spelling (slash form), no blanks, no duplicates. Both the live record and the
// turn notice go through it, so "the same roots" is one string — otherwise a
// reordered or re-spelled list would read as a change and inject a notice.
func normalizeExtraRoots(roots []string) []string {
	// 空列表必须是 [] 而不是 nil：通知里要把它序列化成 []，否则「没有附加工作区」
	// 会退化成缺失字段。
	out := make([]string, 0, len(roots))
	seen := make(map[string]bool, len(roots))
	for _, root := range roots {
		root = strings.TrimSpace(root)
		if root == "" {
			continue
		}
		root = filepath.ToSlash(filepath.Clean(root))
		if seen[root] {
			continue
		}
		seen[root] = true
		out = append(out, root)
	}
	return out
}
