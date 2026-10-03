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

import { parsePartialToolArgs } from './toolArgsPartial.mjs';

// ── 参照实现：改造前 App.vue 里那一套（逐字段 indexOf + 逐字符拼串）────────────
// 新实现只许更快，不许改结果：下面所有用例都是拿它做对拍。
const LEGACY_FIELDS = [
  'target', 'path', 'content', 'command', 'cmd', 'pattern', 'glob', 'expression',
  'description', 'url', 'title', 'html', 'changes', 'oldText', 'oldString',
  'newString', 'newText',
];

function legacyParse(raw) {
  const text = String(raw || '');
  if (!text.trim()) return {};
  try {
    const parsed = JSON.parse(text);
    return parsed && typeof parsed === 'object' ? parsed : {};
  } catch (_) {
    const partial = {};
    for (const field of LEGACY_FIELDS) {
      const found = legacyStringField(text, field);
      if (found.found) partial[field] = found.value;
    }
    const lines = legacyArrayField(text, 'lines');
    if (lines.found) partial.lines = lines.value;
    return partial;
  }
}

function legacyStringField(text, field) {
  const needle = `"${field}"`;
  let keyIndex = text.indexOf(needle);
  while (keyIndex >= 0) {
    let i = keyIndex + needle.length;
    while (/\s/.test(text[i] || '')) i++;
    if (text[i] !== ':') {
      keyIndex = text.indexOf(needle, keyIndex + needle.length);
      continue;
    }
    i++;
    while (/\s/.test(text[i] || '')) i++;
    if (text[i] !== '"') return { found: false, value: '' };
    return legacyString(text, i);
  }
  return { found: false, value: '' };
}

function legacyArrayField(text, field) {
  const needle = `"${field}"`;
  let keyIndex = text.indexOf(needle);
  while (keyIndex >= 0) {
    let i = keyIndex + needle.length;
    while (/\s/.test(text[i] || '')) i++;
    if (text[i] !== ':') {
      keyIndex = text.indexOf(needle, keyIndex + needle.length);
      continue;
    }
    i++;
    while (/\s/.test(text[i] || '')) i++;
    if (text[i] !== '[') return { found: false, value: [] };
    i++;
    const value = [];
    while (i < text.length) {
      while (/[\s,]/.test(text[i] || '')) i++;
      if (text[i] === ']') return { found: true, value, complete: true };
      if (text[i] !== '"') return value.length ? { found: true, value, complete: false } : { found: false, value: [] };
      const item = legacyString(text, i);
      if (!item.found) return value.length ? { found: true, value, complete: false } : { found: false, value: [] };
      value.push(item.value);
      i = item.nextIndex || text.length;
      if (!item.complete) return { found: true, value, complete: false };
    }
    return { found: true, value, complete: false };
  }
  return { found: false, value: [] };
}

function legacyString(text, quoteIndex) {
  let value = '';
  for (let i = quoteIndex + 1; i < text.length; i++) {
    const ch = text[i];
    if (ch === '"') return { found: true, value, complete: true, nextIndex: i + 1 };
    if (ch !== '\\') {
      value += ch;
      continue;
    }
    i++;
    if (i >= text.length) return { found: true, value, complete: false, nextIndex: i };
    const esc = text[i];
    if (esc === 'n') value += '\n';
    else if (esc === 'r') value += '\r';
    else if (esc === 't') value += '\t';
    else if (esc === 'b') value += '\b';
    else if (esc === 'f') value += '\f';
    else if (esc === 'u') {
      const hex = text.slice(i + 1, i + 5);
      if (/^[0-9a-fA-F]{4}$/.test(hex)) {
        value += String.fromCharCode(parseInt(hex, 16));
        i += 4;
      } else {
        return { found: true, value, complete: false, nextIndex: i };
      }
    } else {
      value += esc;
    }
  }
  return { found: true, value, complete: false, nextIndex: text.length };
}

// ── 对拍 ───────────────────────────────────────────────────────────────────

const SAMPLES = [
  '',
  '   ',
  'not json',
  '{"path": "a.txt"}',
  '{"path": "a.txt"',
  '{"path":"a.txt","content":"line1\\nline2"}',
  '{"content":"say \\"hi\\"","path":"x"}',
  '{"content":"\\u4e2d\\u6587","command":"echo"}',
  '{"content":"\\u4e2d", "path":"p"}',
  '{"content":"\\u4e", "path":"p"}',
  '{"content":"cut\\\\',
  '{"content":"tail\\\\back","path":"p"}',
  '{"lines":["a","b"],"path":"p"}',
  '{"lines":["a","b"',
  '{"lines":[],"path":"p"}',
  '{"lines":["a",',
  '{"lines":"not an array","path":"p"}',
  '{"path":"has \\"content\\": inside"}',
  '{"target":"t","changes":"c","oldText":"o","newText":"n","html":"<b>","url":"u"}',
  '{"unknown":"x","path":"p"}',
  '{"path" : "spaced" }',
  '{"path"x:"no"}',
  '{"nested":{"path":"deep"}}',
  '{"path":"","content":""}',
  String.raw`{"content":"\\\"`,
  String.raw`{"content":"a\\b"}`,
  String.raw`{"content":"tail\\`,
];

test('新实现与旧实现逐字一致（固定样本）', () => {
  for (const sample of SAMPLES) {
    assert.deepEqual(
      parsePartialToolArgs(sample),
      legacyParse(sample),
      `样本不一致：${JSON.stringify(sample)}`,
    );
  }
});

// 最强的一条：把一段真实形状的参数在**每一个位置**截断，逐一比对。
// 流式期间每一拍看到的正是这样一个前缀，任何一处行为差异都会在这里现形。
test('新实现与旧实现逐字一致（逐字符截断）', () => {
  const full = '{"target":"t","path":"a.txt","content":"line1\\nline2 \\"q\\" \\u4e2d\\u6587 tail\\\\end",'
    + '"lines":["x","y"],"command":"go test ./...","description":"d","oldString":"o","newString":"n"}';
  for (let cut = 0; cut <= full.length; cut++) {
    const text = full.slice(0, cut);
    assert.deepEqual(
      parsePartialToolArgs(text),
      legacyParse(text),
      `截断到 ${cut} 时不一致：${JSON.stringify(text)}`,
    );
  }
});

// 值为数组的字段在流式里会一个个长出来，逐个截断同样要对得上。
test('lines 数组逐字符截断也与旧实现一致', () => {
  const full = '{"lines":["first","second","third"],"path":"p"}';
  for (let cut = 0; cut <= full.length; cut++) {
    const text = full.slice(0, cut);
    assert.deepEqual(parsePartialToolArgs(text), legacyParse(text), `截断到 ${cut}：${JSON.stringify(text)}`);
  }
});

test('JSON 已闭合时直接走 JSON.parse，与兜底解析无关', () => {
  const done = '{"path":"a.txt","content":"done"}';
  assert.deepEqual(parsePartialToolArgs(done), { path: 'a.txt', content: 'done' });
});
