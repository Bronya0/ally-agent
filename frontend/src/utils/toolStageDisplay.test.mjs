// SPDX-License-Identifier: GPL-3.0-only
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import { isStageBlockedTool, setToolStatus, findToolEventByData, toolEventId } from './toolEventState.mjs';
import { displaySourceMessages, isRenderableMessage } from './toolPreview.mjs';
import { toolCardRenderSignature } from './toolCardSignature.mjs';

// Execute the real event wiring without importing the desktop i18n/UI runtime.
const eventsSource = readFileSync(new URL('../composables/useToolEvents.mjs', import.meta.url), 'utf8');
const useToolEvents = new Function('setToolStatus', 'findToolEventByData', 'toolEventId', 'isStageBlockedTool',
  eventsSource.slice(eventsSource.indexOf('export function useToolEvents')).replace('export function', 'function') + '; return useToolEvents;',
)(setToolStatus, findToolEventByData, toolEventId, isStageBlockedTool);

function fixture() {
  const session = { id: 'session', messages: [] };
  const handlers = new Map();
  let saves = 0;
  const noop = () => {};
  useToolEvents({
    onRuntimeEvent: (name, fn) => handlers.set(name, fn),
    sessionByEvent: () => session,
    flushStreamBuffer: noop, flushToolUpdateBuffer: noop,
    finalizeStreamingMessageForRun: noop, refreshContextTokens: noop,
    isHiddenTool: () => false,
    activeSessionId: { value: 'session' },
    scheduleSaveSessions: () => { saves++; },
    scrollMessagesToBottom: noop,
    formatDurationShort: () => '',
    appendToolEventFallback: () => { assert.fail('skip created a fallback card'); },
  });
  return { session, error: handlers.get('tool:error'), get saves() { return saves; } };
}

test('stage-blocked events remove placeholders, preserve real failures, and stay idempotent', () => {
  const f = fixture();
  const failed = { role: 'tool_call', kind: 'command', toolCallId: 'failed', status: 'running' };
  const skipped = { role: 'tool_call', kind: 'command', toolCallId: 'skipped', status: 'running' };
  f.session.messages.push(failed, skipped);
  f.error({ name: 'command', toolCallId: 'failed', error: 'actual failure', errorCode: 'E_COMMAND_FAILED' });
  assert.equal(failed.status, 'error');
  assert.equal(failed.body, 'actual failure');
  const event = { name: 'command', toolCallId: 'skipped', errorCode: 'E_STAGE_BLOCKED' };
  f.error(event);
  f.error(event);
  assert.deepEqual(f.session.messages, [failed]);
  assert.equal(f.saves, 2, 'removal is persisted; repeated events never recreate a card');
});

test('stored skip cards use the shared visibility predicate and invalidate display caching', () => {
  const failed = { role: 'tool_call', kind: 'command', status: 'error', errorCode: 'E_COMMAND_FAILED' };
  const skipped = { ...failed, errorCode: 'E_STAGE_BLOCKED' };
  assert.equal(isRenderableMessage(failed), true);
  assert.equal(isRenderableMessage(skipped), false);
  assert.notEqual(toolCardRenderSignature(failed), toolCardRenderSignature(skipped));
  assert.deepEqual(displaySourceMessages({ messages: [failed, skipped] }), [failed]);
});

test('sub-agent skip events remove the placeholder instead of showing an error', () => {
  const source = readFileSync(new URL('../App.vue', import.meta.url), 'utf8');
  const start = source.indexOf("  onRuntimeEvent('sub:tool:error',");
  const end = source.indexOf("  onRuntimeEvent('sub:done',", start);
  assert.ok(start >= 0 && end > start);
  const msg = { toolCalls: [{ toolCallId: 'failed' }, { toolCallId: 'skipped' }] };
  let handler;
  let saves = 0;
  new Function('onRuntimeEvent', 'findSubagentMsg', 'isStageBlockedTool', 'scheduleSaveSessions', source.slice(start, end))(
    (_name, fn) => { handler = fn; }, () => msg, isStageBlockedTool, () => { saves++; },
  );
  handler({ toolCallId: 'skipped', errorCode: 'E_STAGE_BLOCKED' });
  assert.deepEqual(msg.toolCalls, [{ toolCallId: 'failed' }]);
  assert.equal(saves, 1);
});
