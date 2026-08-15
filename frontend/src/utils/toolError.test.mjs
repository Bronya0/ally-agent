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
import { formatToolErrorBody } from './toolError.mjs';

test('strips trailing blocked-command echo with fullwidth colon', () => {
  const body = [
    '安全围栏已拦截：command 不允许直接执行文件删除命令。',
    '原因：shell 删除命令可能绕过工作区边界。',
    '被拦截的命令：rm -rf /tmp/demo',
  ].join('\n');

  assert.equal(
    formatToolErrorBody(body),
    [
      '安全围栏已拦截：command 不允许直接执行文件删除命令。',
      '原因：shell 删除命令可能绕过工作区边界。',
    ].join('\n'),
  );
});

test('strips trailing blocked-command echo with ascii colon', () => {
  const body = '高危命令拒绝: 检测到fork炸弹 - 命令已被安全围栏拦截。\n被拦截的命令: :(){ :|:& };:';
  assert.equal(
    formatToolErrorBody(body),
    '高危命令拒绝: 检测到fork炸弹 - 命令已被安全围栏拦截。',
  );
});

test('returns empty when body only contains blocked-command echo', () => {
  assert.equal(formatToolErrorBody('被拦截的命令: npm run dev'), '');
});

test('keeps body without blocked-command echo', () => {
  assert.equal(
    formatToolErrorBody('cwd is not a directory: /tmp/missing'),
    'cwd is not a directory: /tmp/missing',
  );
});

test('normalizes CRLF and trims trailing whitespace', () => {
  const body = '命令被拦截。\r\n被拦截的命令: echo hi\r\n';
  assert.equal(formatToolErrorBody(body), '命令被拦截。');
});
