/*
 * SPDX-License-Identifier: GPL-3.0-only
 *
 * Copyright (C) 2026 tangssst <tangssst@qq.com>
 * GitHub: https://github.com/Bronya0/ally-agent
 *
 * This file is part of ally-agent, licensed under the GNU General
 * Public License v3. See the LICENSE file for details.
 */
// Appearance preferences: a color THEME (whole palette) and a color MODE
// (dark / light). Both are pure front-end concerns, persisted in localStorage
// and applied to <html> before mount, independent of the backend config.
//
//   theme → <html data-theme="…">    (attribute absent for the default "amber")
//   mode  → <html data-mode="light"> (attribute absent for the default "dark")
//
// style.css owns the palettes; this file only owns the ids and the storage.
// Every theme ships one block per mode in style.css —
// `:root[data-theme="x"]:not([data-mode="light"])` for dark and
// `:root[data-theme="x"][data-mode="light"]` for light — so the two axes are
// resolved by selector, never by source order (a previous build resolved them by
// source order and silently lost six markdown palettes). Adding a theme = one
// entry below + those two blocks.

const THEME_STORAGE_KEY = 'ally_accent_theme';
const MODE_STORAGE_KEY = 'ally_color_mode';
export const DEFAULT_THEME = 'amber';
export const DEFAULT_MODE = 'dark'; // 'dark' | 'light'

// Single source of truth for the theme ids. `swatch` mirrors the seed declared
// in style.css and is only used to paint the picker dot.
export const THEMES = [
  { id: 'amber',    label: 'Amber 琥珀',      swatch: '#e0a458' },
  { id: 'icecream', label: 'Ice Cream 冰淇淋', swatch: '#ff9ec4' },
  { id: 'ocean',    label: 'Ocean 海洋',      swatch: '#5b9cf6' },
  { id: 'forest',   label: 'Forest 森林',     swatch: '#57c98a' },
  { id: 'violet',   label: 'Violet 紫罗兰',   swatch: '#a78bfa' },
];

const VALID_THEMES = new Set(THEMES.map((t) => t.id));

export function normalizeTheme(value) {
  return VALID_THEMES.has(value) ? value : DEFAULT_THEME;
}

export function getStoredTheme() {
  try {
    return normalizeTheme(localStorage.getItem(THEME_STORAGE_KEY));
  } catch {
    return DEFAULT_THEME;
  }
}

// Apply the theme to the document root. The default theme carries no data-theme
// attribute so it falls through to the amber palette in :root.
export function applyTheme(theme) {
  const next = normalizeTheme(theme);
  const root = document.documentElement;
  if (next === DEFAULT_THEME) root.removeAttribute('data-theme');
  else root.setAttribute('data-theme', next);
  return next;
}

export function setTheme(theme) {
  const next = applyTheme(theme);
  try {
    localStorage.setItem(THEME_STORAGE_KEY, next);
  } catch {
    /* storage unavailable — theme still applies for this session */
  }
  return next;
}

// ── Color mode (dark / light) ──

export function normalizeMode(value) {
  return value === 'light' ? 'light' : DEFAULT_MODE;
}

export function getStoredMode() {
  try {
    return normalizeMode(localStorage.getItem(MODE_STORAGE_KEY));
  } catch {
    return DEFAULT_MODE;
  }
}

// Apply the mode to the document root. The default (dark) carries no
// data-mode attribute so it falls through to the :root token set.
export function applyMode(mode) {
  const next = normalizeMode(mode);
  const root = document.documentElement;
  if (next === DEFAULT_MODE) root.removeAttribute('data-mode');
  else root.setAttribute('data-mode', next);
  return next;
}

export function setMode(mode) {
  const next = applyMode(mode);
  try {
    localStorage.setItem(MODE_STORAGE_KEY, next);
  } catch {
    /* storage unavailable — mode still applies for this session */
  }
  return next;
}

export function initMode() {
  return applyMode(getStoredMode());
}

// Call once at startup, before mount, so the first paint already carries the
// saved theme and mode (no flash of the amber default).
export function initTheme() {
  const theme = applyTheme(getStoredTheme());
  const mode = initMode();
  return { theme, mode };
}
