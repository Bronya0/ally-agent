<!--
SPDX-License-Identifier: GPL-3.0-only

Copyright (C) 2026 tangssst <tangssst@qq.com>
GitHub: https://github.com/Bronya0/ally-agent

This file is part of ally-agent, licensed under the GNU General
Public License v3. See the LICENSE file for details.
-->
<template>
  <div class="messages-scroll-shell">
    <n-scrollbar ref="scrollbarRef" class="messages-scroll" @scroll="handleScroll">
      <div ref="messagesRootRef" class="messages">
        <template v-for="(msg, index) in messages" :key="msgKey(msg)" v-memo="messageRenderMemo(msg)">
        <button v-if="msg.role === 'archive'" class="message-archive-toggle" @click.stop="$emit('toggleArchive', msg.sessionId)">
          <span>{{ msg.expanded ? $t('chat.archive.collapse') : $t('chat.archive.expand') }}</span>
          <span>{{ $t('chat.archive.summary', { count: msg.count }) }}</span>
        </button>
        <div v-else-if="msg.role === 'user'" :class="['message', msg.role, { error: msg.error }]" data-user-question>
          <div class="user-message-content">
            <div class="message-body user-text">
              <span v-if="msg.skill" class="skill-chip">/{{ msg.skill.name }}</span>
              <template v-if="userMessageText(msg)">
                <div
                  v-if="isLongUserMessage(msg) && !isUserMessageExpanded(msg)"
                  :class="['user-text-preview', { 'skill-user-text': msg.skill }]"
                >{{ userMessagePreview(msg) }}</div>
                <div
                  v-else
                  :class="['markdown-body', { 'skill-user-text': msg.skill }]"
                  v-html="renderFn(userMessageText(msg), false)"
                ></div>
                <button
                  v-if="isLongUserMessage(msg)"
                  class="user-text-toggle"
                  type="button"
                  :aria-expanded="isUserMessageExpanded(msg)"
                  @click.stop="toggleUserMessage(msg)"
                >{{ userMessageToggleLabel(msg) }}</button>
              </template>
            </div>
            <RenderBoundary :label="$t('chat.attachment')"><MessageAttachments :attachments="msg.attachments || []" /></RenderBoundary>
          </div>
          <button
            class="user-delete-btn"
            type="button"
            :title="$t('chat.userMessage.delete')"
            :aria-label="$t('chat.userMessage.delete')"
            @click.stop="$emit('deleteUserMessage', msg)"
          >
            <CloseOutlined />
          </button>
        </div>
        <div v-else-if="msg.role !== 'tool_call' && msg.kind !== 'subagent'" :class="['message', msg.role, { error: msg.error, system: msg.system }]">
          <div
            class="reasoning-block"
            :class="{ 'reasoning-hidden': msg.reasoningEndedAt || !(msg.reasoningChars > 0 || msg.reasoningStartedAt) }"
          >
            <div class="reasoning-header">
              <span class="reasoning-label">
                <span class="reasoning-title reasoning-title-thinking">Thinking</span>
                <span class="reasoning-tokens">{{ fmtK(Math.max(1, Math.round((msg.reasoningChars || 0) / 3))) }} tokens</span>
              </span>
            </div>
          </div>
          <RenderBoundary v-if="msg.welcome" :label="$t('chat.welcome')"><WelcomeMessage :welcome="msg.welcome" :tools="tools" :mcp-servers="mcpServers" /></RenderBoundary>
          <StreamingMarkdownBody v-else :msg="msg" :render-fn="renderFn" />
          <RenderBoundary :label="$t('chat.attachment')"><MessageAttachments :attachments="msg.attachments || []" /></RenderBoundary>
          <div v-if="msg.role === 'assistant' && msg.suggestions?.length && !msg.streaming" class="suggest-row">
            <button v-for="(label, i) in msg.suggestions" :key="i" class="suggest-chip" @click.stop="$emit('sendSuggest', label)">{{ label }}</button>
          </div>
          <!-- 本轮统计行（时长/cache/tokens）：数据由 run:done 事件在结束时
               一次性下发，过程中无法实时显示。仅当本条是列表最后一条消息且正文
               已开始输出时渲染不可见占位行（数据最终只会填到本轮最后一条
               assistant 消息上）；中间消息不占位——否则占位行夹在正文和
               后续折叠组之间，成为折叠组上方的幽灵间距。新消息追加与占位
               移除落在同一次渲染 patch 里，无中间态跳动。 -->
          <div v-if="msg.role === 'assistant' && (msg.roundDurationText || (msg.streaming && hasAnswerBody(msg) && isLastDisplayMessage(msg)))" :class="['message-duration', { 'is-placeholder': !msg.roundDurationText }]">
            <span class="duration-text">{{ msg.roundDurationText || '\u00a0' }}</span>
            <span
              v-if="typeof msg.cacheRate === 'number'"
              class="cache-rate"
              :title="`cache hit ${msg.cacheHit} / miss ${msg.cacheMiss} (this run)`"
            >cache {{ msg.cacheRate }}%</span>
            <span
              v-if="msg.runInputTokens > 0 || msg.runOutputTokens > 0"
              class="run-tokens"
              :title="`input ${msg.runInputTokens} / output ${msg.runOutputTokens} tokens (this run)`"
            >↑{{ fmtTokens(msg.runInputTokens) }} ↓{{ fmtTokens(msg.runOutputTokens) }}</span>
            <span
              v-if="tokenSpeed(msg)"
              class="run-tokens"
              :title="`${fmtTokens(msg.runOutputTokens)} output tokens / ${(msg.roundDurationMs / 1000).toFixed(1)}s (whole run)`"
            >{{ tokenSpeed(msg) }} token/s</span>
            <n-dropdown
              trigger="click"
              placement="top-end"
              :options="exportOptions"
              @select="(key) => $emit('export', key, msg)"
            >
              <button class="export-icon-btn" :title="$t('chat.export.title')" :aria-label="$t('chat.export.title')" @click.stop>
                <ExportOutlined />
              </button>
            </n-dropdown>
            <button
              v-if="msg === lastAnswerMessage"
              class="export-icon-btn"
              :title="$t('chat.copySummary.title')"
              :aria-label="$t('chat.copySummary.title')"
              @click.stop="copyFinalSummary"
            >
              <CopyOutlined />
            </button>
            <n-dropdown
              trigger="click"
              placement="top-end"
              :options="quickMessageOptions"
              @select="(key) => $emit('quickMessage', key)"
            >
              <button class="export-icon-btn" :title="$t('chat.quickMessage.title')" :aria-label="$t('chat.quickMessage.title')" @click.stop>
                <MessageOutlined />
              </button>
            </n-dropdown>
          </div>
        </div>
        <RenderBoundary v-else-if="msg.kind === 'ask'" :label="$t('chat.ask')">
          <AskToolCard :msg="msg" @submit="$emit('submitAsk', msg, $event)" />
        </RenderBoundary>
        <!-- Tool call cards -->
        <RenderBoundary
          v-else-if="!['run','read-group','read-grep-group','subagent','render_html'].includes(msg.kind)"
          :label="$t('chat.toolCard')"
        >
          <ToolCallCard
            :msg="msg"
            @toggle="$emit('toggleTool', msg)"
          />
        </RenderBoundary>
        <!-- Read group card -->
        <RenderBoundary v-else-if="msg.kind === 'read-group'" :label="$t('chat.readResult')">
          <ReadGroupCard
            :msg="msg"
            @toggle="$emit('toggleTool', msg)"
          />
        </RenderBoundary>
        <!-- Read + grep folded group card -->
        <RenderBoundary v-else-if="msg.kind === 'read-grep-group'" :label="$t('chat.readResult')">
          <ReadGrepGroupCard
            :msg="msg"
            @toggle="$emit('toggleTool', msg)"
          />
        </RenderBoundary>
        <!-- Sub-agent -->
        <RenderBoundary v-else-if="msg.kind === 'subagent'" :label="$t('chat.subagent')"><SubagentInlineCard :msg="msg" /></RenderBoundary>
        <!-- HTML Render -->
        <RenderBoundary v-else-if="msg.kind === 'render_html'" :label="$t('tools.kind.renderHtml')">
          <HtmlRenderCard :msg="msg" />
        </RenderBoundary>
        </template>
        <div v-if="messages.length === 0" class="empty-chat">
          <n-empty :description="$t('chat.empty')" />
        </div>
        <div ref="bottomAnchorRef" class="messages-bottom-anchor" aria-hidden="true"></div>
      </div>
    </n-scrollbar>
    <div v-if="showJumpToBottom" class="jump-controls">
      <button class="jump-circle-btn" :title="$t('composer.question.previous')" @click="scrollToUserQuestion('up')">
        <ArrowUpOutlined />
      </button>
      <button class="jump-circle-btn" @click="jumpToBottom">
        <ArrowDownOutlined />
      </button>
    </div>
  </div>
