<!--
SPDX-License-Identifier: GPL-3.0-only

Copyright (C) 2026 tangssst <tangssst@qq.com>
GitHub: https://github.com/Bronya0/ally-agent

This file is part of ally-agent, licensed under the GNU General
Public License v3. See the LICENSE file for details.
-->
<template>
  <div class="ally-row-card subagent-inline">
    <div class="ally-row-main subagent-heading">
      <span :class="['subagent-status-dot', `is-${state}`, { 'is-paused': !presentationActive }]" aria-hidden="true"></span>
      <span class="subagent-role">{{ msg.subagentRole || $t('tools.kind.subagent') }}</span>
      <span class="subagent-status-label" :title="msg.error || ''">{{ $t(`subagent.status.${state}`) }}</span>
      <time v-if="displayDuration" class="subagent-time">{{ displayDuration }}</time>
    </div>
    <p class="subagent-description" :title="msg.description || ''">{{ msg.description }}</p>
    <!-- Keep the control above the report, in the same place in both states. -->
    <div class="subagent-controls">
      <div class="subagent-stats" :title="tokenTooltip">
        <span v-if="stepLimit > 0" :title="$t('subagent.stepBudget')">{{ $t('subagent.budget', { current: steps, limit: stepLimit }) }}</span>
        <span v-if="msg.contextTokens > 0">{{ contextChip }}</span>
        <span v-if="cacheChip">{{ cacheChip }}</span>
        <span v-if="msg.totalTokens > 0">{{ tokenChip }}</span>
      </div>
      <button type="button" class="subagent-toggle" :disabled="!hasReport"
        :aria-label="expanded ? $t('common.collapse') : $t('common.expand')"
        :title="expanded ? $t('common.collapse') : $t('common.expand')"
        :aria-expanded="expanded" :aria-controls="detailsId" @click="toggleReport">
        {{ $t('subagent.report') }}
        <RightOutlined :class="['subagent-chevron', { 'is-expanded': expanded }]" />
      </button>
    </div>
    <div v-if="expanded && hasReport" :id="detailsId" class="subagent-report">
      <p v-if="msg.summary" class="subagent-result">{{ msg.summary }}</p>
      <p v-if="msg.error" class="subagent-error">{{ msg.error }}</p>
    </div>
  </div>
</template>

<script setup>
import { computed, ref, useId } from 'vue';
import RightOutlined from '@vicons/antd/RightOutlined';
import { normalizeToolStatus } from '../utils/toolEventState.mjs';
import { useDisplayClock, usePresentationActive } from '../composables/presentation.mjs';
import { t } from '../i18n.mjs';
import { fmtDuration } from '../utils/format.mjs';

const props = defineProps({ msg: { type: Object, required: true } });
const emit = defineEmits(['toggleReport']);
const expanded = ref(false);
function toggleReport() {
  emit('toggleReport');
  expanded.value = !expanded.value;
}
const detailsId = useId();
const state = computed(() => props.msg?.error ? 'error' : normalizeToolStatus(props.msg?.status));
const hasReport = computed(() => !!props.msg?.error || (state.value !== 'running' && !!props.msg?.summary));
const steps = computed(() => Math.max(0, Number(props.msg?.steps) || 0));
const stepLimit = computed(() => Math.max(0, Number(props.msg?.maxSteps) || 0));
const presentationActive = usePresentationActive();
const now = useDisplayClock(() => presentationActive.value && state.value === 'running' && !!Number(props.msg?.startTime));
const displayDuration = computed(() => {
  if (state.value !== 'running') return props.msg?.durationText || '';
  const startTime = Number(props.msg?.startTime || 0);
  return startTime ? fmtDuration(Math.max(0, now.value - startTime)) : '';
});

function formatTokens(n) {
  if (!n || n <= 0) return '0';
  if (n < 1000) return String(n);
  if (n < 1000000) return (n / 1000).toFixed(1) + 'k';
  return (n / 1000000).toFixed(1) + 'M';
}

const tokenChip = computed(() => {
  const total = props.msg?.totalTokens || 0;
  if (total <= 0) return '';
  return `↑${formatTokens(props.msg?.inputTokens)} ↓${formatTokens(props.msg?.outputTokens)}`;
});

// 缓存命中率沿用主对话统计行的口径（Σhit/Σ(hit+miss)）；两个桶都没报时不显示，
// 因为 0% 意味着“一点没命中”，与“没有数据”是两回事。
const cacheChip = computed(() => {
  const rate = props.msg?.cacheRate;
  if (typeof rate !== 'number') return '';
  return t('subagent.cache', { rate });
});

