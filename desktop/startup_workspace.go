// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	backend "ally-dev/internal/app"
)

// startupWorkspace uses the same history as both workspace menus, including a
// recent workspace that has never had a saved conversation.
func startupWorkspace(recent []string, sessions []backend.SessionIndexEntry, fallback string) string {
	if roots := workspaceHistoryRoots(recent, sessions); len(roots) > 0 {
		return roots[0]
	}
	return fallback
}
