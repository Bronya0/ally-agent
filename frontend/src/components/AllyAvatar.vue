<!--
SPDX-License-Identifier: GPL-3.0-only

Copyright (C) 2026 tangssst <tangssst@qq.com>
GitHub: https://github.com/Bronya0/ally-agent

This file is part of ally-agent, licensed under the GNU General
Public License v3. See the LICENSE file for details.
-->
<template>
  <div ref="root" class="ally-avatar" role="img" :aria-label="$t('avatar.aria')">
    <svg class="ally-eye" viewBox="0 0 164 164" aria-hidden="true">
      <!-- 欢迎页多 Tab 共享同一组渐变（inlineDefs=false）；启动页内联独立渐变
           （inlineDefs=true），避免两个 SVG 同时引用同一 paint server 触发
           WebKit 渲染缓存串扰（启动动画期间欢迎页瞳孔出现淡黄色块）。
           瞳孔 clipPath 始终本地唯一，WebKit 会在 transform 动画时丢跨根引用。 -->
      <defs v-if="inlineDefs">
        <radialGradient :id="irisId" cx="50%" cy="50%" r="55%">
          <stop offset="0" stop-color="#ffd896"/>
          <stop offset="0.32" stop-color="#d49050"/>
          <stop offset="0.7" stop-color="#6b3a18"/>
          <stop offset="1" stop-color="#1a1208"/>
        </radialGradient>
        <radialGradient :id="glowId" cx="50%" cy="50%" r="54%">
          <stop offset="0" stop-color="#e0a458" stop-opacity="0.34"/>
          <stop offset="0.58" stop-color="#e0a458" stop-opacity="0.12"/>
          <stop offset="1" stop-color="#e0a458" stop-opacity="0"/>
        </radialGradient>
        <linearGradient :id="vortexId" x1="0%" y1="0%" x2="0%" y2="100%">
          <stop offset="0" stop-color="#fff3d6"/>
          <stop offset="0.5" stop-color="#e0a458"/>
          <stop offset="1" stop-color="#fff3d6"/>
        </linearGradient>
      </defs>
      <defs>
        <clipPath :id="pupilClipId">
          <path d="M82 34 Q 71 82 82 130 Q 93 82 82 34 Z"/>
        </clipPath>
      </defs>

      <ellipse cx="82" cy="88" rx="72" ry="62" :fill="`url(#${glowId}) transparent`"/>
      <ellipse cx="82" cy="140" rx="42" ry="7" fill="#000" opacity="0.25"/>
      <path class="ally-eye-shell" d="M17 82c14-28 38-43 65-43s51 15 65 43c-14 28-38 43-65 43S31 110 17 82Z"/>
      <ellipse cx="82" cy="82" rx="47" ry="47" :fill="`url(#${irisId}) #d49050`" stroke="#f2c078" stroke-width="2.2"/>
      <path d="M40 69c22-21 62-22 84 0" fill="none" stroke="#fff3d6" stroke-width="2" opacity="0.34" stroke-linecap="round"/>

      <g class="ally-eye-pupil">
        <path d="M82 34 Q 71 82 82 130 Q 93 82 82 34 Z" fill="#05070a"/>
        <g :clip-path="`url(#${pupilClipId})`">
          <!-- Vortex: a vertical swirl made of two opposing S-curves plus
               concentric lens-shaped rings, rotating to read as a whirlpool. -->
          <g class="ally-eye-vortex">
            <path d="M82 54 Q 88 66 82 78 Q 76 90 82 102 Q 88 114 82 126"
                  fill="none" :stroke="`url(#${vortexId}) #e0a458`" stroke-width="1.6" stroke-linecap="round" opacity="0.9"/>
            <path d="M82 54 Q 76 66 82 78 Q 88 90 82 102 Q 76 114 82 126"
                  fill="none" :stroke="`url(#${vortexId}) #e0a458`" stroke-width="1.2" stroke-linecap="round" opacity="0.65"/>
            <ellipse cx="82" cy="82" rx="3.5" ry="24" fill="none" stroke="#fff3d6" stroke-width="0.9" opacity="0.55"/>
          </g>
          <g class="ally-eye-vortex ally-eye-vortex-slow">
            <ellipse cx="82" cy="82" rx="6.5" ry="36" fill="none" stroke="#e0a458" stroke-width="1.2" opacity="0.42"/>
            <ellipse cx="82" cy="82" rx="2" ry="14" fill="none" stroke="#f8fafc" stroke-width="0.8" opacity="0.5"/>
          </g>
          <!-- Lightning bolt running down the slit. -->
          <path class="ally-eye-lightning" d="M85 50 L 78 76 L 83 76 L 75 114 L 91 70 L 84 70 Z" fill="#fff3d6"/>
        </g>
      </g>

      <path d="M26 82c13 18 32 28 56 28s43-10 56-28" fill="none" stroke="#e0a458" stroke-width="2" opacity="0.32" stroke-linecap="round"/>
    </svg>

    <!-- Keep the speech bubble outside SVG. HTML layout and CSS positioning are
         not reliably applied to a div created inside the SVG namespace. -->
    <div v-if="speech" class="ally-speech" role="status" @click.stop="dismissSpeech">
      {{ speech }}
      <span class="ally-speech-tail" aria-hidden="true"></span>
    </div>
  </div>
