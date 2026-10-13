const SECTION_KEYS = Object.freeze({
  workspaces: 'ally_rail_workspaces_expanded',
  sessions: 'ally_rail_sessions_expanded',
});

export function loadRailSectionCollapsed(section, storage) {
  try {
    const key = Object.hasOwn(SECTION_KEYS, section) ? SECTION_KEYS[section] : undefined;
    if (!key) return true;
    return (storage ?? globalThis.localStorage)?.getItem(key) !== '1';
  } catch {
    return true;
  }
}

export function saveRailSectionCollapsed(section, collapsed, storage) {
  try {
    const key = Object.hasOwn(SECTION_KEYS, section) ? SECTION_KEYS[section] : undefined;
    if (key) (storage ?? globalThis.localStorage)?.setItem(key, collapsed ? '0' : '1');
  } catch { /* Keep the live preference when storage is unavailable. */ }
}
