// SPDX-License-Identifier: GPL-3.0-only
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import { isStageBlockedTool, setToolStatus, findToolEventByData, toolEventId } from './toolEventState.mjs';
import { displaySourceMessages, isRenderableMessage } from './toolPreview.mjs';
import { toolCardRenderSignature } from './toolCardSignature.mjs';
import { subagentBatchStatus } from './subagentRows.mjs';

// Execute the real event wiring without importing the desktop i18n/UI runtime.
const eventsSource = readFileSync(new URL('../composables/useToolEvents.mjs', import.meta.url), 'utf8');
const useToolEvents = new Function('setToolStatus', 'findToolEventByData', 'toolEventId', 'isStageBlockedTool',
  eventsSource.slice(eventsSource.indexOf('export function useToolEvents')).replace('export function', 'function') + '; return useToolEvents;',
)(setToolStatus, findToolEventByData, toolEventId, isStageBlockedTool);

function fixture({ fallbackCard = null } = {}) {
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
    scrollMessagesToBottom: noop, scrollMessagesToBottomIfStale: noop,
    formatDurationShort: () => '',
    parseToolResultData: (raw) => { try { return raw ? JSON.parse(raw) : {}; } catch { return {}; } },
    appendToolEventFallback: () => {
      if (!fallbackCard) assert.fail('skip created a fallback card');
      session.messages.push(fallbackCard);
      return fallbackCard;
    },
  });
  return { session, error: handlers.get('tool:error'), result: handlers.get('tool:result'), get saves() { return saves; } };
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

test('a card rebuilt from the sub-agent result keeps the step-limit reason', () => {
  // The card can be rebuilt from the result alone (a missed sub:done event or a
  // fallback card), so the result JSON has to carry the terminal reason by
  // itself; otherwise the UI says "completed" while the parent sees step_limit.
  const card = { role: 'tool_call', kind: 'subagent', toolCallId: 'a1', status: 'running' };
  const f = fixture({ fallbackCard: card });
  f.result({
    name: 'subagent', toolCallId: 'a1',
    result: JSON.stringify({ agentId: 'a1', role: 'tester', status: 'completed', stopReason: 'step_limit', summary: 'partial report', steps: 4 }),
  });
  assert.equal(card.stopReason, 'step_limit');
  assert.equal(card.status, 'success');
  assert.equal(subagentBatchStatus([card]), 'partial');

  // A later clean result on the same card clears the marker instead of leaving
  // a stale "step limit reached" behind.
  const finished = { role: 'tool_call', kind: 'subagent', toolCallId: 'a2', status: 'running', stopReason: 'step_limit' };
  const g = fixture({ fallbackCard: finished });
  g.result({ name: 'subagent', toolCallId: 'a2', result: JSON.stringify({ agentId: 'a2', status: 'completed', summary: 'done' }) });
  assert.equal(finished.stopReason, '');
  assert.equal(subagentBatchStatus([finished]), 'success');
});

test('sub-agent completion keeps its step-limit reason for the card and persistence', () => {
  const source = readFileSync(new URL('../App.vue', import.meta.url), 'utf8');
  const start = source.indexOf("  onRuntimeEvent('sub:done',");
  const end = source.indexOf("  onRuntimeEvent('sub:error',", start);
  assert.ok(start >= 0 && end > start);
  const msg = { status: 'running', steps: 1 };
  let handler;
  new Function('onRuntimeEvent', 'findSubagentMsg', 'setToolStatus', 'formatDurationShort', 'applyCacheUsage', source.slice(start, end))(
    (_, fn) => { handler = fn; }, () => msg, setToolStatus, () => '', () => {},
  );
  handler({ status: 'completed', stopReason: 'step_limit', summary: 'Partial report', steps: 5 });
  assert.equal(msg.stopReason, 'step_limit');
  assert.equal(msg.summary, 'Partial report');
  assert.equal(subagentBatchStatus([msg]), 'partial');
});
