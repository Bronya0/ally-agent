// Reuse surviving nodes so controlled expansion never points at empty shells.
export function mergeExplorerNodes(previous, incoming) {
  const byKey = new Map(previous.map((node) => [node.key, node]));
  return incoming.map((node) => {
    const old = byKey.get(node.key);
    if (!old || old.dir !== node.dir) return node;
    const { children: _children, ...metadata } = node;
    return Object.assign(old, metadata);
  });
}

// Refresh visible expanded branches only, with bounded bridge concurrency.
// Each level is discovered after its parents finish, so removed branches are
// never requested and collapsed cached branches stay lazy.
export async function refreshExpandedExplorerNodes(roots, isExpanded, refresh, isCurrent) {
  let level = roots;
  while (level.length && isCurrent()) {
    const directories = level.filter((node) => node.dir && isExpanded(node.key));
    const next = [];
    for (let offset = 0; offset < directories.length && isCurrent(); offset += 4) {
      await Promise.all(directories.slice(offset, offset + 4).map(async (node) => {
        if (!isCurrent() || !isExpanded(node.key)) return;
        const refreshed = await refresh(node);
        if (refreshed && isCurrent() && isExpanded(node.key)) next.push(...(node.children || []));
      }));
    }
    level = next;
  }
}

export function remapExplorerPath(path, oldPath, newPath) {
  if (path === oldPath) return newPath;
  return path.startsWith(`${oldPath}/`) ? newPath + path.slice(oldPath.length) : path;
}

export function remapExplorerNodes(roots, oldPath, newPath) {
  const pending = [...roots];
  while (pending.length) {
    const node = pending.pop();
    node.key = remapExplorerPath(node.key, oldPath, newPath);
    if (node.path) node.path = remapExplorerPath(node.path, oldPath, newPath);
    if (node.children?.length) pending.push(...node.children);
  }
}

export function explorerNodeKeys(roots) {
  const keys = new Set();
  const directories = new Set();
  const pending = [...roots];
  while (pending.length) {
    const node = pending.pop();
    if (node.placeholder) continue;
    keys.add(node.key);
    if (node.dir) directories.add(node.key);
    if (node.children?.length) pending.push(...node.children);
  }
  return { keys, directories };
}