</template>

<script setup>
import { computed, nextTick, onBeforeUnmount, onMounted, provide, reactive, ref, toRaw } from 'vue';
import { t } from '../i18n.mjs';
import { copyText } from '../utils/clipboard.mjs';
import { toolCardRenderSignature } from '../utils/toolCardSignature.mjs';
import MessageAttachments from './MessageAttachments.vue';
import WelcomeMessage from './WelcomeMessage.vue';
import ToolCallCard from './ToolCallCard.vue';
import AskToolCard from './AskToolCard.vue';
import ReadGroupCard from './ReadGroupCard.vue';
import ReadGrepGroupCard from './ReadGrepGroupCard.vue';
import SubagentInlineCard from './SubagentInlineCard.vue';
import HtmlRenderCard from './HtmlRenderCard.vue';
import StreamingMarkdownBody from './StreamingMarkdownBody.vue';
import RenderBoundary from './RenderBoundary.vue';
import ExportOutlined from '@vicons/antd/ExportOutlined';
import MessageOutlined from '@vicons/antd/MessageOutlined';
import ArrowUpOutlined from '@vicons/antd/ArrowUpOutlined';
import ArrowDownOutlined from '@vicons/antd/ArrowDownOutlined';
import CloseOutlined from '@vicons/antd/CloseOutlined';
import CopyOutlined from '@vicons/antd/CopyOutlined';
import { useMessage } from 'naive-ui';

