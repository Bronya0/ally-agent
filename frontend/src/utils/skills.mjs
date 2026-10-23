/*
 * SPDX-License-Identifier: GPL-3.0-only
 *
 * Copyright (C) 2026 tangssst <tangssst@qq.com>
 * GitHub: https://github.com/Bronya0/ally-agent
 *
 * This file is part of ally-agent, licensed under the GNU General
 * Public License v3. See the LICENSE file for details.
 */
// Match backend skill identity: preserve Unicode and punctuation; trim and ignore case.
export function normalizeSkillName(name) {
  return String(name || '').trim().toLowerCase();
}

export function isSkillActive(name, activeSkillNames) {
  const target = normalizeSkillName(name);
  const list = Array.isArray(activeSkillNames) ? activeSkillNames : [];
  return target !== '' && list.some((item) => normalizeSkillName(item) === target);
}
