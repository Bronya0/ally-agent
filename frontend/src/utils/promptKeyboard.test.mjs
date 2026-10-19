// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 tangssst <tangssst@qq.com>
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import { isolateImeKeydown } from './imeKeyboard.mjs';

const source = readFileSync(new URL('../App.vue', import.meta.url), 'utf8');
function section(start, end) {
  const from = source.indexOf(start);
  const to = source.indexOf(end, from);
  assert.ok(from >= 0 && to > from, `App.vue section missing: ${start}`);
  return source.slice(from, to);
}

function key(key = 'Enter', overrides = {}) {
  return Object.assign(new Event('keydown', { cancelable: true }), {
    key, isComposing: false, shiftKey: false, ...overrides,
  });
}

function fixture() {
  const ref = (value) => ({ value });
  const noop = () => {};
  const targets = { a: { value: '' }, b: { value: '' } };
  const tabs = ref([{ id: 'a', sessionId: 'sa' }, { id: 'b', sessionId: 'sb' }]);
  const drafts = { sa: '你好', sb: '另一个 Tab' };
  let active = 'a';
  const actions = [];
  const bindings = {
    promptText: { get value() { return drafts[tabs.value.find((tab) => tab.id === active).sessionId] || ''; } },
    workspaceTabs: tabs, sessionPromptTexts: drafts,
    promptInputRefs: Object.fromEntries(Object.entries(targets).map(([id, target]) => [id, {
      $el: { contains: (el) => el === target },
    }])),
    activeSession: ref(null), commandMenuVisible: ref(false), sessionsVisible: ref(false),
    fileMentionVisible: ref(false), commandHistoryIndex: ref(-1), commandHistory: ref([]),
    filteredCommands: ref([{ label: '/test' }]), selectedCommandIndex: ref(0),
    currentWorkspaceSessions: ref([]), sessionsSelectedIndex: ref(0),
    fileMentionEntries: ref([]), fileMentionLoading: ref(false), fileMentionSelectedIndex: ref(0),
    isSlashCommandDraft: (text) => /^\/\S*$/.test(text), nextTick: (fn) => fn(),
    getPromptTextarea: () => targets[active],
    updateFileMentionFromCaret: () => actions.push('mention'), closeFileMentionMenu: noop,
    handlePromptPaste: noop, handlePromptCursorActivity: noop,
    sendPrompt: () => actions.push('send'), applyCommand: () => actions.push('command'),
    applyFileMention: () => actions.push('file'), selectSession: () => actions.push('session'),
  };
  // 执行真实处理器、DOM 归属和草稿搬运，不在测试里复制组词状态机。
  const api = new Function(...Object.keys(bindings), `
    ${section('function handlePromptInput(', '// Tab→会话链接')}
    ${section('function repointTabSession(', 'function getPromptTextarea(')}
    ${section('function handlePromptKeyup(', 'function applyCommand(')}
    return { input: PROMPT_INPUT_PROPS.onInputCapture, props: PROMPT_INPUT_PROPS,
      blur: handlePromptBlur, repointTabSession };
  `)(...Object.values(bindings));
  const target = new EventTarget();
  target.addEventListener('keydown', isolateImeKeydown, true);
  // 从真实 App 源码取键盘处理器，与实际捕获边界一起运行。
  const handler = new Function(...Object.keys(bindings), `
    ${section('function handlePromptKeydown(', 'function applyCommand(')}
    return handlePromptKeydown;
  `)(...Object.values(bindings));
  target.addEventListener('keydown', handler);
  return {
    ...api, ...bindings, tabs, drafts, targets, actions,
    activate: (id) => { active = id; },
    keydown: (event) => target.dispatchEvent(event),
  };
}

test('普通 Enter 发送，Shift+Enter 保留换行', () => {
  const f = fixture();
  const newline = key('Enter', { shiftKey: true });
  f.keydown(newline);
  assert.equal(newline.defaultPrevented, false);
  assert.deepEqual(f.actions, []);
  const send = key();
  f.keydown(send);
  assert.equal(send.defaultPrevented, true);
  assert.deepEqual(f.actions, ['send']);
});

for (const name of ['Enter', 'ArrowUp', 'ArrowDown', 'Tab', 'Escape', '/']) {
  test(`组词期间 ${name} 不提交、切菜单或改变历史`, () => {
    const f = fixture();
    f.commandMenuVisible.value = true;
    const event = key(name, { isComposing: true });
    f.keydown(event);
    assert.equal(event.defaultPrevented, false);
    assert.equal(f.commandMenuVisible.value, true);
    assert.deepEqual(f.actions, []);
  });
}