const props = defineProps({
  messages: { type: Array, required: true },
  renderFn: { type: Function, required: true },
  fmtK: { type: Function, required: true },
  tools: { type: Array, default: () => [] },
  mcpServers: { type: Array, default: () => [] },
});

const expandedUserMessages = reactive(new WeakSet());
const userMessageStatsCache = new WeakMap();
// read-grep 折叠组的展开状态：按组首条 eventId 索引。组对象在流式累加时
// 每次重建，状态放组件级 Map 才能在计数增长时保持展开。
const readGrepGroupExpanded = reactive(new Map());
provide('readGrepGroupExpanded', readGrepGroupExpanded);
const USER_MESSAGE_COLLAPSE_CHAR_LIMIT = 800;
const USER_MESSAGE_COLLAPSE_LINE_LIMIT = 10;
const USER_MESSAGE_PREVIEW_CHAR_LIMIT = 400;
const USER_MESSAGE_PREVIEW_LINE_LIMIT = 6;

function userMessageText(msg) {
  return String(msg?.skill ? msg.skill.args || '' : msg?.content || '');
}

// 统计占位行的渲染条件：assistant 消息的正文是否已开始输出。读一次性
// 标志 hasBody（App.vue 在首个内容增量时置位），不读 msg.content——
// 那会让父级渲染 effect 订阅流式增量，破坏 StreamingMarkdownBody 的
// 独立渲染作用域设计。
function hasAnswerBody(msg) {
  return msg?.hasBody === true;
}

// 统计占位行只挂在列表最后一条消息上：run:done 的统计数据只会填到本轮
// 最后一条 assistant 消息，中间消息（后面还跟着折叠组等）占位只会成为
// 幽灵间距。v-memo 数组里带同款判断，保证"最后一条"易主时旧消息能
// 重渲染并移除占位行。
function isLastDisplayMessage(msg) {
  const list = props.messages;
  return list[list.length - 1] === msg;
}

// Keep historical message subtrees out of the patch path while the active
// assistant message streams. v-memo 必须挂在 v-for 的 <template> 上：只有这
// 种写法 Vue 才会生成逐条目缓存（renderList 按 :key 校验后复用）。若把
// v-memo 放在 v-for 内部的分支元素上，会编译成 withMemo 共享单个缓存槽，
// 后续所有 memo 值相同的消息（如纯文本用户消息）都会复用第一条消息缓存
// 的 vnode，界面永远显示第一条的内容。Every value read by the template
// branches (all kinds, not just user/assistant) that can change between
// renders is represented here.
// 注意：这里刻意不读 msg.content——memo 数组在父组件渲染作用域内求值，
// 一旦读取内容就会让整个列表的渲染 effect 订阅流式增量。活跃消息的正文
// 由 StreamingMarkdownBody 子组件独立渲染；已完成消息的内容不再变化，
// 流式结束时 streaming 标志翻转即触发最后一帧补丁。
function messageRenderMemo(msg) {
  const attachments = Array.isArray(msg?.attachments) ? msg.attachments : [];
  const lastAttachment = attachments.length ? attachments[attachments.length - 1] : null;
  return [
    msg?.role,
    msg?.kind,
    msg?.skill?.name || '',
    msg?.eventId,
    msg?.expanded,
    msg?.count,
    msg === lastAnswerMessage.value,
    // 正文是否已开始输出（一次性标志，首个内容增量时置位）：统计占位行的
    // 渲染条件依赖它，翻转时必须触发父级重渲染；之后不再变化，流式正文
    // 增量依旧只由 StreamingMarkdownBody 子组件渲染。
    msg?.hasBody === true,
    // 是否为列表最后一条消息：统计占位行只挂在最后一条上，新消息追加后
    // 旧"最后一条"的 memo 变化 → 重渲染 → 占位行移除（与新增消息同 patch）。
    isLastDisplayMessage(msg),
    msg?.reasoningChars,
    msg?.reasoningStartedAt,
    msg?.reasoningEndedAt,
    msg?.streaming,
    msg?.done,
    msg?.status,
    msg?.error,
    msg?.system,
    msg?.welcome,
    msg?.roundDurationText,
    msg?.roundDurationMs,
    msg?.cacheRate,
    msg?.cacheHit,
    msg?.cacheMiss,
    msg?.runInputTokens,
    msg?.runOutputTokens,
    msg?.suggestions?.length || 0,
    attachments.length,
    lastAttachment?.previewUrl,
    lastAttachment?.dataUrl,
    lastAttachment?.partial,
    msg?.role === 'user' ? isUserMessageExpanded(msg) : false,
    // Tool-card safety net: any field that can change how a tool card renders
    // (status above all) is folded into one signature so a missed memo field
    // no longer freezes the card. See utils/toolCardSignature.mjs.
    toolCardRenderSignature(msg),
  ];
}

