<!--
SPDX-License-Identifier: GPL-3.0-only

Copyright (C) 2026 tangssst <tangssst@qq.com>
GitHub: https://github.com/Bronya0/ally-agent

This file is part of ally-agent, licensed under the GNU General
Public License v3. See the LICENSE file for details.
-->
<template>
  <div :class="['rich-tool-card', displayedMsg.kind, displayedMsg.status, { expanded: displayedMsg.expanded }]" @click.stop>
    <div
      :class="['tool-line', { clickable: hasExpandableBody(displayedMsg) }]"
      @click.stop="hasExpandableBody(displayedMsg) && handleToggle(displayedMsg)"
    >
      <ToolStatusIcon :status="statusMark" />
      <span class="tool-verb">{{ toolVerb(displayedMsg) }}</span>
      <span class="tool-name">{{ toolDisplayName(displayedMsg) }}</span>
      <span v-if="displayedMsg.title && displayedMsg.kind === 'command'" class="tool-command" :title="displayedMsg.title">
        <span class="tool-command-paren">(</span>
        <code v-html="highlightCommand(displayedMsg)"></code>
        <span class="tool-command-paren">)</span>
      </span>
      <span v-else-if="displayedMsg.kind === 'plan' && displayedMsg.title" class="tool-arg plan-next-step" :title="displayedMsg.title">({{ displayedMsg.title }})</span>
      <span v-else-if="displayedMsg.title" class="tool-arg" :title="displayedMsg.title">({{ displayedMsg.title }})</span>
      <span v-if="displayedMsg.kind === 'edit' && displayedMsg.status === 'success' && (displayedMsg.editAdded || displayedMsg.editRemoved)" class="tool-chip edit-change-chip">
        <span class="tool-chip-dot">&middot;</span>
        <span v-if="displayedMsg.editAdded" class="edit-chip-added">+{{ displayedMsg.editAdded }}</span>
        <span v-if="displayedMsg.editRemoved" class="edit-chip-removed">-{{ displayedMsg.editRemoved }}</span>
      </span>
      <span v-else-if="displayedMsg.kind === 'wait' && waitCountdown" class="tool-chip wait-countdown">{{ waitCountdown }}</span>
      <span v-else-if="displayedMsg.chip" class="tool-chip">{{ displayedMsg.chip }}</span>
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

    <!-- 远端传输的详情：按字段铺开（方向 / 两端路径 / 文件与目录数 / 体积），行随结果
         而来，与删除卡的逐行同一套路数，不用展开就能看到全部字段。 -->
    <div v-if="transferFields.length" class="tool-field-grid">
      <div v-for="field in transferFields" :key="field.key" class="tool-field-row">
        <span class="tool-field-label">{{ field.label }}:</span>
        <span class="tool-field-value" :title="field.value">{{ field.value }}</span>
      </div>
    </div>

    <CardGrid
      v-if="cardGrid"
      :items="cardGrid.items"
      :caption="cardGrid.caption"
      :empty-text="cardGrid.emptyText"
    />

    <div v-if="completedEditEntries.length" class="tool-body-swap edit-file-groups">
      <div v-for="(entry, ei) in completedEditEntries" :key="entry.path || ei" class="edit-file-group">
        <div v-if="displayedMsg.editEntries.length > 1" class="edit-file-header">
          <span class="edit-file-name">{{ entry.path || $t('tools.file', { index: ei + 1 }) }}</span>
          <span v-if="entry.added" class="edit-chip-added">+{{ entry.added }}</span>
          <span v-if="entry.removed" class="edit-chip-removed">-{{ entry.removed }}</span>
        </div>
        <div class="edit-file-content" :class="{ 'no-header': displayedMsg.editEntries.length <= 1 }">
          <DiffView layout="split" :diff-text="entry.diff" :file-path="entry.path" :show-header="false" :collapsed="!displayedMsg.expanded" :added-count="entry.added || 0" :removed-count="entry.removed || 0" @toggle="handleToggle(displayedMsg)" />
        </div>
      </div>
    </div>
    <DiffView
      v-else-if="displayedMsg.kind === 'edit' && displayedMsg.status === 'success' && displayedMsg.editDiff"
      class="tool-body-swap"
      layout="split"
      :diff-text="displayedMsg.editDiff"
      :file-path="displayedMsg.editFilePath"
      :show-header="false"
      :collapsed="!displayedMsg.expanded"
      :added-count="displayedMsg.editAdded || 0"
      :removed-count="displayedMsg.editRemoved || 0"
      @toggle="handleToggle(displayedMsg)"
    />
    <pre v-else-if="displayedMsg.kind === 'edit' && displayedMsg.status === 'success' && displayedMsg.editChangedLinesBlock" class="edit-changed-lines-block" :class="{ 'edit-changed-lines-preview': !displayedMsg.expanded }">{{ displayedMsg.editChangedLinesBlock }}</pre>
    <div v-if="displayedMsg.editWarnings && displayedMsg.editWarnings.length" class="edit-warning-list">
      <div v-for="(warning, wi) in displayedMsg.editWarnings" :key="wi" class="edit-warning">{{ warning }}</div>
    </div>
    <CodeView
      v-if="displayedMsg.kind === 'create' && displayedMsg.status !== 'error'"
      :class="{ 'tool-body-swap': !isBodyLive }"
      :code="displayedMsg.codeContent || ''"
      :file-path="displayedMsg.editFilePath || ''"
      :collapsed="isCreatePreview(displayedMsg)"
      :max-lines="BODY_PREVIEW_LINES"
      preview-mode="tail"
      :live="isBodyLive"
      @overflow="setBodyOverflow(displayedMsg, $event)"
    />
    <TerminalOutputView
      v-else-if="displayedMsg.kind === 'command' && displayedMsg.status !== 'error'"
      :class="{ 'tool-body-swap': !isBodyLive }"
      :text="displayedMsg.body || ''"
      :collapsed="!displayedMsg.expanded"
      :max-lines="COMMAND_PREVIEW_LINES"
      @overflow="setBodyOverflow(displayedMsg, $event)"
    />
    <!-- 截图：正体就是截到的图（点击在原尺寸与预览高度间切换）。
         dataUrl 是运行时字段（sanitizeStoredMessage 落盘时剥离），列表动作
         没有图，回落到通用 body 渲染窗口列表。 -->
    <div
      v-else-if="displayedMsg.kind === 'screenshot' && displayedMsg.screenshotDataUrl && displayedMsg.status !== 'error'"
      :class="['screenshot-preview', { expanded: screenshotExpanded }]"
      @click.stop="screenshotExpanded = !screenshotExpanded"
    >
      <img :src="displayedMsg.screenshotDataUrl" alt="screenshot" loading="lazy" />
    </div>
    <pre v-else-if="displayedMsg.body && !cardGrid && displayedMsg.status !== 'error' && displayedMsg.kind !== 'edit' && displayedMsg.kind !== 'read' && displayedMsg.kind !== 'remote_read' && displayedMsg.kind !== 'calculate' && displayedMsg.kind !== 'grep' && displayedMsg.kind !== 'plan' && (displayedMsg.kind !== 'list' || displayedMsg.expanded)" ref="bodyPreRef" :class="['tool-body', { 'fixed-scroll': isFixedBodyKind(displayedMsg.kind), 'body-preview': isBodyPreview(displayedMsg), 'tail-default': isServiceReadResult(displayedMsg), 'scroll-enabled': bodyScrollEnabled && isScrollableBody(displayedMsg), 'tool-body-swap': !isBodyLive }]" @click.stop="handleBodyClick(displayedMsg)">{{ toolBodyText(displayedMsg) }}</pre>
    <div v-if="isValidationWarning(displayedMsg)" class="edit-warning-list validation-warning-list" role="status" aria-live="polite">
      <div class="edit-warning validation-warning" :title="displayedMsg.validation">
        <span class="validation-warning-label">{{ $t('tools.validationWarning') }}</span>
        {{ displayedMsg.validation }}
      </div>
    </div>
    <div v-if="displayedMsg.status === 'error'" class="tool-error-block" role="alert">
      <div v-if="displayedMsg.errorCode" class="tool-error-detail">
        <code>{{ displayedMsg.errorCode }}</code>
      </div>
      <pre v-if="errorReasonText(displayedMsg)" class="tool-error-reason">{{ errorReasonText(displayedMsg) }}</pre>
    </div>

    <!-- 沙箱拦下的写入：工具结果是“命令执行成功”，绿色 √ 会把“没写成”说成“成了”，
         所以这行必须固定显示。位置固定在正体之后：输出照实呈现（内核那句拒绝、原样的
         退出码都在正体里），告警收尾，不与正文抢头一行。 -->
    <div v-if="displayedMsg.sandboxDenied" class="tool-sandbox-denied" role="alert">
      {{ $t('app.tools.sandboxDenied') }}
    </div>
  </div>
