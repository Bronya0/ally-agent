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

import {
  STREAM_MIN_STEP,
  TAIL_EAGER_MAX_CHARS,
  advanceShown,
  createStreamRenderState,
  shouldEagerParseTail,
  splitStreamContent,
} from './streamEase.mjs';

test('advanceShown 每帧只推进一部分差值，多帧内收敛到目标', () => {
  const target = 100;
  let shown = 0;
  const steps = [];
  while (shown < target) {
    const next = advanceShown(shown, target);
    assert.ok(next > shown, '每帧必须推进，否则永远追不上');
    assert.ok(next <= target, '不能超过已接收长度');
    steps.push(next - shown);
    shown = next;
  }
  assert.equal(shown, target);
  assert.ok(steps.length > 3, '一批 100 字要摊到多帧，而不是一帧全出');
  assert.equal(steps[0], 30, '首帧按比例推进差值');
  assert.ok(steps[steps.length - 1] <= steps[0], '推进量随时间收敛');
});

test('advanceShown 至少推进 STREAM_MIN_STEP，避免差值收敛后卡住', () => {
  assert.equal(advanceShown(99, 100), 99 + STREAM_MIN_STEP);
});

test('advanceShown 目标回退（内容被截断/重试丢弃）时直接对齐', () => {
  assert.equal(advanceShown(500, 120), 120);
  assert.equal(advanceShown(10, 0), 0);
});

test('advanceShown 对非法输入按 0 处理', () => {
  assert.equal(advanceShown(NaN, 10), advanceShown(0, 10));
  assert.equal(advanceShown(5, NaN), 0);
});

test('splitStreamContent 只在空白行之后切块，段落中途不切', () => {
  const content = '第一段\n\n第二段还在流';
  const cut = splitStreamContent(content, content.length);
  assert.equal(cut.committedEnd, content.indexOf('第二段'));
  assert.equal(cut.openFence, false);
});

test('splitStreamContent 的 limit 落在段落中途时保留上一个边界', () => {
  const content = '第一段\n\n第二段';
  // limit 只到第一段行尾：空白行还没显示出来，不算块边界。
  assert.equal(splitStreamContent(content, 3).committedEnd, 0);
  assert.equal(splitStreamContent(content, 4).committedEnd, 0);
  // limit 越过空白行：第一个块已完整，边界推到空白行之后。
  assert.equal(splitStreamContent(content, content.indexOf('第', 3)).committedEnd, 5);
});

test('splitStreamContent 不切在未闭合的围栏里（含围栏内的空白行）', () => {
  const content = '# 标题\n\n```go\nfunc main() {\n\n}\n';
  const cut = splitStreamContent(content, content.length);
  assert.equal(cut.committedEnd, '# 标题\n\n'.length, '围栏开始前的边界才是安全的');
  assert.equal(cut.openFence, true);
});

test('splitStreamContent 围栏闭合后恢复切分', () => {
  const content = '```go\ncode\n```\n\n尾段';
  const cut = splitStreamContent(content, content.length);
  assert.equal(cut.committedEnd, '```go\ncode\n```\n\n'.length);
  assert.equal(cut.openFence, false);
});

test('splitStreamContent 支持波浪号围栏与更长的闭合围栏', () => {
  const content = '~~~js\nlet a = 1;\n~~~~\n\ntail';
  const cut = splitStreamContent(content, content.length);
  assert.equal(cut.committedEnd, content.indexOf('tail'), '围栏闭合后的空白行才是边界');
  assert.equal(cut.openFence, false);

  const shorter = splitStreamContent('```\nx\n``\n', 8);
  assert.equal(shorter.openFence, true, '闭合围栏短于开启围栏不算闭合');
});

test('splitStreamContent 缩进 4 空格的围栏不算代码围栏', () => {
  const cut = splitStreamContent('    ```go\nnot a fence\n\n段落\n', 28);
  assert.equal(cut.openFence, false);
  assert.equal(cut.committedEnd, '    ```go\nnot a fence\n\n'.length);
});

test('splitStreamContent 内容以空白行结尾时全部提交、尾部为空', () => {
  const content = '段落一\n\n';
  const cut = splitStreamContent(content, content.length);
  assert.equal(cut.committedEnd, content.length);
  assert.equal(content.slice(cut.committedEnd), '');
});

test('splitStreamContent 容忍 limit 超界与非法值', () => {
  assert.equal(splitStreamContent('a\n\nb', 999).committedEnd, 3);
  assert.equal(splitStreamContent('a\n\nb', NaN).committedEnd, 3);
  assert.equal(splitStreamContent('', 10).committedEnd, 0);
});

test('shouldEagerParseTail 对短尾部逐帧解析，对长尾/公式/图片降频', () => {
  assert.equal(shouldEagerParseTail('普通短段落'), true);
  assert.equal(shouldEagerParseTail('a'.repeat(TAIL_EAGER_MAX_CHARS)), true);
  assert.equal(shouldEagerParseTail('a'.repeat(TAIL_EAGER_MAX_CHARS + 1)), false);
  assert.equal(shouldEagerParseTail('行内公式 $x^2$ 之后'), false);
  assert.equal(shouldEagerParseTail('![图](a.png)'), false);
  assert.equal(shouldEagerParseTail(''), true);
});

// ── 状态机契约 ────────────────────────────────────────────────────────────