</template>

<script setup>
import { ref, onMounted, onBeforeUnmount, useId } from 'vue';
import { isZh } from '../i18n.mjs';
import { EYE_STYLES } from '../data/eyeLines.mjs';

const props = defineProps({
  // 启动页单实例用内联渐变（唯一 ID，自带资源），与欢迎页共享渐变互不干扰；
  // 欢迎页多 Tab 保持共享（默认 inlineDefs=false），不逐实例复制渐变节点。
  inlineDefs: { type: Boolean, default: false },
});

const uid = useId().replace(/:/g, '-');
const pupilClipId = `ally-eye-pupil-clip-${uid}`;
const irisId = props.inlineDefs ? `ally-eye-iris-${uid}` : 'ally-eye-iris';
const glowId = props.inlineDefs ? `ally-eye-glow-${uid}` : 'ally-eye-glow';
const vortexId = props.inlineDefs ? `ally-vortex-grad-${uid}` : 'ally-vortex-grad';

// 所有欢迎页实例共享一份 SVG paint server(<defs>):每个 workspace tab 的欢迎消息
// 都常驻 DOM(v-show),逐实例复制渐变会重复创建节点。共享定义注入
// 到 body 顶层并放到视口外。资源宿主保留 1x1 viewport，避免 WebKit 在启动
// 动画结束后丢弃 0x0 SVG 的 paint server；各 url() 还带颜色 fallback，资源短暂
// 失效时虹膜也不会变黑。引用计数管理生命周期:最后一个实例卸载时才移除。
let sharedDefs = null;
let sharedDefsRefs = 0;

const DEFS_MARKUP =
  '<defs>' +
  '<radialGradient id="ally-eye-iris" cx="50%" cy="50%" r="55%">' +
  '<stop offset="0" stop-color="#ffd896"/>' +
  '<stop offset="0.32" stop-color="#d49050"/>' +
  '<stop offset="0.7" stop-color="#6b3a18"/>' +
  '<stop offset="1" stop-color="#1a1208"/>' +
  '</radialGradient>' +
  '<radialGradient id="ally-eye-glow" cx="50%" cy="50%" r="54%">' +
  '<stop offset="0" stop-color="#e0a458" stop-opacity="0.34"/>' +
  '<stop offset="0.58" stop-color="#e0a458" stop-opacity="0.12"/>' +
  '<stop offset="1" stop-color="#e0a458" stop-opacity="0"/>' +
  '</radialGradient>' +
  '<linearGradient id="ally-vortex-grad" x1="0%" y1="0%" x2="0%" y2="100%">' +
  '<stop offset="0" stop-color="#fff3d6"/>' +
  '<stop offset="0.5" stop-color="#e0a458"/>' +
  '<stop offset="1" stop-color="#fff3d6"/>' +
  '</linearGradient>' +
  '</defs>';

function acquireSharedDefs() {
  sharedDefsRefs++;
  if (sharedDefs && document.body.contains(sharedDefs)) return;
  sharedDefs = document.createElementNS('http://www.w3.org/2000/svg', 'svg');
  sharedDefs.setAttribute('width', '1');
  sharedDefs.setAttribute('height', '1');
  sharedDefs.setAttribute('aria-hidden', 'true');
  sharedDefs.setAttribute('focusable', 'false');
  sharedDefs.style.position = 'fixed';
  sharedDefs.style.left = '-2px';
  sharedDefs.style.top = '-2px';
  sharedDefs.style.pointerEvents = 'none';
  sharedDefs.style.overflow = 'hidden';
  sharedDefs.innerHTML = DEFS_MARKUP;
  document.body.appendChild(sharedDefs);
}

