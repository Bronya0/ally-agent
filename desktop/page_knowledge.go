// SPDX-License-Identifier: GPL-3.0-only
package desktop

import "strings"

// kbRefresh reloads the knowledge-base listing so the index status shown in the
// composer action bar is accurate.
func (a *desktop) kbRefresh() {
	if strings.TrimSpace(a.config.KBRoot) == "" {
		return
	}
	t := a.current()
	if t == nil || !sameWorkspace(t.Workspace, a.config.KBRoot) {
		a.openWorkspace(a.config.KBRoot)
		return
	}
	a.refreshFiles()
}
