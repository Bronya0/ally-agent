<!--
SPDX-License-Identifier: GPL-3.0-only

Copyright (C) 2026 tangssst <tangssst@qq.com>
GitHub: https://github.com/Bronya0/ally-agent

This file is part of ally-agent, licensed under the GNU General
Public License v3. See the LICENSE file for details.
-->
<template>
  <div :class="['splash-screen', { leaving }]" @click="finish">
    <div class="splash-bg" aria-hidden="true"></div>
    <div class="splash-stage">
      <div class="splash-eye-wrap">
        <div class="splash-eye-glow"></div>
        <AllyAvatar />
      </div>
      <div class="splash-wordmark-row">
        <div class="splash-wordmark" aria-hidden="true">Ally</div>
        <div class="splash-version">{{ buildVersion }}</div>
      </div>
    </div>
    <div class="splash-hud">
      <div class="splash-line">{{ $t('splash.loading') }}</div>
    </div>
  </div>
</template>

<script setup>
import { onMounted, ref } from 'vue';
import AllyAvatar from './AllyAvatar.vue';
import { buildVersion } from '../utils/buildVersion';

const emit = defineEmits(['done']);

const leaving = ref(false);

let timer = 0;
let finished = false;

const duration = 2800;

function start() {
  const reduceMotion = window.matchMedia?.('(prefers-reduced-motion: reduce)')?.matches;
  if (reduceMotion) {
    emit('done');
    return;
  }
  timer = window.setTimeout(finish, duration + 120);
}

function finish() {
  if (finished || leaving.value) return;
  leaving.value = true;
  window.clearTimeout(timer);
  // The 420ms opacity fade is driven by the .leaving CSS transition on
  // .splash-screen; emit done after it so the shell can unmount the overlay.
  window.setTimeout(() => {
    finished = true;
    emit('done');
  }, 420);
}

onMounted(start);
</script>

<style scoped>
/* 深色氛围背景：径向渐变 + 缓慢漂移的网格 + 中央金色光晕 */
.splash-bg {
  position: absolute;
  inset: 0;
  background:
    radial-gradient(ellipse 62% 48% at 50% 47%, rgba(224, 164, 88, 0.1), rgba(224, 164, 88, 0.04) 45%, transparent 72%),
    radial-gradient(ellipse at 50% 42%, #151922 0%, #080a0f 42%, #020305 100%);
  animation: splash-bg-drift 2.8s linear infinite;
}

.splash-bg::after {
  content: '';
  position: absolute;
  inset: 0;
  background-image:
    linear-gradient(rgba(154, 163, 175, 0.07) 1px, transparent 1px),
    linear-gradient(90deg, rgba(154, 163, 175, 0.07) 1px, transparent 1px);
  background-size: clamp(28px, 5.5vh, 56px) clamp(28px, 5.5vh, 56px);
  animation: splash-grid-drift 3.2s linear infinite;
}

@keyframes splash-bg-drift {
  0% { opacity: 1; }
  50% { opacity: 0.9; }
  100% { opacity: 1; }
}

@keyframes splash-grid-drift {
  0% { background-position: 0 0; }
  100% { background-position: clamp(28px, 5.5vh, 56px) clamp(28px, 5.5vh, 56px); }
}

/* 主体舞台：居中放欢迎页同款竖瞳 + 品牌字 */
.splash-stage {
  position: absolute;
  inset: 0;
  z-index: 1;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: clamp(20px, 4.5vh, 52px);
  pointer-events: none;
  user-select: none;
}

.splash-eye-wrap {
  position: relative;
  width: clamp(180px, 30vmin, 360px);
  height: clamp(180px, 30vmin, 360px);
  animation: splash-eye-in 1.15s cubic-bezier(0.22, 1, 0.36, 1) both;
}

/* 复用欢迎页 AllyAvatar：尺寸由外层容器接管，内部 SVG 自适应 */
.splash-eye-wrap :deep(.ally-avatar) {
  width: 100%;
  height: 100%;
}

.splash-eye-glow {
  position: absolute;
  inset: -12%;
  background: radial-gradient(circle, rgba(224, 164, 88, 0.22) 0%, rgba(224, 164, 88, 0.06) 45%, transparent 70%);
  animation: splash-glow-pulse 2.8s ease-in-out infinite;
}

.splash-wordmark {
  font-size: clamp(52px, 8.5vmin, 100px);
  font-weight: 700;
  font-family: Inter, system-ui, sans-serif;
  letter-spacing: 0.5px;
  background: linear-gradient(90deg, #f8fafc 0%, #e0a458 36%, #d7dde8 68%, #f8fafc 100%);
  -webkit-background-clip: text;
  background-clip: text;
  color: transparent;
  filter: drop-shadow(0 0 18px rgba(224, 164, 88, 0.25));
  opacity: 0;
  animation: splash-wordmark-in 0.9s ease 0.5s forwards;
}

/* 字标列：Ally 上方，版本号徽章在正下方 */
.splash-wordmark-row {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 12px;
}

.splash-version {
  font-family: var(--ally-mono-font);
  font-size: 13px;
  font-weight: 600;
  letter-spacing: 0.3px;
  color: rgba(199, 210, 254, 0.92);
  background: rgba(199, 210, 254, 0.08);
  border: 1px solid rgba(199, 210, 254, 0.2);
  border-radius: 999px;
  padding: 3px 11px 4px;
  opacity: 0;
  animation: splash-version-in 0.6s ease 0.85s forwards;
}

@keyframes splash-eye-in {
  0% {
    opacity: 0;
    transform: scale(0.78) translateY(16px);
  }
  60% {
    opacity: 1;
    transform: scale(1.04) translateY(0);
  }
  100% {
    opacity: 1;
    transform: scale(1);
  }
}

@keyframes splash-glow-pulse {
  0%, 100% { opacity: 0.7; }
  50% { opacity: 1; }
}

@keyframes splash-wordmark-in {
  from {
    opacity: 0;
    transform: translateY(16px);
  }
  to {
    opacity: 1;
    transform: translateY(0);
  }
}

@keyframes splash-version-in {
  from {
    opacity: 0;
    transform: translateY(8px) scale(0.88);
  }
  to {
    opacity: 1;
    transform: translateY(0) scale(1);
  }
}

@media (prefers-reduced-motion: reduce) {
  .splash-eye-wrap,
  .splash-wordmark,
  .splash-version,
  .splash-eye-glow,
  .splash-bg,
  .splash-bg::after {
    animation: none;
    opacity: 1;
  }
}
</style>
