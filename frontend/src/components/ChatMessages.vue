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
      <div ref="messagesRootRef" class="messages" @click="$emit('clearFocus')">
        <template v-for="(msg, index) in messages" :key="msgKey(msg)">
        <button v-if="msg.role === 'archive'" class="message-archive-toggle" @click.stop="$emit('toggleArchive', msg.sessionId)">
          <span>{{ msg.expanded ? $t('chat.archive.collapse') : $t('chat.archive.expand') }}</span>
          <span>{{ $t('chat.archive.summary', { count: msg.count }) }}</span>
        </button>
        <div v-else-if="msg.role === 'user'" v-memo="messageRenderMemo(msg)" :class="['message', msg.role, { error: msg.error }]" data-user-question>
          <span class="user-rail" aria-hidden="true">›</span>
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
        </div>
        <div v-else-if="msg.role !== 'tool_call'" v-memo="messageRenderMemo(msg)" :class="['message', msg.role, { error: msg.error, system: msg.system }]">
          <div v-if="msg.reasoningChars > 0 || msg.reasoningStartedAt" class="reasoning-block">
            <div class="reasoning-header">
              <span class="reasoning-label">
                <span :class="['reasoning-title', { 'reasoning-title-thinking': !msg.reasoningEndedAt }]">{{ msg.reasoningEndedAt ? reasoningTitleText(msg) : 'Thinking' }}</span>
                <span class="reasoning-tokens">{{ fmtK(Math.max(1, Math.round((msg.reasoningChars || 0) / 3))) }} tokens</span>
              </span>
            </div>
          </div>
          <RenderBoundary v-if="msg.welcome" :label="$t('chat.welcome')"><WelcomeMessage :welcome="msg.welcome" :tools="tools" :mcp-servers="mcpServers" /></RenderBoundary>
          <div v-else class="message-body markdown-body" v-html="renderFn(msg.content, msg.streaming)"></div>
          <RenderBoundary :label="$t('chat.attachment')"><MessageAttachments :attachments="msg.attachments || []" /></RenderBoundary>
          <div v-if="msg.role === 'assistant' && msg.roundDurationText && !msg.streaming" class="message-duration">
            <span class="duration-text">{{ msg.roundDurationText }}</span>
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
            <button class="export-icon-btn" @click.stop="$emit('exportOneMsg', msg)" :title="$t('chat.export.responseTitle')" aria-label="export response">
              <svg viewBox="0 0 16 16" width="13" height="13" fill="none" stroke="currentColor" stroke-width="1.35" stroke-linecap="round" stroke-linejoin="round">
                <path d="M3 13c.8-4.8 3.6-8 9-9.5M8.5 2.5 12 3.5 11 7"/>
              </svg>
            </button>
            <button class="export-icon-btn" @click.stop="$emit('exportAllMsgs')" :title="$t('chat.export.sessionTitle')" aria-label="export session">
              <svg viewBox="0 0 16 16" width="15" height="15" fill="none" stroke="currentColor" stroke-width="1.35" stroke-linecap="round" stroke-linejoin="round">
                <path d="M1.5 10.5C2 6.6 4.4 4.1 8.2 3M5.2 2.3l3.5.5-1 3.4"/>
                <path d="M5.5 14c.5-3.9 2.9-6.4 6.7-7.5M9.2 5.8l3.5.5-1 3.4"/>
              </svg>
            </button>
          </div>
        </div>
        <RenderBoundary v-else-if="msg.kind === 'ask'" :label="$t('chat.ask')">
          <AskToolCard :msg="msg" @submit="$emit('submitAsk', msg, $event)" />
        </RenderBoundary>
        <!-- Tool call cards -->
        <RenderBoundary
          v-else-if="!['run','read-group','subagent','render_html'].includes(msg.kind)"
          :label="$t('chat.toolCard')"
        >
          <ToolCallCard
            :msg="msg"
            :focused="focusedId === msg.eventId"
            @focus="$emit('focusTool', msg.eventId)"
            @toggle="$emit('toggleTool', msg)"
          />
        </RenderBoundary>
        <!-- Read group card -->
        <RenderBoundary v-else-if="msg.kind === 'read-group'" :label="$t('chat.readResult')">
          <ReadGroupCard
            :msg="msg"
            :focused="focusedId === msg.eventId"
            @focus="$emit('focusTool', msg.eventId)"
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
      </div>
    </n-scrollbar>
    <div v-if="showJumpToBottom" class="jump-controls">
      <button class="jump-circle-btn" :title="$t('composer.question.previous')" @click="scrollToUserQuestion('up')">
        ↑
      </button>
      <button class="jump-circle-btn" @click="jumpToBottom">
        ↓
      </button>
    </div>
  </div>
