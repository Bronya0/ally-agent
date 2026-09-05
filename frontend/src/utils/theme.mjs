/*
 * SPDX-License-Identifier: GPL-3.0-only
 *
 * Copyright (C) 2026 tangssst <tangssst@qq.com>
 * GitHub: https://github.com/Bronya0/ally-agent
 *
 * This file is part of ally-agent, licensed under the GNU General
 * Public License v3. See the LICENSE file for details.
 */
// Accent theme + color mode management. Both are pure front-end concerns: a
// theme only swaps the --ally-accent seed on <html data-theme>, a mode swaps
// the whole surface/text token set on <html data-mode>. Every derived shade is
// computed from it via color-mix in style.css. Both are persisted in
// localStorage so they survive reloads independent of the backend config.

const STORAGE_KEY = 'ally_accent_theme';
const MODE_STORAGE_KEY = 'ally_color_mode';
export const DEFAULT_THEME = 'amber';
export const DEFAULT_MODE = 'dark'; // 'dark' | 'light'

export const MODES = [
  { id: 'dark', label: '深色' },
  { id: 'light', label: '浅色 (Beta)' },
];

// Single source of truth for the selector UI. `swatch` mirrors the seed defined
// in style.css (:root / [data-theme=...]) purely for rendering the picker dot.
export const THEMES = [
  { id: 'amber',   label: 'Amber 琥珀',   swatch: '#e0a458' },
  { id: 'cool',    label: 'Cool 冷蓝',    swatch: '#60a5fa' },
  { id: 'emerald', label: 'Emerald 翠绿', swatch: '#34d399' },
  { id: 'violet',  label: 'Violet 紫罗兰', swatch: '#a78bfa' },
  { id: 'rose',    label: 'Rose 玫瑰',    swatch: '#fb7185' },
  { id: 'cyan',    label: 'Cyan 青碧',    swatch: '#22d3ee' },
  { id: 'slate',   label: 'Slate 石墨',   swatch: '#94a3b8' },
];

const VALID = new Set(THEMES.map((t) => t.id));

export function normalizeTheme(value) {
  return VALID.has(value) ? value : DEFAULT_THEME;
}

export function getStoredTheme() {
  try {
    return normalizeTheme(localStorage.getItem(STORAGE_KEY));
  } catch {
    return DEFAULT_THEME;
  }
}

// Apply the theme to the document root. The default theme carries no data-theme
// attribute so it falls through to the :root seed.
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
    localStorage.setItem(STORAGE_KEY, next);
  } catch {
    /* storage unavailable — theme still applies for this session */
  }
  return next;
}

// Call once at startup, before mount, to avoid a flash of the default accent.
export function initTheme() {
  const theme = applyTheme(getStoredTheme());
  const mode = initMode();
  return { theme, mode };
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
