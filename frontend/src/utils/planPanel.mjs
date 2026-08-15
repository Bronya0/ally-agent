/*
 * SPDX-License-Identifier: GPL-3.0-only
 *
 * Copyright (C) 2026 tangssst <tangssst@qq.com>
 * GitHub: https://github.com/Bronya0/ally-agent
 *
 * This file is part of ally-agent, licensed under the GNU General
 * Public License v3. See the LICENSE file for details.
 */
function normalizeTodoEntries(todos) {
  if (!Array.isArray(todos)) return [];
  return todos
    .filter((todo) => todo && typeof todo === 'object' && String(todo.title || '').trim())
    .map((todo, sourceIndex) => {
      const title = String(todo.title || '').trim();
      const status = String(todo.status || '').trim();
      return {
        key: `${sourceIndex}:${status}:${title}`,
        sourceIndex,
        number: sourceIndex + 1,
        status,
        title,
      };
    });
}

// The panel keeps the plan in its original creation order: items are listed
// exactly as they were defined, so the displayed numbers always read 1, 2, 3…
// from top to bottom. Completed items stay in place instead of moving.
export function orderPlanPanelEntries(todos) {
  return normalizeTodoEntries(todos);
}

// Scroll delta that centers the current in_progress item inside the plan
// panel list viewport. Positive means the item sits below the visible area
// and the list needs to scroll down; negative scrolls back up.
export function planFocusScrollDelta(listRect, itemRect) {
  return itemRect.top - listRect.top - (listRect.height - itemRect.height) / 2;
}
