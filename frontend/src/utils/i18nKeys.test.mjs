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

// i18n.mjs 顶层 import naive-ui，node --test 里不能 import 它（utils/*.mjs 因此一律
// 不引 i18n），所以这条按文本解析：取出 zh 与 enOverrides 两个块比键集合。
const source = readFileSync(new URL('../i18n.mjs', import.meta.url), 'utf8');

function blockKeys(startMarker, endMarker) {
  const start = source.indexOf(startMarker);
  const end = source.indexOf(endMarker);
  assert.ok(start >= 0 && end > start, `i18n.mjs 里找不到 ${startMarker} … ${endMarker} 这一段（改名了？）`);
  const keys = new Set();
  for (const match of source.slice(start, end).matchAll(/'([A-Za-z0-9_.]+)':/g)) keys.add(match[1]);
  return keys;
}

const zhKeys = blockKeys('const zh = {', 'const enOverrides = {');
const enKeys = blockKeys('const enOverrides = {', 'const en = {');

// 英文表是 { ...zh, ...enOverrides }：漏译不会报错，英文界面只是显示中文。所以两边
// 必须逐键对齐——少一个就是英文界面里冒中文，多一个就是没人用的死键（两个方向的漂移
// 都只能靠这条测试看见）。
test('英文覆盖表与中文表的键逐一对齐', () => {
  assert.deepEqual(
    [...zhKeys].filter((key) => !enKeys.has(key)),
    [],
    '这些键没有英文，英文界面会显示中文',
  );
  assert.deepEqual(
    [...enKeys].filter((key) => !zhKeys.has(key)),
    [],
    '这些键只存在于英文覆盖表，中文表里没有',
  );
});

// 同一个块里重复定义同一个键时，后者静默覆盖前者（对象字面量合法），是复制粘贴最
// 容易留下的痕迹。
test('两个块内部都没有重复键', () => {
  for (const [label, marker, endMarker] of [
    ['zh', 'const zh = {', 'const enOverrides = {'],
    ['enOverrides', 'const enOverrides = {', 'const en = {'],
  ]) {
    const start = source.indexOf(marker);
    const end = source.indexOf(endMarker);
    const seen = new Set();
    const duplicates = [];
    for (const match of source.slice(start, end).matchAll(/'([A-Za-z0-9_.]+)':/g)) {
      if (seen.has(match[1])) duplicates.push(match[1]);
      seen.add(match[1]);
    }
    assert.deepEqual(duplicates, [], `${label} 块里有重复键`);
  }
});