function userMessageStats(msg) {
  const text = userMessageText(msg);
  const cached = userMessageStatsCache.get(msg);
  if (cached?.text === text) return cached;
  const stats = {
    text,
    characters: text.length,
    lines: text ? text.split(/\r\n|\r|\n/).length : 0,
  };
  userMessageStatsCache.set(msg, stats);
  return stats;
}

function isLongUserMessage(msg) {
  const stats = userMessageStats(msg);
  return stats.characters > USER_MESSAGE_COLLAPSE_CHAR_LIMIT || stats.lines > USER_MESSAGE_COLLAPSE_LINE_LIMIT;
}

function isUserMessageExpanded(msg) {
  return expandedUserMessages.has(msg);
}

function userMessagePreview(msg) {
  const text = userMessageStats(msg).text.replace(/\r\n|\r/g, '\n');
  const linePreview = text.split('\n').slice(0, USER_MESSAGE_PREVIEW_LINE_LIMIT).join('\n');
  const preview = linePreview.slice(0, USER_MESSAGE_PREVIEW_CHAR_LIMIT).trimEnd();
  return preview.length < text.length ? `${preview}\n…` : preview;
}

function toggleUserMessage(msg) {
  if (expandedUserMessages.has(msg)) expandedUserMessages.delete(msg);
  else expandedUserMessages.add(msg);
}

function userMessageToggleLabel(msg) {
  const stats = userMessageStats(msg);
  return isUserMessageExpanded(msg)
    ? t('chat.userMessage.collapse')
    : t('chat.userMessage.expand', { lines: stats.lines, characters: stats.characters });
}

// Stable v-for key for messages that lack an eventId (user / assistant /
// archive / system). tool_call messages already carry a stable eventId, so we
// reuse it. For the rest, lazily assign a per-object id via WeakMap so the
// same message object keeps the same key across re-renders even when its
// content/role/index shifts — previous key used `index` which forced every
// downstream message to re-mount on any insert.
const msgKeyMap = new WeakMap();
let msgKeyCounter = 0;
function msgKey(msg) {
  if (msg.eventId) return msg.eventId;
  let key = msgKeyMap.get(msg);
  if (!key) {
    msgKeyCounter++;
    key = `local-${msg.role || 'msg'}-${msgKeyCounter}`;
    msgKeyMap.set(msg, key);
  }
  return key;
}

defineEmits([
  'toggleArchive',
  'toggleTool',
  'export',
  'quickMessage',
  'submitAsk',
  'deleteUserMessage',
]);

function fmtTokens(n) {
  const v = Number(n || 0);
  if (v >= 1e9) return (v / 1e9).toFixed(2) + 'B';
  if (v >= 1e6) return (v / 1e6).toFixed(2) + 'M';
  if (v >= 1e3) return (v / 1e3).toFixed(1) + 'k';
  return String(v);
}

// Aggregate output speed over the whole run (all LLM steps + tool time),
// matching how roundDurationMs / runOutputTokens are accumulated on the
// backend. Returns '' while either value is missing.
function tokenSpeed(msg) {
  const ms = Number(msg?.roundDurationMs || 0);
  const out = Number(msg?.runOutputTokens || 0);
  if (ms <= 0 || out <= 0) return '';
  const speed = out / (ms / 1000);
  return String(Math.round(speed));
}

const quickMessageOptions = computed(() => [
  { label: t('chat.quickMessage.continue'), key: 'continue' },
  { label: t('chat.quickMessage.plainSpeak'), key: 'plainSpeak' },
  { label: t('chat.quickMessage.push'), key: 'push' },
  { label: t('chat.quickMessage.review'), key: 'review' },
  { label: t('chat.quickMessage.lesson'), key: 'lesson' },
]);

const exportOptions = computed(() => [
  { label: t('chat.export.response'), key: 'response' },
  { label: t('chat.export.session'), key: 'session' },
]);

