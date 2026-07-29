<template>
  <div class="messages-scroll-shell">
    <n-scrollbar ref="scrollbarRef" class="messages-scroll" @scroll="handleScroll">
      <div ref="messagesRootRef" class="messages" @click="$emit('clearFocus')">
        <template v-for="(msg, index) in messages" :key="msgKey(msg)">
        <button v-if="msg.role === 'archive'" class="message-archive-toggle" @click.stop="$emit('toggleArchive', msg.sessionId)">
          <span>{{ msg.expanded ? $t('chat.archive.collapse') : $t('chat.archive.expand') }}</span>
          <span>{{ $t('chat.archive.summary', { count: msg.count, tokens: fmtK(msg.tokens) }) }}</span>
        </button>
        <div v-else-if="msg.role === 'user'" :class="['message', msg.role, { error: msg.error }]" data-user-question>
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
        <div v-else-if="msg.role !== 'tool_call'" :class="['message', msg.role, { error: msg.error, system: msg.system }]">
          <div v-if="msg.reasoningBody" class="reasoning-block">
            <div class="reasoning-header" @click.stop="msg.reasoningExpanded = !msg.reasoningExpanded">
              <span class="reasoning-label">
                <span :class="['reasoning-title', { 'reasoning-title-thinking': !msg.reasoningEndedAt }]">{{ msg.reasoningEndedAt ? 'Thought' : 'Thinking' }}</span>
                <span class="reasoning-tokens">{{ fmtK(Math.max(1, Math.round(String(msg.reasoningBody).length / 3))) }} tokens</span>
              </span>
            </div>
            <pre v-if="msg.reasoningExpanded" class="reasoning-body">{{ msg.reasoningBody }}</pre>
          </div>
          <RenderBoundary v-if="msg.welcome" :label="$t('chat.welcome')"><WelcomeMessage :welcome="msg.welcome" :tools="tools" :mcp-servers="mcpServers" /></RenderBoundary>
          <div v-else class="message-body markdown-body" v-html="renderFn(msg.content, msg.streaming)"></div>
          <RenderBoundary :label="$t('chat.attachment')"><MessageAttachments :attachments="msg.attachments || []" /></RenderBoundary>
          <div v-if="msg.role === 'assistant' && msg.roundDurationText && !msg.streaming" class="message-duration">
            <span class="duration-text">{{ msg.roundDurationText }}</span>
            <span
              v-if="typeof msg.cacheRate === 'number'"
              class="cache-rate"
              :class="cacheRateClass(msg.cacheRate)"
              :title="`cache hit ${msg.cacheHit} / miss ${msg.cacheMiss} (this run)`"
            >cache {{ msg.cacheRate }}%</span>
            <span
              v-if="msg.runInputTokens > 0 || msg.runOutputTokens > 0"
              class="run-tokens"
              :title="`input ${msg.runInputTokens} / output ${msg.runOutputTokens} tokens (this run)`"
            >↑{{ fmtTokens(msg.runInputTokens) }} ↓{{ fmtTokens(msg.runOutputTokens) }}</span>
            <button class="export-icon-btn" @click.stop="$emit('exportOneMsg', msg)" :title="$t('chat.export.responseTitle')" aria-label="export response">
              <svg viewBox="0 0 16 16" width="13" height="13" fill="none" stroke="currentColor" stroke-width="1.2" stroke-linecap="round" stroke-linejoin="round">
                <path d="M8 2v9M4.5 7.5L8 11l3.5-3.5M2.5 13.5h11"/>
              </svg>
            </button>
            <button class="export-icon-btn" @click.stop="$emit('exportAllMsgs')" :title="$t('chat.export.sessionTitle')" aria-label="export session">
              <svg viewBox="0 0 16 16" width="13" height="13" fill="none" stroke="currentColor" stroke-width="1.2" stroke-linecap="round" stroke-linejoin="round">
                <rect x="2.5" y="2" width="9" height="12" rx="1"/>
                <path d="M5 5h4M5 7.5h4M5 10h2.5M11.5 5.5l2 2v6.5h-2"/>
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
import { nextTick, onBeforeUnmount, reactive, ref } from 'vue';
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

defineEmits([
  'toggleArchive',
  'toggleTool',
  'focusTool',
  'clearFocus',
  'exportOneMsg',
  'exportAllMsgs',
  'submitAsk',
]);

