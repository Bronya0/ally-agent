// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	backend "ally-dev/internal/app"
	"path/filepath"
	"strings"
)

const maxRecentWorkspaces = 100

type workspaceHistoryKey struct {
	sessions, visits uint64
}

func historyWorkspace(root string) bool {
	return strings.TrimSpace(root) != "" && !strings.HasPrefix(strings.ToLower(filepath.Base(filepath.Clean(root))), "ally-temp-")
}

// workspaceHistoryRoots keeps explicit visits first and fills in older records
// from the session index. Opening an empty workspace needs no saved conversation.
func workspaceHistoryRoots(recent []string, sessions []backend.SessionIndexEntry) []string {
	seen := make(map[string]bool, len(recent)+len(sessions))
	var roots []string
	add := func(root string) {
		if !historyWorkspace(root) {
			return
		}
		key := workspaceKey(root)
		if seen[key] {
			return
		}
		seen[key] = true
		roots = append(roots, root)
	}
	for _, root := range recent {
		add(root)
	}
	for _, session := range sessions {
		add(session.Workspace)
	}
	return roots
}

func (a *desktop) workspaceHistoryRoots() []string {
	return a.wsRootsMemo.get(workspaceHistoryKey{a.sessRev, a.workspaceVisitsRev}, func(workspaceHistoryKey) []string {
		return workspaceHistoryRoots(a.prefs.RecentWorkspaces, a.sessions)
	})
}

// rememberWorkspace runs at the shared tab activation entry, covering folder
// selection, history, restored sessions and switches back to an existing tab.
func (a *desktop) rememberWorkspace(root string) {
	if a.closing || !historyWorkspace(root) {
		return
	}
	recent := a.prefs.RecentWorkspaces
	if len(recent) > 0 && sameWorkspace(recent[0], root) {
		return
	}
	roots := []string{filepath.Clean(root)}
	for _, old := range recent {
		if !sameWorkspace(old, root) && len(roots) < maxRecentWorkspaces {
			roots = append(roots, old)
		}
	}
	a.prefs.RecentWorkspaces = roots
	a.workspaceVisitsRev++
	a.savePrefs()
}

func (a *desktop) forgetWorkspaceVisit(root string) {
	var roots []string
	for _, old := range a.prefs.RecentWorkspaces {
		if !sameWorkspace(old, root) {
			roots = append(roots, old)
		}
	}
	if len(roots) == len(a.prefs.RecentWorkspaces) {
		return
	}
	a.prefs.RecentWorkspaces = roots
	a.workspaceVisitsRev++
	a.savePrefs()
}
