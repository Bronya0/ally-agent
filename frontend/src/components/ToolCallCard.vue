<!--
SPDX-License-Identifier: GPL-3.0-only

Copyright (C) 2026 tangssst <tangssst@qq.com>
GitHub: https://github.com/Bronya0/ally-agent

This file is part of ally-agent, licensed under the GNU General
Public License v3. See the LICENSE file for details.
-->
<template>
  <div :class="['rich-tool-card', msg.kind, msg.status, { focused: focused && !isFocusDisabledTool(msg), expanded: msg.expanded, 'non-interactive': isFocusDisabledTool(msg) }]" @click.stop="handleCardClick(msg)">
    <div
      :class="['tool-line', { clickable: hasExpandableBody(msg) }]"
      @click.stop="hasExpandableBody(msg) && handleToggle(msg)"
    >
      <ToolStatusIcon :status="msg.status" />
      <span class="tool-verb">{{ toolVerb(msg) }}</span>
      <span class="tool-name">{{ toolDisplayName(msg) }}</span>
      <span v-if="msg.title && msg.kind === 'command'" class="tool-command" :title="msg.title">
        <span class="tool-command-paren">(</span>
        <code v-html="highlightCommand(msg)"></code>
        <span class="tool-command-paren">)</span>
      </span>
      <span v-else-if="msg.kind === 'plan' && msg.title" class="tool-arg plan-next-step" :title="msg.title">({{ msg.title }})</span>
      <span v-else-if="msg.title" class="tool-arg" :title="msg.title">({{ msg.title }})</span>
      <span v-if="msg.kind === 'edit' && (msg.editAdded || msg.editRemoved)" class="tool-chip edit-change-chip">
        <span class="tool-chip-dot">&middot;</span>
        <span v-if="msg.editAdded" class="edit-chip-added">+{{ msg.editAdded }}</span>
        <span v-if="msg.editRemoved" class="edit-chip-removed">-{{ msg.editRemoved }}</span>
      </span>
      <span v-else-if="msg.kind === 'wait' && waitCountdown" class="tool-chip wait-countdown">{{ waitCountdown }}</span>
      <span v-else-if="msg.chip" class="tool-chip">{{ msg.chip }}</span>
      <span v-if="msg.durationText" class="tool-duration">{{ msg.durationText }}</span>
    </div>

    <div v-if="msg.kind === 'edit' && msg.status !== 'error' && msg.editEntries?.length" class="edit-file-groups">
      <div v-for="(entry, ei) in msg.editEntries" :key="entry.path || ei" class="edit-file-group">
        <div v-if="msg.editEntries.length > 1" class="edit-file-header">
          <span class="edit-file-name">{{ entry.path || $t('tools.file', { index: ei + 1 }) }}</span>
          <span v-if="entry.added" class="edit-chip-added">+{{ entry.added }}</span>
          <span v-if="entry.removed" class="edit-chip-removed">-{{ entry.removed }}</span>
        </div>
        <div class="edit-file-content" :class="{ 'no-header': msg.editEntries.length <= 1 }">
          <DiffView v-if="entry.diff" layout="split" :diff-text="entry.diff" :file-path="entry.path" :show-header="false" :collapsed="!msg.expanded" :added-count="entry.added || 0" :removed-count="entry.removed || 0" @toggle="handleToggle(msg)" />
          <DiffView v-for="(change, ci) in entry.changes" v-else layout="split" :key="ci" :old-text="change.oldText || ''" :new-text="change.newText || ''" :file-path="entry.path" :show-header="false" :collapsed="!msg.expanded" :is-incomplete="msg.status === 'running'" @toggle="handleToggle(msg)" />
        </div>
      </div>
    </div>
    <DiffView
      v-else-if="msg.kind === 'edit' && hasEditPreview(msg)"
      layout="split"
      :diff-text="editDiffText(msg)"
      :old-text="msg.editOldString"
      :new-text="msg.editNewString"
      :file-path="msg.editFilePath"
      :show-header="false"
      :collapsed="!msg.expanded"
      :added-count="msg.editAdded || 0"
      :removed-count="msg.editRemoved || 0"
      :is-incomplete="msg.status === 'running' && !editDiffText(msg)"
      @toggle="handleToggle(msg)"
    />
    <pre v-else-if="msg.kind === 'edit' && msg.editChangedLinesBlock" class="edit-changed-lines-block edit-changed-lines-preview">{{ msg.editChangedLinesBlock }}</pre>
    <div v-if="msg.editWarnings && msg.editWarnings.length" class="edit-warning-list">
      <div v-for="(warning, wi) in msg.editWarnings" :key="wi" class="edit-warning">{{ warning }}</div>
    </div>
    <pre v-if="msg.expanded && msg.editChangedLinesBlock" class="edit-changed-lines-block">{{ msg.editChangedLinesBlock }}</pre>
    <CodeView
      v-else-if="msg.kind === 'create' && msg.status !== 'error'"
      :code="msg.codeContent || ''"
      :file-path="msg.editFilePath || ''"
      :collapsed="isCreatePreview(msg)"
      :max-lines="BODY_PREVIEW_LINES"
      preview-mode="tail"
    />
    <pre v-else-if="msg.body && msg.status !== 'error' && msg.kind !== 'edit' && msg.kind !== 'read' && msg.kind !== 'calculate' && msg.kind !== 'scheduled' && msg.kind !== 'grep' && msg.kind !== 'plan' && (msg.kind !== 'list' || msg.expanded)" ref="bodyPreRef" :class="['tool-body', { 'fixed-scroll': isFixedKind(msg.kind), 'body-preview': isBodyPreview(msg), 'tail-default': isServiceReadResult(msg), 'scroll-enabled': bodyScrollEnabled && isScrollableBody(msg) }]" @click.stop="handleBodyClick(msg)">{{ toolBodyText(msg) }}</pre>
    <div v-if="isValidationWarning(msg)" class="edit-warning-list validation-warning-list" role="status" aria-live="polite">
      <div class="edit-warning validation-warning" :title="msg.validation">
        <span class="validation-warning-label">{{ $t('tools.validationWarning') }}</span>
        {{ msg.validation }}
      </div>
    </div>
    <div v-if="msg.status === 'error'" class="tool-error-block">
      <div v-if="msg.errorCode" class="tool-error-detail">
        <span>{{ errorDescription(msg) }}</span>
        <code>{{ msg.errorCode }}</code>
      </div>
      <pre v-if="errorReasonText(msg)" class="tool-error-reason">{{ errorReasonText(msg) }}</pre>
    </div>
  </div>
