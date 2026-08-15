<!--
SPDX-License-Identifier: GPL-3.0-only

Copyright (C) 2026 tangssst <tangssst@qq.com>
GitHub: https://github.com/Bronya0/ally-agent

This file is part of ally-agent, licensed under the GNU General
Public License v3. See the LICENSE file for details.
-->
<template>
  <div :class="['rich-tool-card', 'subagent-inline', msg.status]">
    <div class="tool-line">
      <span :class="['tool-status-icon', msg.status]">{{ statusIcon(msg.status) }}</span>
      <span v-if="msg.subagentRole" class="tool-name">{{ msg.subagentRole }}</span>
      <span v-else-if="rolePending" class="tool-name subagent-name-pending" aria-hidden="true">
        <span class="subagent-name-dot"></span>
        <span class="subagent-name-dot"></span>
        <span class="subagent-name-dot"></span>
      </span>
      <span v-else class="tool-name">{{ fallbackLabel }}</span>
      <span class="tool-arg" :title="msg.description">({{ msg.description }})</span>
      <span class="tool-chip">{{ $t('subagent.steps', { current: msg.steps }) }}</span>
      <span v-if="msg.toolCalls?.length" class="tool-chip">{{ $t('subagent.toolCount', { count: msg.toolCalls.length }) }}</span>
      <span v-if="msg.totalTokens > 0" class="tool-chip subagent-token-chip" :title="tokenTooltip">{{ tokenChip }}</span>
      <span v-if="displayDuration" class="tool-duration">{{ displayDuration }}</span>
    </div>
    <div v-if="recentTools.length" class="subagent-inline-body">
      <div v-for="(tc, ti) in recentTools" :key="tc.toolCallId || ti" :class="['subagent-inline-entry', tc.status]">
        <span class="subagent-inline-tree">{{ ti === recentTools.length - 1 ? '└─' : '├─' }}</span>
        <span :class="['subagent-inline-icon', tc.status]">{{ statusIcon(tc.status) }}</span>
        <span class="subagent-inline-name">{{ subToolVerb(tc) }}</span>
        <span v-if="toolArgsTitle(tc)" class="subagent-inline-args" :title="toolArgsTitle(tc)">({{ toolArgsTitle(tc) }})</span>
        <span v-if="tc.summary" class="subagent-inline-summary">{{ compactSummary(tc.summary) }}</span>
        <span v-if="tc.durationText" class="subagent-inline-duration">{{ tc.durationText }}</span>
      </div>
    </div>
    <div v-if="msg.summary && msg.status !== 'running'" class="subagent-inline-result">{{ compactSubagentSummary(msg.summary) }}</div>
    <div v-if="msg.error" class="subagent-inline-error">{{ msg.error }}</div>
  </div>
</template>

<script setup>
import { computed, onBeforeUnmount, ref, watch } from 'vue';
import { t } from '../i18n.mjs';
import { formatHttpToolTitle } from '../utils/toolPreview.mjs';
import { toolVerbLabel } from '../utils/toolVerb.mjs';

const props = defineProps({
  msg: { type: Object, required: true },
});

// The card label is the sub-agent's dynamic role (e.g. "code reviewer").
// While the tool-call arguments are still streaming (before sub:spawn), the
// role hasn't been parsed yet — show a muted dot placeholder instead of the
// generic kind label, so the name doesn't flash 子代理 → role when the args
// finish. Legacy/restored cards without a role fall back to the generic label.
const fallbackLabel = computed(() => t('tools.kind.subagent'));
const rolePending = computed(() =>
  !props.msg?.subagentRole && props.msg?.status === 'running' && !props.msg?.subagentId
);

const now = ref(Date.now());
let durationTimer = null;