</template>

<script setup>
import { onBeforeUnmount, onMounted, reactive, ref } from 'vue';
import { t } from '../i18n.mjs';
import MessageAttachments from './MessageAttachments.vue';
import WelcomeMessage from './WelcomeMessage.vue';
import ToolCallCard from './ToolCallCard.vue';
import AskToolCard from './AskToolCard.vue';
import ReadGroupCard from './ReadGroupCard.vue';
import SubagentInlineCard from './SubagentInlineCard.vue';
import HtmlRenderCard from './HtmlRenderCard.vue';
import RenderBoundary from './RenderBoundary.vue';

defineProps({
  messages: { type: Array, required: true },
  focusedId: { type: String, default: '' },
  renderFn: { type: Function, required: true },
  fmtK: { type: Function, required: true },
  tools: { type: Array, default: () => [] },
  mcpServers: { type: Array, default: () => [] },
});

const expandedUserMessages = reactive(new WeakSet());
const userMessageStatsCache = new WeakMap();
const USER_MESSAGE_COLLAPSE_CHAR_LIMIT = 800;
const USER_MESSAGE_COLLAPSE_LINE_LIMIT = 10;
const USER_MESSAGE_PREVIEW_CHAR_LIMIT = 400;
const USER_MESSAGE_PREVIEW_LINE_LIMIT = 6;

function userMessageText(msg) {
  return String(msg?.skill ? msg.skill.args || '' : msg?.content || '');
}