function releaseSharedDefs() {
  sharedDefsRefs--;
  if (sharedDefsRefs <= 0 && sharedDefs) {
    sharedDefs.remove();
    sharedDefs = null;
    sharedDefsRefs = 0;
  }
}

// Pause all decorative animations when the avatar is offscreen or the window
// is hidden. CSS transforms on infinite animations keep the compositor alive
// even at 0% change, so this is the main lever for idle GPU usage.
const root = ref(null);
let observer = null;
let visibilityHandler = null;
let isIntersecting = true;

// —— 主动搭话：眼睛被"看着"（视口内且窗口可见）时，每隔随机
// 每 18 秒弹一句台词气泡。纯绝对定位浮层，不占文档流，布局零影响。
const speech = ref(null); // 当前台词，null = 不显示
const active = ref(false); // 用户是否正看着本眼睛
let speechTimer = null; // 气泡展示时长定时器
let nextSpeechTimer = null; // 下一次搭话定时器

const SPEECH_SHOW_MS = 3400; // 与 .ally-speech 的 CSS 动画时长一致
const SPEECH_GAP_MS = 18000; // 固定间隔：每 18 秒一句

// 纯随机：把全部风格台词合并成一个池子，直接随机抽一句，不做权重/去重。
const ZH_SPEECH_POOL = EYE_STYLES.flatMap((style) => style.zh);
const EN_SPEECH_POOL = EYE_STYLES.flatMap((style) => style.en);

function pickSpeechLine() {
  const pool = isZh ? ZH_SPEECH_POOL : EN_SPEECH_POOL;
  if (!pool || pool.length === 0) return null;
  return pool[Math.floor(Math.random() * pool.length)];
}

function scheduleNextSpeech() {
  clearTimeout(nextSpeechTimer);
  nextSpeechTimer = null;
  if (!active.value || speech.value) return;
  const gap = SPEECH_GAP_MS;
  nextSpeechTimer = setTimeout(showSpeech, gap);
}

function showSpeech() {
  nextSpeechTimer = null;
  if (!active.value) return;
  speech.value = pickSpeechLine();
  root.value?.classList.add('ally-avatar--speaking');
  speechTimer = setTimeout(hideSpeech, SPEECH_SHOW_MS);
}

function hideSpeech() {
  speechTimer = null;
  speech.value = null;
  root.value?.classList.remove('ally-avatar--speaking');
  scheduleNextSpeech();
}

function dismissSpeech() {
  clearTimeout(speechTimer);
  hideSpeech();
}

function setPaused(paused) {
  if (!root.value) return;
  root.value.classList.toggle('ally-avatar--paused', paused);
  const nowActive = !paused;
  if (nowActive === active.value) return;
  active.value = nowActive;
  if (nowActive) {
    scheduleNextSpeech();
  } else {
    clearTimeout(nextSpeechTimer);
    clearTimeout(speechTimer);
    nextSpeechTimer = null;
    speechTimer = null;
    speech.value = null;
    root.value.classList.remove('ally-avatar--speaking');
  }
}

onMounted(() => {
  if (!props.inlineDefs) acquireSharedDefs();
  if (typeof IntersectionObserver !== 'undefined' && root.value) {
    observer = new IntersectionObserver(
      (entries) => {
        for (const entry of entries) {
          isIntersecting = entry.isIntersecting;
          setPaused(document.hidden || !isIntersecting);
        }
      },
      { threshold: 0.01 }
    );
    observer.observe(root.value);
  } else {
    // Older WebViews have no IntersectionObserver; the avatar is mounted in
    // the welcome panel, so assume it is visible and keep the feature usable.
    setPaused(document.hidden);
  }
  visibilityHandler = () => setPaused(document.hidden || !isIntersecting);
  document.addEventListener('visibilitychange', visibilityHandler);
});

onBeforeUnmount(() => {
  if (!props.inlineDefs) releaseSharedDefs();
  if (observer) observer.disconnect();
  if (visibilityHandler) document.removeEventListener('visibilitychange', visibilityHandler);
  clearTimeout(speechTimer);
  clearTimeout(nextSpeechTimer);
});
</script>

