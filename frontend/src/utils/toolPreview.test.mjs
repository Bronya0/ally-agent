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
import {
  assistantRowRenderState,
  codePreviewWindow,
  deleteFailedCount,
  deletePathList,
  deletePathRows,
  deletePathSummary,
  displaySourceMessages,
  formatHttpToolTitle,
  formatPlanArgsTitle,
  isRenderableMessage,
  scheduledTaskRow,
  scheduledTaskRows,
  serviceListRows,
  serviceRow,
  sshClusterNodeRow,
  sshServerRows,
} from './toolPreview.mjs';

test('collapsed create preview uses the latest generated lines', () => {
  const code = Array.from({ length: 12 }, (_, i) => `line ${i + 1}`).join('\n');

  const preview = codePreviewWindow(code, {
    collapsed: true,
    maxLines: 6,
    mode: 'tail',
  });

  assert.equal(preview.startLine, 7);
  assert.deepEqual(preview.lines, ['line 7', 'line 8', 'line 9', 'line 10', 'line 11', 'line 12']);
  assert.equal(preview.omittedBefore, true);
  assert.equal(preview.omittedAfter, false);
});

test('large card content does not trigger archive by itself', () => {
  const hugeDiff = Array.from({ length: 50000 }, (_, i) => `+line ${i + 1}`).join('\n');
  const session = {
    id: 's1',
    messages: [{
      role: 'tool_call',
      kind: 'edit',
      status: 'success',
      eventId: 'run-1:tool:1',
      expanded: false,
      editDiff: hugeDiff,
    }],
  };

  const display = displaySourceMessages(session, new Set(), { maxMessages: 1 });

  assert.equal(display.length, 1);
  assert.equal(display[0].eventId, 'run-1:tool:1');
});

test('archives older cards when card count exceeds limit', () => {
  const session = {
    id: 's1',
    messages: Array.from({ length: 4 }, (_, i) => ({ role: 'assistant', content: `message ${i}` })),
  };

  const display = displaySourceMessages(session, new Set(), { maxMessages: 2 });

  assert.equal(display[0].role, 'archive');
  assert.equal(display[0].count, 2);
  assert.equal(display.length, 3);
  assert.equal(display.at(-1).content, 'message 3');
});

test('grep tool call remains renderable as a compact status card', () => {
  assert.equal(isRenderableMessage({ role: 'tool_call', kind: 'grep', name: 'grep' }), true);
  assert.equal(isRenderableMessage({ role: 'tool_call', kind: 'run', name: 'run' }), false);
});

test('status run messages are excluded from the archive card count', () => {
  const session = {
    id: 's1',
    messages: [
      { role: 'assistant', content: 'message 0' },
      { role: 'assistant', content: 'message 1' },
      { role: 'assistant', content: 'message 2' },
      { role: 'tool_call', kind: 'run', status: 'success', eventId: 'run' },
    ],
  };

  const display = displaySourceMessages(session, new Set(), { maxMessages: 2 });

  assert.equal(display[0].role, 'archive');
  assert.equal(display[0].count, 1);
  assert.equal(display.some((msg) => msg.kind === 'run'), false);
  assert.equal(display.at(-1).content, 'message 2');
});

test('latest card remains visible when older cards are archived', () => {
  const session = {
    id: 's1',
    messages: [
      { role: 'assistant', content: 'old message' },
      { role: 'tool_call', kind: 'edit', status: 'success', eventId: 'run-1:tool:1' },
    ],
  };

  const display = displaySourceMessages(session, new Set(), { maxMessages: 1 });

  assert.equal(display[0].role, 'archive');
  assert.equal(display.at(-1).eventId, 'run-1:tool:1');
});

test('expanded archives remain bounded instead of rendering the whole session', () => {
  const session = {
    id: 's1',
    messages: Array.from({ length: 1000 }, (_, i) => ({ role: 'assistant', content: `message ${i}` })),
  };

  const display = displaySourceMessages(session, new Set(['s1']), {
    maxMessages: 100,
    expandedMaxMessages: 200,
  });

  assert.equal(display[0].role, 'archive');
  assert.equal(display[0].expanded, true);
  assert.ok(display.length <= 201);
  assert.equal(display.at(-1).content, 'message 999');
});

test('assistant rows without visible content never enter the message list', () => {
  assert.equal(isRenderableMessage(ghost()), false, 'completed empty assistant row');
  assert.equal(isRenderableMessage({ ...ghost(), streaming: true, done: false, reasoningActive: true }), false, 'thinking-only streaming row');
});

