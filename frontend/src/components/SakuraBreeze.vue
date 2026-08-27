<!--
SPDX-License-Identifier: GPL-3.0-only

Copyright (C) 2026 tangssst <tangssst@qq.com>
GitHub: https://github.com/Bronya0/ally-agent

This file is part of ally-agent, licensed under the GNU General
Public License v3. See the LICENSE file for details.
-->
<template>
  <Teleport to="body">
    <div v-if="alive" class="sb-layer" :class="{ 'is-visible': shown, 'is-held': held }" aria-hidden="true">
      <!-- 开场光带：一次性的斜向掠过，致敬动画 OP 的运镜 -->
      <div v-if="sheen" class="sb-sheen"></div>
      <div class="sb-field">
        <span v-for="(p, i) in petals" :key="i" class="sb-petal" :class="p.cls" :style="p.vars">
          <span class="sb-petal-core"></span>
        </span>
      </div>
    </div>
  </Teleport>
</template>

<script setup>
import { ref, watch, onBeforeUnmount } from 'vue';

const props = defineProps({
  // 由父级眼球点击控制；父级同时负责在眼睛不可见时置回 false
  open: { type: Boolean, default: false },
});

/* ---- 单例协调：整个窗口只允许一个樱花层，后开者会让先开者自动收起 ---- */
let registeredClose = null;
function takeOver(closeSelf) {
  if (registeredClose && registeredClose !== closeSelf) registeredClose();
  registeredClose = closeSelf;
}
function stepDown(closeSelf) {
  if (registeredClose === closeSelf) registeredClose = null;
}

/* 动画全部是 transform/opacity 合成器属性；关闭时整层 DOM 直接卸载，
   不留任何定时器或渲染层，符合“平时零占用”的要求。 */
const REDUCED_MOTION =
  typeof window !== 'undefined' && window.matchMedia &&
  window.matchMedia('(prefers-reduced-motion: reduce)').matches;

const alive = ref(false);     // 特效层是否存在
const shown = ref(false);     // 淡入淡出类
const sheen = ref(false);     // 开场光带
const held = ref(false);      // 窗口隐藏时暂停所有花瓣
const petals = ref([]);

let teardownTimer = null;
let sheenTimer = null;
let visHandler = null;
const closeFromOutside = () => closeEffect();

/* 花瓣参数：每次开启随机重撒，位置/时长/摇摆幅度全部错开；
   负的 animation-delay 让开启瞬间就有花瓣处于半空，而不是等几秒。 */
function buildPetals() {
  const w = (typeof window !== 'undefined' && window.innerWidth) || 1280;
  const h = (typeof window !== 'undefined' && window.innerHeight) || 800;
  const n = Math.max(10, Math.min(22, Math.round((w * h) / 95000)));
  const list = [];
  for (let i = 0; i < n; i++) {
    const leaf = Math.random() < 0.16; // 约 1/6 是青草叶片
    const glow = !leaf && Math.random() < 0.25;
    list.push({
      cls: [
        leaf ? 'is-leaf' : '',
        glow ? 'is-glow' : '',
      ].filter(Boolean).join(' '),
      vars: {
        '--x': `${(Math.random() * 104 - 2).toFixed(1)}%`,
        '--size': `${(7 + Math.random() * 8).toFixed(1)}px`,
        '--dur': `${(9 + Math.random() * 8).toFixed(2)}s`,
        '--delay': `${(-Math.random() * 17).toFixed(2)}s`,
        '--swayDur': `${(2.6 + Math.random() * 2.4).toFixed(2)}s`,
        '--flipDur': `${(2.8 + Math.random() * 3.4).toFixed(2)}s`,
        '--amp': `${Math.round(14 + Math.random() * 30)}px`,
        '--windDx': `${(10 + Math.random() * 16).toFixed(1)}vw`,
        '--opMax': (0.55 + Math.random() * 0.4).toFixed(2),
      },
    });
  }
  petals.value = list;
}

function openEffect() {
  if (REDUCED_MOTION) return;
  clearTimeout(teardownTimer);
  teardownTimer = null;
  takeOver(closeFromOutside);
  buildPetals();
  alive.value = true;
  sheen.value = true;
  clearTimeout(sheenTimer);
  sheenTimer = setTimeout(() => { sheen.value = false; }, 1150);
  // 下一帧再加淡入类，保证过渡生效
  requestAnimationFrame(() => { shown.value = true; });
}

function closeEffect() {
  shown.value = false;
  sheen.value = false;
  stepDown(closeFromOutside);
  clearTimeout(teardownTimer);
  // 淡出完成后再卸载 DOM，期间 animation-play-state 已由 is-held 外的方式继续跑完
  teardownTimer = setTimeout(() => {
    alive.value = false;
    petals.value = [];
  }, 430);
}

watch(
  () => props.open,
  (v) => { if (v) openEffect(); else closeEffect(); },
);

// 窗口最小化/隐藏时暂停花瓣，省掉看不见的合成开销
watch(alive, (isAlive) => {
  if (isAlive) {
    visHandler = () => { held.value = !!document.hidden; };
    document.addEventListener('visibilitychange', visHandler);
  } else if (visHandler) {
    document.removeEventListener('visibilitychange', visHandler);
    visHandler = null;
  }
});

