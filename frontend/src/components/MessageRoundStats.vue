<!--
SPDX-License-Identifier: GPL-3.0-only

Copyright (C) 2026 tangssst <tangssst@qq.com>
GitHub: https://github.com/Bronya0/ally-agent

This file is part of ally-agent, licensed under the GNU General
Public License v3. See the LICENSE file for details.
-->
<template>
  <div :class="['message-duration', { 'is-placeholder': isPlaceholder }]">
    <span class="duration-text">{{ owner.roundDurationText || '\u00a0' }}<template v-if="owner.completedAtText">{{ '\u00a0' + owner.completedAtText }}</template></span>
    <span
      v-if="typeof owner.cacheRate === 'number'"
      class="cache-rate"
      :title="`cache hit ${owner.cacheHit} / miss ${owner.cacheMiss} (this run)`"
    >cache {{ owner.cacheRate }}%</span>
    <span
      v-if="owner.runInputTokens > 0 || owner.runOutputTokens > 0"
      class="run-tokens"
      :title="`input ${owner.runInputTokens} / output ${owner.runOutputTokens} tokens (this run)`"
    >↑{{ fmtTokens(owner.runInputTokens) }} ↓{{ fmtTokens(owner.runOutputTokens) }}</span>
    <span
      v-if="tokenSpeed"
      class="run-tokens"
      :title="`${fmtTokens(owner.runOutputTokens)} output tokens / ${(owner.roundDurationMs / 1000).toFixed(1)}s (whole run)`"
    >{{ tokenSpeed }} token/s</span>
    <n-dropdown
      trigger="click"
      placement="top-end"
      :options="exportOptions"
      @select="(key) => $emit('export', key)"
    >
      <button class="export-icon-btn" :title="$t('chat.export.title')" :aria-label="$t('chat.export.title')" @click.stop>
        <ExportOutlined />
      </button>
    </n-dropdown>
    <button
      v-if="isLastAnswer"
      class="export-icon-btn"
      :title="$t('chat.copySummary.title')"
      :aria-label="$t('chat.copySummary.title')"
      @click.stop="$emit('copySummary')"
    >
      <CopyOutlined />
    </button>
    <n-dropdown
      trigger="click"
      placement="top-end"
      :options="quickMessageOptions"
      @select="(key) => $emit('quickMessage', key)"
    >
      <button class="export-icon-btn" :title="$t('chat.quickMessage.title')" :aria-label="$t('chat.quickMessage.title')" @click.stop>
        <MessageOutlined />
      </button>
    </n-dropdown>
  </div>
</template>

<script setup>
import { computed } from 'vue';
import ExportOutlined from '@vicons/antd/ExportOutlined';
import MessageOutlined from '@vicons/antd/MessageOutlined';
import CopyOutlined from '@vicons/antd/CopyOutlined';

// 本轮统计行（时长 / cache / tokens / 导出按钮）。数据由 run:done 一次性下发，
// 落在本轮最后一条 assistant 消息上（App.vue 的 assistantMessageForRun），
// owner 就是那条消息——按钮与导出也作用在它身上。渲染位置由 ChatMessages 决定：
// 画在本轮收尾消息之后，那未必是 owner 自己（模型可以在同一条回复里先写正文、
// 再调工具），这样统计行不会被夹在正文与工具卡中间。
const props = defineProps({
  owner: { type: Object, required: true },
  isLastAnswer: { type: Boolean, default: false },
  exportOptions: { type: Array, default: () => [] },
  quickMessageOptions: { type: Array, default: () => [] },
});
defineEmits(['export', 'quickMessage', 'copySummary']);

// 流式期间这条行是占位：只占高度、内容不可见（内容是一个不换行空格），
// run:done 把时长填进来之后同一位置变可见。可见与否由鼠标悬停控制，
// 见 ChatMessages 的 `.turn-stats` 规则。
const isPlaceholder = computed(() => !props.owner.roundDurationText);

function fmtTokens(n) {
  const v = Number(n || 0);
  if (v >= 1e9) return (v / 1e9).toFixed(2) + 'B';
  if (v >= 1e6) return (v / 1e6).toFixed(2) + 'M';
  if (v >= 1e3) return (v / 1e3).toFixed(1) + 'k';
  return String(v);
}

// Aggregate output speed over the whole run (all LLM steps + tool time),
// matching how roundDurationMs / runOutputTokens are accumulated on the
// backend. Returns '' while either value is missing.
const tokenSpeed = computed(() => {
  const ms = Number(props.owner?.roundDurationMs || 0);
  const out = Number(props.owner?.runOutputTokens || 0);
  if (ms <= 0 || out <= 0) return '';
  return String(Math.round(out / (ms / 1000)));
});
</script>

<style scoped>
.message-duration {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-size: var(--ally-sub-font-size, 13px);
  color: var(--ally-text-faint);
  margin-top: 4px;
  /* 统计行默认隐藏但保留占位（visibility 不参与布局收缩，无跳动）。
     visibility 一起进 transition：显示时立即可见、淡入；隐藏时等 opacity
     落到 0 才真正 hidden，浮出与淡出双向都有过渡，不突兀 */
  visibility: hidden;
  opacity: 0;
  transition: opacity 0.2s ease, visibility 0.2s;
}

/* 流式期间的占位行：只占高度，内容不可见 */
.message-duration.is-placeholder {
  visibility: hidden;
}

.duration-text {
  font-variant-numeric: tabular-nums;
}

.run-tokens {
  font-variant-numeric: tabular-nums;
}

.cache-rate {
  font-variant-numeric: tabular-nums;
  padding: 0 5px;
  border-radius: 3px;
  font-size: var(--ally-aux-font-size, 12px);
  line-height: 16px;
}

.export-icon-btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 18px;
  height: 18px;
  padding: 0;
  border: none;
  border-radius: 4px;
  background: transparent;
  color: var(--ally-text-faint);
  cursor: pointer;
  transition: color 0.12s, background 0.12s;
  --wails-draggable: no-drag;
}

.export-icon-btn:hover {
  color: var(--ally-text-high);
  background: var(--ally-hover-strong);
}

.export-icon-btn svg {
  display: block;
  width: 14px;
  height: 14px;
}
</style>