test('every row that renders something stays renderable', () => {
  assert.equal(isRenderableMessage({ ...ghost(), content: '正文' }), true, 'body fallback for legacy snapshots');
  assert.equal(isRenderableMessage({ ...ghost(), hasBody: true }), true, 'body has started');
  assert.equal(isRenderableMessage({ ...ghost(), roundDurationText: '3m42s' }), true, 'round stats');
  assert.equal(isRenderableMessage({ ...ghost(), attachments: [{ id: 'a' }] }), true, 'attachment');
  assert.equal(isRenderableMessage({ ...ghost(), suggestions: ['再来一个'] }), true, 'suggestion chips');
  assert.equal(isRenderableMessage({ ...ghost(), error: true }), true, 'error row');
  assert.equal(isRenderableMessage({ ...ghost(), system: true }), true, 'system row');
  assert.equal(isRenderableMessage({ role: 'user', content: '' }), true, 'user rows are unaffected');
  assert.equal(isRenderableMessage({ role: 'tool_call', kind: 'read', done: true }), true, 'tool cards are unaffected');
  assert.equal(isRenderableMessage({ role: 'tool_call', kind: 'run' }), false, 'internal run status is not a message');
});

// The display-list cache key tracks only fields that affect rendered message rows.
test('row render key tracks visible output and ignores streaming thinking metadata', () => {
  const base = assistantRowRenderState(ghost()).key;
  assert.equal(assistantRowRenderState(ghost()).key, base, 'same inputs produce the same key');
  const flips = [
    { hasBody: true },
    { error: true },
    { system: true },
    { welcome: {} },
    { attachments: [{ id: 'a' }] },
    { suggestions: ['x'] },
    { roundDurationText: '3s' },
  ];
  for (const patch of flips) {
    assert.notEqual(assistantRowRenderState({ ...ghost(), ...patch }).key, base, `visible field changes key: ${JSON.stringify(patch)}`);
  }
  assert.equal(assistantRowRenderState({ ...ghost(), content: '正文' }).key, base, 'body text is not in the key');
  assert.equal(assistantRowRenderState({ ...ghost(), reasoningChars: 999, reasoningActive: true }).key, base, 'thinking metadata is not in the key');
  assert.equal(assistantRowRenderState({ role: 'user', content: 'hi' }).key, '', 'non-assistant rows are excluded');
});

function ghost() {
  return {
    role: 'assistant',
    content: '',
    streaming: false,
    done: true,
    reasoningChars: 256,
    reasoningActive: false,
  };
}

test('empty assistant rows are excluded whether streaming or completed', () => {
  assert.equal(assistantRowRenderState(ghost()).empty, true);
  assert.equal(assistantRowRenderState({ ...ghost(), streaming: true, done: false, reasoningActive: true }).empty, true);
  assert.equal(assistantRowRenderState({ ...ghost(), content: '正文' }).empty, false, 'legacy body fallback');
  assert.equal(assistantRowRenderState({ ...ghost(), hasBody: true }).empty, false, 'body has started');
  assert.equal(assistantRowRenderState({ role: 'user', content: '' }).empty, false, 'non-assistant row');
});

test('formatHttpToolTitle shows URL only for default GET, no options', () => {
  assert.equal(
    formatHttpToolTitle({ url: 'https://api.example.com' }),
    'https://api.example.com',
  );
});

test('formatHttpToolTitle appends non-default method', () => {
  assert.equal(
    formatHttpToolTitle({ url: 'https://api.example.com', method: 'post' }),
    'https://api.example.com · POST',
  );
});

test('formatHttpToolTitle skips GET since it is the default', () => {
  assert.equal(
    formatHttpToolTitle({ url: 'https://api.example.com', method: 'GET' }),
    'https://api.example.com',
  );
});

