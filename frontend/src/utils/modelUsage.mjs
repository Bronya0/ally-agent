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
const MODEL_STORAGE_KEY = 'ally_specific_model_usage';

function readUsage(storageKey = STORAGE_KEY) {
  try {
    const raw = localStorage.getItem(storageKey);
    if (!raw) return {};
    const parsed = JSON.parse(raw);
    if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) return {};
    return parsed;
  } catch {
    return {};
  }
}

// getModelUsage returns the persisted `{ key: count }` map for provider groups.
export function getModelUsage() {
  return readUsage(STORAGE_KEY);
}

// getSpecificModelUsage returns the persisted `{ modelIdentity: count }` map.
export function getSpecificModelUsage() {
  return readUsage(MODEL_STORAGE_KEY);
}

// recordModelUsage bumps the count for `key` (provider group) and optionally `modelIdentity`.
export function recordModelUsage(key, validKeys, modelIdentity, validModelIdentities) {
  const k = String(key || '').trim();
  if (k) {
    const usage = readUsage(STORAGE_KEY);
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
    } catch {}
  }

  const mid = String(modelIdentity || '').trim();
  if (mid) {
    const modelUsageMap = readUsage(MODEL_STORAGE_KEY);
    modelUsageMap[mid] = (Number(modelUsageMap[mid]) || 0) + 1;
    if (Array.isArray(validModelIdentities) && validModelIdentities.length) {
      const validMids = new Set(validModelIdentities.map((v) => String(v || '').trim()).filter(Boolean));
      validMids.add(mid);
      for (const existing of Object.keys(modelUsageMap)) {
        if (!validMids.has(existing)) delete modelUsageMap[existing];
      }
    }
    try {
      localStorage.setItem(MODEL_STORAGE_KEY, JSON.stringify(modelUsageMap));
    } catch {}
  }
}
