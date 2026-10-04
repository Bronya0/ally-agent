<!--
SPDX-License-Identifier: GPL-3.0-only

Copyright (C) 2026 tangssst <tangssst@qq.com>
GitHub: https://github.com/Bronya0/ally-agent

This file is part of ally-agent, licensed under the GNU General
Public License v3. See the LICENSE file for details.
-->
<template>
  <div :class="['rich-tool-card', msg.kind, msg.status, { expanded: msg.expanded }]" @click.stop>
    <div
      :class="['tool-line', { clickable: hasExpandableBody(msg) }]"
      @click.stop="hasExpandableBody(msg) && handleToggle(msg)"
    >
      <ToolStatusIcon :status="statusMark" />
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
      <span v-if="displayDuration" class="tool-duration">{{ displayDuration }}</span>
    </div>

    <div v-if="deleteRowsShown" class="read-group-body">
      <div
        v-for="(row, index) in deleteRows"
        :key="`${row.path}-${index}`"
        :class="['read-group-entry', row.ok ? 'success' : 'error']"
      >
        <span class="read-group-tree">{{ treePrefix(index) }}</span>
        <span class="read-group-path" :title="row.path">{{ row.path }}</span>
        <span v-if="row.absent" class="read-group-chip" :title="t('tools.delete.absent')">{{ t('tools.delete.absent') }}</span>
        <span v-else-if="!row.ok && (row.error || row.errorCode)" class="read-group-chip read-group-chip-error" :title="row.error || row.errorCode">{{ deleteRowError(row) }}</span>
      </div>
    </div>

    <CardGrid
      v-if="cardGrid"
      :items="cardGrid.items"
      :caption="cardGrid.caption"
      :empty-text="cardGrid.emptyText"
    />

    <div v-if="msg.kind === 'edit' && msg.status !== 'error' && msg.editEntries?.length" :class="{ 'tool-body-swap': !isBodyLive }" class="edit-file-groups">
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
      :class="{ 'tool-body-swap': !isBodyLive }"
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
      :class="{ 'tool-body-swap': !isBodyLive }"
      :code="msg.codeContent || ''"
      :file-path="msg.editFilePath || ''"
      :collapsed="isCreatePreview(msg)"
      :max-lines="BODY_PREVIEW_LINES"
      preview-mode="tail"
      :live="isBodyLive"
      @overflow="setBodyOverflow(msg, $event)"
    />
    <TerminalOutputView
      v-else-if="msg.kind === 'command' && msg.status !== 'error'"
      :class="{ 'tool-body-swap': !isBodyLive }"
      :text="msg.body || ''"
      :collapsed="!msg.expanded"
      :max-lines="COMMAND_PREVIEW_LINES"
      @overflow="setBodyOverflow(msg, $event)"
    />
    <!-- 截图：正体就是截到的图（点击在原尺寸与预览高度间切换）。
         dataUrl 是运行时字段（sanitizeStoredMessage 落盘时剥离），列表动作
         没有图，回落到通用 body 渲染窗口列表。 -->
    <div
      v-else-if="msg.kind === 'screenshot' && msg.screenshotDataUrl && msg.status !== 'error'"
      :class="['screenshot-preview', { expanded: screenshotExpanded }]"
      @click.stop="screenshotExpanded = !screenshotExpanded"
    >
      <img :src="msg.screenshotDataUrl" alt="screenshot" loading="lazy" />
    </div>
    <pre v-else-if="msg.body && !cardGrid && msg.status !== 'error' && msg.kind !== 'edit' && msg.kind !== 'read' && msg.kind !== 'remote_read' && msg.kind !== 'calculate' && msg.kind !== 'grep' && msg.kind !== 'plan' && (msg.kind !== 'list' || msg.expanded)" ref="bodyPreRef" :class="['tool-body', { 'fixed-scroll': isFixedBodyKind(msg.kind), 'body-preview': isBodyPreview(msg), 'tail-default': isServiceReadResult(msg), 'scroll-enabled': bodyScrollEnabled && isScrollableBody(msg), 'tool-body-swap': !isBodyLive }]" @click.stop="handleBodyClick(msg)">{{ toolBodyText(msg) }}</pre>
    <div v-if="isValidationWarning(msg)" class="edit-warning-list validation-warning-list" role="status" aria-live="polite">
      <div class="edit-warning validation-warning" :title="msg.validation">
        <span class="validation-warning-label">{{ $t('tools.validationWarning') }}</span>
        {{ msg.validation }}
      </div>
    </div>
    <div v-if="msg.status === 'error'" class="tool-error-block" role="alert">
      <div v-if="msg.errorCode" class="tool-error-detail">
        <span>{{ errorDescription(msg) }}</span>
        <code>{{ msg.errorCode }}</code>
      </div>
      <pre v-if="errorReasonText(msg)" class="tool-error-reason">{{ errorReasonText(msg) }}</pre>
    </div>

    <!-- 沙箱拦下的写入：工具结果是“命令执行成功”，绿色 √ 会把“没写成”说成“成了”，
         所以这行必须固定显示。位置固定在正体之后：输出照实呈现（内核那句拒绝、原样的
         退出码都在正体里），告警收尾，不与正文抢头一行。 -->
    <div v-if="msg.sandboxDenied" class="tool-sandbox-denied" role="alert">
      {{ $t('app.tools.sandboxDenied') }}
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
import { isFixedBodyKind, kindLabelKey, toolShowsDuration } from '../utils/toolKind.mjs';
import { t } from '../i18n.mjs';
import { countLinesCapped, normalizedLines, tailLines } from '../utils/toolPreview.mjs';
import CardGrid from './CardGrid.vue';
import ToolStatusIcon from './ToolStatusIcon.vue';