function formatDurationShort(ms) {
  const value = Number(ms);
  if (!Number.isFinite(value) || value <= 0) return '';
  if (value < 1000) return '<1s';
  const secs = Math.max(1, Math.round(value / 1000));
  const hours = Math.floor(secs / 3600);
  const mins = Math.floor((secs % 3600) / 60);
  const rest = secs % 60;
  if (hours > 0) return `${hours}h${mins > 0 ? `${mins}m` : ''}`;
  if (mins > 0) return `${mins}m${rest > 0 ? `${rest}s` : ''}`;
  return `${rest}s`;
}

function stopDurationTimer() {
  if (durationTimer === null) return;
  window.clearInterval(durationTimer);
  durationTimer = null;
}

function syncDurationTimer() {
  if (props.msg?.status !== 'running' || !Number(props.msg?.startTime)) {
    stopDurationTimer();
    return;
  }
  now.value = Date.now();
  if (durationTimer === null) {
    durationTimer = window.setInterval(() => {
      now.value = Date.now();
    }, 1000);
  }
}

watch(
  () => [props.msg?.status, props.msg?.startTime],
  syncDurationTimer,
  { immediate: true },
);

onBeforeUnmount(stopDurationTimer);

const displayDuration = computed(() => {
  if (props.msg?.status !== 'running') return props.msg?.durationText || '';
  const startTime = Number(props.msg?.startTime || 0);
  if (!startTime) return '';
  return formatDurationShort(Math.max(0, now.value - startTime));
});

