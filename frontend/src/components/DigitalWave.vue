<!--
SPDX-License-Identifier: GPL-3.0-only

Copyright (C) 2026 tangssst <tangssst@qq.com>
GitHub: https://github.com/Bronya0/ally-agent

This file is part of ally-agent, licensed under the GNU General
Public License v3. See the LICENSE file for details.
-->
<template>
  <!-- 单次播放的特效层：不播放时 DOM 根本不存在，播完整层卸载，
       不留监听、定时器与合成层（平时零占用）。 -->
  <Teleport to="body">
    <canvas v-if="alive" ref="canvasRef" class="dw-canvas" aria-hidden="true"></canvas>
  </Teleport>
</template>

<script setup>
import { nextTick, onBeforeUnmount, onMounted, ref } from 'vue';
import { registerDigitalWave } from '../composables/digitalWave.mjs';

/* ── 数码波浪 ──
   点左上角 logo 时，从落点向外扩散的一圈方形数码块冲击波。
   形状 = 圆形波前 + 高斯波峰；"数码味"来自三件事：画的是一格一格方块（不是光斑圆环）、
   亮度被量化成 LEVELS 档（不是连续渐变）、每格带固定的随机半径偏移（波前是毛边，
   而不被打磨成光滑圆环）。只留一圈波前、不做尾随波：观感是“啪”一下的冲击，
   而不是一层层荡漾的涟漪。 */

const CELL = 14;          // 方块网格边长（CSS px）
const BAND = 44;          // 波峰半宽（CSS px）：越小波越薄
const SWEEP_MS = 1150;    // 波前从落点跨到最远角落的时间
const TAIL_MS = 120;      // 波前离屏后的收尾余量
const LEVELS = 7;         // 亮度档数：量化本身就是数码感的来源
const SPARK_RATE = 0.07;  // 少数格子额外提亮两档，模拟 LED 闪烁
const DPR_CAP = 2;        // 4K 屏按 2 倍绘制足够清晰，再高只是白白翻倍方块数

// 主题 token 读不到时的兜底：--ally-accent / --ally-accent-bright 的默认琥珀色
const FALLBACK_ACCENT = [224, 164, 88];
const FALLBACK_BRIGHT = [230, 180, 118];

const REDUCED_MOTION =
  typeof window !== 'undefined' && window.matchMedia
    ? window.matchMedia('(prefers-reduced-motion: reduce)').matches
    : false;

const alive = ref(false);
const canvasRef = ref(null);

let ctx = null;
let grid = null;    // 当前网格（由窗口尺寸 + 落点决定，每次播放重建）
let palette = null; // LEVELS 级 rgba 字符串 + 高亮格颜色
let maxDist = 1;    // 落点到最远角落的距离（设备像素）
let lifeMs = 2200;  // 本次播放的总时长（由网格尺寸算出，见 buildGrid）
let originX = 0;
let originY = 0;
let raf = 0;
let runToken = 0;   // 连点时用它作废上一轮还没挂上动画的启动流程
let startTs = 0;
let watching = false;

function clamp255(value) {
  if (!Number.isFinite(value)) return 0;
  return Math.max(0, Math.min(255, Math.round(value)));
}

function parseColor(value, fallback) {
  const text = String(value || '').trim();
  if (!text) return fallback;
  const nums = text.match(/-?[0-9]*\.?[0-9]+(?:e-?[0-9]+)?/gi);
  if (!nums || nums.length < 3) return fallback;
  // color(srgb 0.88 0.64 0.34) 是 0~1 分量，rgb() 是 0~255，两种写法都要认
  const scale = text.startsWith('color(') ? 255 : 1;
  return [0, 1, 2].map((i) => clamp255(Number(nums[i]) * scale));
}

/* canvas 只吃具体颜色，而 token 的值可能是 color-mix() 表达式，
   直接读会拿到未解析的字符串。挂一个探针元素让浏览器把它算成具体颜色。 */
function resolveToken(token, fallback) {
  const root = document.documentElement;
  if (!getComputedStyle(root).getPropertyValue(token).trim()) return fallback;
  const probe = document.createElement('span');
  probe.setAttribute('aria-hidden', 'true');
  probe.style.cssText = 'position:fixed;left:-100px;top:-100px;width:0;height:0;pointer-events:none;';
  probe.style.color = `var(${token})`;
  root.appendChild(probe);
  const resolved = getComputedStyle(probe).color;
  probe.remove();
  return parseColor(resolved, fallback);
}