test('formatHttpToolTitle surfaces body/json/saveTo/timeout/maxBytes', () => {
  assert.equal(
    formatHttpToolTitle({ url: 'https://api.example.com', body: 'hi' }),
    'https://api.example.com · body',
  );
  assert.equal(
    formatHttpToolTitle({ url: 'https://api.example.com', json: { a: 1 } }),
    'https://api.example.com · json',
  );
  assert.equal(
    formatHttpToolTitle({ url: 'https://api.example.com', saveTo: 'out.json' }),
    'https://api.example.com · → out.json',
  );
  assert.equal(
    formatHttpToolTitle({ url: 'https://api.example.com', timeout: 30 }),
    'https://api.example.com · 30s',
  );
  // Default timeout is 60s and is omitted.
  assert.equal(
    formatHttpToolTitle({ url: 'https://api.example.com', timeout: 60 }),
    'https://api.example.com',
  );
  assert.equal(
    formatHttpToolTitle({ url: 'https://api.example.com', maxBytes: 1024 }),
    'https://api.example.com · ≤1.0 KB',
  );
});

test('formatHttpToolTitle combines multiple fields in a fixed order', () => {
  assert.equal(
    formatHttpToolTitle({
      url: 'https://api.example.com',
      method: 'POST',
      json: { q: 1 },
      timeout: 10,
      maxBytes: 512,
      saveTo: 'r.json',
    }),
    'https://api.example.com · POST · json · → r.json · 10s · ≤512 B',
  );
});

test('formatHttpToolTitle returns empty for missing url or non-object input', () => {
  assert.equal(formatHttpToolTitle(null), '');
  assert.equal(formatHttpToolTitle(undefined), '');
  assert.equal(formatHttpToolTitle({}), '');
  assert.equal(formatHttpToolTitle({ method: 'POST' }), '');
});

// The plan card's running title comes from the call ARGUMENTS, whose shape is
// steps / finish. Feeding it the result payload shape (or reading a result key
// off the arguments) is what left the card blank, so both directions are pinned
// here.
test('formatPlanArgsTitle reads the plan call arguments, not the result shape', () => {
  assert.equal(formatPlanArgsTitle({ steps: ['Read code', 'Run tests'] }), 'Read code');
  assert.equal(formatPlanArgsTitle({ steps: ['  ', 'Run tests'] }), 'Run tests');
  assert.equal(formatPlanArgsTitle({ finish: 'Run tests' }), 'Run tests');
  assert.equal(formatPlanArgsTitle({ finish: ['Run tests'] }), '');
  assert.equal(formatPlanArgsTitle({ steps: [] }), '');
  assert.equal(formatPlanArgsTitle({ finish: true }), '');
  assert.equal(formatPlanArgsTitle({}), '');
  assert.equal(formatPlanArgsTitle({ plan: [{ title: 'Read code', status: 'in_progress' }] }), '');
  assert.equal(formatPlanArgsTitle(null), '');
  assert.equal(formatPlanArgsTitle('steps'), '');
});

// The delete cards read three payload shapes: the call arguments (path or
// paths), the current result (paths[], one slot per path) and the result older
// sessions stored (a bare deleted/path string). Reading only one of them is what
// blanks a card, so all three are pinned here.
test('deletePathList reads arguments, the slot result and the legacy result shape', () => {
  assert.deepEqual(deletePathList({ path: 'a.txt' }), ['a.txt']);
  assert.deepEqual(deletePathList({ paths: ['a.txt', 'b.txt'] }), ['a.txt', 'b.txt']);
  assert.deepEqual(deletePathList({ paths: [{ path: 'a.txt', ok: true }, { path: 'b.txt', ok: false }] }), ['a.txt', 'b.txt']);
  assert.deepEqual(deletePathList({ deleted: 'old.txt', path: 'old.txt' }), ['old.txt']);
  assert.deepEqual(deletePathList({ paths: [] }), []);
  assert.deepEqual(deletePathList({}), []);
  assert.deepEqual(deletePathList(null), []);
});

test('deletePathRows carries one row per path with its slot outcome', () => {
  // 结果里的槽对象是唯一带成败的来源（批量删可以有的成、有的败）。
  assert.deepEqual(
    deletePathRows({ paths: [{ path: 'a.txt', ok: true }, { path: 'b.txt', ok: false, error: 'permission denied' }] }),
    [{ path: 'a.txt', ok: true, error: '' }, { path: 'b.txt', ok: false, error: 'permission denied' }],
  );
  // 入参、老会话的单路径字符串、以及空/非法输入。
  assert.deepEqual(deletePathRows({ paths: ['a.txt', 'b.txt'] }), [{ path: 'a.txt', ok: true, error: '' }, { path: 'b.txt', ok: true, error: '' }]);
  assert.deepEqual(deletePathRows({ path: 'a.txt' }), [{ path: 'a.txt', ok: true, error: '' }]);
  assert.deepEqual(deletePathRows({ deleted: 'old.txt', path: 'old.txt' }), [{ path: 'old.txt', ok: true, error: '' }]);
  // 槽里有没有 ok 字段决定成败：缺字段不能把成功行误判成失败行。
  assert.deepEqual(deletePathRows({ paths: [{ path: 'a.txt' }] }), [{ path: 'a.txt', ok: true, error: '' }]);
  assert.deepEqual(deletePathRows({ paths: [{ path: '  ', ok: true }, { path: '', ok: false, error: 'x' }] }), []);
  assert.deepEqual(deletePathRows({ paths: [] }), []);
  assert.deepEqual(deletePathRows({}), []);
  assert.deepEqual(deletePathRows(null), []);
});

