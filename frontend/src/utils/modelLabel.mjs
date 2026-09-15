/*
 * SPDX-License-Identifier: GPL-3.0-only
 *
 * Copyright (C) 2026 tangssst <tangssst@qq.com>
 * GitHub: https://github.com/Bronya0/ally-agent
 *
 * This file is part of ally-agent, licensed under the GNU General
 * Public License v3. See the LICENSE file for details.
 */
// Single source of truth for the model label shown in the UI: the composer info
// bar (ComposerInfoBar.vue) and the welcome info table's model row (App.vue)
// must render the exact same string, otherwise the two places "disagree" about
// which model is active. Missing provider/model degrade to '-'.
export function formatModelLabel(snapshot) {
  return `${snapshot?.providerName || '-'} · ${snapshot?.model || '-'}`;
}