</template>

<script setup>
import { computed, defineAsyncComponent, nextTick, onUnmounted, ref, watch } from 'vue';
import hljs from 'highlight.js/lib/core';
import bash from 'highlight.js/lib/languages/bash';
import powershell from 'highlight.js/lib/languages/powershell';
import { highlightShellCommand } from '../utils/shellHighlight.mjs';
import { formatToolErrorBody } from '../utils/toolError.mjs';
import { toolVerbLabel, hasNamedVerb } from '../utils/toolVerb.mjs';
import { t } from '../i18n.mjs';
import ToolStatusIcon from './ToolStatusIcon.vue';

const BODY_PREVIEW_LINES = 6;
const TOOL_OUTPUT_PREVIEW_LINES = 4;

const bodyPreRef = ref(null);
const bodyScrollEnabled = ref(false);

if (!hljs.getLanguage('bash')) {
  hljs.registerLanguage('bash', bash);
}
if (!hljs.getLanguage('powershell')) {
  hljs.registerLanguage('powershell', powershell);
}

const props = defineProps({
  msg: { type: Object, required: true },
  focused: { type: Boolean, default: false },
});

const emit = defineEmits(['focus', 'toggle']);

const DiffView = defineAsyncComponent(() => import('./DiffView.vue'));
const CodeView = defineAsyncComponent(() => import('./CodeView.vue'));
const nowMs = ref(Date.now());
let waitTimer = null;

const waitCountdown = computed(() => {
  if (props.msg.kind !== 'wait' || props.msg.status !== 'running') return '';
  const seconds = Number(props.msg.waitSeconds || 0);
  const startedAt = Number(props.msg.waitStartedAt || 0);
  if (!seconds || !startedAt) return '';
  const remaining = Math.max(0, Math.ceil((startedAt + seconds * 1000 - nowMs.value) / 1000));
  return t('tools.wait.remaining', { seconds: remaining });
});

