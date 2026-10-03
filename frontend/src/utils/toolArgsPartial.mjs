/*
 * SPDX-License-Identifier: GPL-3.0-only
 *
 * Copyright (C) 2026 tangssst <tangssst@qq.com>
 * GitHub: https://github.com/Bronya0/ally-agent
 *
 * This file is part of ally-agent, licensed under the GNU General
 * Public License v3. See the LICENSE file for details.
 */

/**
 * 流式工具参数的兜底解析：请求的 arguments 还在流式（JSON 尚未闭合、JSON.parse
 * 必抛）时，尽力认出几个字段，好让工具卡当场显示标题与预览，不必等结果回来。
 *
 * 旧实现在 App.vue 里为每个字段各做一次 indexOf 全文扫描，找到值再逐字符
 * `value += ch` 拼字符串：1MB 累积参数约 33ms、4MB 约 144ms（超线性）。而工具卡
 * 每一拍都要算一次（小载荷 120ms，见 utils/toolUpdateFlush.mjs），写大文件的整个
 * 流式期间主线程就被这一件事占满，卡片旁边正在跑的动画跟着掉帧。
 *
 * 这里只改实现、不改语义（对拍用例见 toolArgsPartial.test.mjs）：
 *   - 键用一次原生正则扫描全部找出，不再按字段逐个 indexOf；
 *   - 字符串值用 slice 整段取出，不再逐字符拼串；
 *   - 同一个键只认第一次出现，且要求后面跟着冒号；第一次出现的位置若拿不到
 *     字符串（或数组）就放弃该字段，不再往后找同名键。
 */

/** 兜底解析认得的标量字段。`lines` 是数组，单独处理。 */
export const PARTIAL_TOOL_ARG_FIELDS = [
  'target',
  'path',
  'content',
  'command',
  'cmd',
  'pattern',
  'glob',
  'expression',
  'description',
  'url',
  'title',
  'html',
  'changes',
  'oldText',
  'oldString',
  'newString',
  'newText',
];

const FIELD_SET = new Set(PARTIAL_TOOL_ARG_FIELDS);

// 对象键。值内部出现同形文本时会误匹配，这一点与旧实现（indexOf '"field"'）同级：
// 都靠「后面必须跟冒号」过滤，且只有认得的字段名才会被采用。冒号后的空白一并
// 吞掉，match[0] 的末尾就是值的起点（旧实现同样会跳过这里的空白）。
const KEY_PATTERN = /"([^"\\]*)"[ \t\r\n]*:[ \t\r\n]*/g;
// 字符串值里需要特殊处理的字符：收尾引号与转义引导符。普通片段一次 slice 取出。
const STRING_SPECIAL_PATTERN = /["\\]/g;
const ESCAPE_REPLACEMENTS = { n: '\n', r: '\r', t: '\t', b: '\b', f: '\f' };
const HEX4_PATTERN = /^[0-9a-fA-F]{4}$/;

export function parsePartialToolArgs(raw) {
  const text = String(raw || '');
  if (!text.trim()) return {};
  try {
    const parsed = JSON.parse(text);
    return parsed && typeof parsed === 'object' ? parsed : {};
  } catch (_) {
    // JSON 还没闭合（流式中）：走兜底。
  }
  return readPartialToolArgs(text);
}

function readPartialToolArgs(text) {
  const out = {};
  const seen = new Set();
  KEY_PATTERN.lastIndex = 0;
  let match;
  while ((match = KEY_PATTERN.exec(text)) !== null) {
    const field = match[1];
    if (seen.has(field)) continue;
    if (field !== 'lines' && !FIELD_SET.has(field)) continue;
    // 认得的字段名才记入 seen：只在第一次出现的位置取值，与旧实现一致。
    seen.add(field);
    const valueStart = match.index + match[0].length;
    if (field === 'lines') {
      const lines = readPartialJsonStringArray(text, valueStart);
      if (lines.found) out.lines = lines.value;
      continue;
    }
    if (text[valueStart] !== '"') continue;
    const value = readPartialJsonString(text, valueStart);
    if (value.found) out[field] = value.value;
  }
  return out;
}

/**
 * 读一个可能还没闭合的 JSON 字符串字面量，从引号位置开始。
 * 返回 { found, value, complete, nextIndex }：
 *   complete 为真表示遇到了收尾引号（值已定稿）；
 *   nextIndex 只在 complete 时被调用方使用（跳过整个字面量）。
 */
export function readPartialJsonString(text, quoteIndex) {
  let value = '';
  let plainStart = quoteIndex + 1;
  STRING_SPECIAL_PATTERN.lastIndex = plainStart;
  for (;;) {
    const hit = STRING_SPECIAL_PATTERN.exec(text);
    if (!hit) {
      // 还没遇到收尾引号：剩下的全是普通字符，一次取走。
      return { found: true, value: value + text.slice(plainStart), complete: false, nextIndex: text.length };
    }
    const at = hit.index;
    if (text[at] === '"') {
      value += text.slice(plainStart, at);
      return { found: true, value, complete: true, nextIndex: at + 1 };
    }
    // 反斜杠：先取走它前面的普通段，再处理这个转义。
    value += text.slice(plainStart, at);
    const esc = text[at + 1];
    if (esc === undefined) {
      // 转义序列还没收全：值里先不带它，下一帧重扫时会重新遇到。
      return { found: true, value, complete: false, nextIndex: at };
    }
    if (esc === 'u') {
      const hex = text.slice(at + 2, at + 6);
      if (!HEX4_PATTERN.test(hex)) {
        // 旧实现遇到读不全/非法的 \\u 就在这里停住；保持同样行为。
        return { found: true, value, complete: false, nextIndex: at };
      }
      value += String.fromCharCode(parseInt(hex, 16));
      plainStart = at + 6;
    } else {
      value += ESCAPE_REPLACEMENTS[esc] ?? esc;
      plainStart = at + 2;
    }
    STRING_SPECIAL_PATTERN.lastIndex = plainStart;
  }
}

function readPartialJsonStringArray(text, bracketIndex) {
  if (text[bracketIndex] !== '[') return { found: false, value: [] };
  const value = [];
  let i = bracketIndex + 1;
  while (i < text.length) {
    while (i < text.length && /[\s,]/.test(text[i])) i++;
    if (text[i] === ']') return { found: true, value, complete: true };
    if (text[i] !== '"') {
      return value.length ? { found: true, value, complete: false } : { found: false, value: [] };
    }
    const item = readPartialJsonString(text, i);
    if (!item.found) {
      return value.length ? { found: true, value, complete: false } : { found: false, value: [] };
    }
    value.push(item.value);
    i = item.nextIndex || text.length;
    if (!item.complete) return { found: true, value, complete: false };
  }
  return { found: true, value, complete: false };
}
