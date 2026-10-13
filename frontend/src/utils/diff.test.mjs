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
import { readFileSync } from 'node:fs';
import test from 'node:test';
import { computeDiffLines, computeEditStats } from './diff.js';

const read = (relative) => readFileSync(new URL(relative, import.meta.url), 'utf8');

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

// 取样区是后端与前端共用的一个标记：真实变更量超出上限时后端只给统计与两侧取样，
// 前端见到这个前缀就不再给后续行编号（那些行不是逐行 diff，位置也对不上）。跨语言没法
// 共用常量，只能把同一个词钉在两边；一边改了措辞而另一边没改时症状很轻，界面照常渲染，
// 只是行号全错，所以这里必须红。
test('取样区标记在后端渲染器与前端解析器里是同一个词', () => {
  const marker = '[span:';
  assert.ok(
    read('../../../internal/tools/edit/diff.go').includes(`"${marker}`),
    '后端不再用这个标记起头输出取样区（改了措辞？DiffView 要一起改）',
  );
  assert.ok(
    read('../components/DiffView.vue').includes(marker),
    'DiffView 不再按这个标记识别取样区（后端标记要一起改）',
  );
});
