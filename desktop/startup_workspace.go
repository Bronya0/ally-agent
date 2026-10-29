// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	backend "ally-dev/internal/app"
	"path/filepath"
	"strings"
)

// startupWorkspace mirrors the sidebar's history workspace list: session
// entries arrive newest first, and the first non-temporary workspace wins.
func startupWorkspace(sessions []backend.SessionIndexEntry, fallback string) string {
	seen := make(map[string]bool, len(sessions))
	for _, session := range sessions {
		path := session.Workspace
		if path == "" || strings.HasPrefix(filepath.Base(path), "ally-temp-") {
			continue
		}
		key := strings.ToLower(filepath.Clean(path))
		if seen[key] {
			continue
		}
		seen[key] = true
		return path
	}
	return fallback
}
