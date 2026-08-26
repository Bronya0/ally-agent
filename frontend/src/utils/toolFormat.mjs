/*
 * SPDX-License-Identifier: GPL-3.0-only
 *
 * Copyright (C) 2026 tangssst <tangssst@qq.com>
 * GitHub: https://github.com/Bronya0/ally-agent
 *
 * This file is part of ally-agent, licensed under the GNU General
 * Public License v3. See the LICENSE file for details.
 */

/**
 * Build a compact chip describing a read range, e.g. "· 12 lines 1-12" or
 * "· 5 lines 8-12 (truncated)". Pure and dependency-free so it can be unit
 * tested without the Vue runtime.
 */
export function formatReadRangeChip(startLine, endLine, totalLines, truncated) {
  const start = Number(startLine) || 1;
  const end = Number(endLine) || Number(totalLines) || 0;
  const total = Number(totalLines) || 0;
  const actualLines = total > 0 ? end - start + 1 : 0;
  if (actualLines <= 0) return '';
  const parts = [];
  if (start > 1 || end < total) {
    parts.push(`${actualLines} line${actualLines !== 1 ? 's' : ''} ${start}-${end}`);
    if (truncated) parts.push('(truncated)');
  } else {
    parts.push(`${actualLines} line${actualLines !== 1 ? 's' : ''}`);
  }
  return parts.length ? `· ${parts.join(' · ')}` : '';
}