</template>

<script setup>
import { computed, defineAsyncComponent, nextTick, ref, shallowRef, watch } from 'vue';
import { useDisplayClock, usePresentationActive } from '../composables/presentation.mjs';
import hljs from 'highlight.js/lib/core';
import bash from 'highlight.js/lib/languages/bash';
import powershell from 'highlight.js/lib/languages/powershell';
import { highlightShellCommand } from '../utils/shellHighlight.mjs';
import { commandHighlightCache as highlightCache } from '../utils/highlightCache.mjs';
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

const presentationActive = usePresentationActive();
// Copy fields only when pausing, retaining immutable string payloads by
// reference. Active cards read the live message without allocating snapshots.
const pausedMsg = shallowRef({ ...props.msg });
watch(presentationActive, (active) => {
  if (!active) pausedMsg.value = { ...props.msg };
}, { flush: 'sync' });
const displayedMsg = computed(() => presentationActive.value ? props.msg : pausedMsg.value);

const emit = defineEmits(['toggle']);

// 被沙箱拦下的写入按拒绝显示状态标记：工具调用在后端口径里是成功的（内核拒绝是命令
// 结果、不是工具错误），但这次写没有发生。正体照旧渲染命令输出——拒绝原因与可写根
// 都在里面，标记只负责让用户一眼看到“这次写没成”。
const statusMark = computed(() => (displayedMsg.value?.sandboxDenied ? 'error' : displayedMsg.value?.status));

