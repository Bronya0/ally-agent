// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 tangssst <tangssst@qq.com>
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import { computed, reactive, ref, toRaw } from 'vue';
import * as drift from './contextDriftNotice.mjs';
import { assistantRowRenderState, displaySourceMessages, hasVisibleAssistantText, isRenderableMessage, normalizeAssistantBodyVisibility } from './toolPreview.mjs';
import { toolCardRenderSignature } from './toolCardSignature.mjs';
import { applyFastCompaction } from './fastCompaction.mjs';

// 执行 App 的真实列表合并与事件处理代码，只替换 Vue ref 和桌面桥。
// 避免在测试里复制折叠算法，否则调用点漏接边界时纯函数测试仍会全绿。
const source = readFileSync(new URL('../App.vue', import.meta.url), 'utf8');
function section(start, end) {
  const from = source.indexOf(start);
  const to = source.indexOf(end, from);
  assert.ok(from >= 0 && to > from, `App.vue section missing: ${start}`);
  return source.slice(from, to);
}
function evaluate(code, bindings) {
  return new Function(...Object.keys(bindings), code)(...Object.values(bindings));
}
const noop = () => {};
const tool = (name) => ({ role: 'tool_call', kind: 'read', name: 'read', title: name, status: 'success' });
const rowLabels = (rows) => rows.map((row) => row.role === 'notice'
  ? 'notice' : row.kind === 'read-grep-group'
    ? row.readEntries.map((entry) => entry.title).join(',') : row.role);

function fixture(messages, renderable = isRenderableMessage) {
  const session = reactive({ id: 's', messages });
  const sessions = ref([session]);
  const bindings = {
    ...drift, computed, ref, sessions, assistantRowRenderState, isRenderableMessage: renderable,
    toolCardRenderSignature, buildDisplaySourceMessages: displaySourceMessages,
    expandedArchiveSessions: ref(new Set()), t: (key) => key, prefixDriftDetail: () => '',
  };
  const api = evaluate(`${section('const MAX_RENDER_MESSAGES =', 'const contextTokens =')}
    return { displayMessagesForSession, buildDisplayMessagesSignature, recordContextDriftNotice, dropContextDriftNotices,
      contextDriftNoticesBySession };`, bindings);
  return {
    ...api, session, sessions,
    expandedArchiveSessions: bindings.expandedArchiveSessions,
    display: () => api.displayMessagesForSession(session),
    signature: () => api.buildDisplayMessagesSignature(session, bindings.expandedArchiveSessions.value),
    record: () => api.recordContextDriftNotice({ sessionId: session.id, kind: 'head' }),
  };
}

test('零宽字符增量不产生消息间距，首个可见正文才使列表失效', () => {
  const f = fixture([]);
  f.session.runId = 'run';
  const streamBuffers = new Map();
  const flush = evaluate(`${section('function flushStreamBuffer(runId)', 'function endReasoningStatus(msg)')}
    return flushStreamBuffer;`, {
    streamBuffers, sessionByRunId: () => f.session, sessionByCompactStreamRunId: () => null,
    endReasoningStatus: noop, activeSessionId: ref('s'), scrollMessagesToBottom: noop,
    hasVisibleAssistantText,
  });
  const send = (content) => {
    streamBuffers.set('run', { content, reasoningLen: 0 });
    flush('run');
  };
  send('\u200b');
  assert.equal(f.session.messages[0].hasBody, false);
  assert.equal(f.display().length, 0);
  const signature = f.signature();
  send(' \n\u2060');
  assert.equal(f.signature(), signature);
  assert.equal(f.display().length, 0);
  send('正文');
  assert.equal(f.session.messages[0].hasBody, true);
  assert.notEqual(f.signature(), signature);
  const visibleSignature = f.signature();
  send('继续');
  assert.equal(f.signature(), visibleSignature, '正文增量不重建列表');
  assert.equal(f.display().length, 1);
});

