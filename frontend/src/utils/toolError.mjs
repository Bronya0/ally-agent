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
 * Format tool error text for UI display. The body is the very text the model
 * receives, so nothing is reworded here: only two envelope shells are peeled
 * off — the leading "[CODE] " marker (the card shows the code in its own chip)
 * and the trailing blocked-command echo (the command card title shows it).
 */
const CODE_PREFIX = /^\[E_[A-Z0-9_]+\][ \t]*/;

export function formatToolErrorBody(body) {
  const text = String(body || '').replace(/\r\n/g, '\n').replace(CODE_PREFIX, '');
  if (!text) return '';

  const lines = text.split('\n');
  let cut = -1;
  for (let i = lines.length - 1; i >= 0; i -= 1) {
    if (/^\s*被拦截的命令[：:]/.test(lines[i])) {
      cut = i;
      break;
    }
  }
  if (cut >= 0) {
    lines.splice(cut);
  }

  return lines.join('\n').replace(/\s+$/u, '');
}