test('deletePathSummary says one path or N paths', () => {
  assert.equal(deletePathSummary({ path: 'a.txt' }), 'a.txt');
  assert.equal(deletePathSummary({ paths: ['a.txt', 'b.txt'] }), '2 paths');
  assert.equal(deletePathSummary({}), '');
});

test('deleteFailedCount surfaces a partial failure', () => {
  assert.equal(deleteFailedCount({ failedCount: 1, deletedCount: 2 }), 1);
  assert.equal(deleteFailedCount({ failedCount: 0 }), 0);
  assert.equal(deleteFailedCount({}), 0);
  assert.equal(deleteFailedCount(null), 0);
});

test('sshServerRows reads one row per authorized server', () => {
  const rows = sshServerRows({
    count: 1,
    servers: [{ alias: '47.120.8.34', host: '47.120.8.34', port: 22, username: 'root', description: '生产服务器', riskLevel: 'high', status: 'approved' }],
  });
  assert.deepEqual(rows, [{
    alias: '47.120.8.34',
    endpoint: 'root@47.120.8.34:22',
    description: '生产服务器',
    riskLevel: 'high',
    pending: false,
  }]);
});

// null 与 [] 是两件事：add / 授权的结果没有 servers 数组（null，卡片保持通用键值体），
// 列表结果里一台都没有才是空清单（[]，卡片显示空态文案）。
test('sshServerRows tells "not a list result" apart from an empty list', () => {
  assert.equal(sshServerRows(null), null);
  assert.equal(sshServerRows({}), null);
  assert.equal(sshServerRows({ servers: 'nope' }), null);
  assert.deepEqual(sshServerRows({ count: 0, servers: [] }), []);
});

test('sshServerRows tolerates missing fields and defaults the port', () => {
  assert.deepEqual(sshServerRows({ servers: [{ host: '10.0.0.5', username: 'deploy' }] }), [{
    alias: '10.0.0.5', endpoint: 'deploy@10.0.0.5:22', description: '', riskLevel: '', pending: false,
  }]);
  assert.deepEqual(sshServerRows({ servers: [{ alias: 'n', host: 'h', username: 'u', status: 'pending_approval' }] }), [{
    alias: 'n', endpoint: 'u@h:22', description: '', riskLevel: '', pending: true,
  }]);
  // 连身份都没有的槽整条丢掉。
  assert.deepEqual(sshServerRows({ servers: [{ port: 22 }] }), []);
});

// 已登记节点的授权结果只有别名（没有 host/username），端点自然为空；列表结果带
// servers 数组，不走这一支。changed 只认显式 false（本次什么都没改），缺字段按「改了」
// 显示——拿 alreadyRegistered 当「没动」会把「本次才授权」说成没变化。
test('sshClusterNodeRow reads a single add / authorize result', () => {
  assert.deepEqual(
    sshClusterNodeRow({ alias: 'dev-node', host: '10.0.0.5', port: 2222, username: 'ops', description: '构建机', changed: true }),
    { alias: 'dev-node', endpoint: 'ops@10.0.0.5:2222', description: '构建机', changed: true },
  );
  assert.deepEqual(
    sshClusterNodeRow({ alias: 'dev-node', alreadyRegistered: true, changed: false, note: 'nothing was modified' }),
    { alias: 'dev-node', endpoint: '', description: '', changed: false },
  );
  assert.equal(sshClusterNodeRow({ alias: 'dev-node', alreadyRegistered: true }).changed, true);
  assert.equal(sshClusterNodeRow({ alias: 'dev-node' }).changed, true);
  assert.equal(sshClusterNodeRow({ servers: [] }), null);
  assert.equal(sshClusterNodeRow({ authorized: true }), null);
  assert.equal(sshClusterNodeRow(null), null);
});