// 一次删多条时逐行列出每一条（成/败），行样式复用读卡的折叠行：一条路径一行，
// 失败行沿用同款红字标记 + 错误文案。行数据来自结果适配器（见 useToolEvents），
// 所以行的判定与标题摘要同源（utils/toolPreview 的 deletePathRows）。
const deleteRows = computed(() => {
  if (displayedMsg.value?.kind !== 'delete' || !Array.isArray(displayedMsg.value.deleteEntries)) return [];
  return displayedMsg.value.deleteEntries;
});

// 单条且成功时标题已经写了那条路径，再铺一行是重复；多路径（标题只剩 "N paths"）、
// 唯一那条失败（标题看不出失败）、或唯一那条本来就不存在（标题的 "Deleted" 里没有
// 任何东西被删）时必须铺开，否则卡片上看不到真实结果。
const deleteRowsShown = computed(() => deleteRows.value.length > 1
  || (deleteRows.value.length === 1 && (!deleteRows.value[0].ok || deleteRows.value[0].absent)));

function treePrefix(index) {
  return index === deleteRows.value.length - 1 ? '└─' : '├─';
}

// 逐行失败原因是错误正体同一份文案的截取：原始串可能多行（远端删除最容易撞上），
// 行内只留第一行，完整文本照旧挂在 title 上，行本身再由 .read-group-chip-error
// 截断。这里同样不套本地化标签，免得逐行原因与错误块把同一件事说成两句不同的话。
function deleteRowError(row) {
  return formatToolErrorBody(row?.error).split('\n', 1)[0].trim();
}

// 一批同类记录的工具结果（ssh_cluster 服务器清单 / service 进程清单 / scheduled_task
// 任务清单）由结果适配器解析并映射成卡片条目，整包写进 msg.cardGrid（见 useToolEvents
// 的 setCardGrid）：条目、标题行、空态文案一起走，卡片正体因此可以整块清空。没有这个
// 字段的是旧卡片或其它工具，继续走原来的 body。
const cardGrid = computed(() => {
  const grid = displayedMsg.value?.cardGrid;
  return grid && Array.isArray(grid.items) ? grid : null;
});

