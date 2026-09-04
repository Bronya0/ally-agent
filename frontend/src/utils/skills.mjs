/*
 * SPDX-License-Identifier: GPL-3.0-only
 *
 * Copyright (C) 2026 tangssst <tangssst@qq.com>
 * GitHub: https://github.com/Bronya0/ally-agent
 *
 * This file is part of ally-agent, licensed under the GNU General
 * Public License v3. See the LICENSE file for details.
 */
// Single source of truth for skill-name normalization and active-state lookup.
// App.vue and SettingsModal.vue used to carry two divergent copies (plain
// trim+lowercase vs hyphen-collapse+strip); this module adopts the stronger
// normalization, which is the correct canonical form for matching backend
// skill names.

// normalizeSkillName collapses a skill name to its canonical key: lowercase,
// runs of hyphens/whitespace become a single hyphen, and every character
// outside [a-z0-9-] is dropped. e.g. "My_Skill  v2" -> "myskill-v2".
export function normalizeSkillName(name) {
  return String(name || '').toLowerCase().replace(/[-\s]+/g, '-').replace(/[^a-z0-9-]/g, '');
}

// isSkillActive reports whether `name` is present in the active skill name
// list under the canonical key. `activeSkillNames` is the array of enabled
// skill names (the GetActiveSkills binding result); missing/invalid input
// simply compares false.
export function isSkillActive(name, activeSkillNames) {
  const target = normalizeSkillName(name);
  const list = Array.isArray(activeSkillNames) ? activeSkillNames : [];
  return list.some((item) => normalizeSkillName(item) === target);
}