<style scoped>
/* 台词气泡：绝对定位浮层，不占文档流，任何布局都不受影响 */
.ally-speech {
  position: absolute;
  left: calc(100% - 4px);
  top: 2px;
  z-index: 10;
  width: max-content;
  max-width: 220px;
  padding: 8px 12px;
  border-radius: 10px;
  background: #27272c;
  border: 1px solid rgba(255, 255, 255, 0.09);
  box-shadow: 0 8px 24px rgba(0, 0, 0, 0.5);
  color: #ececec;
  font-size: var(--ally-sub-font-size);
  line-height: 1.55;
  cursor: pointer;
  user-select: none;
  animation: ally-speech-pop 3.4s ease forwards;
}

.ally-speech-tail {
  position: absolute;
  left: -6px;
  top: 23px;
  width: 10px;
  height: 10px;
  background: #27272c;
  border-left: 1px solid rgba(255, 255, 255, 0.09);
  border-bottom: 1px solid rgba(255, 255, 255, 0.09);
  transform: rotate(45deg);
}

@keyframes ally-speech-pop {
  0% { opacity: 0; transform: translateY(6px) scale(0.94); }
  10% { opacity: 1; transform: translateY(0) scale(1); }
  80% { opacity: 1; transform: translateY(0) scale(1); }
  100% { opacity: 0; transform: translateY(-3px) scale(0.97); }
}

/* 说话时瞳孔微微放大，说完恢复眨眼 */
.ally-avatar--speaking .ally-eye-pupil {
  animation: ally-eye-speak 3.4s ease;
}

@keyframes ally-eye-speak {
  0%, 100% { transform: scaleX(1.8); }
  20% { transform: scaleX(2.15) scaleY(1.06); }
  55% { transform: scaleX(1.9) scaleY(1.02); }
}

.ally-avatar {
  position: relative;
  width: 164px;
  height: 164px;
}

.ally-eye {
  display: block;
  width: 100%;
  height: 100%;
  user-select: none;
  pointer-events: none;
}

.ally-eye-shell {
  fill: #0b0e13;
  stroke: #343a45;
  stroke-width: 2;
}

.ally-eye-vortex {
  transform-origin: 82px 82px;
  animation: ally-eye-spin 14s linear infinite;
}

.ally-eye-vortex-slow {
  animation-duration: 22s;
  animation-direction: reverse;
}

/* Pupil blink: scaleX squish around the eye center. Vertical-slit pupils
   close horizontally (left/right lids meeting in the middle), not top/bottom.
   Open state is widened past 1.0 so the slit reads as fully open rather than
   squinting. GPU-composited transform, no layout/paint cost; ~350ms blink
   inside a 7s cycle with a double-blink pattern to avoid feeling metronomic. */
.ally-eye-pupil {
  transform-origin: 82px 82px;
  animation: ally-eye-blink 7s linear infinite;
}

.ally-eye-lightning {
  opacity: 0;
  filter: none;
  animation: ally-eye-flash 5.6s steps(1, end) infinite;
}

@keyframes ally-eye-spin {
  /* Spin one full turn in the first ~14% of the cycle, then hold. This turns
     a continuous per-frame rotation into a brief 2s burst every 14s, which is
     the main idle-GPU win — the compositor stops ticking for the other 12s. */
  0% { transform: rotate(0deg); }
  14% { transform: rotate(360deg); }
  100% { transform: rotate(360deg); }
}

@keyframes ally-eye-flash {
  0%, 76%, 100% { opacity: 0; }
  77% { opacity: 0.82; }
  78% { opacity: 0.18; }
  79% { opacity: 0.64; }
  80% { opacity: 0; }
}

@keyframes ally-eye-blink {
  0%, 68% { transform: scaleX(1.8); }
  70%, 71% { transform: scaleX(0.06); }
  73% { transform: scaleX(1.8); }
  85% { transform: scaleX(1.8); }
  87%, 88% { transform: scaleX(0.06); }
  90% { transform: scaleX(1.8); }
  100% { transform: scaleX(1.8); }
}

@media (prefers-reduced-motion: reduce) {
  .ally-eye-vortex,
  .ally-eye-lightning,
  .ally-eye-pupil,
  .ally-avatar--speaking .ally-eye-pupil {
    animation: none;
  }

  .ally-speech {
    animation: none;
  }
}

/* Pause decorative animations when the avatar is offscreen or the window is
   hidden. Saves the compositor from ticking on a static frame. */
.ally-avatar--paused .ally-eye-vortex,
.ally-avatar--paused .ally-eye-lightning,
.ally-avatar--paused .ally-eye-pupil {
  animation-play-state: paused;
}
</style>
