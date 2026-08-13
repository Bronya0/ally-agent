import assert from 'node:assert/strict';
import test from 'node:test';

import { orderTodoPanelEntries, todoFocusScrollDelta } from './todoPanel.mjs';

function compact(entries) {
  return entries.map(({ status, title }) => `${status}:${title}`);
}

test('completed items stay on top, newest first, before current and pending work', () => {
  const entries = orderTodoPanelEntries([
    { title: 'Inspect implementation', status: 'done' },
    { title: 'Update UI', status: 'done' },
    { title: 'Verify behavior', status: 'in_progress' },
    { title: 'Run build', status: 'pending' },
    { title: 'Summarize results', status: 'pending' },
  ]);

  assert.deepEqual(compact(entries), [
    'done:Update UI',
    'done:Inspect implementation',
    'in_progress:Verify behavior',
    'pending:Run build',
    'pending:Summarize results',
  ]);
});

test('a new todo list begins with its current item and pending work', () => {
  const entries = orderTodoPanelEntries([
    { title: 'Inspect implementation', status: 'in_progress' },
    { title: 'Run tests', status: 'pending' },
  ]);

  assert.deepEqual(compact(entries), [
    'in_progress:Inspect implementation',
    'pending:Run tests',
  ]);
});

test('todo panel degrades gracefully when a list has no current item', () => {
  const entries = orderTodoPanelEntries([
    { title: 'Finished task', status: 'done' },
    { title: 'Not yet started', status: 'pending' },
  ]);

  assert.deepEqual(compact(entries), [
    'done:Finished task',
    'pending:Not yet started',
  ]);
});

test('todoFocusScrollDelta centers the current item inside the list viewport', () => {
  // 200px-tall viewport, item below the fold: positive delta scrolls it up to the middle.
  const below = todoFocusScrollDelta(
    { top: 0, height: 200 },
    { top: 400, height: 24 },
  );
  assert.equal(below, 400 - 0 - (200 - 24) / 2);

  // Item above the fold: negative delta scrolls back up.
  const above = todoFocusScrollDelta(
    { top: 0, height: 200 },
    { top: -100, height: 24 },
  );
  assert.equal(above, -100 - (200 - 24) / 2);

  // Item already centered: delta is zero.
  const centered = todoFocusScrollDelta(
    { top: 0, height: 200 },
    { top: (200 - 24) / 2, height: 24 },
  );
  assert.equal(centered, 0);
});

test('todo entries carry stable plan numbers in source order', () => {
  const entries = orderTodoPanelEntries([
    { title: 'First', status: 'done' },
    { title: 'Second', status: 'in_progress' },
    { title: 'Third', status: 'pending' },
  ]);

  assert.deepEqual(entries.map((entry) => entry.number), [1, 2, 3]);
  assert.equal(entries[1].status, 'in_progress');
  assert.equal(entries[1].number, 2);
});
