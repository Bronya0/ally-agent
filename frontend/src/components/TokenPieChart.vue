<!--
SPDX-License-Identifier: GPL-3.0-only

Copyright (C) 2026 tangssst <tangssst@qq.com>
GitHub: https://github.com/Bronya0/ally-agent

This file is part of ally-agent, licensed under the GNU General
Public License v3. See the LICENSE file for details.
-->
<template>
  <div class="pie-wrap">
    <svg :viewBox="`0 0 ${size} ${size}`" class="pie-svg" role="img">
      <circle :cx="center" :cy="center" :r="radius" class="pie-track" />
      <circle
        v-for="(seg, i) in segments"
        :key="i"
        :cx="center"
        :cy="center"
        :r="radius"
        class="pie-seg"
        :stroke="seg.color"
        :stroke-dasharray="`${seg.dash} ${circumference}`"
        :stroke-dashoffset="seg.offset"
        :transform="`rotate(-90 ${center} ${center})`"
      />
      <text
        v-if="segments.length"
        x="100"
        y="96"
        text-anchor="middle"
        class="pie-center-value"
      >{{ fmtTokens(totalTokens) }}</text>
      <text x="100" y="114" text-anchor="middle" class="pie-center-label">{{ t('stats.totalTokensShort') }}</text>
    </svg>
    <div v-if="segments.length" class="pie-legend">
      <div v-for="(seg, i) in segments" :key="i" class="pie-legend-row">
        <i class="pie-dot" :style="{ background: seg.color }"></i>
        <span class="pie-name" :title="seg.fullName || seg.name">{{ seg.name }}</span>
        <span class="pie-pct">{{ seg.percent }}%</span>
        <span class="pie-tokens">{{ fmtTokens(seg.total) }}</span>
      </div>
    </div>
    <div v-else class="pie-empty">{{ t('stats.empty') }}</div>
  </div>
</template>

<script setup>
import { computed } from 'vue';
import { t } from '../i18n.mjs';
import { fmtTokens } from '../utils/format.mjs';

const props = defineProps({
  items: { type: Array, default: () => [] },
});

const size = 200;
const center = 100;
const radius = 80;
const circumference = 2 * Math.PI * radius;

const PALETTE = [
  '#4ade80', '#6db3f2', '#f2c94c', '#f2994a', '#eb5757',
  '#9b51e0', '#2d9cdb', '#27ae60', '#f7b731', '#a55eea',
];

const segments = computed(() => {
  const items = Array.isArray(props.items) ? props.items : [];
  const total = items.reduce((sum, it) => sum + (it.inputTokens || 0) + (it.outputTokens || 0), 0);
  if (!total) return [];
  let acc = 0;
  return items.map((it, i) => {
    const value = (it.inputTokens || 0) + (it.outputTokens || 0);
    const dash = (value / total) * circumference;
    const seg = {
      name: it.name || '?',
      fullName: it.fullName || '',
      total: value,
      dash,
      offset: -acc,
      percent: ((value / total) * 100).toFixed(1),
      color: PALETTE[i % PALETTE.length],
    };
    acc += dash;
    return seg;
  });
});

const totalTokens = computed(() =>
  segments.value.reduce((sum, seg) => sum + seg.total, 0),
);
</script>

<style scoped>
.pie-wrap {
  display: flex;
  gap: 16px;
  align-items: flex-start;
}
.pie-svg {
  width: 150px;
  height: 150px;
  flex-shrink: 0;
}
.pie-track {
  fill: none;
  stroke: var(--ally-hover-strong);
  stroke-width: 18;
}
.pie-seg {
  fill: none;
  stroke-width: 18;
}
.pie-center-value {
  fill: #f1f1f1;
  font-size: 18px;
  font-weight: 650;
}
.pie-center-label {
  fill: var(--ally-text-muted);
  font-size: 10px;
}
.pie-legend {
  flex: 1;
  min-width: 0;
}
.pie-legend-row {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 3px 0;
  font-size: 12px;
}
.pie-dot {
  width: 9px;
  height: 9px;
  border-radius: 2px;
  flex-shrink: 0;
}
.pie-name {
  flex: 1;
  min-width: 0;
  color: var(--ally-text-body);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.pie-pct {
  color: var(--ally-text-muted);
  font-variant-numeric: tabular-nums;
}
.pie-tokens {
  color: var(--ally-text-high);
  font-family: ui-monospace, SFMono-Regular, Consolas, monospace;
  font-variant-numeric: tabular-nums;
}
.pie-empty {
  color: var(--ally-text-faint);
  font-size: 12px;
  padding: 30px 0;
}
</style>
