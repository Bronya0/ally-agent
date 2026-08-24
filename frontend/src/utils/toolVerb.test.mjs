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

import { toolVerbLabel, hasNamedVerb } from './toolVerb.mjs';

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

test('error status names the action instead of a bare failure', () => {
  assert.equal(toolVerbLabel('service', 'other', 'error', 'stop'), 'Service stop failed');
  assert.equal(toolVerbLabel('edit', 'edit', 'error'), 'Edit failed');
});

test('action-keyed tools still count as named verbs', () => {
  assert.ok(hasNamedVerb('service'));
  assert.ok(hasNamedVerb('scheduled_task'));
});
