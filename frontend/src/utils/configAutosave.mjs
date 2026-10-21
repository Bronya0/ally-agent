// SPDX-License-Identifier: GPL-3.0-only

const clone = (value) => JSON.parse(JSON.stringify(value));

// Settings are saved once when leaving the page (or when the window loses
// focus), not per keystroke: diff the draft against the entry snapshot and
// send only changed fields, so a panel's old snapshot never undoes other writers.
export function diffSettingsPatch(previous, draft, { snapshot = clone } = {}) {
  const next = snapshot(draft);
  const patch = {};
  for (const key of Object.keys(next)) {
    if (JSON.stringify(next[key]) !== JSON.stringify(previous[key])) patch[key] = next[key];
  }
  return patch;
}

export function isValidProxyURL(value) {
  const text = String(value || '').trim();
  if (!text) return false;
  try {
    const url = new URL(text.includes('://') ? text : `http://${text}`);
    return ['http:', 'https:', 'socks5:', 'socks5h:'].includes(url.protocol) && Boolean(url.hostname);
  } catch {
    return false;
  }
}

export function settingsSnapshot(draft) {
  const next = clone(draft);
  // The image picker persists this field through its own backend method.
  delete next.backgroundImage;
  // An unfinished manual address stays in the editor. Other settings can still
  // save; once valid, the complete pending proxy configuration is submitted.
  if (next.proxyMode === 'manual' && !isValidProxyURL(next.proxyUrl)) {
    delete next.proxyMode;
    delete next.proxyUrl;
    delete next.proxyNoProxy;
  }
  return next;
}
