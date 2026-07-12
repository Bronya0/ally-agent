<template>
  <div :class="['rich-tool-card', 'read', msg.status, { expanded: msg.expanded, 'non-interactive': true }]">
    <div class="tool-line">
      <span :class="['tool-status-icon', msg.status]">{{ toolIcon(msg) }}</span>
      <span class="tool-verb">{{ readVerb }}</span>
      <span class="tool-name">{{ $t('tools.files', { count: msg.readEntries.length, countSuffix: msg.readEntries.length === 1 ? '' : 's' }) }}</span>
      <span v-if="msg.readTotalLines > 0" class="tool-chip">{{ readChip }}</span>
      <span v-if="msg.durationText" class="tool-duration">{{ msg.durationText }}</span>
      <button v-if="hasEntries" class="tool-expand-btn" @click.stop="$emit('toggle')">{{ msg.expanded ? '▾' : '▸' }}</button>
    </div>
    <div v-if="hasEntries && msg.expanded" class="read-group-body">
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

const props = defineProps({
  msg: { type: Object, required: true },
  focused: { type: Boolean, default: false },
});

defineEmits(['focus', 'toggle']);

const hasEntries = computed(() => Array.isArray(props.msg.readEntries) && props.msg.readEntries.length > 0);

const readVerb = computed(() => {
  if (props.msg.status === 'error' || props.msg.status === 'failed') return t('tools.status.failed');
  return props.msg.readTotalLines > 0
    ? (props.msg.status === 'running' ? t('tools.reading') : t('tools.readDone'))
    : t('tools.reading');
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