const BODY_PREVIEW_LINES = 6;
const TOOL_OUTPUT_PREVIEW_LINES = 4;
const COMMAND_PREVIEW_LINES = 4;

// 折叠视图实测是否真的裁掉了内容。行数判定（hasExpandableBody）会把 minified
// JSON、单行超长日志判成“只有 1 行，无需展开”，而折叠高度上限按渲染行数卡，
// 于是内容被裁掉又点不开。两处口径必须同源：裁剪与否由被裁的那个盒子实测。
// 只增不减：展开后上限撤掉、不再溢出，若跟着回落就会失去 clickable 而收不回。
const bodyOverflow = ref(false);
// 截图预览默认限高，点击切原尺寸；单卡状态，随消息对象重建无需持久化。
const screenshotExpanded = ref(false);

function setBodyOverflow(msg, value) {
  if (value) bodyOverflow.value = true;
}

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
});

const emit = defineEmits(['toggle']);

// 被沙箱拦下的写入按拒绝显示状态标记：工具调用在后端口径里是成功的（内核拒绝是命令
// 结果、不是工具错误），但这次写没有发生。正体照旧渲染命令输出——拒绝原因与可写根
// 都在里面，标记只负责让用户一眼看到“这次写没成”。
const statusMark = computed(() => (props.msg?.sandboxDenied ? 'error' : props.msg?.status));

// 一次删多条时逐行列出每一条（成/败），行样式复用读卡的折叠行：一条路径一行，
// 失败行沿用同款红字标记 + 错误文案。行数据来自结果适配器（见 useToolEvents），
// 所以行的判定与标题摘要同源（utils/toolPreview 的 deletePathRows）。
const deleteRows = computed(() => {
  if (props.msg?.kind !== 'delete' || !Array.isArray(props.msg.deleteEntries)) return [];
  return props.msg.deleteEntries;
});

// 单条且成功时标题已经写了那条路径，再铺一行是重复；多路径（标题只剩 "N paths"）、
// 唯一那条失败（标题看不出失败）、或唯一那条本来就不存在（标题的 "Deleted" 里没有
// 任何东西被删）时必须铺开，否则卡片上看不到真实结果。
const deleteRowsShown = computed(() => deleteRows.value.length > 1
  || (deleteRows.value.length === 1 && (!deleteRows.value[0].ok || deleteRows.value[0].absent)));

function treePrefix(index) {
  return index === deleteRows.value.length - 1 ? '└─' : '├─';
}

// 逐行失败原因：错误码有本地化句子就用它（与卡片告警行、错误块标签同一句，见 i18n
// 的 tools.error.<CODE>），没译文才退回结果里的原始文本。原始串可能多行、且带错误
// 码前缀与整段可写根提示（远端删除不给错误码，最容易撞上），所以行内只留第一行，
// 完整文本照旧挂在 title 上，行本身再由 .read-group-chip-error 截断。
function deleteRowError(row) {
  const label = errorCodeLabel(row?.errorCode);
  if (label) return label;
  const raw = String(row?.error || '').trim();
  return raw.split(/[\r\n]/, 1)[0].trim();
}

// 一批同类记录的工具结果（ssh_cluster 服务器清单 / service 进程清单 / scheduled_task
// 任务清单）由结果适配器解析并映射成卡片条目，整包写进 msg.cardGrid（见 useToolEvents
// 的 setCardGrid）：条目、标题行、空态文案一起走，卡片正体因此可以整块清空。没有这个
// 字段的是旧卡片或其它工具，继续走原来的 body。
const cardGrid = computed(() => {
  const grid = props.msg?.cardGrid;
  return grid && Array.isArray(grid.items) ? grid : null;
});

const DiffView = defineAsyncComponent(() => import('./DiffView.vue'));
const CodeView = defineAsyncComponent(() => import('./CodeView.vue'));
const TerminalOutputView = defineAsyncComponent(() => import('./TerminalOutputView.vue'));
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

// 耗时显示：瞬时工具（read/grep/glob/list/delete/calculate/plan/skill）不展示——
// 结果自身已含规模信息，<1s 的耗时纯噪音；ask 计的是人不是工具；create/edit/service
// 与 http_request/web_fetch（Requested/Fetched）按产品约定也不显示，render_html 的
// 耗时在 HtmlRenderCard 内展示。
// 名单与“默认折叠 / 正体固定高度”同源，数据在 utils/toolKind.mjs 那一张表里：本卡片
// 不再自己列 kind（曾经四处各列一份，于是远端读卡比本地读卡多一条耗时角标）。
const displayDuration = computed(() => (toolShowsDuration(props.msg.name) ? props.msg.durationText || '' : ''));

