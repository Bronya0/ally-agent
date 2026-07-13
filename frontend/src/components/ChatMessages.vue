<template>
  <div class="messages-scroll-shell">
    <n-scrollbar ref="scrollbarRef" class="messages-scroll" @scroll="handleScroll">
      <div ref="messagesRootRef" class="messages" @click="$emit('clearFocus')">
        <template v-for="(msg, index) in messages" :key="`${index}-${msg.role}-${msg.kind || ''}-${msg.readEntries?.length || 0}`">
        <button v-if="msg.role === 'archive'" class="message-archive-toggle" @click.stop="$emit('toggleArchive', msg.sessionId)">
          <span>{{ msg.expanded ? $t('chat.archive.collapse') : $t('chat.archive.expand') }}</span>
          <span>{{ $t('chat.archive.summary', { count: msg.count, tokens: fmtK(msg.tokens) }) }}</span>
        </button>
        <div v-else-if="msg.role === 'user'" :class="['message', msg.role, { error: msg.error }]" data-user-question>
          <span class="user-prefix">{{ $t('chat.askPrefix') }}</span>
          <div class="message-body user-text markdown-body" v-html="renderFn(msg.content, false)"></div>
          <RenderBoundary :label="$t('chat.attachment')"><MessageAttachments :attachments="msg.attachments || []" /></RenderBoundary>
        </div>
        <div v-else-if="msg.role !== 'tool_call'" :class="['message', msg.role, { error: msg.error, system: msg.system }]">
          <pre v-if="msg.reasoningBody" class="reasoning-body">{{ msg.reasoningBody }}</pre>
          <RenderBoundary v-if="msg.welcome" :label="$t('chat.welcome')"><WelcomeMessage :welcome="msg.welcome" :tools="tools" /></RenderBoundary>
          <div v-else class="message-body markdown-body" v-html="renderFn(msg.content, msg.streaming)"></div>
          <RenderBoundary :label="$t('chat.attachment')"><MessageAttachments :attachments="msg.attachments || []" /></RenderBoundary>
          <div v-if="msg.role === 'assistant' && msg.roundDurationText && !msg.streaming" class="message-duration">
            {{ $t('chat.duration', { duration: msg.roundDurationText }) }}
            <span class="duration-sep">·</span>
            <span class="export-btn" @click.stop="$emit('exportOneMsg', msg)" :title="$t('chat.export.responseTitle')">{{ $t('chat.export.response') }}</span>
            <span class="duration-sep">·</span>
            <span class="export-btn" @click.stop="$emit('exportAllMsgs')" :title="$t('chat.export.sessionTitle')">{{ $t('chat.export.session') }}</span>
          </div>
        </div>
        <RenderBoundary v-else-if="msg.kind === 'ask'" :label="$t('chat.ask')">
          <AskToolCard :msg="msg" @submit="$emit('submitAsk', msg, $event)" />
        </RenderBoundary>
        <!-- Tool call cards -->
        <RenderBoundary
          v-else-if="['edit','create','read','command','calculate','delete','glob','grep','other','todo','scheduled','memory','service','wait','mcp'].includes(msg.kind) && msg.kind !== 'run'"
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
        </template>
        <div v-if="messages.length === 0" class="empty-chat">
          <n-empty :description="$t('chat.empty')" />
        </div>
      </div>
    </n-scrollbar>
    <button v-if="showJumpToBottom" class="jump-bottom-btn" @click="jumpToBottom">
      ↓
    </button>
  </div>
</template>

<script setup>
import { nextTick, ref } from 'vue';
import MessageAttachments from './MessageAttachments.vue';
import WelcomeMessage from './WelcomeMessage.vue';
import ToolCallCard from './ToolCallCard.vue';
import AskToolCard from './AskToolCard.vue';
import ReadGroupCard from './ReadGroupCard.vue';
import SubagentInlineCard from './SubagentInlineCard.vue';
import RenderBoundary from './RenderBoundary.vue';

defineProps({
  messages: { type: Array, required: true },
  lastUserMsgIndex: { type: Number, default: -1 },
  focusedId: { type: String, default: '' },
  renderFn: { type: Function, required: true },
  fmtK: { type: Function, required: true },
  tools: { type: Array, default: () => [] },
});

defineEmits([
  'toggleArchive',
  'toggleTool',
  'focusTool',
  'clearFocus',
  'exportOneMsg',
  'exportAllMsgs',
  'submitAsk',
]);

const scrollbarRef = ref(null);
const messagesRootRef = ref(null);
const showJumpToBottom = ref(false);
const autoFollow = ref(true);
const bottomThreshold = 96;
let scrollRaf = 0;

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
  if (!force && !autoFollow.value) {
    showJumpToBottom.value = true;
    return;
  }
  if (scrollRaf) {
    cancelAnimationFrame(scrollRaf);
    scrollRaf = 0;
  }
  scrollRaf = requestAnimationFrame(() => {
    scrollRaf = requestAnimationFrame(() => {
      scrollRaf = 0;
      if (!force && !autoFollow.value) {
        showJumpToBottom.value = true;
        return;
      }
      const viewport = getScrollViewport();
      const bottom = viewport?.scrollHeight || 999999;
      scrollbarRef.value?.scrollTo({ top: bottom });
      if (viewport) viewport.scrollTop = bottom;
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
      return { index: i, offset: Math.round(rect.top - viewportTop) };
    }
  }
  return { index: children.length - 1, offset: 0 };
}

/**
 * Restore a previously saved anchor by scrolling the indexed message element
 * back to its recorded offset from the viewport top.
 *
 * Two correction passes are performed (each a double-rAF apart). The first
 * pass positions the element; the second pass corrects drift caused by
 * content-visibility: auto elements above the target expanding from their
 * placeholder heights to real heights once they scroll near the viewport.
 */
function restoreScrollPosition(anchor) {
  if (!anchor || typeof anchor !== 'object' || anchor.index == null) return;
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
    requestAnimationFrame(() => {
      requestAnimationFrame(() => {
        apply();
        // Second pass: content-visibility elements above the target may have
        // expanded after the first scroll, shifting the anchor. Re-correct.
        requestAnimationFrame(() => {
          requestAnimationFrame(() => apply());
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

.user-text {
  color: #f7b977 !important;
}

.user-text :not(pre) > code {
  color: #b8d7ff !important;
}

.message.user {
  display: flex;
  align-items: flex-start;
  gap: 8px;
}

.user-prefix {
  flex: none;
  margin-top: 6px;
  color: #f7b977;
  font-family: var(--ally-ui-font);
  font-size: 14px;
  font-weight: 600;
  line-height: 1.7;
}

.thinking-badge {
  display: inline-flex;
  flex: none;
  margin-top: 8px;
}

.message-body {
  padding: 6px 0;
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

.jump-bottom-btn {
  position: absolute;
  right: 24px;
  bottom: 18px;
  z-index: 5;
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

.jump-bottom-btn:hover {
  color: #fff;
  background: rgba(55, 55, 55, 0.96);
  border-color: rgba(255, 255, 255, 0.2);
}

.message-duration {
  font-size: 12px;
  color: #737373;
  margin-top: 4px;
}

.duration-sep {
  margin: 0 6px;
  color: #555;
}

.export-btn {
  cursor: pointer;
  color: inherit;
  transition: color 0.12s;
}

.export-btn:hover {
  color: inherit;
  text-decoration: underline;
}
</style>
