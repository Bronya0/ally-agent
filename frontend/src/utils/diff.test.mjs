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
import { computeDiffLines, computeEditStats } from './diff.js';

test('large diffs use a bounded fallback and preserve shared edges', () => {
  const oldText = ['same start', ...Array.from({ length: 1500 }, (_, i) => `old ${i}`), 'same end'].join('\n');
  const newText = ['same start', ...Array.from({ length: 1500 }, (_, i) => `new ${i}`), 'same end'].join('\n');

  const lines = computeDiffLines(oldText, newText);

  assert.equal(lines[0].kind, 'context');
  assert.equal(lines[0].code, 'same start');
  assert.equal(lines.at(-1).kind, 'context');
  assert.equal(lines.at(-1).code, 'same end');
  assert.equal(lines.filter((line) => line.kind === 'delete').length, 1500);
  assert.equal(lines.filter((line) => line.kind === 'add').length, 1500);
});

test('small diffs retain exact edit statistics', () => {
  const stats = computeEditStats('a\nb\nc', 'a\nx\nc');
  assert.deepEqual(stats, { added: 1, removed: 1 });
});

test('large fallback folds long unchanged runs between scattered changes', () => {
  const lines = Array.from({ length: 300 }, (_, i) => `line ${i}`);
  const mod = [...lines];
  mod[1] = 'changed a';
  mod[298] = 'changed b';

  const diff = computeDiffLines(lines.join('\n'), mod.join('\n'));

  const folded = diff.filter((l) => l.kind === 'context' && l.code.includes('unchanged lines'));
  assert.equal(folded.length, 1);
  assert.equal(diff.filter((l) => l.kind === 'delete').length, 2);
  assert.equal(diff.filter((l) => l.kind === 'add').length, 2);
});