/* 由暗到亮的一整套方块颜色：亮度靠"颜色向 --ally-accent-bright 混 + 透明度"
   两条腿一起抬，所以浅色主题（accent-bright 是深琥珀）同样看得见。 */
function buildPalette() {
  const accent = resolveToken('--ally-accent', FALLBACK_ACCENT);
  const bright = resolveToken('--ally-accent-bright', FALLBACK_BRIGHT);
  const colors = [];
  for (let i = 0; i < LEVELS; i++) {
    const t = i / (LEVELS - 1);
    const mix = t * 0.92;
    const r = Math.round(accent[0] + (bright[0] - accent[0]) * mix);
    const g = Math.round(accent[1] + (bright[1] - accent[1]) * mix);
    const b = Math.round(accent[2] + (bright[2] - accent[2]) * mix);
    colors.push(`rgba(${r}, ${g}, ${b}, ${(0.16 + 0.74 * t).toFixed(3)})`);
  }
  return { colors, spark: `rgba(${bright[0]}, ${bright[1]}, ${bright[2]}, 1)` };
}

/* 每个格子的"到落点距离 / 半径抖动 / 是否高亮"只跟尺寸和落点有关，
   一次算好存进定长数组，逐帧只做加减乘：波峰带以外的格子先被便宜地跳过。 */
function buildGrid() {
  const canvas = canvasRef.value;
  if (!canvas) return false;
  const dpr = Math.min(window.devicePixelRatio || 1, DPR_CAP);
  const w = Math.max(1, Math.round(window.innerWidth * dpr));
  const h = Math.max(1, Math.round(window.innerHeight * dpr));
  if (canvas.width !== w) canvas.width = w;
  if (canvas.height !== h) canvas.height = h;
  const surface = canvas.getContext('2d');
  if (!surface) return false;
  ctx = surface;

  const cell = Math.max(6, Math.round(CELL * dpr));
  const cols = Math.ceil(w / cell) + 1;
  const rows = Math.ceil(h / cell) + 1;
  const total = cols * rows;
  const dist = new Float32Array(total);
  const jitter = new Float32Array(total);
  const spark = new Uint8Array(total);
  // 方块尺寸随亮度分档：暗的收小留缝、亮的撑满，读起来就是"一格一格亮起来"
  const sizes = [];
  for (let i = 0; i < LEVELS; i++) {
    sizes.push(Math.max(2, Math.round(cell * (0.3 + 0.5 * ((i + 1) / LEVELS)))));
  }

  // 固定种子的线性同余：抖动与高亮每次播放都长一样，闪得稳定、不会每帧抖成噪点
  let seed = 0x2545f491;
  let farthest = 1;
  for (let row = 0; row < rows; row++) {
    const dy = row * cell + cell / 2 - originY;
    for (let col = 0; col < cols; col++) {
      const i = row * cols + col;
      const dx = col * cell + cell / 2 - originX;
      const d = Math.sqrt(dx * dx + dy * dy);
      dist[i] = d;
      if (d > farthest) farthest = d;
      seed = (seed * 1664525 + 1013904223) >>> 0;
      jitter[i] = ((seed >>> 9) % 2000) / 1000 - 1;
      spark[i] = (seed >>> 21) % 1000 < SPARK_RATE * 1000 ? 1 : 0;
    }
  }

  grid = { w, h, dpr, cell, cols, rows, dist, jitter, spark, sizes };
  maxDist = farthest;
  palette = buildPalette();
  // 波前要彻底离屏，得走过“最远角落 + 3 倍波峰半宽”
  lifeMs = SWEEP_MS * ((farthest + 3 * BAND * dpr) / farthest) + TAIL_MS;
  return true;
}

/* 波前半径不封顶，一路走出视口后自然消失，不靠总时长截断
   （被截断就会是“最后半圈停在半路”的观感）。 */