watch(
  () => props.msg.kind === 'wait' && props.msg.status === 'running',
  (active) => {
    if (waitTimer) {
      clearInterval(waitTimer);
      waitTimer = null;
    }
    if (active) {
      nowMs.value = Date.now();
      waitTimer = setInterval(() => { nowMs.value = Date.now(); }, 250);
    }
  },
  { immediate: true },
);

onUnmounted(() => {
  if (waitTimer) clearInterval(waitTimer);
});

function toolKindLabel(kind) {
  const labels = {
    edit: t('tools.kind.edit'),
    create: t('tools.kind.create'),
    delete: t('tools.kind.delete'),
    command: t('tools.kind.command'),
    calculate: t('tools.kind.calculate'),
    list: t('tools.kind.list'),
    read: t('tools.kind.read'),
    glob: t('tools.kind.glob'),
    grep: t('tools.kind.grep'),
    run: t('tools.kind.run'),
    other: t('tools.kind.tool'),
    plan: t('tools.kind.plan'),
    scheduled: t('tools.kind.scheduled'),
    memory: t('tools.kind.memory'),
    service: t('tools.kind.service'),
    wait: t('tools.kind.wait'),
    subagent: t('tools.kind.subagent'),
    mcp: 'MCP',
  };
  return labels[kind] || '';
}

function toolDisplayName(msg) {
  if (msg.kind === 'mcp') {
    if (msg.mcpServer && msg.mcpTool) return `${msg.mcpServer}/${msg.mcpTool}`;
    return msg.mcpServer || msg.mcpTool || formatToolName(msg.name) || 'MCP';
  }
  // Sub-agent cards that were not upgraded to the inline card still show the
  // dynamic role name (e.g. "code reviewer") instead of the fixed kind label.
  if (msg.kind === 'subagent' && msg.subagentRole) return msg.subagentRole;
  // When the verb already names the action (Edited/Read/Ran/Grep/...), don't
  // repeat it as the name; the target/file is shown in the (msg.title) arg span.
  if (hasNamedVerb(msg.name)) return '';
  const kindLabel = toolKindLabel(msg.kind);
  if (kindLabel && msg.kind !== 'other') return kindLabel;
  return formatToolName(msg.name) || t('tools.kind.tool');
}

function formatToolName(name) {
  const raw = String(name || '').trim();
  if (!raw || raw === 'tool') return '';
  return raw;
}
function toolVerb(msg) {
  return toolVerbLabel(msg.name, msg.kind, msg.status, msg.scheduledAction);
}

// Bounded LRU cache for highlighted command HTML. highlight.js is expensive
// (full tokenize) and ToolCallCard re-renders on every tool:update flush; without
// caching, a 200ms flush cycle on a long command would re-tokenize it ~5x/s.
// 128 entries / ~256KB cap covers typical session volume.
const HIGHLIGHT_CACHE_MAX = 128;
const highlightCache = new Map();
function highlightCommand(msg) {
  const command = String(msg?.title || '');
  const language = commandHighlightLanguage(msg, command);
  const cacheKey = `${language}::${command}`;
  const cached = highlightCache.get(cacheKey);
  if (cached !== undefined) {
    // Move-to-end for LRU semantics.
    highlightCache.delete(cacheKey);
    highlightCache.set(cacheKey, cached);
    return cached;
  }
  let result;
  if (language === 'bash') result = highlightShellCommand(command);
  else {
    try {
      result = hljs.highlight(command, { language, ignoreIllegals: true }).value;
    } catch {
      result = escapeHtml(command);
    }
  }
  highlightCache.set(cacheKey, result);
  if (highlightCache.size > HIGHLIGHT_CACHE_MAX) {
    const oldest = highlightCache.keys().next().value;
    if (oldest !== undefined) highlightCache.delete(oldest);
  }
  return result;
}

