<!--
SPDX-License-Identifier: GPL-3.0-only

Copyright (C) 2026 tangssst <tangssst@qq.com>
GitHub: https://github.com/Bronya0/ally-agent

This file is part of ally-agent, licensed under the GNU General
Public License v3. See the LICENSE file for details.
-->
<template>
  <div :class="['rich-tool-card', 'read-grep-group', msg.status, { expanded: isExpanded, 'non-interactive': true }]">
    <div class="tool-line read-grep-toggle" @click="toggleExpanded">
      <!-- 状态图标全程占位（运行中闪烁圆点 → 完成对勾），避免完成时
           图标突然出现把整行往右顶 -->
      <ToolStatusIcon :status="msg.status" />
      <!-- 组内还有 running 调用时统计文字带 shimmer 流光；全部完成/出错恢复常色 -->
      <span v-if="isRunning" class="read-grep-stats read-grep-stats-thinking">{{ statsLabel }}</span>
      <span v-else class="read-grep-stats">{{ statsLabel }}</span>
      <span v-if="msg.readTotalLines > 0 && msg.readCount > 0" class="tool-chip">{{ readChip }}</span>
      <span v-if="hitsChip" class="tool-chip">{{ hitsChip }}</span>
      <!-- 折叠指示紧跟文字（不靠最右）；展开时旋转 90° -->
      <span class="read-grep-caret" :title="isExpanded ? $t('common.collapse') : $t('common.expand')">&gt;</span>
    </div>
    <div v-if="isExpanded" class="read-grep-body">
      <template v-if="msg.readCount > 0">
        <div
          v-for="(entry, index) in msg.readEntries"
          :key="`read-${entry.title || index}`"
          :class="['read-group-entry', entry.status || msg.status]"
        >
          <span class="read-group-tree">{{ treePrefix(index, msg.readEntries.length) }}</span>
          <span class="tool-verb read-grep-entry-verb">Read</span>
          <span class="read-group-path" :title="entry.title">{{ entry.title || $t('common.untitled') }}</span>
          <span v-if="childChip(entry)" class="read-group-chip">{{ childChip(entry) }}</span>
        </div>
      </template>
      <div
        v-for="(item, index) in msg.grepItems"
        :key="`grep-${item.title || index}`"
        :class="['read-group-entry', 'grep-entry', item.status || msg.status]"
      >
        <span class="read-group-tree">{{ treePrefix(msg.readCount > 0 ? index + msg.readEntries.length : index, totalEntryCount) }}</span>
        <span class="tool-verb read-grep-entry-verb">Grep</span>
        <span class="read-group-path" :title="item.title">{{ item.title || $t('common.untitled') }}</span>
        <span v-if="item.chip" class="read-group-chip">{{ item.chip }}</span>
      </div>
    </div>
  </div>
</template>

<script setup>
import { computed, inject, ref } from 'vue';
import { t } from '../i18n.mjs';
import ToolStatusIcon from './ToolStatusIcon.vue';

const props = defineProps({
  msg: { type: Object, required: true },
  focused: { type: Boolean, default: false },
});

defineEmits(['focus', 'toggle']);

// 展开状态存组件级 Map（由 ChatMessages 注入），按组首条 eventId 索引。
// 组对象在流式累加时每次重建，状态放 Map 里不丢失。
const groupExpanded = inject('readGrepGroupExpanded', null);
const localExpanded = ref(false);

const isExpanded = computed(() => {
  if (groupExpanded) {
    const key = groupKey(props.msg);
    if (key) return groupExpanded.get(key) === true;
    return false;
  }
  return localExpanded.value;
});

function groupKey(msg) {
  return String(msg?.eventId || '');
}

function toggleExpanded() {
  if (groupExpanded) {
    const key = groupKey(props.msg);
    if (!key) return;
    groupExpanded.set(key, !(groupExpanded.get(key) === true));
    return;
  }
  localExpanded.value = !localExpanded.value;
}

const isRunning = computed(() => props.msg.status === 'running');

// 统计行："已读取 N 次，搜索 M 次"（只读/只搜时省略另一半，逗号分隔）
const statsLabel = computed(() => {
  const parts = [];
  if (props.msg.readCount > 0) {
    parts.push(t('tools.readGrep.readCount', { count: props.msg.readCount }));
  }
  if (props.msg.grepCount > 0) {
    parts.push(t('tools.readGrep.grepCount', { count: props.msg.grepCount }));
  }
  if (!parts.length) parts.push(t('tools.readGrep.readCount', { count: 1 }));
  return parts.join(t('common.commaSep'));
});

// grep 命中数追加在统计行尾（固定英文 "hits"，与 "lines" 一致不翻译）
const hitsChip = computed(() => {
  const hits = Number(props.msg.grepTotalHits) || 0;
  return hits > 0 ? `· ${hits} hits` : '';
});

const readChip = computed(() => {
  const lines = props.msg.readTotalLines || 0;
  return `· ${lines} lines`;
});

const totalEntryCount = computed(() => (props.msg.readEntries?.length || 0) + (props.msg.grepItems?.length || 0));

function treePrefix(index, total) {
  return index === total - 1 ? '└─' : '├─';
}

// 子 read 的 chip：只显示行数；失败条目保留错误信息（与 ReadGroupCard 一致）
function childChip(entry) {
  if (entry.chip && String(entry.chip).startsWith('failed:')) return entry.chip;
  const lineCount = Number(entry.lineCount) || 0;
  if (lineCount <= 0) return '';
  const parts = [`${lineCount} lines`];
  if (entry.truncated) parts.push(`(${t('common.truncated')})`);
  return `· ${parts.join(' · ')}`;
}
</script>
