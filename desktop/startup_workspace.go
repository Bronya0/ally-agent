// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	backend "ally-dev/internal/app"
	"os"
)

// startupWorkspace uses the same history as both workspace menus, including a
// recent workspace that has never had a saved conversation.
func startupWorkspace(recent []string, sessions []backend.SessionIndexEntry, fallback string) string {
	for _, root := range workspaceHistoryRoots(recent, sessions) {
		info, err := os.Stat(root)
		if err == nil && info.IsDir() {
			return root
		}
		// A temporarily inaccessible location should not be silently forgotten.
		if err != nil && !os.IsNotExist(err) {
			return root
		}
	}
	return fallback
}
