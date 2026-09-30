/*
 * SPDX-License-Identifier: GPL-3.0-only
 *
 * Copyright (C) 2026 tangssst <tangssst@qq.com>
 * GitHub: https://github.com/Bronya0/ally-agent
 *
 * This file is part of ally-agent, licensed under the GNU General
 * Public License v3. See the LICENSE file for details.
 */
import assert from 'node:assert/strict';
import test from 'node:test';

import { orderPlanPanelEntries, planDoneCount, planFocusScrollDelta } from './planPanel.mjs';

function compact(entries) {
  return entries.map(({ status, title }) => `${status}:${title}`);
}

test('plan entries keep their original creation order', () => {
  const entries = orderPlanPanelEntries([
    { title: 'Inspect implementation', status: 'done' },
    { title: 'Update UI', status: 'in_progress' },
    { title: 'Verify behavior', status: 'pending' },
  ]);

  assert.deepEqual(compact(entries), [
    'done:Inspect implementation',
    'in_progress:Update UI',
    'pending:Verify behavior',
  ]);
});

// Ordering is independent of status: a done item before pending work stays
// before it, and the current item is not moved to the top.
test('order is stable across status changes', () => {
  const entries = orderPlanPanelEntries([
    { title: 'First', status: 'in_progress' },
    { title: 'Second', status: 'done' },
    { title: 'Third', status: 'pending' },
  ]);

  assert.deepEqual(compact(entries), [
    'in_progress:First',
    'done:Second',
    'pending:Third',
  ]);
});

test('plan panel degrades gracefully when a list has no current item', () => {
  const entries = orderPlanPanelEntries([
    { title: 'Finished task', status: 'done' },
    { title: 'Not yet started', status: 'pending' },
  ]);

  assert.deepEqual(compact(entries), [
    'done:Finished task',
    'pending:Not yet started',
  ]);
});

test('planFocusScrollDelta centers the current item inside the list viewport', () => {
  // 200px-tall viewport, item below the fold: positive delta scrolls it up to the middle.
  const below = planFocusScrollDelta(
    { top: 0, height: 200 },
    { top: 400, height: 24 },
  );
  assert.equal(below, 400 - 0 - (200 - 24) / 2);

  // Item above the fold: negative delta scrolls back up.
  const above = planFocusScrollDelta(
    { top: 0, height: 200 },
    { top: -100, height: 24 },
  );
  assert.equal(above, -100 - (200 - 24) / 2);

  // Item already centered: delta is zero.
  const centered = planFocusScrollDelta(
    { top: 0, height: 200 },
    { top: (200 - 24) / 2, height: 24 },
  );
  assert.equal(centered, 0);
});

test('plan entries carry stable plan numbers in source order', () => {
  const entries = orderPlanPanelEntries([
    { title: 'First', status: 'done' },
    { title: 'Second', status: 'in_progress' },
    { title: 'Third', status: 'pending' },
  ]);

  assert.deepEqual(entries.map((entry) => entry.number), [1, 2, 3]);
  assert.equal(entries[1].status, 'in_progress');
  assert.equal(entries[1].number, 2);
});

// The header reports finished steps, not the current step's number: a plan that
// ran to its last step has no current step left. A list carrying a pending row
// above a finished one (a shape the tool does not write) still counts only the
// finished rows.
test('planDoneCount counts finished steps, not the current position', () => {
  assert.equal(planDoneCount([
    { title: 'First', status: 'done' },
    { title: 'Second', status: 'pending' },
    { title: 'Third', status: 'done' },
  ]), 2);

  assert.equal(planDoneCount([]), 0);
  assert.equal(planDoneCount(null), 0);
});

// One declaration of the statuses, one fallback: the panel and the tool card
// must not disagree about the same plan.
test('an unrecognized status reads as pending for every reader', () => {
  const entries = orderPlanPanelEntries([{ title: 'First', status: 'in-progress' }]);
  assert.equal(entries[0].status, 'pending');
  assert.equal(planDoneCount([{ title: 'First', status: 'in-progress' }]), 0);
  assert.equal(planDoneCount([{ title: 'First', status: 'DONE' }]), 0);
});