// Context is the latest prompt size; input/output totals accumulate across steps.
const contextChip = computed(() => {
  const context = props.msg?.contextTokens || 0;
  if (context <= 0) return '';
  return t('subagent.context', { tokens: formatTokens(context) });
});

// 提示里保留带标签的写法：符号本身不自解释，悬停时给出的仍是完整含义与精确数字。
const tokenTooltip = computed(() => {
  const input = props.msg?.inputTokens || 0;
  const output = props.msg?.outputTokens || 0;
  const total = props.msg?.totalTokens || 0;
  const context = props.msg?.contextTokens || 0;
  const parts = [`${t('subagent.context', { tokens: context })} · Input: ${input} · Output: ${output} · Total: ${total}`];
  if (typeof props.msg?.cacheRate === 'number') {
    parts.push(`Cache hit ${props.msg.cacheHit || 0} / miss ${props.msg.cacheMiss || 0}`);
  }
  return parts.join(' · ');
});
</script>

<style scoped>
/* Match MCP entity cards; the collapsed layout reserves the same three rows
   for running and completed tasks, regardless of report availability. */
.subagent-inline {
  min-width: 0;
}
.subagent-heading {
  gap: 7px;
  min-height: 22px;
}
.subagent-status-dot {
  width: 6px;
  height: 6px;
  flex-shrink: 0;
  border-radius: 50%;
  background: var(--ally-text-faint);
}
.subagent-status-dot.is-running {
  background: var(--ally-accent-text);
  animation: subagent-status-blink 1.2s ease-in-out infinite;
  will-change: opacity;
}
.subagent-status-dot.is-paused {
  animation-play-state: paused;
  will-change: auto;
}
@keyframes subagent-status-blink {
  0%, 100% { opacity: 1; }
  50% { opacity: 0.3; }
}
.subagent-status-dot.is-success {
  background: var(--ally-success-pale);
}
.subagent-status-dot.is-error {
  background: var(--ally-danger-pale);
}
.subagent-role {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: var(--ally-text-primary);
  font-size: var(--ally-sub-font-size);
  font-weight: 500;
}
.subagent-status-label {
  margin-left: auto;
  flex-shrink: 0;
  color: var(--ally-text-faint);
  font-size: var(--ally-aux-font-size);
}
.subagent-time {
  flex-shrink: 0;
  color: var(--ally-text-ghost);
  font-size: var(--ally-aux-font-size);
  font-variant-numeric: tabular-nums;
}
.subagent-description {
  margin: 0;
  height: 3.2em;
  color: var(--ally-text-soft);
  font-size: var(--ally-aux-font-size);
  line-height: 1.6;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
  overflow-wrap: anywhere;
}
.subagent-controls {
  display: flex;
  align-items: center;
  gap: 8px;
  min-height: 22px;
}
.subagent-stats {
  flex: 1;
  min-width: 0;
  display: flex;
  align-items: center;
  gap: 10px;
  overflow: hidden;
  white-space: nowrap;
  color: var(--ally-text-ghost);
  font-size: var(--ally-aux-font-size);
  font-variant-numeric: tabular-nums;
}
.subagent-stats span {
  flex-shrink: 0;
}
.subagent-toggle {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  flex-shrink: 0;
  border: 0;
  border-radius: 4px;
  padding: 2px 3px;
  background: transparent;
  color: var(--ally-text-faint);
  font: inherit;
  font-size: var(--ally-aux-font-size);
  white-space: nowrap;
  cursor: pointer;
}
.subagent-toggle:enabled:hover {
  background: var(--ally-hover-faint);
  color: var(--ally-accent-text);
}
.subagent-toggle:disabled {
  opacity: .35;
  cursor: default;
}
.subagent-toggle:focus-visible {
  outline: 2px solid var(--ally-accent-text);
  outline-offset: 2px;
}
.subagent-chevron {
  width: 10px;
  height: 10px;
  transition: transform 160ms ease-out;
}
.subagent-chevron.is-expanded {
  transform: rotate(90deg);
}
.subagent-report {
  padding-top: 8px;
  border-top: 1px solid var(--ally-border-subtle);
  min-width: 0;
}
.subagent-result, .subagent-error {
  margin: 0;
  color: var(--ally-text-secondary);
  font-size: var(--ally-aux-font-size);
  line-height: 1.65;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}
.subagent-error {
  color: var(--ally-danger);
}
.subagent-result + .subagent-error {
  margin-top: 6px;
}
@media (prefers-reduced-motion: reduce) {
  .subagent-status-dot.is-running {
    animation: none;
    will-change: auto;
  }
  .subagent-chevron {
    transition: none;
  }
}
</style>