onBeforeUnmount(() => {
  stepDown(closeFromOutside);
  clearTimeout(teardownTimer);
  clearTimeout(sheenTimer);
  if (visHandler) document.removeEventListener('visibilitychange', visHandler);
});
</script>

<style scoped>
/* 全屏固定层：不拦截任何鼠标事件，浮在最上层；不开启时 DOM 根本不存在 */
.sb-layer {
  position: fixed;
  inset: 0;
  z-index: 20000;
  pointer-events: none;
  overflow: hidden;
  contain: layout paint style;
  opacity: 0;
  visibility: hidden;
  transition: opacity 0.38s ease, visibility 0.38s ease;
}

.sb-layer.is-visible {
  opacity: 1;
  visibility: visible;
}

/* 窗口隐藏或淡出期间暂停花瓣动画 */
.sb-layer.is-held .sb-petal,
.sb-layer.is-held .sb-petal *,
.sb-layer.is-held .sb-sheen {
  animation-play-state: paused !important;
}

.sb-field {
  position: absolute;
  inset: 0;
}

/* ---- 花瓣：两层拆分变换轴，控制合成层数（每片只占 2 个图层）----
   外层: 垂直下落 + 顺风横移 (transform/opacity)
   内核: 左右摇摆(translate 通道) + 3D 翻滚(rotate/scale 通道)，
         用独立变换属性在同一元素上叠加，省掉中间包裹层。 */
.sb-petal {
  position: absolute;
  top: -6vh;
  left: var(--x);
  width: var(--size);
  height: var(--size);
  opacity: 0;
  animation: sb-fall var(--dur, 12s) linear var(--delay, 0s) infinite;
}

.is-visible .sb-petal {
  will-change: transform, opacity;
}

@keyframes sb-fall {
  0%   { transform: translate3d(0, -4vh, 0); opacity: 0; }
  6%   { opacity: var(--opMax, 0.8); }
  86%  { opacity: var(--opMax, 0.8); }
  100% { transform: translate3d(var(--windDx, 14vw), 108vh, 0); opacity: 0; }
}

.sb-petal-core {
  display: block;
  width: 100%;
  height: 100%;
  /* 同一元素两条动画各占一个变换通道（translate 与 rotate/scale），
     互不覆盖，不再需要中间包裹元素及其合成层。 */
  animation:
    sb-sway var(--swayDur, 3.6s) ease-in-out var(--delay, 0s) infinite alternate,
    sb-flip var(--flipDur, 4s) linear var(--delay, 0s) infinite;
}

@keyframes sb-sway {
  from { translate: calc(var(--amp, 24px) * -1) 0; }
  to   { translate: var(--amp, 24px) 0; }
}

@keyframes sb-flip {
  0%   { rotate: 1 1 0.4 0deg;   scale: 1 1; }
  50%  { rotate: 1 1 0.4 180deg; scale: 0.55 1.06; }
  100% { rotate: 1 1 0.4 360deg; scale: 1 1; }
}

/* 樱花瓣：暖粉渐变 + 高光 + 契合眼瞳配色的细描边 */
.sb-petal:not(.is-leaf) .sb-petal-core {
  background:
    radial-gradient(circle at 30% 25%, rgba(255, 255, 255, 0.78), rgba(255, 255, 255, 0) 44%),
    linear-gradient(135deg, #ffe0ec 0%, #f9b7cd 46%, #ee92b3 100%);
  border-radius: 82% 18% 76% 24% / 68% 32% 72% 28%;
  box-shadow: inset 0 0 2px rgba(194, 84, 122, 0.36);
}

/* 青草叶：鲜绿渐变，呼应“青草被风吹飞” */
.sb-petal.is-leaf .sb-petal-core {
  background: linear-gradient(135deg, #d9f5ba 0%, #98d579 55%, #6db054 100%);
  border-radius: 88% 12% 84% 16% / 62% 38% 58% 42%;
  box-shadow: inset 0 0 2px rgba(66, 118, 48, 0.42);
}

/* 少数花瓣自带柔光，营造镜头景深感 */
.sb-petal.is-glow .sb-petal-core {
  box-shadow:
    inset 0 0 2px rgba(194, 84, 122, 0.3),
    0 0 6px 1px rgba(255, 193, 216, 0.55);
}

/* 开场光带：一次斜向掠过 */
.sb-sheen {
  position: absolute;
  inset: -20%;
  opacity: 0;
  background: linear-gradient(
    105deg,
    transparent 30%,
    rgba(255, 214, 228, 0.42) 46%,
    rgba(198, 245, 186, 0.26) 56%,
    transparent 72%
  );
  animation: sb-sheen 1.05s cubic-bezier(0.22, 0.61, 0.36, 1) forwards;
}

@keyframes sb-sheen {
  0%   { transform: translate3d(-70%, 0, 0); opacity: 0; }
  14%  { opacity: 0.85; }
  100% { transform: translate3d(70%, 0, 0); opacity: 0; }
}

@media (prefers-reduced-motion: reduce) {
  .sb-layer {
    display: none;
  }
}
</style>
