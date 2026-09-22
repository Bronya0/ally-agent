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
  displaySourceMessages,
  formatHttpToolTitle,
  isReasoningHidden,
  isRenderableMessage,
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

test('blank assistant rows are not renderable messages (archive quota & count share this)', () => {
  // 纯思考步骤留下的空壳行：思考已收起、没有正文，只是占位
  assert.equal(isRenderableMessage(ghost()), false);
});

test('every row that renders something stays renderable', () => {
  assert.equal(isRenderableMessage({ ...ghost(), streaming: true }), true, '流式行（可能正在思考/即将出正文）');
  assert.equal(isRenderableMessage({ ...ghost(), done: false }), true, '未封口的行');
  assert.equal(isRenderableMessage({ ...ghost(), reasoningEndedAt: undefined }), true, '思考还在显示');
  assert.equal(isRenderableMessage({ ...ghost(), content: '正文' }), true, '有正文（无 hasBody 标志的老快照行）');
  assert.equal(isRenderableMessage({ ...ghost(), hasBody: true }), true, '正文已开始的标志');
  assert.equal(isRenderableMessage({ ...ghost(), roundDurationText: '3m42s' }), true, '本轮统计行（悬停可见 + 导出按钮）');
  assert.equal(isRenderableMessage({ ...ghost(), attachments: [{ id: 'a' }] }), true, '附件');
  assert.equal(isRenderableMessage({ ...ghost(), suggestions: ['再来一个'] }), true, '建议芯片');
  assert.equal(isRenderableMessage({ ...ghost(), error: true }), true, '报错行');
  assert.equal(isRenderableMessage({ ...ghost(), system: true }), true, '系统提示行');
  assert.equal(isRenderableMessage({ role: 'user', content: '' }), true, '用户行不参与');
  assert.equal(isRenderableMessage({ role: 'tool_call', kind: 'read', done: true }), true, '工具卡行不参与');
  assert.equal(isRenderableMessage({ role: 'tool_call', kind: 'run' }), false, '内部 run 状态行不算消息');
});

// 显示列表缓存靠这个 key 失效：任何一个决定“渲染出不渲染出东西”的字段翻转，
// 都必须让 key 变，否则缓存过的列表会跟真实渲染静默分叉。
// 反向也要成立：key 里不能混进会按流式增量变化的字段（正文），否则每个增量
// 都要重算整张列表——所以只比“是否为空”，不比正文本身。
test('row render key flips with every deciding field, ignores body text', () => {
  const base = assistantRowRenderState(ghost()).key;
  assert.equal(assistantRowRenderState(ghost()).key, base, '同样的输入得到同样的 key');
  const flips = [
    { streaming: true },
    { done: false },
    { hasBody: true },
    { error: true },
    { system: true },
    { welcome: {} },
    { reasoningEndedAt: undefined },
    { attachments: [{ id: 'a' }] },
    { suggestions: ['x'] },
    { roundDurationText: '3s' },
  ];
  for (const patch of flips) {
    assert.notEqual(assistantRowRenderState({ ...ghost(), ...patch }).key, base, `字段翻转必须换 key: ${JSON.stringify(patch)}`);
  }
  assert.equal(assistantRowRenderState({ ...ghost(), content: '正文' }).key, base, '正文不进 key（不订阅流式增量）');
  assert.equal(assistantRowRenderState({ role: 'user', content: 'hi' }).key, '', '非 assistant 行不参与');
});

function ghost() {
  return {
    role: 'assistant',
    content: '',
    streaming: false,
    done: true,
    reasoningChars: 256,
    reasoningStartedAt: 1,
    reasoningEndedAt: 2,
  };
}

// 空壳行（当下一个像素都渲染不出来）必须被样式侧认出来：它要退出 content-visibility
// 的尺寸估计，否则被浏览器当作离屏内容时会用 120px 估计值占位（折叠组上方凭空多出一
// 大截间距、稍后才突然收敛）。注意 empty 与 blank 的差别：运行中尚未封口的空壳行
// empty=true 但它仍是本轮的一条消息（blank=false），不能从列表里丢掉。
test('rows rendering nothing are flagged empty, only settled ones are blank', () => {
  assert.equal(assistantRowRenderState(ghost()).empty, true, '思考已收起的空壳行：渲染不出东西');
  assert.equal(assistantRowRenderState(ghost()).blank, true, '且已封口 → 连消息都不算');
  const runtimeGhost = { ...ghost(), streaming: true, done: false };
  assert.equal(assistantRowRenderState(runtimeGhost).empty, true, '运行中的空壳行同样渲染不出东西');
  assert.equal(assistantRowRenderState(runtimeGhost).blank, false, '但仍是本轮消息，不能丢');
  assert.equal(assistantRowRenderState({ ...ghost(), reasoningEndedAt: undefined }).empty, false, '思考还在显示');
  assert.equal(assistantRowRenderState({ ...ghost(), hasBody: true }).empty, false, '正文已开始');
  assert.equal(assistantRowRenderState({ role: 'user', content: '' }).empty, false, '非 assistant 行不参与');
});

test('reasoning overlay visibility is one shared rule', () => {
  assert.equal(isReasoningHidden({}), true);
  assert.equal(isReasoningHidden({ reasoningChars: 10, reasoningStartedAt: 1 }), false);
  assert.equal(isReasoningHidden({ reasoningChars: 10, reasoningStartedAt: 1, reasoningEndedAt: 2 }), true);
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
