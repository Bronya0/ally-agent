// SPDX-License-Identifier: GPL-3.0-only
import { sameWorkspacePath } from './workspacePath.mjs';

// Missing ownership is visible only in the global management view. A process
// cwd (including an extra root) does not establish its workspace owner.
export function tasksForWorkspace(items, workspace) {
  if (!String(workspace || '').trim()) return [];
  return items.filter((item) => String(item?.workspace || '').trim()
    && sameWorkspacePath(item.workspace, workspace));
}

// User management has a global view; a missing workspace must not select an
// unavailable workspace filter or hide running processes from its badges.
export function effectiveTaskScope(scope, workspace) {
  return scope === 'workspace' && String(workspace || '').trim() ? 'workspace' : 'all';
}

export function tasksForView(items, workspace, scope = 'workspace') {
  return effectiveTaskScope(scope, workspace) === 'all' ? items : tasksForWorkspace(items, workspace);
}