test('未收到 compositionend/blur，下一次非组合 Enter 仍立即发送', () => {
  const f = fixture();
  f.targets.a.value = '尚未结束的组词';
  f.input({ target: f.targets.a, isComposing: true });
  assert.equal(f.drafts.sa, '尚未结束的组词');
  const send = key();
  f.keydown(send);
  assert.equal(send.defaultPrevented, true);
  assert.deepEqual(f.actions, ['send']);
});

test('组合结束后立即按 Enter 不会被计时窗口吞掉', () => {
  const f = fixture();
  f.targets.a.value = '已确认';
  f.props.onCompositionend({ target: f.targets.a });
  assert.equal(f.drafts.sa, '已确认');
  f.actions.length = 0;
  f.keydown(key());
  assert.deepEqual(f.actions, ['send']);
});

test('组合结束后 WebKit 的 229 确认键不发送，下一次普通 Enter 正常发送', () => {
  const f = fixture();
  f.targets.a.value = '已确认';
  f.props.onCompositionend({ target: f.targets.a });
  f.actions.length = 0;
  const confirm = key('Enter', { keyCode: 229 });
  f.keydown(confirm);
  assert.equal(confirm.defaultPrevented, false);
  assert.deepEqual(f.actions, []);
  f.keydown(key());
  assert.deepEqual(f.actions, ['send']);
});

test('A Tab 组合中切到 B，A 的草稿与组词按键不会污染 B', () => {
  const f = fixture();
  f.activate('b');
  f.targets.a.value = 'A 的组合文本';
  f.input({ target: f.targets.a, isComposing: true });
  assert.equal(f.drafts.sa, 'A 的组合文本');
  assert.equal(f.drafts.sb, '另一个 Tab');
  f.keydown(key());
  assert.deepEqual(f.actions, ['send']);
});

test('槽位重指后，组合文本按 DOM 归属写到新会话', () => {
  const f = fixture();
  f.repointTabSession(f.tabs.value[0], 'replacement', { carryDraft: true });
  f.targets.a.value = '重指期间继续输入';
  f.input({ target: f.targets.a, isComposing: true });
  assert.equal(f.drafts.replacement, '重指期间继续输入');
  assert.equal(f.drafts.sa, undefined);
  assert.equal(f.drafts.sb, '另一个 Tab');
});

test('组合中的斜杠文本只镜像草稿，不触发命令或文件补全', () => {
  const f = fixture();
  f.targets.a.value = '/待确认';
  f.input({ target: f.targets.a, isComposing: true });
  assert.equal(f.drafts.sa, '/待确认');
  assert.equal(f.commandMenuVisible.value, false);
  assert.deepEqual(f.actions, []);
});

for (const menu of ['command', 'sessions', 'file']) {
  test(`${menu} 菜单打开时 Shift+Enter 仍只换行`, () => {
    const f = fixture();
    if (menu === 'command') f.commandMenuVisible.value = true;
    if (menu === 'sessions') {
      f.sessionsVisible.value = true;
      f.currentWorkspaceSessions.value = [{}];
    }
    if (menu === 'file') {
      f.fileMentionVisible.value = true;
      f.fileMentionEntries.value = [{}];
    }
    const newline = key('Enter', { shiftKey: true });
    f.keydown(newline);
    assert.equal(newline.defaultPrevented, false);
    assert.deepEqual(f.actions, []);
  });
}

test('会话菜单为空时 Enter 只关闭菜单，下一次 Enter 才发送', () => {
  const f = fixture();
  f.sessionsVisible.value = true;
  const send = key();
  f.keydown(send);
  assert.equal(send.defaultPrevented, true);
  assert.equal(f.sessionsVisible.value, false);
  assert.deepEqual(f.actions, []);
  f.keydown(key());
  assert.deepEqual(f.actions, ['send']);
});

for (const menu of ['command', 'sessions', 'file']) {
  test(`${menu} 菜单有候选时普通 Enter 正常选中`, () => {
    const f = fixture();
    if (menu === 'command') f.commandMenuVisible.value = true;
    if (menu === 'sessions') {
      f.sessionsVisible.value = true;
      f.currentWorkspaceSessions.value = [{}];
    }
    if (menu === 'file') {
      f.fileMentionVisible.value = true;
      f.fileMentionEntries.value = [{}];
    }
    const event = key();
    f.keydown(event);
    assert.equal(event.defaultPrevented, true);
    assert.deepEqual(f.actions, [menu === 'sessions' ? 'session' : menu]);
  });
}