function draw(t) {
  const g = grid;
  if (!g || !ctx) return;
  ctx.clearRect(0, 0, g.w, g.h);
  const radius = (maxDist / SWEEP_MS) * t; // 波前半径（设备像素）
  const half = BAND * g.dpr;
  const invHalf = 1 / half;
  const flash = Math.exp(-t / 130); // 落点起始闪光：让“从 logo 起波”看得见
  const jitterSpan = g.cell * 1.5;
  const top = LEVELS - 1;
  // 波前还没离开落点、闪光也已散掉时，本帧无事可做
  if (radius < half * 0.4 && flash <= 0.02) return;

  for (let row = 0; row < g.rows; row++) {
    const yBase = row * g.cell;
    for (let col = 0; col < g.cols; col++) {
      const i = row * g.cols + col;
      const d = g.dist[i] + g.jitter[i] * jitterSpan;
      const delta = (d - radius) * invHalf;
      let a = delta > 3 || delta < -3 ? 0 : Math.exp(-delta * delta); // 截断高斯尾巴
      if (flash > 0.02) a += flash * Math.exp(-d / (g.cell * 2.6));
      if (a < 0.12) continue;

      let level = a >= 1 ? top : (a * LEVELS) | 0;
      const sparkle = g.spark[i] === 1;
      if (sparkle) level = Math.min(top, level + 2);
      const size = g.sizes[level];
      const offset = (g.cell - size) >> 1;
      ctx.fillStyle = sparkle ? palette.spark : palette.colors[level];
      ctx.fillRect(col * g.cell + offset, yBase + offset, size, size);
    }
  }
}

function frame(now) {
  const t = now - startTs;
  if (t >= lifeMs) {
    finish();
    return;
  }
  draw(t);
  raf = requestAnimationFrame(frame);
}

function onResize() {
  // 播放中改窗口尺寸：按新尺寸重算网格，落点与已用时间不变，续播剩下的部分
  if (alive.value) buildGrid();
}

function onVisibilityChange() {
  // 单次播放没有"回来接着看"的意义，窗口一隐藏就收工
  if (document.hidden) finish();
}

function startWatchers() {
  if (watching) return;
  watching = true;
  window.addEventListener('resize', onResize);
  document.addEventListener('visibilitychange', onVisibilityChange);
}

function stopWatchers() {
  if (!watching) return;
  watching = false;
  window.removeEventListener('resize', onResize);
  document.removeEventListener('visibilitychange', onVisibilityChange);
}

function finish() {
  stopWatchers();
  if (raf) {
    cancelAnimationFrame(raf);
    raf = 0;
  }
  alive.value = false;
  ctx = null;
  grid = null;
  palette = null;
}

function start(token) {
  if (token !== runToken) return;
  if (!buildGrid()) {
    finish();
    return;
  }
  startWatchers();
  startTs = performance.now();
  raf = requestAnimationFrame(frame);
}

/* 触发点（logo、思考强度切到最高档等）经 burstDigitalWave 调到这里，
   传入的位置即波心。重复触发 = 重新起波（放弃上一轮）。 */
async function play(clientX, clientY) {
  if (REDUCED_MOTION || typeof window === 'undefined') return;
  const token = ++runToken;
  if (raf) {
    cancelAnimationFrame(raf);
    raf = 0;
  }
  const dpr = Math.min(window.devicePixelRatio || 1, DPR_CAP);
  originX = clientX * dpr;
  originY = clientY * dpr;
  alive.value = true;
  // 首帧渲染完才有 canvas 可画；已在播放时这一步同样安全（走的是同一条路）
  await nextTick();
  start(token);
}

/* 把自己注册到全局入口（composables/digitalWave.mjs）：全屏特效层只此一份，
   触发点可以在任意位置。 */
let unregister = null;
onMounted(() => {
  unregister = registerDigitalWave(play);
});

onBeforeUnmount(() => {
  runToken += 1; // 作废尚未挂上动画的启动流程
  finish();
  if (unregister) {
    unregister();
    unregister = null;
  }
});
</script>

<style scoped>
.dw-canvas {
  position: fixed;
  inset: 0;
  width: 100%;
  height: 100%;
  /* 与樱花风同一层：纯装饰全屏层，永远浮在应用 UI 之上且不吃鼠标事件 */
  z-index: 20000;
  pointer-events: none;
  contain: layout paint style;
}
</style>
