// SPDX-License-Identifier: GPL-3.0-only
// 路径风格以路径本身为准：桌面、远程工作区和纯 Node 测试共用，不依赖宿主 OS。
function pathStyle(path) {
  if (/^(?:\\\\|\/\/)/.test(path)) return 'unc';
  if (/^[a-z]:[\\/]/i.test(path)) return 'drive';
  return 'posix';
}

// 只用于身份比较，不得把小写化的键当作真实文件路径。
export function workspaceHistoryDedupeKey(path) {
  const original = String(path || '').trim();
  const style = pathStyle(original);
  const isWindowsPath = style !== 'posix';
  // POSIX 文件名可含字面反斜杠；UNC 的双斜杠则是卷前缀，不能压成 POSIX 根。
  let value = (isWindowsPath ? original.replace(/\\/g, '/') : original).replace(/\/{2,}/g, '/');
  if (style === 'unc') value = '/' + value;
  if (value.length > 1 && !/^[a-z]:\/$/i.test(value)) value = value.replace(/\/+$/, '');
  return isWindowsPath ? value.toLowerCase() : value;
}

export function sameWorkspacePath(a, b) {
  return workspaceHistoryDedupeKey(a) === workspaceHistoryDedupeKey(b);
}

// 将已知相对路径拼到根目录，用于复制/展示/提示；保留大小写和 POSIX 字面反斜杠。
// 不做 .. 解析、文件系统访问或授权判定：实际读写仍由后端路径边界负责。
export function joinWorkspacePath(root, relative) {
  const base = String(root || '');
  const child = String(relative || '');
  if (!base) return child;
  if (!child) return base;
  if (pathStyle(base) !== 'posix') {
    return base.replace(/\//g, '\\').replace(/\\+$/, '') + '\\' + child.replace(/\//g, '\\');
  }
  return base.replace(/\/+$/, '') + '/' + child;
}
