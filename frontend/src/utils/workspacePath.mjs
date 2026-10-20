// SPDX-License-Identifier: GPL-3.0-only
// Workspace identity shared by tab routing and task/service views.
export function workspaceHistoryDedupeKey(path) {
  const original = String(path || '').trim();
  let value = original.replace(/\\/g, '/').replace(/\/{2,}/g, '/');
  if (value.length > 1 && !/^[a-z]:\/$/i.test(value)) value = value.replace(/\/+$/, '');
  const isWindowsPath = /^[a-z]:\//i.test(value) || /^(?:\\\\|\/\/)/.test(original);
  return isWindowsPath ? value.toLowerCase() : value;
}

export function sameWorkspacePath(a, b) {
  return workspaceHistoryDedupeKey(a) === workspaceHistoryDedupeKey(b);
}