function cacheRateClass(rate) {
  if (rate >= 80) return 'cache-high';
  if (rate >= 40) return 'cache-mid';
  return 'cache-low';
}

function fmtTokens(n) {
  const v = Number(n || 0);
  if (v >= 1000) return (v / 1000).toFixed(1) + 'k';
  return String(v);
}

const scrollbarRef = ref(null);
const messagesRootRef = ref(null);
const showJumpToBottom = ref(false);
const autoFollow = ref(true);
const bottomThreshold = 96;
let scrollRaf = 0;
// All pending rAF ids from scrollToBottom + restoreScrollPosition, so that
// onBeforeUnmount can cancel them in one pass. Without this, an unmount
// mid-restore would fire 4 rAFs against a null messagesRootRef and rely on
// the implicit null check inside apply() to no-op — correct, but wasteful
// and easy to break if apply() is ever changed.
const pendingRafs = new Set();
function scheduleRaf(fn) {
  const id = requestAnimationFrame(() => {
    pendingRafs.delete(id);
    fn();
  });
  pendingRafs.add(id);
  return id;
}

onBeforeUnmount(() => {
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

/**
 * Save the current scroll position as an anchor: the index of the topmost
 * visible message element plus its pixel offset from the viewport top.
 *
 * Unlike absolute scrollTop, an anchor is immune to content-visibility
 * placeholder-height drift — when we restore, we scroll the *same element*
 * back into view rather than trusting a pixel offset that was computed
 * against fake placeholder heights.
 */
function saveScrollPosition() {
  const root = messagesRootRef.value;
  const viewport = getScrollViewport();
  if (!root || !viewport) return null;
  const children = root.children;
  if (!children.length) return null;
  const viewportTop = viewport.getBoundingClientRect().top;
  // Find the topmost child whose bottom edge is below the viewport top
  // (i.e. at least partially visible).
  for (let i = 0; i < children.length; i++) {
    const rect = children[i].getBoundingClientRect();
    if (rect.bottom > viewportTop + 1) {
      return { index: i, offset: Math.round(rect.top - viewportTop), scrollTop: viewport.scrollTop };
    }
  }
  return { index: children.length - 1, offset: 0, scrollTop: viewport.scrollTop };
}

/**
 * Restore a previously saved anchor by scrolling the indexed message element
 * back to its recorded offset from the viewport top.
 *
 * Pass 0 restores the absolute scrollTop immediately (before the next paint)
 * so the user never sees the top of the chat. Pass 1 and 2 correct drift
 * caused by content-visibility: auto elements above the target expanding
 * from their placeholder heights to real heights once they scroll near
 * the viewport.
 */
function restoreScrollPosition(anchor) {
  if (!anchor || typeof anchor !== 'object' || anchor.index == null) return;
  // Pass 0: immediate absolute scrollTop restoration — runs before the
  // next paint, eliminating the flash of showing the top of the chat.
  if (anchor.scrollTop != null) {
    const viewport = getScrollViewport();
    if (viewport) viewport.scrollTop = anchor.scrollTop;
  }
  const apply = () => {
    const root = messagesRootRef.value;
    const viewport = getScrollViewport();
    if (!root || !viewport) return;
    const child = root.children[anchor.index];
    if (!child) return;
    const viewportTop = viewport.getBoundingClientRect().top;
    const currentOffset = child.getBoundingClientRect().top - viewportTop;
    const delta = currentOffset - anchor.offset;
    if (Math.abs(delta) < 1) return;
    const target = viewport.scrollTop + delta;
    scrollbarRef.value?.scrollTo({ top: target });
    viewport.scrollTop = target;
  };
  nextTick(() => {
    scheduleRaf(() => {
      scheduleRaf(() => {
        apply();
        // Second pass: content-visibility elements above the target may have
        // expanded after the first scroll, shifting the anchor. Re-correct.
        scheduleRaf(() => {
          scheduleRaf(() => apply());
        });
      });
    });
  });
}

defineExpose({ scrollbarRef, scrollToBottom, scrollToUserQuestion, saveScrollPosition, restoreScrollPosition });
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
  font-size: 12px;
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
  font-size: 15px;
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
  font-size: 12px;
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
  font-size: 12px;
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
  font-size: 11px;
  line-height: 16px;
}
.cache-rate.cache-high { color: #4ade80; }
.cache-rate.cache-mid  { color: #fbbf24; }
.cache-rate.cache-low  { color: #f87171; }

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
