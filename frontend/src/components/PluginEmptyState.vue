<!--
SPDX-License-Identifier: GPL-3.0-only

Copyright (C) 2026 tangssst <tangssst@qq.com>
GitHub: https://github.com/Bronya0/ally-agent

This file is part of ally-agent, licensed under the GNU General
Public License v3. See the LICENSE file for details.
-->
<template>
  <!-- 插件页空态：装第一个插件之前的横幅。只回答"这是什么"，并给出开发教程入口；
       具体能力、写法与流程都在开发教程（docs/plugin-system.md）里，不在这里摊开。 -->
  <div class="pe-root">
    <section class="pe-hero">
      <div class="pe-hero-copy">
        <span class="pe-badge">
          <ThunderboltOutlined />
          {{ t('plugins.emptyBadge') }}
        </span>
        <h2 class="pe-title">{{ t('plugins.emptyTitle') }}</h2>
        <p class="pe-lead">{{ t('plugins.emptyLead') }}</p>
        <div class="pe-actions">
          <n-button size="small" secondary :title="t('plugins.guideHint')" @click="emit('guide')">
            <template #icon><ReadOutlined /></template>
            {{ t('plugins.guide') }}
          </n-button>
        </div>
      </div>

      <!-- 装饰层：绕着核心转的是 host 暴露出去的能力。纯 transform/opacity 动画，
           窗口不可见时整棵挂起（同 SakuraBreeze 的口径）。 -->
      <div class="pe-orbit" :class="{ 'is-paused': paused }" aria-hidden="true">
        <div class="pe-orbit-ring"></div>
        <div class="pe-orbit-sweep"></div>
        <div class="pe-orbit-glow"></div>
        <div class="pe-orbit-core"><AppstoreOutlined /></div>
        <div v-for="(chip, index) in ORBIT_CHIPS" :key="chip" class="pe-chip" :style="chipPos(index)">
          <span class="pe-chip-inner" :style="{ animationDelay: `-${index * 0.9}s` }">
            <i class="pe-chip-dot"></i>{{ chip }}
          </span>
        </div>
      </div>
    </section>
  </div>
</template>

<script setup>
import { onBeforeUnmount, onMounted, ref } from 'vue';
import { AppstoreOutlined, ReadOutlined, ThunderboltOutlined } from '@vicons/antd';
import { t } from '../i18n.mjs';

// 开发教程的打开动作归面板所有（它持有文档 URL）；横幅只声明"用户点了教程"。
const emit = defineEmits(['guide']);

// 轨道上的 6 个 token：就是 host 挂在插件页面上的那几样，名字与契约一致（不是缩写）。
const ORBIT_CHIPS = ['host.http', 'host.store', 'host.files', 'host.ui.notify', 'host.events', 'host.ui.theme'];
// 轨道半径（容器半边的百分比）。挑 40% 是为了让最长的 token 也压不出 hero 的右内边距。
const CHIP_RADIUS = 40;

// 角度算一次就固定：位置写进 left/top，动画只碰 transform，不参与布局。
function chipPos(index) {
  const angle = (index / ORBIT_CHIPS.length) * Math.PI * 2 - Math.PI / 2;
  return {
    left: `${50 + CHIP_RADIUS * Math.cos(angle)}%`,
    top: `${50 + CHIP_RADIUS * Math.sin(angle)}%`,
  };
}

// 无限动画在窗口不可见时要挂起（见 AGENTS.md 前端渲染压力一节）：这里只切一个类，
// 动画本身仍由 CSS 驱动，不进 JS 帧循环。
const paused = ref(false);
function syncVisibility() {
  paused.value = document.visibilityState === 'hidden';
}
onMounted(() => {
  syncVisibility();
  document.addEventListener('visibilitychange', syncVisibility);
});
onBeforeUnmount(() => {
  document.removeEventListener('visibilitychange', syncVisibility);
});
</script>

<style scoped>
.pe-root {
  max-width: 1040px;
  margin: 0 auto;
}

/* ── 横幅 ─────────────────────────────────────────────── */
.pe-hero {
  position: relative;
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: center;
  gap: 26px;
  padding: 28px 32px;
  border: 1px solid var(--ally-border-subtle);
  border-radius: 16px;
  background:
    radial-gradient(120% 130% at 0% -20%, color-mix(in srgb, var(--ally-accent) 11%, transparent) 0%, transparent 52%),
    linear-gradient(180deg, color-mix(in srgb, var(--ally-surface-raised) 60%, transparent) 0%, transparent 100%),
    var(--ally-surface-panel);
  box-shadow:
    0 1px 0 color-mix(in srgb, var(--ally-text-primary) 4%, transparent),
    0 14px 32px -22px color-mix(in srgb, var(--ally-accent) 40%, transparent);
}

