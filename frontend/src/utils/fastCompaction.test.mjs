// SPDX-License-Identifier: GPL-3.0-only
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import { applyFastCompaction } from './fastCompaction.mjs';
import { toolCardRenderSignature } from './toolCardSignature.mjs';

test('fast compaction replaces only matching tool cards and releases specialized payloads', () => {
  const user = { role: 'user', content: 'requirements' };
  const old = { role: 'tool_call', kind: 'edit', toolCallId: 'old', eventId: 'event',
    title: 'a.go', status: 'success', body: 'large output', editEntries: [{ diff: 'large diff' }], cardGrid: { items: [{}] } };
  const recent = { role: 'tool_call', kind: 'read', toolCallId: 'recent', body: 'recent output' };
  const answer = { role: 'assistant', content: 'answer' };
  const session = { messages: [user, old, recent, answer], _lastToolMsg: old, _lastToolEventId: 'event' };
  applyFastCompaction(session, { toolSummaries: { old: 'partial excerpt' } }, 'compacted');
  const card = session.messages[1];
  assert.equal(session.messages[0], user);
  assert.equal(session.messages[2], recent);
  assert.equal(session.messages[3], answer);
  assert.equal(card.body, 'partial excerpt');
  assert.equal(card.kind, 'generic');
  assert.equal(card.eventId, 'event');
  assert.equal(card.status, 'success');
  assert.equal(card.fastCompacted, true);
  assert.equal(card.editEntries, undefined);
  assert.equal(card.cardGrid, undefined);
  assert.equal(session._lastToolMsg, undefined);
  assert.notEqual(toolCardRenderSignature(old), toolCardRenderSignature(card));
});

const source = readFileSync(new URL('../App.vue', import.meta.url), 'utf8');
test('old answer edits preserve identical recent replies and remove empty body flags', () => {
  const old = { role: 'assistant', content: 'same answer', hasBody: true, runId: 'old' };
  const recent = { role: 'assistant', content: 'same answer', hasBody: true, runId: 'recent' };
  const notice = { role: 'assistant', system: true, content: 'same answer' };
  const user = { role: 'user', content: 'same answer' };
  const session = { messages: [old, user, notice, recent] };
  applyFastCompaction(session, { assistantEdits: [{ before: 'same answer', after: '', fromEnd: 2 }] }, 'compacted');
  assert.equal(session.messages[0].content, '');
  assert.equal(session.messages[0].hasBody, false);
  assert.equal(session.messages[0].fastCompacted, true);
  assert.equal(session.messages[0].runId, 'old');
  assert.equal(session.messages[1], user);
  assert.equal(session.messages[2], notice);
  assert.equal(session.messages[3], recent);
});

test('subsequent compaction can discard an earlier excerpt without changing message positions', () => {
  const session = { messages: [{ role: 'assistant', content: 'original', hasBody: true, roundDurationText: '1s' }] };
  applyFastCompaction(session, { assistantEdits: [{ before: 'original', after: 'partial excerpt', fromEnd: 1 }] }, 'compacted');
  assert.equal(session.messages[0].content, 'partial excerpt');
  assert.equal(session.messages[0].hasBody, true);
  applyFastCompaction(session, { assistantEdits: [{ before: 'partial excerpt', after: '', fromEnd: 1 }] }, 'compacted');
  assert.equal(session.messages.length, 1);
  assert.equal(session.messages[0].content, '');
  assert.equal(session.messages[0].hasBody, false);
  assert.equal(session.messages[0].roundDurationText, '1s');
});

test('legacy backend reasoning suffixes match both original UI replies and synchronized excerpts', () => {
  const suffix = '\n\n[Earlier reasoning excerpt; incomplete]\npartial reasoning';
  for (const content of ['answer', 'answer' + suffix]) {
    const session = { messages: [{ role: 'assistant', content, hasBody: true }] };
    applyFastCompaction(session, { assistantEdits: [{ before: 'answer', after: '', fromEnd: 1 }] }, 'compacted');
    assert.equal(session.messages[0].content, '');
    assert.equal(session.messages[0].hasBody, false);
  }
});

test('persisted fast read excerpts survive the ordinary read-output cleanup', () => {
  const from = source.indexOf('function sanitizeStoredMessage(msg)');
  const to = source.indexOf('function sessionFromIndexEntry', from);
  const sanitize = new Function('t', 'MAX_STORED_MESSAGE_CHARS', 'MAX_STORED_TOOL_BODY_CHARS', 'MAX_STORED_ATTACHMENT_TEXT_CHARS',
    `${source.slice(from, to)}; return sanitizeStoredMessage;`)((key) => key, 10000, 10000, 10000);
  const normal = { role: 'tool_call', name: 'read', body: 'old file contents' };
  assert.equal(sanitize(normal).body, '');
  assert.equal(sanitize({ ...normal, kind: 'generic', fastCompacted: true, body: 'bounded excerpt' }).body, 'bounded excerpt');
});

test('fast bridge completion stays with its originating session after a Tab switch', async () => {
  const from = source.indexOf('async function handleFastCompactCommand()');
  const to = source.indexOf('// 上下文气泡的两个维护按钮', from);
  const origin = { id: 'origin', messages: [{ role: 'user', content: 'keep' }] };
  const other = { id: 'other', messages: [] };
  const activeSession = { value: origin };
  const busy = {};
  const saved = [];
  const noop = () => {};
  const bindings = { activeSession, sessionBusy: () => false, message: { info: noop },
    t: (key) => key, startCompactTracking: (id) => { busy[id] = true; },
    FastCompactSession: async (id) => {
      assert.equal(id, 'origin');
      activeSession.value = other;
      return { changed: true, keepRecent: 4, toolSummaries: {}, reasoningCount: 1 };
    }, applyFastCompaction, persistCompletedSession: (s) => saved.push(s.id),
    refreshContextTokens: noop, compactToastText: () => 'done', compactingSessions: busy };
  await new Function(...Object.keys(bindings), `${source.slice(from, to)}; return handleFastCompactCommand();`)(...Object.values(bindings));
  assert.equal(origin.messages.length, 2);
  assert.equal(other.messages.length, 0);
  assert.deepEqual(saved, ['origin']);
  assert.deepEqual(busy, {});
});