// 会话底部复制按钮：复制「最后一次工具调用之后」的总结（最后一条
// assistant 消息的 markdown 正文）。每条 assistant 消息 = 一个 run 步骤，
// tool:result 时封口，因此最后一条有正文的 assistant 消息就是最终总结。
const lastAnswerMessage = computed(() => {
  const msgs = props.messages || [];
  for (let i = msgs.length - 1; i >= 0; i--) {
    const m = msgs[i];
    // 不读代理上的 content：那会让本 computed 订阅所有 assistant 消息内容，
    // 流式增量把整个列表渲染拖下水。toRaw 绕过代理读原始对象，不建立依赖。
    // 中间步骤的 assistant 消息可能没有正文（只有 tool_calls），必须跳过。
    if (m?.role === 'assistant' && !m.welcome && String(toRaw(m)?.content || '').trim()) return m;
  }
  return null;
});

function copyFinalSummary() {
  const msg = lastAnswerMessage.value;
  const text = msg ? String(msg.content || '') : '';
  if (!text.trim()) {
    message.warning(t('chat.copySummary.empty'));
    return;
  }
  copyText(text).then((ok) => {
    if (ok) message.success(t('chat.copySummary.done'));
    else message.error(t('chat.copySummary.failed'));
  });
}

const message = useMessage();

const scrollbarRef = ref(null);
const messagesRootRef = ref(null);
const bottomAnchorRef = ref(null);
const showJumpToBottom = ref(false);
const autoFollow = ref(true);
const bottomThreshold = 96;
let scrollRaf = 0;
// autoFollow 只由"真实用户意图"驱动，绝不根据滚动位置来关闭。程序化滚动
// （流式增长、大 diff 跨帧展开、alignToLastToolCard 停在卡头、resize 补滚）
// 触发的 scroll 事件与用户滚动无法区分，所以裸 scroll 事件永远不会关掉跟随；
// 只有 wheel / touch / 键盘 / 拖动滚动条这类真实手势才暂停跟随。暂停后一旦
// 用户停止操作（空闲）就自动恢复贴底——自愈兜底，任何原因都不会永久卡死。
let userIntentUntil = 0;        // 手势窗口：该时间戳前的 scroll 事件视为用户驱动
let idleResumeTimer = 0;
let restoreRequestId = 0;
const userIntentWindow = 250;   // 手势后 250ms 内的 scroll 事件按用户处理
const idleResumeDelay = 8000;   // 停止操作 8s 后自动恢复自动滚动
// Track pending animation frames so unmounting a closed workspace Tab cannot
// leave callbacks targeting a disposed scrollbar.
const pendingRafs = new Set();
function scheduleRaf(fn) {
  const id = requestAnimationFrame(() => {
    pendingRafs.delete(id);
    fn();
  });
  pendingRafs.add(id);
  return id;
}

let viewportResizeObserver = null;
let userIntentTarget = null;

// 消息区底部一旦被布局挤压（plan 面板出现/展开、输入框自动增高、窗口
// resize、Tab 从隐藏切回可见），可用高度变小，最新内容会被推到视口之下，
// 看起来像被遮挡。这类变化不经过任何事件处理器，无法靠逐个补滚动覆盖；
// 这里统一观察滚动视口的尺寸变化：只要用户仍处于自动跟随状态就重新贴底。
function ensureViewportResizeObserver() {
  const viewport = getScrollViewport();
  if (!viewport || viewportResizeObserver) return;
  viewportResizeObserver = new ResizeObserver(() => {
    if (!autoFollow.value) return;
    scrollToBottom();
  });
  viewportResizeObserver.observe(viewport);
}

// A run is active while any message is still streaming. Once the run ends the
// user may scroll up to read freely — nothing should yank them back to the
// bottom. All auto-resume / auto-follow behavior is gated on this.
const isRunActive = computed(() => Array.isArray(props.messages) && props.messages.some((m) => m?.streaming));

// 记录一次真实用户手势，并重置空闲恢复计时器。计时器只被用户手势刷新，
// 程序化滚动不参与，因此不会被流式期间的自动滚动污染。
function markUserIntent() {
  restoreRequestId += 1;
  userIntentUntil = Date.now() + userIntentWindow;
  armIdleResume();
}

function armIdleResume() {
  // 对话已结束时不再安排"空闲自动贴底"：用户从上往下看不该被拉回底部。
  if (!isRunActive.value) {
    clearIdleResume();
    return;
  }
  if (idleResumeTimer) clearTimeout(idleResumeTimer);
  idleResumeTimer = window.setTimeout(() => {
    idleResumeTimer = 0;
    if (!isRunActive.value) return;
    autoFollow.value = true;
    showJumpToBottom.value = false;
    scrollToBottom({ force: true });
  }, idleResumeDelay);
}

function clearIdleResume() {
  if (idleResumeTimer) {
    clearTimeout(idleResumeTimer);
    idleResumeTimer = 0;
  }
}