// 流式期间卡片正体显示的是累积的原始参数（live），tool:result 到达后换成结果
// 视图（final）。切换时只加一个状态类触发一次性淡入——不重建元素，因此不会
// 打断内部状态（滚动位置、复制、选中），也不会给将来给这些视图加局部状态埋雷。
const isBodyLive = computed(() => props.msg?.status === 'running');

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
  // 类型标签是兜底：动词已经说清动作的工具在上面就返回了，走不到这里。
  const kindLabel = kindLabelKey(msg.kind);
  if (kindLabel && msg.kind !== 'other') return t(kindLabel);
  return formatToolName(msg.name) || t('tools.kind.tool');
}

function formatToolName(name) {
  const raw = String(name || '').trim();
  if (!raw || raw === 'tool') return '';
  return raw;
}
function toolVerb(msg) {
  return toolVerbLabel(msg.name, msg.kind, msg.status, msg.toolAction);
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

// tool:update 的合并写按载荷降频（小载荷 120ms 起，见 utils/toolUpdateFlush.mjs），
// 但每拍仍会重渲一次卡片，模板里反复调用 lineCount / toolBodyText 会对流式增长中的
// body 全量切行。这里按消息对象做 WeakMap 记忆化：文本与展开状态不变时直接复用上次的行数。
const lineCountMemo = new WeakMap();
// 卡片只问「行数是否超过 4 / 6」，不需要精确行数：数够上限就停（见 toolPreview.mjs
// 的 countLinesCapped）。流式期间这段文本有几 MB，而卡片每拍都要重渲一次，为一次
// 比较切完整段是白费的。cap 取两个阈值里更大的那个 + 1，所以 > COMMAND_PREVIEW_LINES
// 与 > BODY_PREVIEW_LINES 两个判据都仍然成立。
const LINE_COUNT_CAP = Math.max(BODY_PREVIEW_LINES, COMMAND_PREVIEW_LINES) + 1;
function lineCount(msg, text) {
  if (!text) return 0;
  let memo = lineCountMemo.get(msg);
  if (!memo || memo.text !== text) {
    memo = { text, count: countLinesCapped(text, LINE_COUNT_CAP) };
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
    // 只切末尾几行：命令输出是这张卡最热的路径，别为 4 行预览切开整段累积输出
    // （等价写法见 toolPreview.mjs 的 tailLines）。
    return tailLines(body, TOOL_OUTPUT_PREVIEW_LINES, { skipBlank: true }).join('\n');
  }
  if (!isBodyPreview(msg)) return body;
  if (isServiceReadResult(msg)) return tailLines(body, BODY_PREVIEW_LINES).join('\n');
  // 头部预览保持全量切：这条支路取的是开头若干行。
  return normalizedLines(body).slice(0, BODY_PREVIEW_LINES).join('\n');
}

// errorCodeLabel 是错误码的本地化标签：没有译文时返回空串，退回什么由调用方自己
// 决定（错误块退到「工具执行出错」，删除卡的逐行原因退到结果里的原始文本）。
function errorCodeLabel(code) {
  const value = String(code || '').trim();
  if (!value) return '';
  const key = `tools.error.${value}`;
  const translated = t(key);
  return translated !== key ? translated : '';
}

function errorDescription(msg) {
  const label = errorCodeLabel(msg?.errorCode);
  if (label) return label;
  if (!msg?.errorCode) return errorReasonText(msg) || String(msg.body || '');
  return t('tools.error.unknown');
}

// 已有本地化错误描述时不重复展示原始（多为英文的）错误文本；
// 完整错误仍通过工具结果返回给模型。
function errorReasonText(msg) {
  if (errorCodeLabel(msg?.errorCode)) return '';
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

function handleToggle(msg) {
  if (msg.kind === 'command') {
    bodyScrollEnabled.value = false;
  }
  emit('toggle');
}

function hasExpandableBody(msg) {
  if (msg.kind === 'read' || msg.kind === 'remote_read' || msg.kind === 'plan') return false;
  // 命令卡的可展开阈值必须与 CodeView 的裁剪阈值同源，否则 5–6 行输出会被裁成
  // 4 行预览却判定为「无可展开内容」，截断后再也看不到全文。
  if (msg.kind === 'command') return bodyOverflow.value || lineCount(msg, msg.body) > COMMAND_PREVIEW_LINES;
  if (msg.kind === 'edit') return true;
  if (msg.kind === 'create') return bodyOverflow.value || lineCount(msg, msg.codeContent) > BODY_PREVIEW_LINES;
  // calculate: body 只重复 title(expression) + chip(= result)，无需详情卡
  if (msg.kind === 'calculate') return false;
  // grep 永远只显示单行状态和命中统计，不加载匹配行详情。
  if (msg.kind === 'grep') return false;
  return lineCount(msg, msg.body) > BODY_PREVIEW_LINES || msg.kind === 'list';
}
</script>
