<template>
  <div :class="['rich-tool-card', 'subagent-inline', msg.status]">
    <div class="tool-line">
      <span :class="['tool-status-icon', msg.status]">{{ statusIcon(msg.status) }}</span>
      <span class="tool-verb">{{ subagentVerb(msg.status) }}</span>
      <span class="tool-name">Sub-agent</span>
      <span class="tool-arg" :title="msg.description">({{ msg.description }})</span>
      <span class="tool-chip">{{ msg.steps }}/{{ msg.maxSteps }} steps</span>
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
  if (status === 'running') return 'Running';
  if (status === 'completed') return 'Completed';
  if (status === 'timed_out') return 'Stopped';
  return 'Failed';
}

function subToolPendingText(status) {
  if (status === 'running') return 'running';
  if (status === 'error') return 'failed';
  return '';
}

function compactSubagentSummary(summary) {
  const text = String(summary || '').replace(/\s+/g, ' ').trim();
  if (text.length <= 180) return text;
  return text.slice(0, 177) + '...';
}

function subToolLabel(name) {
  const labels = {
    read_file: 'Read',
    remote_read_file: 'remote_read_file',
    batch_read: 'Read',
    edit: 'Edit',
    remote_edit: 'remote_edit',
    create_file: 'Create',
    remote_create_file: 'remote_create_file',
    delete_path: 'Delete',
    remote_delete_path: 'remote_delete_path',
    run_command: 'Command',
    remote_run_command: 'remote_run_command',
    grep_files: 'Grep',
    list_files: 'List',
    remote_list_files: 'remote_list_files',
  };
  return labels[name] || name || 'Tool';
}
</script>