function onUserKey(e) {
  if (['PageUp', 'PageDown', 'ArrowUp', 'ArrowDown', 'Home', 'End', ' '].includes(e.key)) {
    markUserIntent();
  }
}

// 拖动 naive-ui 滚动条 rail/thumb 不触发 wheel/touch，这里单独识别。
function onShellPointerDown(e) {
  if (e.target?.closest?.('.n-scrollbar-rail')) markUserIntent();
}

onMounted(() => {
  ensureViewportResizeObserver();
  const viewport = getScrollViewport();
  if (viewport) {
    viewport.addEventListener('wheel', markUserIntent, { passive: true });
    viewport.addEventListener('touchmove', markUserIntent, { passive: true });
    viewport.addEventListener('keydown', onUserKey);
    const shell = viewport.closest('.messages-scroll-shell') || viewport.parentElement;
    shell?.addEventListener('pointerdown', onShellPointerDown, true);
    userIntentTarget = { viewport, shell };
  }
});

onBeforeUnmount(() => {
  viewportResizeObserver?.disconnect();
  viewportResizeObserver = null;
  if (scrollRaf) cancelAnimationFrame(scrollRaf);
  for (const id of pendingRafs) cancelAnimationFrame(id);
  pendingRafs.clear();
  clearIdleResume();
  if (userIntentTarget) {
    userIntentTarget.viewport?.removeEventListener('wheel', markUserIntent);
    userIntentTarget.viewport?.removeEventListener('touchmove', markUserIntent);
    userIntentTarget.viewport?.removeEventListener('keydown', onUserKey);
    userIntentTarget.shell?.removeEventListener('pointerdown', onShellPointerDown, true);
    userIntentTarget = null;
  }
});

function getScrollViewport() {
  const root = messagesRootRef.value;
  if (!root) return null;
  return root.closest('.n-scrollbar-container') || root.parentElement;
}

function isNearBottom() {
  const viewport = getScrollViewport();
  if (!viewport) return true;
  return viewport.scrollHeight - viewport.scrollTop - viewport.clientHeight <= bottomThreshold;
}

function handleScroll() {
  // 只在处于用户手势窗口内时才根据位置重算跟随状态；窗口之外的 scroll 事件
  // 一律视为程序化滚动，完全不改变 autoFollow，从原理上杜绝"程序滚动误关
  // 自动跟随"这类中断。
  if (Date.now() > userIntentUntil) return;
  const nearBottom = isNearBottom();
  autoFollow.value = nearBottom;
  showJumpToBottom.value = !nearBottom;
  if (nearBottom) clearIdleResume();
  else armIdleResume();
}

function scrollToBottom(options = {}) {
  const force = options?.force === true;
  const alignToLastToolCard = options?.alignToLastToolCard === true;
  if (!force && !autoFollow.value) {
    showJumpToBottom.value = true;
    return;
  }
  if (scrollRaf) {
    cancelAnimationFrame(scrollRaf);
    scrollRaf = 0;
  }
  scrollRaf = requestAnimationFrame(() => {
    scrollRaf = 0;
    scheduleRaf(() => {
      if (!force && !autoFollow.value) {
        showJumpToBottom.value = true;
        return;
      }
      const viewport = getScrollViewport();
      // When alignToLastToolCard is set, scroll so the latest .rich-tool-card
      // top sits at viewport top minus the standard bottom threshold (96px).
      // This keeps the tool call header visible while the card body extends
      // below the fold, instead of pinning the scroll to the card's bottom
      // (which would hide the header above the viewport).
      if (alignToLastToolCard) {
        const root = messagesRootRef.value;
        const cards = root?.querySelectorAll('.rich-tool-card');
        const target = cards?.length ? cards[cards.length - 1] : null;
        if (target && viewport) {
          const viewportTop = viewport.getBoundingClientRect().top;
          const targetTop = target.getBoundingClientRect().top;
          const delta = targetTop - viewportTop - bottomThreshold;
          scrollbarRef.value?.scrollTo({ top: viewport.scrollTop + delta });
          autoFollow.value = true;
          showJumpToBottom.value = false;
          return;
        }
      }
      // Use a large sentinel value so the browser clamps to the true
      // scrollable bottom. This is robust against content-visibility: auto
      // elements whose contain-intrinsic-size placeholders make scrollHeight
      // smaller than the actual rendered content height.
      scrollbarRef.value?.scrollTo({ top: 999999999 });
      if (viewport) viewport.scrollTop = 999999999;
      autoFollow.value = true;
      showJumpToBottom.value = false;
    });
  });
}

function jumpToBottom() {
  autoFollow.value = true;
  showJumpToBottom.value = false;
  clearIdleResume();
  scrollToBottom({ force: true });
}

