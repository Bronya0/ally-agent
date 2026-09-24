<!--
SPDX-License-Identifier: GPL-3.0-only

Copyright (C) 2026 tangssst <tangssst@qq.com>
GitHub: https://github.com/Bronya0/ally-agent

This file is part of ally-agent, licensed under the GNU General
Public License v3. See the LICENSE file for details.
-->

<!--
Composer status row's "running" mark: three vertical bars that stretch on a
1.3s wave travelling left → right — every bar rests at 0% / 80% / 100% and
peaks at 40%, neighbours staggered 0.208s, so the wave never stalls. The stagger
has to stay ≥ 10% of the cycle: twice it must reach the 20% still window,
otherwise the three phases fit inside that window and the whole mark freezes for
a few frames every cycle. It is mounted for the
row's whole lifetime (App.vue mounts it unconditionally inside
.composer-run-status), so it runs alongside the phase label and carries the row
alone whenever the label is empty — every phase except thinking, and a
compaction without a thinking count.

This is the classic `escaleY` bar loader, rebuilt for a text row. Two structural
changes, both load-bearing:

1. Nothing is painted outside the layout box. The original draws its outer bars
   with `left: ±2em` and its peak with `box-shadow: 0 -2em`, so 55×77px of
   visible content hangs off an 11×44px box. A flex row reserves only the box:
   the left bar gets clipped by .composer-run-status's `overflow: hidden`, and
   the right bar lands on top of the label. Here the mark is three in-flow bars
   in a plain flex row, and box-shadow is not used at all.
2. The animation is transform-only. The original animates `height` and
   `box-shadow` — both reflow/repaint, and the bar is an in-flow flex item, so
   the status row is laid out again on every frame. Here the box height is
   frozen at PEAK_H and only scaleY moves, about the box centre, from
   REST_H / PEAK_H to 1. The mark's box is therefore constant, so the row never
   grows or shifts when the mark mounts or swaps places with the label
   (LESSONS: composer-icon-h).

Geometry lives in the script (bar width / pitch / rest and peak height / step)
and reaches CSS through custom properties, so size and timing have one source.
The keyframe stops are the original's; only the animated property differs.

Bars are 3:9 rather than the original's 11:44: at a 9px rest height the exact
ratio would ask for sub-pixel strokes, and 3px is the width the row's dots
already used, so the mark keeps the row's stroke weight. The stretch ratio is
unchanged (16/9 ≈ 1.78 against the original's 77/44 = 1.75).

The window-hidden pause follows the same shape as the project's other infinite
animations (SakuraBreeze / DigitalWave / AllyAvatar): listen to visibilitychange
and freeze through animation-play-state instead of unmounting anything.

Colors reproduce the label's own brightness ramp: the bars run the label's
resting tone --ally-text-faint, one full step under its shimmer ceiling
--ally-text-high. The middle step (--ally-text-soft, the first choice) made the
mark outshine the very text it sits next to — solid bars cover far more area than
the label's thin strokes, so an equal token already reads brighter than the text.
It is a neutral ramp token, so the row keeps a single grey and a theme change
moves label and mark together. Do not switch these bars to --ally-accent (a
second hue), and do not lift them to --ally-text-high.
-->
<template>
  <span
    class="run-bars"
    :class="{ 'is-held': held }"
    aria-hidden="true"
    :style="{
      '--bar': `${BAR_W}px`,
      '--pitch': `${PITCH}px`,
      '--peak': `${PEAK_H}px`,
      '--rest': `${REST_SCALE}`,
      '--cycle': `${CYCLE_MS}ms`,
    }"
  >
    <i v-for="bar in bars" :key="bar.key" :style="bar.style" />
  </span>
</template>

<script setup>
import { onBeforeUnmount, onMounted, ref } from 'vue';

const BAR_COUNT = 3; // 条数
const BAR_W = 3; // 条宽（px）：与此前状态行方阵的点同宽
const PITCH = 6; // 相邻条起点间距（px）= 3 条宽 + 3 间隙
const REST_H = 9; // 静息高度（px）
const PEAK_H = 16; // 峰值高度（px）= 布局盒高度，恒定
const REST_SCALE = REST_H / PEAK_H;
const CYCLE_MS = 1300; // 单条一轮
// 相邻条错相 = 周期的 16%（与旧参数同比例）：波浪 0.416s 从左走到右。下限是
// 周期的 10%，理由见文件头（只看"小于静默窗口"会在长周期下误判）。
const STEP_MS = 208;

// 波浪自左向右：最左条相位最靠前，所以负延迟最大（与原版同序）。负延迟让动画一上来
// 就在周期中间，不会先跳一下。
const bars = Array.from({ length: BAR_COUNT }, (_, index) => ({
  key: index,
  style: {
    left: `${index * PITCH}px`,
    animationDelay: `${-(BAR_COUNT - 1 - index) * STEP_MS}ms`,
  },
}));

// 窗口最小化/隐藏时冻结动画（项目里其它常驻无限动画同此做法）。
const held = ref(false);
const onVisibilityChange = () => { held.value = document.hidden; };
onMounted(() => document.addEventListener('visibilitychange', onVisibilityChange));
onBeforeUnmount(() => document.removeEventListener('visibilitychange', onVisibilityChange));
</script>

<style scoped>
.run-bars {
  position: relative;
  flex: 0 0 auto;
  /* 宽 = 三条外沿；高 = 峰值高度（不变）。盒子恒定，所以这个标记挂载、卸载或与标签
     互换位置时状态行都不动。可见范围不得超出此盒，否则会被 .composer-run-status 的
     overflow: hidden 裁掉并压住标签。 */
  width: calc(var(--pitch) * 2 + var(--bar));
  height: var(--peak);
  /* 亮度基准与标签正文同档（--ally-text-faint）；实心条覆盖面远大于细笔画，同档
     取值已经比文字略“实”。不要往上提：中间档 --ally-text-soft 实测偏亮，已下调；
     --ally-text-high 更不可用。理由见文件头。 */
  color: var(--ally-text-faint);
}

.run-bars i {
  position: absolute;
  top: 0;
  width: var(--bar);
  height: var(--peak);
  background: currentColor;
  /* 只动 scaleY（默认原点为盒中心）：布局盒不变、不触发重排与重绘，走合成层
     （AGENTS.md §4.7）。height / box-shadow 那条老路会把整行每帧重排一次。 */
  transform: scaleY(var(--rest));
  animation: run-bars-wave var(--cycle) ease-in-out infinite;
}

.run-bars.is-held i {
  animation-play-state: paused;
}

/* 静息 → 峰值 → 静息的 1.04s 行程 + 0.26s 静默（百分比与原版一致）；三根条错相
   0.208s，相位跨度 0.416s ≥ 0.26s 静默窗口，所以永远没有三根同时停住的瞬间。
   判据是"错相 ≥ 周期的 10%"，不是"错相 < 静默窗口"——后者在周期 1.3s 配 0.1s
   错相时成立，却会让三根每轮一起僵住几十毫秒。 */
@keyframes run-bars-wave {
  0%, 80%, 100% { transform: scaleY(var(--rest)); }
  40% { transform: scaleY(1); }
}

/* 减弱动效：停成静态三竖条（不再有"在跑"的含义，但形状还在）。 */
@media (prefers-reduced-motion: reduce) {
  .run-bars i {
    animation: none;
  }
}
</style>
