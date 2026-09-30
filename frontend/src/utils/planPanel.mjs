/*
 * SPDX-License-Identifier: GPL-3.0-only
 *
 * Copyright (C) 2026 tangssst <tangssst@qq.com>
 * GitHub: https://github.com/Bronya0/ally-agent
 *
 * This file is part of ally-agent, licensed under the GNU General
 * Public License v3. See the LICENSE file for details.
 */
// Every status a plan carries, declared once: the panel renders them as classes
// and symbols, and readers that only care whether a step is finished must agree
// on what an unexpected value means, so the fallback lives here instead of at
// each reader.
const PLAN_STATUSES = ['pending', 'in_progress', 'done'];

export function normalizePlanEntries(steps) {
  if (!Array.isArray(steps)) return [];
  return steps
    .filter((step) => step && typeof step === 'object' && String(step.title || '').trim())
    .map((step, sourceIndex) => {
      const title = String(step.title || '').trim();
      const raw = String(step.status || '').trim();
      const status = PLAN_STATUSES.includes(raw) ? raw : 'pending';
      return {
        key: `${sourceIndex}:${status}:${title}`,
        sourceIndex,
        number: sourceIndex + 1,
        status,
        title,
      };
    });
}

// planDoneCount reports how many steps a plan has carried out. The header reads
// this instead of the current step's number: once the work reaches the last step
// nothing is current any more, and counting the done rows is the reading that
// stays meaningful both while a plan runs and after it ends.
export function planDoneCount(steps) {
  return normalizePlanEntries(steps).filter((step) => step.status === 'done').length;
}

// The panel keeps the plan in its original creation order: items are listed
// exactly as they were defined, so the displayed numbers always read 1, 2, 3…
// from top to bottom. Completed items stay in place instead of moving.
export function orderPlanPanelEntries(steps) {
  return normalizePlanEntries(steps);
}

// Scroll delta that centers the current in_progress item inside the plan
// panel list viewport. Positive means the item sits below the visible area
// and the list needs to scroll down; negative scrolls back up.
export function planFocusScrollDelta(listRect, itemRect) {
  return itemRect.top - listRect.top - (listRect.height - itemRect.height) / 2;
}
