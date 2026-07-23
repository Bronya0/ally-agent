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
          <span class="user-prefix">{{ $t('chat.askPrefix') }}</span>
          <div v-if="msg.skill" class="message-body user-text">
            <span class="skill-chip">/{{ msg.skill.name }}</span>
            <div v-if="msg.skill.args" class="skill-user-text markdown-body" v-html="renderFn(msg.skill.args, false)"></div>
          </div>
          <div v-else class="message-body user-text markdown-body" v-html="renderFn(msg.content, false)"></div>
          <RenderBoundary :label="$t('chat.attachment')"><MessageAttachments :attachments="msg.attachments || []" /></RenderBoundary>
        </div>
        <div v-else-if="msg.role !== 'tool_call'" :class="['message', msg.role, { error: msg.error, system: msg.system }]">
          <pre v-if="msg.reasoningBody" class="reasoning-body">{{ msg.reasoningBody }}</pre>
          <RenderBoundary v-if="msg.welcome" :label="$t('chat.welcome')"><WelcomeMessage :welcome="msg.welcome" :tools="tools" :mcp-servers="mcpServers" /></RenderBoundary>
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
import { nextTick, onBeforeUnmount, ref } from 'vue';
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
  gap: 8px;
}

.user-prefix {
  flex: none;
  margin-top: 6px;
  color: var(--ally-accent-bright);
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
