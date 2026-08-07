import assert from 'node:assert/strict';
import test from 'node:test';

import { orderTodoPanelEntries } from './todoPanel.mjs';

function compact(entries) {
  return entries.map(({ status, title }) => `${status}:${title}`);
}

test('todo panel puts the latest completed item before current and pending work', () => {
  const entries = orderTodoPanelEntries([
    { title: 'Inspect implementation', status: 'done' },
    { title: 'Update UI', status: 'done' },
    { title: 'Verify behavior', status: 'in_progress' },
    { title: 'Run build', status: 'pending' },
    { title: 'Summarize results', status: 'pending' },
  ]);

  assert.deepEqual(compact(entries), [
    'done:Update UI',
    'in_progress:Verify behavior',
    'pending:Run build',
    'pending:Summarize results',
    'done:Inspect implementation',
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
    'pending:Not yet started',
    'done:Finished task',
  ]);
});
