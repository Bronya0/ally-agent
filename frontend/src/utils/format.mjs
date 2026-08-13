/*
 * SPDX-License-Identifier: GPL-3.0-only
 *
 * Copyright (C) 2026 tangssst <tangssst@qq.com>
 * GitHub: https://github.com/Bronya0/ally-agent
 *
 * This file is part of ally-agent, licensed under the GNU General
 * Public License v3. See the LICENSE file for details.
 */
// Shared number formatting helpers for the token stats UI.
// Kept dependency-free so it stays testable like the other utils.

// fmtTokens renders a token count compactly: 1.2k / 3.4M / 1.02B.
export function fmtTokens(n) {
  const value = Number(n || 0);
  if (value >= 1e9) return (value / 1e9).toFixed(2) + 'B';
  if (value >= 1e6) return (value / 1e6).toFixed(2) + 'M';
  if (value >= 1e3) return (value / 1e3).toFixed(1) + 'k';
  return String(value);
}

// fmtNum renders an integer with thousands separators.
export function fmtNum(n) {
  return Number(n || 0).toLocaleString();
}