test('恢复旧快照后零宽字符空行不再隔开读取和搜索组', async () => {
  const f = fixture([]);
  const snapshot = { messages: [tool('A'), { role: 'assistant', content: '\u200b', hasBody: true }, tool('B')] };
  const load = evaluate(`${section('async function loadSessionMessages(session)', 'function ensureWorkspaceTabSession(tab)')}
    return loadSessionMessages;`, {
    LoadSession: async () => snapshot,
    displayMessagesCacheBySession: new Map(), normalizeAssistantBodyVisibility,
    message: { error: (err) => assert.fail(err) }, t: (key) => key,
  });
  await load(f.session);
  assert.deepEqual(rowLabels(f.display()), ['A,B']);
  assert.equal(f.session.messages[1].hasBody, false);
});

test('工具增量不重扫归档；旧行可见性、数组替换和删除仍使缓存失效', () => {
  let visits = 0;
  const f = fixture(Array.from({ length: 1000 }, (_, i) => ({ ...tool(String(i)), eventId: `e${i}` })), (msg) => {
    if (msg.title === '0') visits++;
    return isRenderableMessage(msg);
  });
  const initial = f.signature();
  assert.ok(visits > 0);
  visits = 0;
  f.session.messages.at(-1).body = 'new output';
  assert.notEqual(f.signature(), initial);
  assert.equal(visits, 0);
  const beforeHide = f.signature();
  f.session.messages[0].errorCode = 'E_STAGE_BLOCKED';
  assert.notEqual(f.signature(), beforeHide);
  assert.equal(f.display()[0].count, 819);
  f.session.messages = f.session.messages.slice(1);
  assert.equal(f.display()[0].count, 819);
  f.session.messages.splice(0, 1);
  assert.equal(f.display()[0].count, 818);
});

test('展开归档夹有空壳消息时，可见的旧工具卡更新仍使列表失效', () => {
  const f = fixture(Array.from({ length: 500 }, (_, i) => [
    { ...tool(String(i)), eventId: `e${i}` },
    { role: 'assistant', content: '', hasBody: false },
  ]).flat());
  f.expandedArchiveSessions.value = new Set(['s']);
  const before = f.display();
  assert.equal(before[1].readEntries[0].title, '140');
  f.session.messages[280].title = 'changed';
  const after = f.display();
  assert.notEqual(after, before);
  assert.equal(after[1].readEntries[0].title, 'changed');
});

test('同形消息数组替换后，展示列表使用新对象', () => {
  const f = fixture([{ role: 'assistant', content: 'answer', hasBody: true }]);
  const before = f.display();
  f.session.messages = f.session.messages.map((msg) => ({ ...msg }));
  const after = f.display();
  assert.notEqual(after, before);
  assert.equal(after[0], f.session.messages[0]);
  assert.notEqual(after[0], before[0]);
});

test('追加连续工具调用后，提示两侧各自折叠且提示位置保持不变', () => {
  const f = fixture([{ role: 'user' }, tool('A')]);
  f.record();
  assert.deepEqual(rowLabels(f.display()), ['user', 'A', 'notice']);
  f.session.messages.push(tool('B'), tool('C'));
  assert.deepEqual(rowLabels(f.display()), ['user', 'A', 'notice', 'B,C']);
  f.record();
  f.session.messages.push(tool('D'));
  assert.deepEqual(rowLabels(f.display()), ['user', 'A', 'notice', 'B,C', 'notice', 'D']);
});

test('归档窗口滑动及展开后，工具组仍在绝对锚点处分开', () => {
  const f = fixture(Array.from({ length: 182 }, (_, i) => tool(String(i))));
  f.record();
  f.session.messages.push(tool('182'), tool('183'));
  const rows = f.display();
  assert.equal(rows[0].role, 'archive');
  assert.equal(rows[0].count, 4);
  assert.equal(rows[1].readEntries.at(-1).title, '181');
  assert.equal(rows[2].role, 'notice');
  assert.equal(rowLabels(rows)[3], '182,183');
  f.expandedArchiveSessions.value = new Set(['s']);
  const expanded = f.display();
  assert.equal(expanded[0].readEntries[0].title, '0');
  assert.equal(expanded[0].readEntries.at(-1).title, '181');
  assert.deepEqual(rowLabels(expanded).slice(1), ['notice', '182,183']);
});