// 会话恢复专用：先滚到底，再在 Vue 更新和浏览器布局完成后补两帧。
// 不依赖内容观察器，也不受之前会话的 autoFollow 状态影响。
async function restoreToBottom() {
  autoFollow.value = true;
  showJumpToBottom.value = false;
  clearIdleResume();
  const requestId = ++restoreRequestId;
  await nextTick();
  const apply = () => {
    if (requestId !== restoreRequestId) return;
    const viewport = getScrollViewport();
    if (!viewport) return;
    scrollbarRef.value?.scrollTo({ top: 999999999 });
    viewport.scrollTop = viewport.scrollHeight;
    bottomAnchorRef.value?.scrollIntoView({ block: 'end', behavior: 'auto' });
  };
  apply();
  scheduleRaf(() => {
    apply();
    scheduleRaf(apply);
  });
}

// 供父级在 tool:result 等一次性大内容（如大 diff）注入后调用：
// 先按当前跟随状态滚到底；若一帧后 diff 仍在展开（content-visibility
// 占位导致 scrollHeight 分帧增长）而未真正贴底，则强制再滚一次补平。
// 仅当用户仍处于自动跟随状态时才补滚，尊重手动滚动。
function scrollToBottomIfStale() {
  const viewport = getScrollViewport();
  if (!viewport) return;
  scrollToBottom();
  scheduleRaf(() => {
    if (!autoFollow.value) return;
    if (viewport.scrollHeight - viewport.scrollTop - viewport.clientHeight > bottomThreshold) {
      scrollToBottom({ force: true });
    }
  });
}

function scrollToUserQuestion(direction) {
  const root = messagesRootRef.value;
  if (!root) return;
  const questions = Array.from(root.querySelectorAll('[data-user-question]'));
  if (!questions.length) return;

  const viewport = root.closest('.n-scrollbar-container') || root.parentElement;
  const viewportTop = viewport?.getBoundingClientRect?.().top ?? 0;
  const downThreshold = viewportTop + 12;
  const upThreshold = viewportTop - 12;
  const target = direction === 'down'
    ? questions.find((el) => el.getBoundingClientRect().top > downThreshold)
    : [...questions].reverse().find((el) => el.getBoundingClientRect().top < upThreshold);

  target?.scrollIntoView({ block: 'start', behavior: 'smooth' });
}


defineExpose({ scrollbarRef, scrollToBottom, scrollToUserQuestion, scrollToBottomIfStale, restoreToBottom });
</script>

<style scoped>
.messages-scroll-shell {
  position: relative;
  display: flex;
  flex: 1;
  min-height: 0;
}

.messages-scroll {
  flex: 1;
  min-height: 0;
}

.messages {
  min-height: 100%;
  padding: 18px 28px;
}

.message {
  position: relative;
  margin-bottom: 10px;
}

.message-archive-toggle {
  display: inline-flex;
  align-items: center;
  gap: 10px;
  margin: 4px 0 12px;
  padding: 4px 8px;
  border: none;
  border-radius: 4px;
  background: transparent;
  color: var(--ally-text-muted);
  font-size: 12px;
  cursor: pointer;
  --wails-draggable: no-drag;
}

.message-archive-toggle:hover {
  color: var(--ally-text-body);
  background: var(--ally-state-hover);
}

.user-message-content {
  flex: 1;
  min-width: 0;
}

.user-text-preview {
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}

.user-text-toggle {
  display: inline-flex;
  margin-top: 5px;
  padding: 2px 0;
  border: 0;
  background: transparent;
  color: color-mix(in srgb, var(--ally-accent-bright) 68%, var(--ally-text-muted));
  font-family: var(--ally-ui-font);
  font-size: var(--ally-aux-font-size);
  line-height: 1.5;
  cursor: pointer;
  --wails-draggable: no-drag;
}

.user-text-toggle:hover {
  color: var(--ally-accent-bright);
  text-decoration: underline;
}

.user-text {
  color: var(--ally-accent-bright) !important;
}

.user-text :not(pre) > code {
  color: var(--ally-accent-pale) !important;
}

.skill-chip {
  display: inline-block;
  padding: 1px 8px;
  border-radius: 4px;
  background: color-mix(in srgb, var(--ally-accent) 14%, transparent);
  color: var(--ally-accent-bright);
  font-weight: 600;
  font-size: 14px;
  line-height: 1.7;
}

.skill-user-text {
  margin-top: 4px;
}

.message.user {
  display: flex;
  align-items: flex-start;
  margin-right: -28px;
  margin-left: -28px;
  /* The row bleeds 28px into the gutter on both sides. The bleed is
     deliberate: it puts the question text on the same x as the assistant's
     body text while letting the tint run the full column width. With the
     old hanging rail glyph gone, the left inset is just the rule plus the
     padding it needs to breathe: 2px rule + 26px padding = 28px. */
  padding: 10px 10px 10px 26px;
  /* One accent rule on the leading edge rather than a box drawn around the
     whole row. A 1px border on a full-bleed band reads as a striped block
     and fights the unboxed assistant turns that follow it. */
  /* 用户消息边线比 accent 降一档亮度：消息正文已用 accent-bright 着色，
     再顶一条全亮 accent 竖线会抢占视线。 */
  border-left: 2px solid var(--ally-accent-dim);
  background: var(--ally-state-hover);
}

