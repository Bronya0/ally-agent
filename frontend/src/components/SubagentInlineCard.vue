<template>
  <div :class="['rich-tool-card', 'subagent-inline', msg.status]">
    <div class="tool-line">
      <span :class="['tool-status-icon', msg.status]">{{ statusIcon(msg.status) }}</span>
      <span class="tool-verb">{{ subagentVerb(msg.status) }}</span>
      <span class="tool-name">{{ $t('tools.kind.subagent') }}</span>
      <span class="tool-arg" :title="msg.description">({{ msg.description }})</span>
      <span class="tool-chip">{{ $t('subagent.steps', { current: msg.steps, max: msg.maxSteps }) }}</span>
      <span v-if="msg.durationText" class="tool-duration">{{ msg.durationText }}</span>
    </div>
    <div v-if="recentTools.length" class="subagent-inline-body">
      <div v-for="(tc, ti) in recentTools" :key="tc.toolCallId || ti" :class="['subagent-inline-entry', tc.status]">
        <span class="subagent-inline-tree">{{ ti === recentTools.length - 1 ? '└─' : '├─' }}</span>
        <span class="subagent-inline-icon">{{ statusIcon(tc.status) }}</span>
        <span class="subagent-inline-name">{{ subToolLabel(tc.name) }}</span>
        <span class="subagent-inline-summary">{{ tc.summary || subToolPendingText(tc.status) }}</span>
        <span v-if="tc.durationText" class="subagent-inline-duration">{{ tc.durationText }}</span>
      </div>
    </div>
    <div v-if="msg.summary && msg.status !== 'running'" class="subagent-inline-result">{{ compactSubagentSummary(msg.summary) }}</div>
    <div v-if="msg.error" class="subagent-inline-error">{{ msg.error }}</div>
  </div>
</template>

<script setup>
import { computed } from 'vue';
import { t } from '../i18n.mjs';

const props = defineProps({
  msg: { type: Object, required: true },
});

const recentTools = computed(() => {
  const tools = Array.isArray(props.msg?.toolCalls) ? props.msg.toolCalls : [];
  return tools.slice(Math.max(0, tools.length - 5));
});

function statusIcon(status) {
  if (status === 'running') return '●';
  if (status === 'success' || status === 'completed') return '✓';
  return '✗';
}

function subagentVerb(status) {
  if (status === 'running') return t('subagent.running');
  if (status === 'completed') return t('subagent.completed');
  if (status === 'timed_out') return t('subagent.stopped');
  return t('subagent.failed');
}

function subToolPendingText(status) {
  if (status === 'running') return t('subagent.running').toLowerCase();
  if (status === 'error') return t('subagent.failed').toLowerCase();
  return '';
}

function compactSubagentSummary(summary) {
  const text = String(summary || '').replace(/\s+/g, ' ').trim();
  if (text.length <= 180) return text;
  return text.slice(0, 177) + '...';
}

function subToolLabel(name) {
  const labels = {
    read_file: t('tools.kind.read'),
    remote_read_file: 'remote_read_file',
    batch_read: t('tools.kind.read'),
    edit: t('tools.kind.edit'),
    remote_edit: 'remote_edit',
    create_file: t('tools.kind.create'),
    remote_create_file: 'remote_create_file',
    delete_path: t('tools.kind.delete'),
    remote_delete_path: 'remote_delete_path',
    run_command: t('tools.kind.command'),
    remote_run_command: 'remote_run_command',
    grep_files: t('tools.kind.grep'),
    list_files: t('tools.kind.read'),
    remote_list_files: 'remote_list_files',
  };
  return labels[name] || name || t('tools.kind.tool');
}
</script>