/* 网格底纹只在右上角透出来：整片铺满会跟后面的内容抢注意力。 */
.pe-hero::before {
  content: '';
  position: absolute;
  inset: 0;
  border-radius: inherit;
  background-image:
    linear-gradient(var(--ally-border-subtle) 1px, transparent 1px),
    linear-gradient(90deg, var(--ally-border-subtle) 1px, transparent 1px);
  background-size: 26px 26px;
  -webkit-mask-image: radial-gradient(75% 75% at 82% 22%, #000 0%, transparent 72%);
  mask-image: radial-gradient(75% 75% at 82% 22%, #000 0%, transparent 72%);
  pointer-events: none;
}

.pe-hero-copy {
  position: relative;
  z-index: 1;
  flex: 1 1 330px;
  min-width: 0;
}

.pe-badge {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  /* 徽标是一个整体：窄容器里文字不能在 "系统" 与 "v1" 之间断行。 */
  white-space: nowrap;
  padding: 3px 10px 3px 8px;
  border: 1px solid color-mix(in srgb, var(--ally-accent) 38%, transparent);
  border-radius: 999px;
  background: color-mix(in srgb, var(--ally-accent) 12%, transparent);
  color: var(--ally-accent-strong);
  font-size: 11px;
  letter-spacing: 0.6px;
}

.pe-title {
  margin: 13px 0 8px;
  color: var(--ally-text-primary);
  font-size: 25px;
  font-weight: 700;
  line-height: 1.3;
}

.pe-lead {
  margin: 0;
  max-width: 48ch;
  color: var(--ally-text-soft);
  font-size: var(--ally-sub-font-size);
  line-height: 1.75;
}

.pe-actions {
  margin-top: 18px;
}

/* ── 轨道 ─────────────────────────────────────────────── */
.pe-orbit {
  position: relative;
  z-index: 1;
  flex: 0 0 auto;
  width: 300px;
  height: 300px;
}

/* 虚线圆环靠旋转才看得出在动（对称图形转了等于没转）。 */
.pe-orbit-ring {
  position: absolute;
  inset: 8px;
  border: 1px dashed var(--ally-border-strong);
  border-radius: 50%;
  opacity: 0.55;
  animation: pe-spin 52s linear infinite;
}

.pe-orbit-sweep {
  position: absolute;
  inset: 30px;
  border-radius: 50%;
  background: conic-gradient(
    from 0deg,
    transparent 0deg,
    color-mix(in srgb, var(--ally-accent) 52%, transparent) 46deg,
    transparent 118deg
  );
  -webkit-mask: radial-gradient(closest-side, transparent 93%, #000 94%);
  mask: radial-gradient(closest-side, transparent 93%, #000 94%);
  animation: pe-spin 11s linear infinite;
}

.pe-orbit-glow {
  position: absolute;
  left: 50%;
  top: 50%;
  width: 150px;
  height: 150px;
  margin: -75px 0 0 -75px;
  border-radius: 50%;
  background: radial-gradient(circle, color-mix(in srgb, var(--ally-accent) 30%, transparent) 0%, transparent 68%);
  animation: pe-breathe 5.4s ease-in-out infinite;
}

.pe-orbit-core {
  position: absolute;
  left: 50%;
  top: 50%;
  display: flex;
  align-items: center;
  justify-content: center;
  width: 78px;
  height: 78px;
  margin: -39px 0 0 -39px;
  border-radius: 22px;
  background: linear-gradient(150deg, var(--ally-accent-bright) 0%, var(--ally-accent) 62%, var(--ally-accent-muted) 100%);
  box-shadow:
    0 0 0 1px color-mix(in srgb, var(--ally-accent) 40%, transparent),
    0 12px 32px color-mix(in srgb, var(--ally-accent) 22%, transparent);
  color: var(--ally-accent-ink);
  font-size: 32px;
  animation: pe-core 6.4s ease-in-out infinite;
}

/* 定位用 left/top + translate(-50%,-50%)，浮动动画挂在里层，两者不抢 transform。 */
.pe-chip {
  position: absolute;
  transform: translate(-50%, -50%);
  white-space: nowrap;
}

.pe-chip-inner {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 3px 9px;
  border: 1px solid var(--ally-border);
  border-radius: 999px;
  background: var(--ally-surface-chrome);
  color: var(--ally-text-soft);
  font-family: var(--ally-mono-font), monospace;
  font-size: 11px;
  line-height: 1.5;
  animation: pe-float 5.6s ease-in-out infinite;
}

.pe-chip-dot {
  width: 5px;
  height: 5px;
  border-radius: 50%;
  background: var(--ally-accent);
  box-shadow: 0 0 6px color-mix(in srgb, var(--ally-accent) 70%, transparent);
}

@keyframes pe-spin {
  to { transform: rotate(360deg); }
}

@keyframes pe-float {
  0%, 100% { transform: translateY(-5px); }
  50% { transform: translateY(5px); }
}

@keyframes pe-breathe {
  0%, 100% { transform: scale(0.9); opacity: 0.6; }
  50% { transform: scale(1.06); opacity: 1; }
}

@keyframes pe-core {
  0%, 100% { transform: translateY(-3px); }
  50% { transform: translateY(3px); }
}

.pe-orbit.is-paused,
.pe-orbit.is-paused * {
  animation-play-state: paused;
}

@media (prefers-reduced-motion: reduce) {
  .pe-orbit * {
    animation: none !important;
  }
}

@media (max-width: 820px) {
  .pe-orbit {
    width: 240px;
    height: 240px;
  }
  .pe-chip-inner {
    padding: 2px 7px;
    font-size: 10px;
  }
}
</style>
