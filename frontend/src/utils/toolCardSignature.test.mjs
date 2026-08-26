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
