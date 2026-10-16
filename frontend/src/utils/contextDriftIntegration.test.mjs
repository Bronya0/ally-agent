// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 tangssst <tangssst@qq.com>
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import * as drift from './contextDriftNotice.mjs';
import { assistantRowRenderState, displaySourceMessages, isRenderableMessage } from './toolPreview.mjs';
import { toolCardRenderSignature } from './toolCardSignature.mjs';

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
const ref = (value) => ({ value });
const noop = () => {};
const tool = (name) => ({ role: 'tool_call', kind: 'read', name: 'read', title: name, status: 'success' });
const rowLabels = (rows) => rows.map((row) => row.role === 'notice'
  ? 'notice' : row.kind === 'read-grep-group'
    ? row.readEntries.map((entry) => entry.title).join(',') : row.role);

function fixture(messages) {
  const session = { id: 's', messages };
  const sessions = ref([session]);
  const bindings = {
    ...drift, ref, sessions, assistantRowRenderState, isRenderableMessage,
    toolCardRenderSignature, buildDisplaySourceMessages: displaySourceMessages,
    expandedArchiveSessions: ref(new Set()), t: (key) => key, prefixDriftDetail: () => '',
  };
  const api = evaluate(`${section('const MAX_RENDER_MESSAGES =', 'const contextTokens =')}
    return { displayMessagesForSession, recordContextDriftNotice, dropContextDriftNotices,
      contextDriftNoticesBySession };`, bindings);
  return {
    ...api, session, sessions,
    expandedArchiveSessions: bindings.expandedArchiveSessions,
    display: () => api.displayMessagesForSession(session),
    record: () => api.recordContextDriftNotice({ sessionId: session.id, kind: 'head' }),
  };
}

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
  });
}

test('自动压缩清理当前会话旧锚点，压缩后的新提示不会被旧提示挡住', () => {
  const f = fixture(Array.from({ length: 100 }, () => ({ role: 'user' })));
  f.record();
  const other = [{ id: 'other-notice', anchor: 8 }];
  f.contextDriftNoticesBySession.value.other = other;
  compactEvent(f, { sessionId: 's', summary: 'summary' });
  assert.equal(f.contextDriftNoticesBySession.value.s, undefined);
  assert.equal(f.contextDriftNoticesBySession.value.other, other);
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