const recentTools = computed(() => {
  const tools = Array.isArray(props.msg?.toolCalls) ? props.msg.toolCalls : [];
  return tools.slice(Math.max(0, tools.length - 8));
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

const tokenTooltip = computed(() => {
  const input = props.msg?.inputTokens || 0;
  const output = props.msg?.outputTokens || 0;
  const total = props.msg?.totalTokens || 0;
  return `Input: ${input} · Output: ${output} · Total: ${total}`;
});

function statusIcon(status) {
  if (status === 'running') return '●';
  if (status === 'success' || status === 'completed') return '✓';
  return '✗';
}

function compactSummary(text) {
  const s = String(text || '').replace(/\s+/g, ' ').trim();
  if (s.length <= 120) return s;
  return s.slice(0, 117) + '...';
}

function compactSubagentSummary(summary) {
  const text = String(summary || '').replace(/\s+/g, ' ').trim();
  if (text.length <= 180) return text;
  return text.slice(0, 177) + '...';
}

function parseToolArgs(raw) {
  const text = String(raw || '');
  if (!text.trim()) return {};
  try {
    const parsed = JSON.parse(text);
    return parsed && typeof parsed === 'object' ? parsed : {};
  } catch (_) {
    return {};
  }
}

// Cache parsed args + derived title on the toolCall object itself, so that
// repeated renders during sub-agent progress updates don't re-run JSON.parse
// on the same args string. Sub-agent progress events emit ~1/s with the same
// args payload; without this cache each tool row would parse once per render.
function toolArgsTitle(tc) {
  const name = tc.name || '';
  const argsText = String(tc.args || '');
  // Re-parse only when args actually changed (new tool call, or streaming
  // completion of a long arg payload like run_command).
  if (tc._argsCacheKey !== argsText) {
    tc._argsCacheKey = argsText;
    tc._argsCacheParsed = parseToolArgs(argsText);
  }
  const parsed = tc._argsCacheParsed;

  if (name === 'run_command' || name === 'remote_run_command') {
    const cmd = parsed.command || parsed.cmd || '';
    if (name === 'remote_run_command' && parsed.target) return `${parsed.target} · ${cmd}`;
    return cmd;
  }
  if (name === 'background_process') {
    if (parsed.action === 'stop') return `stop · ${parsed.id || ''}`;
    const parts = ['start'];
    if (parsed.name) parts.push(parsed.name);
    if (parsed.command) parts.push(parsed.command);
    return parts.join(' · ');
  }
  if (name === 'wait') {
    return parsed.reason || (parsed.seconds ? `${parsed.seconds}s` : '');
  }
  if (name === 'ask') {
    const questions = Array.isArray(parsed.questions) ? parsed.questions : [];
    if (questions.length === 1) return questions[0]?.question || '';
    return questions.length ? t('app.ask.questions', { count: questions.length }) : '';
  }
  if (name === 'edit' || name === 'remote_edit') {
    if (Array.isArray(parsed.files)) return parsed.files.length === 1 ? (parsed.files[0]?.path || '') : `${parsed.files.length} files`;
    return parsed.target ? `${parsed.target} · ${parsed.path || ''}` : (parsed.path || '');
  }
  if (name === 'create_file' || name === 'delete_path' || name === 'remote_create_file' || name === 'remote_delete_path') {
    return parsed.target ? `${parsed.target} · ${parsed.path || ''}` : (parsed.path || '');
  }
  if (name === 'read' || name === 'batch_read' || name === 'read_file' || name === 'remote_read_file') {
    if (parsed.target) return `${parsed.target} · ${parsed.path || ''}`;
    if (parsed.path) return parsed.path;
    const paths = Array.isArray(parsed.paths) ? parsed.paths : [];
    if (paths.length > 0) return paths.join(', ');
    const files = Array.isArray(parsed.files) ? parsed.files.map(f => f && f.path).filter(Boolean) : [];
    if (files.length > 0) return files.join(', ');
  }
  if (name === 'grep_files') {
    return parsed.pattern || '';
  }
  if (name === 'list_files' || name === 'remote_list_files') {
    if (parsed.target) return `${parsed.target}${parsed.path ? ' · ' + parsed.path : ''}`;
    return parsed.path || parsed.pattern || '';
  }
  if (name === 'http_request' || name === 'web_fetch') {
    return formatHttpToolTitle(parsed);
  }
  if (name === 'memory_read' || name === 'memory_write') {
    return parsed.path || parsed.description || '';
  }
  if (name === 'calculate') {
    return parsed.expression || '';
  }
  if (name === 'scheduled_task') {
    if (parsed.action === 'create') return `create · ${parsed.name || ''}`;
    if (parsed.action === 'delete') return `delete · ${parsed.id || ''}`;
    return parsed.action || 'list';
  }
  if (name === 'render_html') {
    return parsed.title || '';
  }
  if (name === 'skill' || name === 'Skill') {
    return parsed.skill || '';
  }
  if (name === 'todo_write') {
    const todos = Array.isArray(parsed.todos) ? parsed.todos : [];
    return todos.length ? `${todos.length} items` : '';
  }
  return '';
}

// Inner sub-agent rows mirror the main tool card: the row's status icon conveys
// state and the tense verb names the action ("Read" / "Edited" / "Ran" / ...),
// via the shared verb table. MCP tools (name `mcp__server__tool`) fall back to
// the mcp kind.
function subToolVerb(tc) {
  const name = tc?.name || '';
  const kind = typeof name === 'string' && name.startsWith('mcp__') ? 'mcp' : '';
  // scheduled_task's verb depends on its action; reuse the args cache primed by
  // toolArgsTitle (falling back to a parse) instead of re-parsing every render.
  let action;
  if (name === 'scheduled_task') {
    const argsText = String(tc?.args || '');
    if (tc._argsCacheKey !== argsText) {
      tc._argsCacheKey = argsText;
      tc._argsCacheParsed = parseToolArgs(argsText);
    }
    action = tc._argsCacheParsed?.action;
  }
  return toolVerbLabel(name, kind, tc?.status, action);
}
</script>

<style scoped>
.subagent-name-pending {
  display: inline-flex;
  align-items: center;
  gap: 3px;
}

.subagent-name-dot {
  width: 3px;
  height: 3px;
  border-radius: 50%;
  background: #8a8a82;
  animation: thinking-bounce 1.3s ease-in-out infinite;
}

.subagent-name-dot:nth-child(2) {
  animation-delay: 0.2s;
}

.subagent-name-dot:nth-child(3) {
  animation-delay: 0.4s;
}
</style>
