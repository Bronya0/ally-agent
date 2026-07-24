<template>
  <div :class="['rich-tool-card', 'read', msg.status, { expanded: msg.expanded, 'non-interactive': true }]">
    <div class="tool-line">
      <span :class="['tool-status-icon', msg.status]">{{ toolIcon(msg) }}</span>
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
      <span v-if="msg.durationText" class="tool-duration">{{ msg.durationText }}</span>
    </div>
    <div v-if="hasEntries && !isSingleFile" class="read-group-body">
      <div
        v-for="(entry, index) in msg.readEntries"
        :key="`${entry.title || 'read'}-${index}`"
        :class="['read-group-entry', entry.status || msg.status]"
      >
        <span class="read-group-tree">{{ treePrefix(index) }}</span>
        <span class="read-group-path" :title="entry.title">{{ entry.title || $t('common.untitled') }}</span>
        <span v-if="entryChip(entry)" class="read-group-chip">{{ entryChip(entry) }}</span>
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

const readChip = computed(() => {
  const lines = props.msg.readTotalLines || 0;
  const tokenPart = props.msg.readTotalTokens ? ` · ~${formatTokenCount(props.msg.readTotalTokens)}t` : '';
  return t('tools.lines', { count: lines, countSuffix: lines === 1 ? '' : 's', tokens: tokenPart });
});

function treePrefix(index) {
  return index === props.msg.readEntries.length - 1 ? '└─' : '├─';
}

function entryChip(entry) {
  if (entry.chip) return entry.chip;
  const startLine = Number(entry.startLine) || 1;
  const endLine = Number(entry.endLine) || 0;
  const totalLines = Number(entry.totalLines) || Number(entry.lineCount) || 0;
  const lineCount = Number(entry.lineCount) || 0;
  const tokens = Number(entry.tokenCount || 0);
  const parts = [];
  if (lineCount > 0) {
    if (startLine > 1 || endLine < totalLines) {
      parts.push(t('tools.lineRange', { start: startLine, end: endLine }));
      if (totalLines > 0) parts.push(t('tools.lineRangeTotal', { total: totalLines }));
      if (entry.truncated) parts.push(`(${t('common.truncated')})`);
    } else {
      parts.push(t('tools.lineCount', { count: lineCount, countSuffix: lineCount === 1 ? '' : 's' }));
    }
  }
  if (tokens > 0) parts.push(`~${formatTokenCount(tokens)}t`);
  return parts.length ? `· ${parts.join(' · ')}` : '';
}

function toolIcon(msg) {
  if (msg.status === 'running') return '●';
  if (msg.status === 'success' || msg.status === 'completed') return '✓';
  if (msg.status === 'error' || msg.status === 'failed') return '✗';
  return '○';
}

function formatTokenCount(tokens) {
  const n = Number(tokens || 0);
  if (n >= 1000000) return (n / 1000000).toFixed(1) + 'M';
  if (n >= 1000) return (n / 1000).toFixed(1) + 'K';
  return String(n);
}
</script>