test('serviceListRows reads one row per tracked service', () => {
  assert.deepEqual(
    serviceListRows({ activeCount: 1, maxActive: 5, services: [{ id: 'svc_1', name: 'frontend', command: 'npm run dev', cwd: 'frontend', pid: 99, status: 'running' }] }),
    [{ id: 'svc_1', name: 'frontend', command: 'npm run dev', cwd: 'frontend', pid: 99, status: 'running', exitCode: 0, outputBytes: 0, startedAt: 0, error: '' }],
  );
  // 列表之外的形状（start / stop 的单条结果、read 的输出结果）都没有 services 数组。
  assert.equal(serviceListRows({ id: 'svc_1', status: 'running', pid: 99 }), null);
  assert.equal(serviceListRows({ id: 'svc_1', output: 'boot\n', bytes: 5, truncated: false }), null);
  assert.equal(serviceListRows(null), null);
  assert.deepEqual(serviceListRows({ activeCount: 0, maxActive: 5, services: [] }), []);
});

// start / stop 的单条 ServiceInfo 走 serviceRow；read 的输出结果必须被挡在外面，
// 否则服务卡会把命令输出顶掉。
test('serviceRow reads a single start / stop result and rejects the read output', () => {
  assert.deepEqual(
    serviceRow({ id: 'svc_1', command: 'npm run dev', cwd: 'frontend', pid: 99, status: 'running', startedAt: 1700000000 }),
    { id: 'svc_1', name: '', command: 'npm run dev', cwd: 'frontend', pid: 99, status: 'running', exitCode: 0, outputBytes: 0, startedAt: 1700000000, error: '' },
  );
  // read 的结果形状是 ServiceOutputResult（id/output/bytes/truncated）。
  assert.equal(serviceRow({ id: 'svc_1', output: '', bytes: 0, truncated: false }), null);
  assert.equal(serviceRow({ id: 'svc_1', output: 'boot\n', bytes: 5, truncated: false }), null);
  assert.equal(serviceRow({ activeCount: 0, maxActive: 5, services: [] }), null);
  assert.equal(serviceRow({ status: 'running' }), null);
  assert.equal(serviceRow(null), null);
});

test('scheduledTaskRows reads one row per task and keeps the schedule raw', () => {
  assert.deepEqual(
    scheduledTaskRows({ count: 1, tasks: [{ id: 't_1', name: 'sync', schedule: { type: 'interval', every: '30m' }, command: 'go test ./...', lastStatus: 'COMPLETED', running: false, nextRunAt: 1700000000000 }] }),
    [{ id: 't_1', name: 'sync', schedule: { type: 'interval', every: '30m' }, command: 'go test ./...', instruction: '', lastStatus: 'completed', running: false, nextRunAt: 1700000000000 }],
  );
  // create（单条 task）与 delete（只有 id）都不是列表结果。
  // 空清单：后端现在也把 tasks 键发出来（json:"tasks"，不带 omitempty），所以下面这条
  // 断言对应的正是真实空态。
  assert.equal(scheduledTaskRows({ task: { id: 't_1' } }), null);
  assert.equal(scheduledTaskRows({ deleted: 't_1' }), null);
  assert.equal(scheduledTaskRows(null), null);
  assert.deepEqual(scheduledTaskRows({ count: 0, tasks: [] }), []);
});

// create 返回的就是列表里那一种行，同一张卡：调度、下次执行、任务内容、状态。
test('scheduledTaskRow reads the single task a create returns', () => {
  assert.deepEqual(
    scheduledTaskRow({ task: { id: 't_2', name: 'nightly', schedule: { type: 'cron', cron: '0 3 * * *' }, command: 'go test ./...', lastStatus: 'scheduled', nextRunAt: 1700000000000 } }),
    { id: 't_2', name: 'nightly', schedule: { type: 'cron', cron: '0 3 * * *' }, command: 'go test ./...', instruction: '', lastStatus: 'scheduled', running: false, nextRunAt: 1700000000000 },
  );
  // delete 只有被删的 id，没有任务信息；不是单条任务结果时返回 null。
  assert.equal(scheduledTaskRow({ deleted: 't_1' }), null);
  assert.equal(scheduledTaskRow({ count: 0, tasks: [] }), null);
  assert.equal(scheduledTaskRow(null), null);
});
