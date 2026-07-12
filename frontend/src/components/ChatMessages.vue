<template>
  <div class="messages-scroll-shell">
    <n-scrollbar ref="scrollbarRef" class="messages-scroll" @scroll="handleScroll">
      <div ref="messagesRootRef" class="messages" @click="$emit('clearFocus')">
        <template v-for="(msg, index) in messages" :key="`${index}-${msg.role}-${msg.kind || ''}-${msg.readEntries?.length || 0}`">
        <button v-if="msg.role === 'archive'" class="message-archive-toggle" @click.stop="$emit('toggleArchive', msg.sessionId)">
          <span>{{ msg.expanded ? '收起早期消息' : '展开早期消息' }}</span>
          <span>{{ msg.count }} 条 · 约 {{ fmtK(msg.tokens) }} tokens</span>
        </button>
        <div v-else-if="msg.role === 'user'" :class="['message', msg.role, { error: msg.error }]" data-user-question>
          <div class="message-body user-text markdown-body" v-html="renderFn(msg.content, false)"></div>
          <RenderBoundary label="附件"><MessageAttachments :attachments="msg.attachments || []" /></RenderBoundary>
        </div>
        <div v-else-if="msg.role !== 'tool_call'" :class="['message', msg.role, { error: msg.error, system: msg.system }]">
          <pre v-if="msg.reasoningBody" class="reasoning-body">{{ msg.reasoningBody }}</pre>
          <RenderBoundary v-if="msg.welcome" label="欢迎消息"><WelcomeMessage :welcome="msg.welcome" :tools="tools" /></RenderBoundary>
          <div v-else class="message-body markdown-body" v-html="renderFn(msg.content, msg.streaming)"></div>
          <RenderBoundary label="附件"><MessageAttachments :attachments="msg.attachments || []" /></RenderBoundary>
          <div v-if="msg.role === 'assistant' && msg.roundDurationText && !msg.streaming" class="message-duration">
            本轮耗时 {{ msg.roundDurationText }}
            <span class="duration-sep">·</span>
            <span class="export-btn" @click.stop="$emit('exportOneMsg', msg)" title="导出本条回答">导出回答</span>
            <span class="duration-sep">·</span>
            <span class="export-btn" @click.stop="$emit('exportAllMsgs')" title="导出完整会话">导出会话</span>
          </div>
        </div>
        <RenderBoundary v-else-if="msg.kind === 'ask'" label="交互提问">
          <AskToolCard :msg="msg" @submit="$emit('submitAsk', msg, $event)" />
        </RenderBoundary>
        <!-- Tool call cards -->
        <RenderBoundary
          v-else-if="['edit','create','read','command','calculate','delete','glob','grep','other','todo','scheduled','memory','service','wait','mcp'].includes(msg.kind) && msg.kind !== 'run'"
          label="工具卡"
        >
          <ToolCallCard
            :msg="msg"
            :focused="focusedId === msg.eventId"
            @focus="$emit('focusTool', msg.eventId)"
            @toggle="$emit('toggleTool', msg)"
          />
        </RenderBoundary>
        <!-- Read group card -->
        <RenderBoundary v-else-if="msg.kind === 'read-group'" label="读取结果">
          <ReadGroupCard
            :msg="msg"
            :focused="focusedId === msg.eventId"
            @focus="$emit('focusTool', msg.eventId)"
            @toggle="$emit('toggleTool', msg)"
          />
        </RenderBoundary>
        <!-- Sub-agent -->
        <RenderBoundary v-else-if="msg.kind === 'subagent'" label="子代理状态"><SubagentInlineCard :msg="msg" /></RenderBoundary>
        </template>
        <div v-if="messages.length === 0" class="empty-chat">
          <n-empty description="开始一个任务，或输入 / 选择指令" />
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

function saveScrollPosition() {
  const viewport = getScrollViewport();
  return viewport ? viewport.scrollTop : 0;
}

function restoreScrollPosition(top) {
  if (top == null) return;
  nextTick(() => {
    // Double rAF to let the browser finish layout (content-visibility needs
    // at least one paint frame to resolve real element sizes from placeholders).
    requestAnimationFrame(() => {
      requestAnimationFrame(() => {
        scrollbarRef.value?.scrollTo({ top });
        const viewport = getScrollViewport();
        if (viewport) viewport.scrollTop = top;
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
  content-visibility: auto;
  contain-intrinsic-size: 64px;
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

.message.user::before {
  content: "Ask: ";
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
