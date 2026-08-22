/*
 * SPDX-License-Identifier: GPL-3.0-only
 *
 * Copyright (C) 2026 tangssst <tangssst@qq.com>
 * GitHub: https://github.com/Bronya0/ally-agent
 *
 * This file is part of ally-agent, licensed under the GNU General
 * Public License v3. See the LICENSE file for details.
 */
// Model group usage frequency. Purely a front-end concern: a `{ key: count }`
// map persisted in localStorage so the model dropdown can order provider groups
// by how often the user picks a model from each group. Independent of the
// backend config, so it survives reloads and new sessions.
//
// Compatibility / lifecycle:
//   - Missing or malformed storage reads as an empty map, so existing users
//     with no data keep the previous alphabetical/active-first ordering.
//   - Added providers simply start at count 0.
//   - Removed providers are pruned on the next recorded switch (see the
//     optional validKeys argument), and any stale keys are ignored by readers
//     until then.

const STORAGE_KEY = 'ally_model_usage';

function readUsage() {
  try {
    const raw = localStorage.getItem(STORAGE_KEY);
    if (!raw) return {};
    const parsed = JSON.parse(raw);
    if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) return {};
    return parsed;
  } catch {
    return {};
  }
}

// getModelUsage returns the persisted `{ key: count }` map. Callers read counts
// with `usage[key] || 0`; unknown keys are treated as zero.
export function getModelUsage() {
  return readUsage();
}

// recordModelUsage bumps the count for `key` by one. When `validKeys` is given
// (the provider keys currently present in the config), keys outside that set
// are dropped in the same write so removed providers do not accumulate stale
// counts.
export function recordModelUsage(key, validKeys) {
  const k = String(key || '').trim();
  if (!k) return;
  const usage = readUsage();
  usage[k] = (Number(usage[k]) || 0) + 1;
  if (Array.isArray(validKeys) && validKeys.length) {
    const valid = new Set(validKeys.map((v) => String(v || '').trim()).filter(Boolean));
    valid.add(k);
    for (const existing of Object.keys(usage)) {
      if (!valid.has(existing)) delete usage[existing];
    }
  }
  try {
    localStorage.setItem(STORAGE_KEY, JSON.stringify(usage));
  } catch {
    /* storage unavailable — ordering falls back to the in-memory default */
  }
}
