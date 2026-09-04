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

// fmtCompact is the single compact number format (lowercase k/M suffix) for
// token counts, context windows and char counts: 990 -> "990", 1500 -> "2k",
// 1500000 -> "2M". Values round to integers; callers that previously showed
// decimals (e.g. 1.5k) or an uppercase K intentionally converge here.
export function fmtCompact(n) {
  const value = Number(n) || 0;
  if (value >= 1e6) return Math.round(value / 1e6) + 'M';
  if (value >= 1e3) return Math.round(value / 1e3) + 'k';
  return String(Math.round(value));
}

// fmtDuration renders a bounded duration: "" for invalid/non-positive values,
// "<1s" below one second, then hours/minutes/seconds with one significant
// unit pair (e.g. 65000 -> "1m5s", 3700000 -> "1h2m").
export function fmtDuration(ms) {
  const value = Number(ms);
  if (!Number.isFinite(value) || value <= 0) return '';
  if (value < 1000) return '<1s';
  const secs = Math.max(1, Math.round(value / 1000));
  const hours = Math.floor(secs / 3600);
  const mins = Math.floor((secs % 3600) / 60);
  const rest = secs % 60;
  if (hours > 0) return `${hours}h${mins > 0 ? `${mins}m` : ''}`;
  if (mins > 0) return `${mins}m${rest > 0 ? `${rest}s` : ''}`;
  return `${rest}s`;
}
