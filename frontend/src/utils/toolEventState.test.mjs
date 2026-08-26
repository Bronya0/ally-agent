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

import {
  commitToolEventMessage,
  findToolEventMessage,
  findToolEventByData,
  normalizeToolStatus,
  setToolStatus,
  toolEventId,
} from './toolEventState.mjs';

test('streaming updates keep one tool card and update its arguments', () => {
  const session = { messages: [] };
  const eventId = 'run-1:tool:0:0';

  let existing = findToolEventMessage(session, eventId);
  commitToolEventMessage(session, eventId, existing, {
    role: 'tool_call',
    eventId,
    name: 'command',
    body: '',
  });

  existing = findToolEventMessage(session, eventId);
  commitToolEventMessage(session, eventId, existing, {
    role: 'tool_call',
    eventId,
    name: 'command',
    body: '{"command":"go test ./..."}',
  });

  assert.equal(session.messages.length, 1);
  assert.equal(session.messages[0].body, '{"command":"go test ./..."}');
  assert.equal(findToolEventMessage(session, eventId), session.messages[0]);
});

test('toolEventId is deterministic from run/step/index coordinates', () => {
  const base = { runId: 'r1', toolBatchId: 'b2', toolCallIndex: 3, name: 'edit' };
  assert.equal(toolEventId(base), 'r1:tool:b2:3');
  // toolBatchId omitted -> step coordinate drops out of the id
  assert.equal(toolEventId({ runId: 'r1', toolCallIndex: 3 }), 'r1:tool:3');
  // no run coordinates -> opaque id falls back to toolCallId then name+time
  assert.equal(toolEventId({ toolCallId: 'tc-9' }), 'tc-9');
});

test('findToolEventByData matches by deterministic eventId', () => {
  const session = {
    messages: [{ role: 'tool_call', eventId: 'r1:tool:b2:3', name: 'edit' }],
  };
  const found = findToolEventByData(session, { runId: 'r1', toolBatchId: 'b2', toolCallIndex: 3, name: 'edit' });
  assert.equal(found, session.messages[0]);
});

test('findToolEventByData matches by opaque toolCallId', () => {
  const session = {
    messages: [{ role: 'tool_call', eventId: 'opaque-1', toolCallId: 'tc-9', name: 'command' }],
  };
  // neither side carries run coordinates, so the id fallback diverges; the
  // toolCallId branch must still bind the card.
  const found = findToolEventByData(session, { toolCallId: 'tc-9', name: 'command' });
  assert.equal(found, session.messages[0]);
});

test('findToolEventByData matches by runId + toolCallIndex', () => {
  const session = {
    // eventId intentionally differs so the test exercises the index branch,
    // not the eventId shortcut.
    messages: [{ role: 'tool_call', eventId: 'stale', runId: 'r1', toolCallIndex: 0, toolBatchId: '', name: 'read' }],
  };
  const found = findToolEventByData(session, { runId: 'r1', toolCallIndex: 0, name: 'read' });
  assert.equal(found, session.messages[0]);
});

test('findToolEventByData never lands a step-N result on a step-(N-1) card', () => {
  const session = {
    messages: [
      { role: 'tool_call', eventId: 'r1:tool:b1:0', runId: 'r1', toolCallIndex: 0, toolBatchId: 'b1', name: 'grep' },
    ],
  };
  // A result for step b2, same index 0, must NOT match the step b1 card.
  const mismatch = findToolEventByData(session, { runId: 'r1', toolCallIndex: 0, toolBatchId: 'b2', name: 'grep' });
  assert.equal(mismatch, null);
  // Once the step b2 card exists, it is the one matched.
  session.messages.push({ role: 'tool_call', eventId: 'r1:tool:b2:0', runId: 'r1', toolCallIndex: 0, toolBatchId: 'b2', name: 'grep' });
  const match = findToolEventByData(session, { runId: 'r1', toolCallIndex: 0, toolBatchId: 'b2', name: 'grep' });
  assert.equal(match, session.messages[1]);
  assert.notEqual(match, session.messages[0]);
});

test('findToolEventByData requires run coordinates when index is present but runId missing', () => {
  const session = {
    messages: [{ role: 'tool_call', eventId: 'x', runId: 'r1', toolCallIndex: 0, toolBatchId: '', name: 'read' }],
  };
  // data has an index but no runId -> hasIndex is false -> no index match.
  const found = findToolEventByData(session, { toolCallIndex: 0, name: 'read' });
  assert.equal(found, null);
});

test('findToolEventByData ignores non-tool_call messages', () => {
  const session = {
    messages: [{ role: 'assistant', eventId: 'r1:tool:b1:0' }],
  };
  assert.equal(findToolEventByData(session, { runId: 'r1', toolBatchId: 'b1', toolCallIndex: 0 }), null);
});

test('setToolStatus writes the canonical success status', () => {
  const msg = { role: 'tool_call', status: 'running' };
  const ret = setToolStatus(msg, 'success');
  assert.equal(msg.status, 'success');
  // chainable: returns the same message object
  assert.equal(ret, msg);
});

test('setToolStatus collapses legacy sub-agent aliases', () => {
  // The backend historically used `completed`/`failed` for sub-agent records;
  // routing every flip through setToolStatus must converge them to the
  // canonical success/error vocabulary so the UI never sees a stray string.
  assert.equal(setToolStatus({}, 'completed').status, 'success');
  assert.equal(setToolStatus({}, 'failed').status, 'error');
  assert.equal(setToolStatus({}, 'done').status, 'success');
  assert.equal(setToolStatus({}, 'in_progress').status, 'running');
  assert.equal(setToolStatus({}, 'info').status, 'running');
});

test('setToolStatus is null-safe and idempotent', () => {
  // no guard needed at call sites: a falsy msg is returned untouched
  assert.equal(setToolStatus(null, 'success'), null);
  assert.equal(setToolStatus(undefined, 'error'), undefined);
  const msg = { role: 'tool_call', status: 'running' };
  setToolStatus(msg, 'success');
  setToolStatus(msg, 'success');
  assert.equal(msg.status, 'success');
});

test('normalizeToolStatus maps the full vocabulary and falls back to default', () => {
  assert.equal(normalizeToolStatus('success'), 'success');
  assert.equal(normalizeToolStatus('error'), 'error');
  assert.equal(normalizeToolStatus('running'), 'running');
  assert.equal(normalizeToolStatus('completed'), 'success');
  assert.equal(normalizeToolStatus('failed'), 'error');
  assert.equal(normalizeToolStatus('info'), 'running');
  assert.equal(normalizeToolStatus('bogus'), 'default');
  assert.equal(normalizeToolStatus('pending'), 'default');
});