/* Delete button on user questions: absolutely pinned to the row's top-right,
   never participates in document flow (so it can never wrap to a second row),
   and hidden until the row is hovered. Centering mirrors the proven
   workspace-explorer-icon-btn pattern: inline-flex + center, svg display:block
   with a fixed size, no font-size/line-height that would disturb baseline. */
.user-delete-btn {
  position: absolute;
  top: 50%;
  right: 8px;
  transform: translateY(-50%);
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 22px;
  height: 22px;
  margin: 0;
  padding: 0;
  border: 0;
  color: var(--ally-text-muted);
  background: transparent;
  cursor: pointer;
  opacity: 0;
  transition: opacity 0.12s ease, color 0.12s ease, background 0.12s ease;
  --wails-draggable: no-drag;
  z-index: 2;
}

.message.user:hover .user-delete-btn,
.user-delete-btn:focus-visible {
  opacity: 1;
}

.user-delete-btn:hover {
  color: #ff6b6b;
  background: rgba(255, 107, 107, 0.12);
}

.user-delete-btn svg {
  display: block;
  width: 13px;
  height: 13px;
}

.thinking-badge {
  display: inline-flex;
  flex: none;
  margin-top: 8px;
}

.message-body {
  padding: 0;
  line-height: 1.7;
  color: var(--ally-text-high);
  font-size: var(--ally-message-font-size, 15.5px);
  background: transparent;
  border: none;
  overflow-wrap: anywhere;
}

.empty-chat {
  display: flex;
  align-items: center;
  justify-content: center;
  min-height: 240px;
}

.jump-controls {
  position: absolute;
  right: 24px;
  bottom: 18px;
  z-index: 5;
  display: flex;
  flex-direction: column;
  gap: 8px;
  align-items: center;
  --wails-draggable: no-drag;
}

.jump-circle-btn {
  padding: 7px 12px;
  border: 1px solid var(--ally-border-strong);
  border-radius: 999px;
  background: var(--ally-copy-btn-bg);
  color: var(--ally-text-high);
  font-size: var(--ally-aux-font-size);
  line-height: 1;
  box-shadow: var(--ally-overlay-shadow);
  cursor: pointer;
  backdrop-filter: var(--ally-glass-blur, blur(10px));
  -webkit-backdrop-filter: var(--ally-glass-blur, blur(10px));
  --wails-draggable: no-drag;
}

.jump-circle-btn:hover {
  color: var(--ally-text-primary);
  background: var(--ally-surface-raised);
  border-color: var(--ally-border-input);
}

.jump-circle-btn svg {
  display: block;
  width: 14px;
  height: 14px;
}

.suggest-row {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  margin-top: 8px;
  margin-bottom: 4px;
}

.suggest-chip {
  display: inline-flex;
  align-items: center;
  padding: 3px 10px;
  border: 1px solid var(--ally-border);
  border-radius: 6px;
  background: transparent;
  color: var(--ally-text-tertiary);
  font-size: var(--ally-aux-font-size, 12px);
  line-height: 1.4;
  cursor: pointer;
  transition: border-color 120ms ease, color 120ms ease;
  user-select: none;
}

.suggest-chip:hover {
  border-color: var(--ally-accent);
  color: var(--ally-accent);
}

.message-duration {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-size: var(--ally-sub-font-size, 13px);
  color: var(--ally-text-faint);
  margin-top: 4px;
}

/* 流式期间的占位行：只占高度，内容不可见 */
.message-duration.is-placeholder {
  visibility: hidden;
}

.duration-text {
  font-variant-numeric: tabular-nums;
}

.run-tokens {
  font-variant-numeric: tabular-nums;
}

.cache-rate {
  font-variant-numeric: tabular-nums;
  padding: 0 5px;
  border-radius: 3px;
  font-size: var(--ally-aux-font-size, 12px);
  line-height: 16px;
}

.export-icon-btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 18px;
  height: 18px;
  padding: 0;
  border: none;
  border-radius: 4px;
  background: transparent;
  color: var(--ally-text-faint);
  cursor: pointer;
  transition: color 0.12s, background 0.12s;
  --wails-draggable: no-drag;
}

.export-icon-btn:hover {
  color: var(--ally-text-high);
  background: var(--ally-hover-strong);
}

.export-icon-btn svg {
  display: block;
  width: 14px;
  height: 14px;
}
</style>