// Keep historical user/assistant subtrees out of the patch path while the
// active assistant message streams. Every value used by these two branches
// that can change during a run is represented here, including user expansion
// state and generated-image attachment updates.
function messageRenderMemo(msg) {
  const attachments = Array.isArray(msg?.attachments) ? msg.attachments : [];
  const lastAttachment = attachments.length ? attachments[attachments.length - 1] : null;
  return [
    msg?.role,
    msg?.content,
    msg?.reasoningChars,
    msg?.reasoningStartedAt,
    msg?.reasoningEndedAt,
    msg?.streaming,
    msg?.done,
    msg?.error,
    msg?.system,
    msg?.welcome,
    msg?.roundDurationText,
    msg?.cacheRate,
    msg?.cacheHit,
    msg?.cacheMiss,
    msg?.runInputTokens,
    msg?.runOutputTokens,
    attachments.length,
    lastAttachment?.previewUrl,
    lastAttachment?.dataUrl,
    lastAttachment?.partial,
    msg?.role === 'user' ? isUserMessageExpanded(msg) : false,
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

// Format the thinking-phase duration for the "Thought for Xs" label.
// Returns '' when the thinking window hasn't closed yet or is invalid.
// 自适应格式：<1s / Ns / Nm / Nm Ns / Nh Nm，与工具卡 durationText 一致。
function reasoningDurationText(msg) {
  const start = Number(msg?.reasoningStartedAt || 0);
  const end = Number(msg?.reasoningEndedAt || 0);
  if (!start || !end || end < start) return '';
  const ms = end - start;
  if (ms < 1000) return '1s';
  const secs = Math.round(ms / 1000);
  const hours = Math.floor(secs / 3600);
  const mins = Math.floor((secs % 3600) / 60);
  const rest = secs % 60;
  if (hours > 0) return `${hours}h${mins > 0 ? `${mins}m` : ''}`;
  if (mins > 0) return `${mins}m${rest > 0 ? `${rest}s` : ''}`;
  return `${secs}s`;
}

// Builds the "Thought for Xs" label shown once the thinking window closes.
// Falls back to just "Thought" when timing wasn't captured.
function reasoningTitleText(msg) {
  const duration = reasoningDurationText(msg);
  return duration ? `Thought for ${duration}` : 'Thought';
}

defineEmits([
  'toggleArchive',
  'toggleTool',
  'focusTool',
  'clearFocus',
  'exportOneMsg',
  'exportAllMsgs',
  'submitAsk',
]);

function fmtTokens(n) {
  const v = Number(n || 0);
  if (v >= 1e9) return (v / 1e9).toFixed(2) + 'B';
  if (v >= 1e6) return (v / 1e6).toFixed(2) + 'M';
  if (v >= 1e3) return (v / 1e3).toFixed(1) + 'k';
  return String(v);
}

const scrollbarRef = ref(null);
const messagesRootRef = ref(null);
const showJumpToBottom = ref(false);
const autoFollow = ref(true);
const bottomThreshold = 96;
let scrollRaf = 0;
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

// 消息区底部一旦被布局挤压（todo 面板出现/展开、输入框自动增高、窗口
// resize、Tab 从隐藏切回可见），可用高度变小，最新内容会被推到视口之下，
// 看起来像被遮挡。这类变化不经过任何事件处理器，无法靠逐个补滚动覆盖；
// 这里统一观察滚动视口的尺寸变化：只要用户仍处于自动跟随状态就重新贴底。
function ensureViewportResizeObserver() {
  const viewport = getScrollViewport();
  if (!viewport || viewportResizeObserver) return;
  viewportResizeObserver = new ResizeObserver(() => {
    if (!autoFollow.value || showJumpToBottom.value) return;
    scrollToBottom();
  });
  viewportResizeObserver.observe(viewport);
}

onMounted(() => {
  ensureViewportResizeObserver();
});

onBeforeUnmount(() => {
  viewportResizeObserver?.disconnect();
  viewportResizeObserver = null;
  if (scrollRaf) cancelAnimationFrame(scrollRaf);
  for (const id of pendingRafs) cancelAnimationFrame(id);
  pendingRafs.clear();
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

function updateAutoFollow() {
  const nearBottom = isNearBottom();
  autoFollow.value = nearBottom;
  showJumpToBottom.value = !nearBottom;
}

function handleScroll() {
  updateAutoFollow();
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
  scrollToBottom({ force: true });
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


defineExpose({ scrollbarRef, scrollToBottom, scrollToUserQuestion });
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
  color: #8a8a8a;
  font-size: 12px;
  cursor: pointer;
  --wails-draggable: no-drag;
}

.message-archive-toggle:hover {
  color: #d4d4d4;
  background: rgba(255, 255, 255, 0.05);
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
  color: color-mix(in srgb, var(--ally-accent-bright) 68%, #8a8a8a);
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
  gap: 6px;
  margin-right: -28px;
  margin-left: -28px;
  /* The row extends 28px into the message gutter. Keep the rail hanging left
     while aligning the question text with the normal body line:
     1px border + 9px padding + 12px rail + 6px gap = 28px. */
  padding: 10px 10px 10px 9px;
  border: 1px solid rgba(255, 255, 255, 0.06);
  border-radius: 0;
  background: rgba(255, 255, 255, 0.07);
}

.user-rail {
  flex: none;
  width: 12px;
  color: var(--ally-accent);
  font-family: var(--ally-ui-font);
  font-size: 15px;
  font-weight: 600;
  line-height: 1.7;
  text-align: center;
  opacity: 0.85;
}

.thinking-badge {
  display: inline-flex;
  flex: none;
  margin-top: 8px;
}

.message-body {
  padding: 0;
  line-height: 1.7;
  color: #e5e5e5;
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
  border: 1px solid rgba(255, 255, 255, 0.12);
  border-radius: 999px;
  background: rgba(38, 38, 38, 0.92);
  color: #e5e5e5;
  font-size: var(--ally-aux-font-size);
  line-height: 1;
  box-shadow: 0 8px 24px rgba(0, 0, 0, 0.28);
  cursor: pointer;
  backdrop-filter: blur(10px);
  --wails-draggable: no-drag;
}

.jump-circle-btn:hover {
  color: #fff;
  background: rgba(55, 55, 55, 0.96);
  border-color: rgba(255, 255, 255, 0.2);
}

.message-duration {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-size: var(--ally-sub-font-size, 13px);
  color: #737373;
  margin-top: 4px;
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
  color: #6a6a6a;
  cursor: pointer;
  transition: color 0.12s, background 0.12s;
  --wails-draggable: no-drag;
}

.export-icon-btn:hover {
  color: #e5e5e5;
  background: rgba(255, 255, 255, 0.08);
}

.export-icon-btn svg {
  display: block;
}
</style>
