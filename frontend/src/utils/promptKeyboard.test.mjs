// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 tangssst <tangssst@qq.com>
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';

// 执行实际输入框处理器，复现漏 compositionend 时的事件序列。
const source = readFileSync(new URL('../App.vue', import.meta.url), 'utf8');
function section(start, end) {
  const from = source.indexOf(start);
  const to = source.indexOf(end, from);
  assert.ok(from >= 0 && to > from, `App.vue section missing: ${start}`);
  return source.slice(from, to);
}

function fixture() {
  const ref = (value) => ({ value });
  const noop = () => {};
  let now = 1000;
  let sent = 0;
  const composing = ref(false);
  const bindings = {
    promptComposing: composing, performance: { now: () => now },
    promptText: ref('你好'), activeSession: ref(null),
    commandMenuVisible: ref(false), sessionsVisible: ref(false),
    fileMentionVisible: ref(false), commandHistoryIndex: ref(-1), commandHistory: ref([]),
    isSlashCommandDraft: () => false, nextTick: (fn) => fn(),
    updateFileMentionFromCaret: noop, mirrorPromptDraftFromDom: noop,
    handlePromptPaste: noop, handlePromptCursorActivity: noop,
    sendPrompt: () => { sent++; },
  };
  const api = new Function(...Object.keys(bindings), `
    let promptCompositionEndedAt = 0;
    ${section('function handlePromptInput(', '// 草稿归属按 DOM 反查')}
    ${section('function handlePromptKeyup(', 'function applyCommand(')}
    return { input: handlePromptInput, keydown: handlePromptKeydown, props: PROMPT_INPUT_PROPS,
      blur: ${section('<n-input', '/>').match(/@blur="(\w+)"/)?.[1] || 'undefined'} };
  `)(...Object.values(bindings));
  return { ...api, composing, sent: () => sent, advance: (ms) => { now += ms; } };
}

function enter(overrides = {}) {
  return {
    key: 'Enter', keyCode: 13, isComposing: false, shiftKey: false,
    defaultPrevented: false,
    preventDefault() { this.defaultPrevented = true; },
    ...overrides,
  };
}

test('普通 Enter 发送，Shift+Enter 保留换行', () => {
  const f = fixture();
  const newline = enter({ shiftKey: true });
  f.keydown(newline);
  assert.equal(newline.defaultPrevented, false);
  assert.equal(f.sent(), 0);
  const send = enter();
  f.keydown(send);
  assert.equal(send.defaultPrevented, true);
  assert.equal(f.sent(), 1);
});

test('输入法组词中的 Enter 留给输入法确认', () => {
  const f = fixture();
  f.props.onCompositionstart();
  const confirm = enter({ isComposing: true, keyCode: 229 });
  f.keydown(confirm);
  assert.equal(confirm.defaultPrevented, false);
  assert.equal(f.sent(), 0);
});

test('漏 compositionend 后，非组合输入事件恢复状态，Enter 正常发送', () => {
  const f = fixture();
  f.props.onCompositionstart();
  f.input({ isComposing: false });
  assert.equal(f.composing.value, false);
  const send = enter();
  f.keydown(send);
  assert.equal(send.defaultPrevented, true);
  assert.equal(f.sent(), 1);
});

test('漏 compositionend 且没有后续输入时，原生 Enter 状态仍可恢复发送', () => {
  const f = fixture();
  f.props.onCompositionstart();
  const send = enter();
  f.keydown(send);
  assert.equal(send.defaultPrevented, true);
  assert.equal(f.sent(), 1);
});

test('输入框失焦清理组合状态，切到另一 Tab 不会只换行', () => {
  const f = fixture();
  f.props.onCompositionstart();
  f.blur?.({ target: { value: '你好' } });
  assert.equal(f.composing.value, false);
  const send = enter();
  f.keydown(send);
  assert.equal(send.defaultPrevented, true);
  assert.equal(f.sent(), 1);
});

test('普通组合输入不提前清除组词状态', () => {
  const f = fixture();
  f.props.onCompositionstart();
  f.input({ isComposing: true });
  assert.equal(f.composing.value, true);
});

test('输入法 keyCode 229 仍保留组词保护，即使 isComposing 为 false', () => {
  const f = fixture();
  f.props.onCompositionstart();
  f.keydown(enter({ keyCode: 229 }));
  assert.equal(f.composing.value, true);
  assert.equal(f.sent(), 0);
});

test('缺少原生 isComposing 信息时不推断组词已结束', () => {
  const f = fixture();
  f.props.onCompositionstart();
  f.input({});
  f.keydown(enter({ isComposing: undefined }));
  assert.equal(f.composing.value, true);
  assert.equal(f.sent(), 0);
});

test('组合结束后的幽灵 Enter 只吞一次，第二次 Enter 发送', () => {
  const f = fixture();
  f.props.onCompositionstart();
  f.props.onCompositionend();
  const ghost = enter();
  f.keydown(ghost);
  assert.equal(ghost.defaultPrevented, true);
  assert.equal(f.sent(), 0);
  f.keydown(enter());
  assert.equal(f.sent(), 1);
});

test('组合结束后 keyCode 229 的 Enter 不发送也不换行', () => {
  const f = fixture();
  f.props.onCompositionend();
  f.advance(200);
  const ghost = enter({ keyCode: 229 });
  f.keydown(ghost);
  assert.equal(ghost.defaultPrevented, true);
  assert.equal(f.sent(), 0);
});