function commandHighlightLanguage(msg, command) {
  const name = String(msg?.name || '');
  if (name === 'Bash') return 'bash';
  if (name === 'command') return looksLikeExplicitBash(command) ? 'bash' : 'powershell';
  if (name === 'remote_run_command') return looksLikePowerShell(command) ? 'powershell' : 'bash';
  return looksLikePowerShell(command) ? 'powershell' : 'bash';
}

function looksLikeExplicitBash(command) {
  return /^\s*(?:bash|sh|zsh|wsl)(?:\.exe)?\b/i.test(command);
}

function looksLikePowerShell(command) {
  return /(?:\$env:|\$\w+|@\{|@'|@"|\b(?:Get|Set|New|Remove|Select|Where|ForEach|Start|Stop|Test|Join|Split|Resolve|Invoke|ConvertTo|ConvertFrom)-[A-Za-z]+\b|\b(?:gci|gc|ls|dir|cd|pwd|cat|echo|rm|cp|mv)\b\s+-[A-Za-z])/i.test(command);
}

function escapeHtml(text) {
  return String(text || '')
    .replaceAll('&', '&amp;')
    .replaceAll('<', '&lt;')
    .replaceAll('>', '&gt;')
    .replaceAll('"', '&quot;');
}

function isFixedKind(kind) {
  return ['edit', 'create', 'command'].includes(kind);
}

function normalizedLines(text) {
  const lines = String(text || '').replace(/\r\n/g, '\n').split('\n');
  if (lines.length > 1 && lines[lines.length - 1] === '') lines.pop();
  return lines;
}

// tool:update 每 ~120ms 强制重渲一次，模板里反复调用 lineCount /
// toolBodyText 会对流式增长中的 body 全量切行。这里按消息对象做 WeakMap
// 记忆化：文本与展开状态不变时直接复用上次的行数。
const lineCountMemo = new WeakMap();
function lineCount(msg, text) {
  if (!text) return 0;
  let memo = lineCountMemo.get(msg);
  if (!memo || memo.text !== text) {
    memo = { text, count: normalizedLines(text).length };
    lineCountMemo.set(msg, memo);
  }
  return memo.count;
}

function isBodyPreview(msg) {
  return msg.kind !== 'command' && !msg.expanded && lineCount(msg, msg.body) > BODY_PREVIEW_LINES;
}

function isCreatePreview(msg) {
  return !msg.expanded && lineCount(msg, msg.codeContent) > BODY_PREVIEW_LINES;
}

function isValidationWarning(msg) {
  const validation = String(msg?.validation || '');
  return /自动校验失败|validation\s+(?:failed|error)/i.test(validation);
}

// 切行结果记忆化：键为 body 文本 + 展开/滚动状态 + 工具状态。命令输出
// 流式刷新时只有 body 真正变化的那一帧才重新 split，其余渲染直接复用。
const bodyTextMemo = new WeakMap();

function toolBodyText(msg) {
  const body = String(msg.body || '');
  let memo = bodyTextMemo.get(msg);
  if (!memo || memo.body !== body || memo.expanded !== !!msg.expanded
    || memo.scrollEnabled !== bodyScrollEnabled.value || memo.status !== (msg.status || '')) {
    memo = {
      body,
      expanded: !!msg.expanded,
      scrollEnabled: bodyScrollEnabled.value,
      status: msg.status || '',
      text: '',
    };
    memo.text = computeToolBodyText(msg, body);
    bodyTextMemo.set(msg, memo);
  }
  return memo.text;
}

function computeToolBodyText(msg, body) {
  if (isScrollableBody(msg) && !bodyScrollEnabled.value) {
    // Tail preview: skip blank lines so the collapsed rows always end with real
    // output instead of the stray trailing newlines shell output commonly has
    // (an all-blank tail would otherwise render as empty rows).
    const tail = normalizedLines(body).filter((line) => line !== '');
    return tail.slice(-TOOL_OUTPUT_PREVIEW_LINES).join('\n');
  }
  if (!isBodyPreview(msg)) return body;
  const lines = normalizedLines(body);
  if (isServiceReadResult(msg)) {
    return lines.slice(Math.max(0, lines.length - BODY_PREVIEW_LINES)).join('\n');
  }
  return lines.slice(0, BODY_PREVIEW_LINES).join('\n');
}

function errorDescription(msg) {
  const code = msg.errorCode;
  if (!code) return errorReasonText(msg) || String(msg.body || '');
  const key = `tools.error.${code}`;
  const translated = t(key);
  return translated !== key ? translated : t('tools.error.unknown');
}

// 已有本地化错误描述时不重复展示原始（多为英文的）错误文本；
// 完整错误仍通过工具结果返回给模型。
function errorReasonText(msg) {
  if (msg?.errorCode) {
    const key = `tools.error.${msg.errorCode}`;
    if (t(key) !== key) return '';
  }
  return formatToolErrorBody(msg?.body);
}

function hasEditPreview(msg) {
  if (msg.status === 'error') return false;
  return Boolean(editDiffText(msg) || msg.editOldString || msg.editNewString);
}

function editDiffText(msg) {
  if (msg.editDiff) return msg.editDiff;
  if (msg.status === 'success' && msg.body) return msg.body;
  return '';
}

function isNonInteractiveTool(msg) {
  return ['create', 'edit', 'delete', 'grep', 'list', 'plan'].includes(String(msg?.kind || ''));
}

function isFocusDisabledTool(msg) {
  // All tool cards disable the click-to-focus behavior now. The focused
  // border was a visual noise source (1px layout shift on toggle) and the
  // focus state had no functional purpose — tool cards don't receive
  // keyboard shortcuts or downstream actions based on being focused.
  return true;
}

// Service read results and command output show their newest lines by default.
// Clicking the output enables manual scrolling through the full body.
function isServiceReadResult(msg) {
  if (msg?.kind !== 'service') return false;
  const body = String(msg?.body || '');
  // formatServiceReadResult emits a "---" divider and a " · "-joined metadata
  // line (id: ... · status: ... · returned: ... B). Detect via the metadata
  // marker that's unique to read results.
  return body.includes('---') && body.includes('returned:');
}

function isScrollableBody(msg) {
  return msg?.kind === 'command' || isServiceReadResult(msg);
}

function handleBodyClick(msg) {
  if (isScrollableBody(msg)) enableBodyScroll();
}

function enableBodyScroll() {
  if (bodyScrollEnabled.value) return;
  bodyScrollEnabled.value = true;
  nextTick(() => {
    const pre = bodyPreRef.value;
    if (!pre) return;
    pre.scrollTop = pre.scrollHeight;
  });
}

watch(
  () => props.msg?.eventId,
  () => {
    bodyScrollEnabled.value = false;
  },
  { immediate: true },
);

watch(
  () => [props.msg?.body, props.msg?.expanded, props.msg?.kind, props.msg?.status],
  () => {
    if (bodyScrollEnabled.value || !isScrollableBody(props.msg)) return;
    nextTick(() => {
      const pre = bodyPreRef.value;
      if (!pre) return;
      pre.scrollTop = pre.scrollHeight;
      requestAnimationFrame(() => {
        if (bodyPreRef.value) bodyPreRef.value.scrollTop = bodyPreRef.value.scrollHeight;
      });
    });
  },
  { immediate: true, flush: 'post' },
);

function handleCardClick(msg) {
  if (isFocusDisabledTool(msg)) return;
  emit('focus');
}

function handleToggle(msg) {
  emit('toggle');
}

function hasExpandableBody(msg) {
  if (msg.kind === 'read' || msg.kind === 'command' || msg.kind === 'plan') return false;
  if (msg.kind === 'edit') return true;
  if (msg.kind === 'create') return lineCount(msg, msg.codeContent) > BODY_PREVIEW_LINES;
  // calculate: body 只重复 title(expression) + chip(= result)，无需详情卡
  if (msg.kind === 'calculate') return false;
  // scheduled_task: 任务详情在 Task Center 面板查看，无需详情卡
  if (msg.kind === 'scheduled') return false;
  // grep 永远只显示单行状态和命中统计，不加载匹配行详情。
  if (msg.kind === 'grep') return false;
  return lineCount(msg, msg.body) > BODY_PREVIEW_LINES || msg.kind === 'list';
}
</script>