/** 模拟一次流式输出：每批 chars 字符、批间隔 frames 帧，返回解析账本。 */
function simulateStream(source, { batch = 25, frames = 4 } = {}) {
  const parses = [];
  const frameCalls = []; // 每帧的解析调用次数（结构性护栏：最多前缀 + 尾部各一次）
  // 渲染函数用恒等实现：这样 committedHtml + tailHtml 就是“屏幕上实际显示的
  // 文字”，可以直接断言不重不漏不换序。
  const state = createStreamRenderState((text) => {
    parses.push(text);
    return text;
  });
  let content = '';
  let shown = 0;
  let displayed = '';
  let naiveParsed = 0;
  for (let i = 0; i < source.length; i += batch) {
    content += source.slice(i, i + batch);
    naiveParsed += content.length; // 改造前的做法：每批把整篇重解析一次
    for (let f = 0; f < frames; f++) {
      const callsBefore = parses.length;
      shown = advanceShown(shown, content.length);
      const out = state.render(content, shown, true);
      const text = out.committedHtml + out.tailHtml;
      assert.ok(content.startsWith(text), '显示的必须是已接收内容的前缀（不重不漏不换序）');
      assert.ok(text.length >= displayed.length, '已显示长度不允许回退');
      displayed = text;
      frameCalls.push(parses.length - callsBefore);
    }
  }
  return { state, content, parses, frameCalls, naiveParsed };
}

const PROSE_A =
  '第一段用来验证逐帧推进：显示层缓动把已接收长度与已显示长度拆开，每一帧只推进剩余差值的'
  + '一部分，于是上游 64ms 一批的字符被摊成连续输出，观感上不再是一跳一跳，而是像有人在'
  + '敲键盘。\n\n';
const PROSE_B =
  '第二段用来验证块边界切分：渲染前会先在已显示范围内找到最后一个“围栏之外的安全边界”，'
  + '把它之前的内容交给已完成块、把它之后的内容留在活动尾部，这样已完成的块只在边界推进时'
  + '解析一次，只有活动尾部会逐帧重解析。\n\n';
const CODE_BLOCK =
  '```js\n'
  + 'const { committedEnd } = splitStreamContent(content, shown);\n'
  + 'const committed = content.slice(0, committedEnd);\n'
  + 'const tail = content.slice(committedEnd, shown);\n'
  + '```\n\n';
const LIST_BLOCK = '- 前缀只在块边界推进时解析\n- 尾部逐帧解析、长尾降频\n- 收尾整篇只解析一次\n\n';
const PROSE_C =
  '最后一段用来验证收尾：流式结束后整篇重渲染一次，恢复代码高亮与图形渲染，并把显示长度'
  + '一次性对齐到已接收长度。\n\n';
// 样本要接近真实模型输出：段落长（块边界稀疏）、含围栏代码块与列表。
const STREAM_SOURCE = [PROSE_A, PROSE_B, CODE_BLOCK, LIST_BLOCK, PROSE_C].join('');

test('状态机：流式期间显示内容始终是已接收内容的前缀，且单调不回退', () => {
  const source = STREAM_SOURCE.repeat(4); // 断言在模拟内部逐帧执行
  assert.ok(source.length > 1500, '样本要足够长才有意义');
  simulateStream(source);
});

test('状态机：单帧解析次数有界（最多一次前缀 + 一次尾部）', () => {
  const { frameCalls } = simulateStream(STREAM_SOURCE.repeat(4));
  const worst = frameCalls.reduce((max, n) => Math.max(max, n), 0);
  assert.ok(worst <= 2, `单帧解析次数应 ≤ 2，实际 ${worst}`);
  assert.ok(frameCalls.some((n) => n > 0), '应该有帧真的发生了解析，否则统计无效');
});

test('状态机：长回答下总解析量明显低于“每批重解析整篇”', () => {
  // 收益来自解析频率：前缀按“块”重解析、尾部只解析当前一块，而不是每批把
  // 整篇重解析一次。所以断言用相对值（同场景下与旧做法的开销比），不依赖
  // 具体字符数——避免以后调整常量时变成假失败。
  const long = STREAM_SOURCE.repeat(12);
  const { content, parses, naiveParsed } = simulateStream(long);
  const parsed = parses.reduce((sum, text) => sum + text.length, 0);
  assert.ok(content.length > 5000, '长回答场景才有意义');
  assert.ok(
    parsed < naiveParsed / 2,
    `解析总量应明显低于逐批整篇重解析（实际 ${parsed} vs ${naiveParsed}）`,
  );
});

test('状态机：收尾强制渲染等于全文，且整篇只解析一次', () => {
  const source = STREAM_SOURCE.repeat(2);
  const { state, content } = simulateStream(source);
  const parses = [];
  const finalState = createStreamRenderState((text) => {
    parses.push(text);
    return text;
  });
  assert.ok(state, '模拟期状态机存在');
  const out = finalState.render(content, content.length, false, true);
  assert.equal(out.committedHtml + out.tailHtml, content, '收尾必须等于全文');
  assert.equal(parses.length, 1, '收尾只解析一次整篇');
});

test('状态机：内容被截断（重试丢弃）时立即对齐到新长度', () => {
  const state = createStreamRenderState((text) => text);
  const long = '甲段文字重复\n\n'.repeat(10);
  assert.equal(state.render(long, long.length, true).committedHtml.length > 0, true);
  const short = '甲段文字重复\n\n';
  const out = state.render(short, short.length, true, true);
  assert.equal(out.committedHtml + out.tailHtml, short, '截断后显示内容不能多出旧文字');
});
