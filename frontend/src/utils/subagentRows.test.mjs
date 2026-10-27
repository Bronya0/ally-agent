/*
 * SPDX-License-Identifier: GPL-3.0-only
 *
 * Copyright (C) 2026 tangssst <tangssst@qq.com>
 * GitHub: https://github.com/Bronya0/ally-agent
 *
 * This file is part of ally-agent, licensed under the GNU General
 * Public License v3. See the LICENSE file for details.
 */
import test from 'node:test';
import assert from 'node:assert/strict';
import { subagentRows, subagentBatchStatus, subagentDisplayState } from './subagentRows.mjs';
const agent = (id, runId = 'run') => ({ kind: 'subagent', eventId: id, runId });

test('adjacent sub-agents share a row and preserve source identities and the final stats index', () => {
  const messages = [agent('a'), agent('b'), agent('c')];
  const rows = subagentRows(messages);
  assert.equal(rows.length, 1);
  assert.deepEqual(rows[0].agents, messages);
  assert.equal(rows[0].agents[0], messages[0]);
  assert.equal(rows[0].msg, messages[2]);
  assert.equal(rows[0].index, 2);
  assert.equal(messages.length, 3);
});

test('prose, tools, archives and other runs separate sub-agent grids', () => {
  for (const separator of [{role:'assistant'}, {kind:'read'}, {role:'archive'}, agent('other', 'other-run')]) {
    const messages = [agent('a'), separator, agent('b')];
    assert.equal(subagentRows(messages).length, 3);
  }
});

test('a round statistics boundary stays between the original cards', () => {
  const rows = subagentRows([agent('a'), agent('b'), agent('c')], (_, index) => index === 0);
  assert.deepEqual(rows.map(row => row.agents.length), [1, 2]);
  assert.deepEqual(rows.map(row => row.index), [0, 2]);
});

test('adding a child keeps the first card identity used as the grid key', () => {
  const a = agent('a'), b = agent('b');
  assert.equal(subagentRows([a])[0].agents[0], subagentRows([a, b])[0].agents[0]);
  assert.deepEqual(subagentRows([]), []);
});

test('batch status waits for running siblings, then reports aggregate success or failure', () => {
  const states = (...statuses) => statuses.map(status => ({status}));
  assert.equal(subagentBatchStatus(states('failed', 'running', 'completed')), 'running');
  assert.equal(subagentBatchStatus(states('completed', 'success')), 'success');
  assert.equal(subagentBatchStatus([{status:'completed', stopReason:'step_limit'}]), 'partial');
  assert.equal(subagentBatchStatus(states('completed', 'failed')), 'error');
  assert.equal(subagentBatchStatus([{status:'completed', error:'missing report'}]), 'error');
  assert.equal(subagentBatchStatus(states('pending', 'completed')), 'default');
  assert.equal(subagentBatchStatus([]), 'default');
});

test('the card state reports partial only for a clean finish that ran out of steps', () => {
  assert.equal(subagentDisplayState({ status: 'completed', stopReason: 'step_limit' }), 'partial');
  assert.equal(subagentDisplayState({ status: 'completed' }), 'success');
  // A real failure is never softened into "step limit reached", even when the
  // run also stopped at the limit.
  assert.equal(subagentDisplayState({ status: 'failed', stopReason: 'step_limit' }), 'error');
  assert.equal(subagentDisplayState({ status: 'completed', error: 'missing report', stopReason: 'step_limit' }), 'error');
  assert.equal(subagentDisplayState({ status: 'running' }), 'running');
  assert.equal(subagentDisplayState({}), 'default');
  assert.equal(subagentDisplayState(null), 'default');
  // A still-running sibling keeps the batch busy regardless of the reason.
  assert.equal(subagentBatchStatus([{ status: 'completed', stopReason: 'step_limit' }, { status: 'running' }]), 'running');
});

test('sequential tool batches in one run keep separate grids and aggregate statuses', () => {
  const messages = [
    { ...agent('a'), toolBatchId: 'batch-1', status: 'success' },
    { ...agent('b'), toolBatchId: 'batch-1', status: 'success' },
    { ...agent('c'), toolBatchId: 'batch-2', status: 'running' },
    { ...agent('d'), toolBatchId: 'batch-2', status: 'failed' },
  ];
  const rows = subagentRows(messages);
  assert.deepEqual(rows.map(row => row.agents.map(msg => msg.eventId)), [['a', 'b'], ['c', 'd']]);
  assert.deepEqual(rows.map(row => subagentBatchStatus(row.agents)), ['success', 'running']);
  assert.deepEqual(rows.map(row => row.index), [1, 3]);
});

test('a known batch does not absorb an adjacent legacy card without batch metadata', () => {
  assert.equal(subagentRows([agent('legacy'), { ...agent('new'), toolBatchId: 'batch' }]).length, 2);
});
