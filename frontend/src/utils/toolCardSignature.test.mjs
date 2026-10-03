/*
 * SPDX-License-Identifier: GPL-3.0-only
 *
 * Copyright (C) 2026 tangssst <tangssst@qq.com>
 * GitHub: https://github.com/Bronya0/ally-agent
 *
 * This file is part of ally-agent, licensed under the GNU General
 * Public License v3. See the LICENSE file for details.
 */
import test from 'node:test';
import assert from 'node:assert/strict';

import { toolCardRenderSignature } from './toolCardSignature.mjs';

test('signature changes when a tool card flips running -> success', () => {
  const running = {
    role: 'tool_call',
    kind: 'read',
    status: 'running',
    title: 'a.txt',
    body: '',
  };
  const success = { ...running, status: 'success', body: 'file contents' };
  assert.notEqual(
    toolCardRenderSignature(running),
    toolCardRenderSignature(success),
    'a status/body change must change the memo signature or the card freezes',
  );
});

test('signature is stable for identical tool cards', () => {
  const a = { role: 'tool_call', kind: 'grep', status: 'success', title: 'x', body: 'y' };
  const b = { role: 'tool_call', kind: 'grep', status: 'success', title: 'x', body: 'y' };
  assert.equal(toolCardRenderSignature(a), toolCardRenderSignature(b));
});

test('non-tool messages contribute an empty signature', () => {
  assert.equal(toolCardRenderSignature({ role: 'assistant', status: 'success' }), '');
  assert.equal(toolCardRenderSignature(null), '');
  assert.equal(toolCardRenderSignature(undefined), '');
});

test('array-length fields are reflected without deep comparison', () => {
  const base = { role: 'tool_call', kind: 'edit', status: 'success' };
  const withEntries = { ...base, editEntries: [{ path: 'a' }, { path: 'b' }] };
  assert.notEqual(toolCardRenderSignature(base), toolCardRenderSignature(withEntries));
  assert.equal(toolCardRenderSignature(withEntries), toolCardRenderSignature({ ...base, editEntries: [{ path: 'c' }, { path: 'd' }] }));
});

test('delete rows are reflected in the signature', () => {
  // 行是 tool:result 到达后才写上去的：不进签名的话 v-memo 会继续复用旧 vnode，
  // 卡片上的 "N paths" 就永远等不到它的逐行明细。
  const running = { role: 'tool_call', kind: 'delete', status: 'running', title: '2 paths' };
  const done = { ...running, status: 'success', deleteEntries: [{ path: 'a.txt', ok: true }, { path: 'b.txt', ok: false, error: 'denied' }] };
  assert.notEqual(
    toolCardRenderSignature(running),
    toolCardRenderSignature(done),
    'delete rows must change the memo signature or the card freezes on the summary title',
  );
});

test('signature changes when the action behind the verb arrives or changes', () => {
  // scheduled_task / service / plan render a verb keyed by the call's action, and
  // the action is captured from arguments that may land after the card is drawn.
  const withoutAction = { role: 'tool_call', kind: 'plan', status: 'running', title: 'Read code' };
  const withAction = { ...withoutAction, toolAction: 'set' };
  assert.notEqual(
    toolCardRenderSignature(withoutAction),
    toolCardRenderSignature(withAction),
    'a late action must change the memo signature or the card keeps the first verb',
  );
  assert.notEqual(
    toolCardRenderSignature(withAction),
    toolCardRenderSignature({ ...withAction, toolAction: 'next' }),
  );
});

test('streaming draft updates change running tool card signature', () => {
  const initial = { role: 'tool_call', kind: 'create', status: 'running', title: '', chip: '' };
  const withPath = { ...initial, title: 'demo.txt', editFilePath: 'demo.txt' };
  const withCode = { ...withPath, codeContent: 'hello world', chip: '11B' };
  assert.notEqual(toolCardRenderSignature(initial), toolCardRenderSignature(withPath));
  assert.notEqual(toolCardRenderSignature(withPath), toolCardRenderSignature(withCode));
});

test('sandbox denial is reflected in the signature', () => {
  // 命令跑完了但写被内核拦下：状态仍是 success，只有这个标记能推动卡片换成
  // 拒绝标记 + 那行醒目报错，不进签名就会一直显示绿色 √。
  const ran = { role: 'tool_call', kind: 'command', status: 'success', title: 'echo hi > /tmp/x' };
  const denied = { ...ran, sandboxDenied: true };
  assert.notEqual(
    toolCardRenderSignature(ran),
    toolCardRenderSignature(denied),
    'the denial flag must change the memo signature or the card keeps the success mark',
  );
});

test('huge content fields enter the signature as a cheap shape, not verbatim', () => {
  // body 是流式累积出来的完整输出（可以到几 MB），而签名每拍都要算一次：ChatMessages
  // 的 v-memo，以及 App.vue 那个跨整段会话的 buildDisplayMessagesSignature。整段拼
  // 进签名等于每拍抄一遍全文，主线程被占满，卡片旁边的动画就跟着掉帧。
  const huge = 'x'.repeat(2 * 1024 * 1024);
  const running = { role: 'tool_call', kind: 'command', status: 'running', title: 'go test', body: huge };
  const sig = toolCardRenderSignature(running);
  assert.ok(sig.length < 1024, `signature must not embed the whole body (got ${sig.length} chars)`);
  // 但每一次流式追加仍必须改变签名，否则 v-memo 会把卡片冻在旧内容上。
  assert.notEqual(sig, toolCardRenderSignature({ ...running, body: `${huge}y` }), 'append must change the signature');
  assert.notEqual(sig, toolCardRenderSignature({ ...running, body: `y${huge}` }), 'prepend must change the signature');
  // codeContent 走同一份指纹。
  const create = { role: 'tool_call', kind: 'create', status: 'running', codeContent: huge };
  assert.ok(toolCardRenderSignature(create).length < 1024, 'codeContent must not be embedded either');
  assert.notEqual(
    toolCardRenderSignature(create),
    toolCardRenderSignature({ ...create, codeContent: `${huge}y` }),
  );
});