// 结果本身就是一组字段的工具（目前只有远端传输）：字段行由结果适配器写好（标签已过
// i18n），这里只决定要不要铺开。
const transferFields = computed(() => (
  displayedMsg.value?.kind === 'remote_transfer' && Array.isArray(displayedMsg.value.transferFields) ? displayedMsg.value.transferFields : []
));

// Pending arguments are not an applied edit. Show only completed backend diffs.
const completedEditEntries = computed(() => (
  displayedMsg.value?.kind === 'edit' && displayedMsg.value.status === 'success' && Array.isArray(displayedMsg.value.editEntries)
    ? displayedMsg.value.editEntries.filter((entry) => entry?.diff) : []
));

const DiffView = defineAsyncComponent(() => import('./DiffView.vue'));
const CodeView = defineAsyncComponent(() => import('./CodeView.vue'));
const TerminalOutputView = defineAsyncComponent(() => import('./TerminalOutputView.vue'));
const nowMs = useDisplayClock(() => presentationActive.value && displayedMsg.value.kind === 'wait' && displayedMsg.value.status === 'running', 250);

const waitCountdown = computed(() => {
  if (displayedMsg.value.kind !== 'wait' || displayedMsg.value.status !== 'running') return '';
  const seconds = Number(displayedMsg.value.waitSeconds || 0);
  const startedAt = Number(displayedMsg.value.waitStartedAt || 0);
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
const displayDuration = computed(() => (toolShowsDuration(displayedMsg.value.name) ? displayedMsg.value.durationText || '' : ''));

// 流式期间卡片正体显示的是累积的原始参数（live），tool:result 到达后换成结果
// 视图（final）。切换时只加一个状态类触发一次性淡入——不重建元素，因此不会
// 打断内部状态（滚动位置、复制、选中），也不会给将来给这些视图加局部状态埋雷。
const isBodyLive = computed(() => displayedMsg.value?.status === 'running');


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

// Shared LRU with entry and character budgets (including highlighted HTML).
function highlightCommand(msg) {
  const command = String(msg?.title || '');
  const language = commandHighlightLanguage(msg, command);
  const cacheKey = `${language}::${command}`;
  const cached = highlightCache.get(cacheKey);
  if (cached !== undefined) {
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

// 错误正体就是后端交给模型的那份原文（同一个 error 字段），前端不再另造一句本地化
// 摘要：多一层标签就等于两套文案。展示层只剥信封，收口在 formatToolErrorBody：去掉
// 开头的 [CODE] 前缀（码已由 chip 单独显示）与标题里已有的「被拦截的命令」回声。
function errorReasonText(msg) {
  return formatToolErrorBody(msg?.body);
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
  () => displayedMsg.value?.eventId,
  () => {
    bodyScrollEnabled.value = false;
  },
  { immediate: true },
);

watch(
  () => [presentationActive.value, displayedMsg.value?.body, displayedMsg.value?.expanded, displayedMsg.value?.kind, displayedMsg.value?.status],
  () => {
    if (!presentationActive.value || bodyScrollEnabled.value || !isScrollableBody(displayedMsg.value)) return;
    nextTick(() => {
      const pre = bodyPreRef.value;
      if (!presentationActive.value || !pre) return;
      pre.scrollTop = pre.scrollHeight;
      requestAnimationFrame(() => {
        if (presentationActive.value && bodyPreRef.value) bodyPreRef.value.scrollTop = bodyPreRef.value.scrollHeight;
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
  if (msg.kind === 'edit') return msg.status === 'success'
    && Boolean(completedEditEntries.value.length || msg.editDiff || msg.editChangedLinesBlock);
  if (msg.kind === 'create') return bodyOverflow.value || lineCount(msg, msg.codeContent) > BODY_PREVIEW_LINES;
  // calculate: body 只重复 title(expression) + chip(= result)，无需详情卡
  if (msg.kind === 'calculate') return false;
  // grep 永远只显示单行状态和命中统计，不加载匹配行详情。
  if (msg.kind === 'grep') return false;
  return lineCount(msg, msg.body) > BODY_PREVIEW_LINES || msg.kind === 'list';
}
</script>
