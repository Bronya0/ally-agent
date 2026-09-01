<!--
SPDX-License-Identifier: GPL-3.0-only

Copyright (C) 2026 tangssst <tangssst@qq.com>
GitHub: https://github.com/Bronya0/ally-agent

This file is part of ally-agent, licensed under the GNU General
Public License v3. See the LICENSE file for details.
-->
<template>
  <div :class="['rich-tool-card', 'read', msg.status, { expanded: msg.expanded, 'non-interactive': true }]">
    <div class="tool-line">
      <ToolStatusIcon :status="msg.status" />
      <span class="tool-verb">{{ readVerb }}</span>
      <!-- 单文件：直接显示文件名和 chip 在一行；多文件：显示 "N files" -->
      <template v-if="isSingleFile">
        <span class="tool-arg" :title="singleEntry.title">{{ singleEntry.title || $t('common.untitled') }}</span>
        <span v-if="singleEntryChip" class="tool-chip">{{ singleEntryChip }}</span>
      </template>
      <template v-else>
        <span class="tool-count">{{ fileCountLabel }}</span>
        <span v-if="msg.readTotalLines > 0" class="tool-chip">{{ readChip }}</span>
      </template>
    </div>
    <div v-if="hasEntries && !isSingleFile" class="read-group-body">
      <div
        v-for="(entry, index) in msg.readEntries"
        :key="`${entry.title || 'read'}-${index}`"
        :class="['read-group-entry', entry.status || msg.status]"
      >
        <span class="read-group-tree">{{ treePrefix(index) }}</span>
        <span class="read-group-path" :title="entry.title">{{ entry.title || $t('common.untitled') }}</span>
        <span v-if="childChip(entry)" class="read-group-chip">{{ childChip(entry) }}</span>
      </div>
    </div>
  </div>
</template>

<script setup>
import { computed } from 'vue';
import { t } from '../i18n.mjs';
import { toolVerbLabel } from '../utils/toolVerb.mjs';

const props = defineProps({
  msg: { type: Object, required: true },
  focused: { type: Boolean, default: false },
});

defineEmits(['focus', 'toggle']);

const hasEntries = computed(() => Array.isArray(props.msg.readEntries) && props.msg.readEntries.length > 0);
const isSingleFile = computed(() => Array.isArray(props.msg.readEntries) && props.msg.readEntries.length === 1);
const singleEntry = computed(() => (isSingleFile.value ? props.msg.readEntries[0] || {} : {}));
const singleEntryChip = computed(() => (isSingleFile.value ? entryChip(singleEntry.value) : ''));

const readVerb = computed(() => toolVerbLabel('read_file', 'read', props.msg.status));

// Secondary count, e.g. "1 file" / "3 files" — kept in English to match the verb.
const fileCountLabel = computed(() => {
  const n = props.msg.readEntries.length;
  return `${n} file${n === 1 ? '' : 's'}`;
});

// 外层总行数固定用英文 "lines"，不走 i18n（与 formatReadRangeChip 一致）。
const readChip = computed(() => {
  const lines = props.msg.readTotalLines || 0;
  return `· ${lines} lines`;
});

function treePrefix(index) {
  return index === props.msg.readEntries.length - 1 ? '└─' : '├─';
}

// 树里子 read 的 chip：只显示读取的行数（如 · 5 lines），不带范围统计；
// 失败条目保留错误信息。
function childChip(entry) {
  if (entry.chip && String(entry.chip).startsWith('failed:')) return entry.chip;
  const lineCount = Number(entry.lineCount) || 0;
  if (lineCount <= 0) return '';
  const parts = [`${lineCount} lines`];
  if (entry.truncated) parts.push(`(${t('common.truncated')})`);
  return `· ${parts.join(' · ')}`;
}

function entryChip(entry) {
  if (entry.chip) return entry.chip;
  const startLine = Number(entry.startLine) || 1;
  const endLine = Number(entry.endLine) || 0;
  const totalLines = Number(entry.totalLines) || Number(entry.lineCount) || 0;
  const lineCount = Number(entry.lineCount) || 0;
  const parts = [];
  if (lineCount > 0) {
    if ((startLine > 1 || endLine < totalLines) && endLine >= startLine) {
      parts.push(t('tools.lineRange', { count: lineCount, countSuffix: lineCount === 1 ? '' : 's', start: startLine, end: endLine }));
      if (entry.truncated) parts.push(`(${t('common.truncated')})`);
    } else {
      parts.push(t('tools.lineCount', { count: lineCount, countSuffix: lineCount === 1 ? '' : 's' }));
    }
  }
  return parts.length ? `· ${parts.join(' · ')}` : '';
}

</script>
