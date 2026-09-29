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

import { isActionKeyedTool, hasNamedVerb, toolActionFromArgs, toolVerbLabel } from './toolVerb.mjs';

test('service verb follows the parsed action', () => {
  assert.equal(toolVerbLabel('service', 'other', 'success', 'stop'), 'Stopped service');
  assert.equal(toolVerbLabel('service', 'other', 'running', 'stop'), 'Stopping service');
  assert.equal(toolVerbLabel('service', 'other', 'success', 'start'), 'Started service');
  assert.equal(toolVerbLabel('service', 'other', 'success', 'list'), 'Listed services');
  assert.equal(toolVerbLabel('service', 'other', 'success', 'read'), 'Read service output');
});

test('service verb falls back when the action is unknown or absent', () => {
  assert.equal(toolVerbLabel('service', 'other', 'success'), 'Started service');
  assert.equal(toolVerbLabel('service', 'other', 'success', ''), 'Started service');
  assert.equal(toolVerbLabel('service', 'other', 'success', 'bogus'), 'Started service');
});

test('scheduled_task keeps its action-keyed verbs', () => {
  assert.equal(toolVerbLabel('scheduled_task', 'other', 'success', 'create'), 'Created Scheduled Task');
  assert.equal(toolVerbLabel('scheduled_task', 'other', 'success', 'delete'), 'Deleted Scheduled Task');
  assert.equal(toolVerbLabel('scheduled_task', 'other', 'success', 'list'), 'Listed Scheduled Tasks');
});

test('plan verb follows the action the call took', () => {
  assert.equal(toolVerbLabel('plan', 'plan', 'running', 'set'), 'Planning');
  assert.equal(toolVerbLabel('plan', 'plan', 'success', 'set'), 'Planned');
  assert.equal(toolVerbLabel('plan', 'plan', 'success', 'clear'), 'Cleared plan');
  assert.equal(toolVerbLabel('plan', 'plan', 'running', 'next'), 'Next step');
  assert.equal(toolVerbLabel('plan', 'plan', 'success', 'finish'), 'Finished plan');
  assert.equal(toolVerbLabel('plan', 'plan', 'success', 'read'), 'Read plan');
});

// A plan call states its action by which source it carries; which one it is has
// to be read from the arguments, because the plan tool has no action field.
test('plan action is read from the call arguments', () => {
  assert.equal(toolActionFromArgs('plan', { steps: ['Read code', 'Run tests'] }), 'set');
  assert.equal(toolActionFromArgs('plan', { steps: [] }), 'clear');
  assert.equal(toolActionFromArgs('plan', { next: 'Run tests' }), 'next');
  assert.equal(toolActionFromArgs('plan', { finish: true }), 'finish');
  // No source states no action. {} is also what arguments that have not arrived
  // yet parse to, so the card must not claim the read-back call here — it falls
  // back to the plain tool name and the result fills the action in.
  assert.equal(toolActionFromArgs('plan', {}), '');
  assert.equal(toolActionFromArgs('plan', { next: '   ' }), '');
  assert.equal(toolActionFromArgs('plan', { finish: false }), '');
  assert.equal(toolActionFromArgs('plan', null), '');
  assert.equal(toolVerbLabel('plan', 'plan', 'success', ''), 'Plan');
});

// The action is captured only for the tools whose verb is keyed by it; every
// other card must not pick up an `action` field of its own arguments.
test('only action-keyed tools give up their action', () => {
  assert.equal(toolActionFromArgs('service', { action: 'STOP' }), 'stop');
  assert.equal(toolActionFromArgs('scheduled_task', { action: 'list' }), 'list');
  assert.equal(toolActionFromArgs('service', {}), '');
  assert.equal(toolActionFromArgs('edit', { action: 'delete' }), '');
  // plan reads its own three sources, never a foreign action key.
  assert.equal(toolActionFromArgs('plan', { action: 'stop' }), '');
  assert.ok(isActionKeyedTool('plan'));
  assert.ok(isActionKeyedTool('service'));
  assert.ok(!isActionKeyedTool('edit'));
});

test('error status names the action instead of a bare failure', () => {
  assert.equal(toolVerbLabel('service', 'other', 'error', 'stop'), 'Service stop failed');
  assert.equal(toolVerbLabel('edit', 'edit', 'error'), 'Edit failed');
  assert.equal(toolVerbLabel('plan', 'plan', 'error', 'finish'), 'Plan finish failed');
});

test('action-keyed tools still count as named verbs', () => {
  assert.ok(hasNamedVerb('service'));
  assert.ok(hasNamedVerb('scheduled_task'));
  assert.ok(hasNamedVerb('plan'));
});