function compactEvent(f, data) {
  evaluate(section("  onRuntimeEvent('run:compacted'", "  onRuntimeEvent('plan:update'"), {
    onRuntimeEvent: (name, handler) => handler(data), sessions: f.sessions,
    compactingSessions: {}, activeSessionId: ref('other'), streamBuffers: new Map(),
    compactStreamRunId: (id) => id, dropContextDriftNotices: f.dropContextDriftNotices,
    scheduleSaveSessions: noop,
    applyFastCompaction, t: (key) => key,
  });
}

test('自动压缩清理当前会话旧锚点，压缩后的新提示不会被旧提示挡住', () => {
  const f = fixture(Array.from({ length: 100 }, () => ({ role: 'user' })));
  f.record();
  const other = [{ id: 'other-notice', anchor: 8 }];
  f.contextDriftNoticesBySession.value.other = other;
  compactEvent(f, { sessionId: 's', summary: 'summary' });
  assert.equal(f.contextDriftNoticesBySession.value.s, undefined);
  assert.equal(toRaw(f.contextDriftNoticesBySession.value.other), other);
  f.session.messages.push({ role: 'user' });
  f.record();
  f.session.messages.push(tool('new'));
  assert.deepEqual(rowLabels(f.display()), ['assistant', 'user', 'notice', 'new']);
});

test('未替换历史的压缩事件保留原提示', () => {
  const f = fixture([tool('A')]);
  f.record();
  const before = f.contextDriftNoticesBySession.value.s;
  compactEvent(f, { sessionId: 's', error: 'failed' });
  compactEvent(f, { sessionId: 's', summary: '' });
  assert.equal(f.contextDriftNoticesBySession.value.s, before);
  assert.deepEqual(rowLabels(f.display()), ['A', 'notice']);
});

test('自动保留最近内容只更新对应工具卡，保留正文及已有提示锚点', () => {
  const user = { role: 'user', content: 'requirements' };
  const answer = { role: 'assistant', content: 'answer' };
  const f = fixture([user, { ...tool('old'), toolCallId: 'old', body: 'old body' }, answer, { ...tool('recent'), toolCallId: 'recent', body: 'recent body' }]);
  f.record();
  const notices = f.contextDriftNoticesBySession.value.s;
  compactEvent(f, { sessionId: 's', tier: 'fast', changed: true, keepRecent: 4, toolSummaries: { old: 'excerpt' }, reasoningCount: 1 });
  assert.equal(toRaw(f.session.messages[0]), user);
  assert.equal(f.session.messages[1].body, 'excerpt');
  assert.equal(toRaw(f.session.messages[2]), answer);
  assert.equal(f.session.messages[3].body, 'recent body');
  assert.equal(f.session.messages[4].system, true);
  assert.equal(f.contextDriftNoticesBySession.value.s, notices);
});

test('手动压缩也会清理被摘要替换的历史锚点', async () => {
  const f = fixture([tool('A')]);
  f.record();
  await evaluate(`${section('async function handleCompactCommand()', '// 上下文气泡的两个维护按钮')}
    return handleCompactCommand();`, {
    activeSession: ref(f.session), compactStateFor: () => null,
    workspaceTabs: ref([]), activeWorkspaceId: ref('tab'), runWorkspaceForTab: () => '',
    startCompactTracking: noop, saveSessions: noop, CompactSession: async () => ({ summary: 'summary' }),
    chatConfig: ref({}), config: { workspace: '' }, compactingSessions: {},
    dropContextDriftNotices: f.dropContextDriftNotices, persistCompletedSession: noop,
    refreshContextTokens: noop, scrollMessagesToBottom: noop, compactToastText: () => '',
    message: { info: noop }, t: (key) => key,
    pushMessage: () => assert.fail('manual compaction should succeed'),
  });
  assert.equal(f.contextDriftNoticesBySession.value.s, undefined);
  assert.deepEqual(rowLabels(f.display()), ['assistant']);
});
