// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 tangssst <tangssst@qq.com>
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import { isolateImeKeydown } from './imeKeyboard.mjs';

const main = readFileSync(new URL('../main.js', import.meta.url), 'utf8');
const explorer = readFileSync(new URL('../components/WorkspaceExplorer.vue', import.meta.url), 'utf8');
function key(name, composing, keyCode = 0) {
  return Object.assign(new Event('keydown', { cancelable: true }), {
    key: name, isComposing: composing, keyCode,
  });
}

test('真实入口在组件挂载前安装捕获边界，业务与第三方监听器均收不到组词按键', () => {
  const target = new EventTarget();
  const delivered = [];
  const app = {
    config: { globalProperties: {} },
    mount() {
      target.addEventListener('keydown', (event) => delivered.push(`app:${event.key}`), true);
      target.addEventListener('keydown', (event) => delivered.push(`menu:${event.key}`));
    },
  };
  // 执行真实 main.js；若边界移到 mount 之后，先挂载的全局监听会使此测试失败。
  new Function('window', 'document', 'locale', 'initTheme', 'createApp', 'App', 't', 'isolateImeKeydown',
    main.replace(/^import .*;\r?$/gm, ''))(
    target, { documentElement: {} }, 'zh', () => {}, () => app, {}, () => '', isolateImeKeydown,
  );
  for (const name of ['Enter', 'Escape', 'ArrowUp', 'ArrowDown', 'Tab', ' ']) {
    const event = key(name, true);
    target.dispatchEvent(event);
    assert.equal(event.defaultPrevented, false, '原生选字行为不能被取消');
  }
  const lateConfirm = key('Enter', false, 229);
  target.dispatchEvent(lateConfirm);
  assert.equal(lateConfirm.defaultPrevented, false);
  assert.deepEqual(delivered, []);
  target.dispatchEvent(key('Enter', false));
  assert.deepEqual(delivered, ['app:Enter', 'menu:Enter']);
});

test('无论组词序列是否完整，非组合按键都不会被边界吞掉', () => {
  const target = new EventTarget();
  let delivered = 0;
  target.addEventListener('keydown', isolateImeKeydown, true);
  target.addEventListener('keydown', () => { delivered++; });
  for (let i = 0; i < 100; i++) {
    target.dispatchEvent(key('Enter', true));
    target.dispatchEvent(key('Enter', false));
  }
  assert.equal(delivered, 100);
});

test('真实新建/重命名弹窗中，确认选字不提交文件操作，普通 Enter 才提交', async () => {
  const from = explorer.indexOf('function promptNameDialog(');
  const to = explorer.indexOf('// 右键「新建文本文件」', from);
  assert.ok(from >= 0 && to > from);
  let options;
  let submitted = 0;
  let destroyed = 0;
  const prompt = new Function('ref', 'dialog', 'h', 'NInput', 't', `${explorer.slice(from, to)}
    return promptNameDialog;`)(
    (value) => ({ value }),
    { create(value) { options = value; return { destroy() { destroyed++; } }; } },
    (_type, props) => props, {}, () => '',
  );
  prompt({ title: '新建', placeholder: '', initialValue: '中文文件.txt', action: async () => {
    submitted++;
    return true;
  } });
  const input = options.content();
  const target = new EventTarget();
  target.addEventListener('keydown', isolateImeKeydown, true);
  target.addEventListener('keydown', input.onKeydown);
  const confirm = key('Enter', true);
  target.dispatchEvent(confirm);
  assert.equal(confirm.defaultPrevented, false);
  const lateConfirm = key('Enter', false, 229);
  target.dispatchEvent(lateConfirm);
  assert.equal(lateConfirm.defaultPrevented, false);
  assert.equal(submitted, 0);
  target.dispatchEvent(key('Enter', false));
  await Promise.resolve();
  assert.equal(submitted, 1);
  assert.equal(destroyed, 1);
});
