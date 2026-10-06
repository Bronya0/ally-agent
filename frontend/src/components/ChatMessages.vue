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
        <template v-for="(msg, index) in messages" :key="msgKey(msg)" v-memo="messageRenderMemo(msg, index)">
        <button v-if="msg.role === 'archive'" class="message-archive-toggle" @click.stop="$emit('toggleArchive', msg.sessionId)">
          <span>{{ msg.expanded ? $t('chat.archive.collapse') : $t('chat.archive.expand') }}</span>
          <span>{{ $t('chat.archive.summary', { count: msg.count }) }}</span>
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
        <div v-else-if="msg.role === 'notice'" :class="['message', 'notice', msg.kind]">
          <WarningOutlined class="notice-icon" />
          <span class="notice-text">{{ msg.text }}</span>
        </div>
        <div v-else-if="msg.role !== 'tool_call' && msg.kind !== 'subagent'" :class="['message', msg.role, { error: msg.error, system: msg.system }]">
          <RenderBoundary v-if="msg.welcome" :label="$t('chat.welcome')"><WelcomeMessage :welcome="msg.welcome" :tools="tools" :mcp-servers="mcpServers" :skill-names="skillNames" /></RenderBoundary>
          <StreamingMarkdownBody v-else :msg="msg" :render-fn="renderFn" />
          <RenderBoundary :label="$t('chat.attachment')"><MessageAttachments :attachments="msg.attachments || []" /></RenderBoundary>
          <div v-if="msg.role === 'assistant' && msg.suggestions?.length && !msg.streaming" class="suggest-row">
            <button v-for="(label, i) in msg.suggestions" :key="i" class="suggest-chip" @click.stop="$emit('sendSuggest', label)">{{ label }}</button>
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
        <!-- 本轮统计行（时长/cache/tokens/导出/复制）：渲染在本轮收尾消息之后——
             它未必是那条承载数据的 assistant 消息。数据由 run:done 一次性下发、落在
             本轮最后一条 assistant 消息上，但模型可以在同一条回复里「先写正文、再调
             工具」，那一轮的收尾就是工具卡；只把行留在 assistant 消息里，统计行会被
             夹在正文与工具卡中间。中间消息不画占位行，否则会成为后续折叠组上方的
             幽灵间距。正文后面没跟工具时，收尾就是那条 assistant 消息，位置同旧。 -->
        <MessageRoundStats
          v-if="roundStatsOwner(msg, index)"
          class="turn-stats"
          :owner="roundStatsOwner(msg, index)"
          :is-last-answer="roundStatsOwner(msg, index) === lastAnswerMessage"
          :export-options="exportOptions"
          :quick-message-options="quickMessageOptions"
          @export="(key) => $emit('export', key, roundStatsOwner(msg, index))"
          @quick-message="(key) => $emit('quickMessage', key)"
          @copy-summary="copyFinalSummary"
        />
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
import { computed, nextTick, onBeforeUnmount, onMounted, provide, reactive, ref, toRaw, watch } from 'vue';
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
import MessageRoundStats from './MessageRoundStats.vue';
import WarningOutlined from '@vicons/antd/WarningOutlined';
import ArrowUpOutlined from '@vicons/antd/ArrowUpOutlined';
import ArrowDownOutlined from '@vicons/antd/ArrowDownOutlined';
import CloseOutlined from '@vicons/antd/CloseOutlined';
import { useMessage } from 'naive-ui';

const props = defineProps({
  messages: { type: Array, required: true },
  renderFn: { type: Function, required: true },
  fmtK: { type: Function, required: true },
  tools: { type: Array, default: () => [] },
  mcpServers: { type: Array, default: () => [] },
  skillNames: { type: Array, default: () => [] },
  // 当前正在跑的那一轮 id（会话空闲时为空串）。统计占位行按它留位，见
  // roundStatsOwner。
  inFlightRunId: { type: String, default: '' },
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


// 统计行渲染在本轮「收尾」位置，而不是固定在承载数据的那条 assistant 消息里：
// 数据由 run:done 一次性下发、落在本轮最后一条 assistant 消息上（App.vue 的
// assistantMessageForRun），但本轮最后一条消息未必是它——模型可以在同一条回复里
// 先写正文、再调工具，工具卡就成了收尾。按收尾位置渲染，统计行才不会被夹在正文
// 与工具卡之间。一次遍历算出每个 runId 的收尾下标与归属消息，渲染时按下标取。
const runStatsOwners = computed(() => {
  const list = props.messages;
  const ownerByRun = new Map();
  const tailIndexByRun = new Map();
  for (let i = 0; i < list.length; i++) {
    const msg = list[i];
    const runId = msg?.runId;
    if (!runId) continue;
    if (msg.role === 'assistant' && !msg.welcome) ownerByRun.set(runId, msg);
    tailIndexByRun.set(runId, i);
  }
  const owners = new Array(list.length).fill(null);
  for (const [runId, tailIndex] of tailIndexByRun) {
    const owner = ownerByRun.get(runId);
    if (owner) owners[tailIndex] = owner;
  }
  return owners;
});

// 收尾消息该不该画出统计行：要么数据已到（run:done 填过时长），要么这一轮还在
// 跑——那时先画一条不可见的占位行占住高度，数据到达后同一位置变可见（可见性由
// 悬停控制，见文件末尾的 .turn-stats 规则）。
//
// 判据必须是「这一轮有没有在跑」（inFlightRunId），不能只看「正文还在不在流式」：
// 工具执行阶段 assistant 消息已经封口（streaming=false）而本轮并没有结束，按流式
// 判据会撤掉那条占位行。列表是贴底的，尾部凭空少掉 32px，整条内容就跟着往下掉一
// 截；下一轮正文一开始又弹回来——观感就是「内容先往上顶一下，又恢复」。本轮结束
// 时（runId 复位）统计数据已在同一批到达，占位行直接转成有数据的行，高度不变。
function roundStatsOwner(msg, index) {
  const owner = runStatsOwners.value[index];
  if (!owner) return null;
  if (owner.roundDurationText) return owner;
  if (props.inFlightRunId && owner.runId === props.inFlightRunId) return owner;
  return owner.streaming && hasAnswerBody(owner) ? owner : null;
}

// v-memo 用的签名：行内每个字段都来自归属消息，所以签名取它那些字段；收尾消息
// 易主时新旧两条的签名都会变，一次性重渲染到位（v-memo 只比数组值）。
function roundStatsMemo(msg, index) {
  const owner = roundStatsOwner(msg, index);
  if (!owner) return '';
  return [
    owner.roundDurationText || '',
    owner.completedAtText || '',
    owner.cacheRate ?? '',
    owner.cacheHit ?? '',
    owner.cacheMiss ?? '',
    owner.runInputTokens ?? '',
    owner.runOutputTokens ?? '',
    owner.roundDurationMs ?? '',
    owner === lastAnswerMessage.value,
  ].join('|');
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
function messageRenderMemo(msg, index) {
  const attachments = Array.isArray(msg?.attachments) ? msg.attachments : [];
  const lastAttachment = attachments.length ? attachments[attachments.length - 1] : null;
  return [
    msg?.role,
    msg?.id,
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
    // 统计行：内容取自本轮统计归属消息（未必是本条），签名带上它那些字段。
    roundStatsMemo(msg, index),
    msg?.streaming,
    msg?.done,
    msg?.status,
    msg?.error,
    msg?.system,
    msg?.welcome,
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
    if (m?.role !== 'assistant' || m.welcome) continue;
    // 正文不读代理（避免订阅流式增量，见下），但每条消息读一次低频翻转
    // 信号：run 结束时 streaming→false / roundDurationText 落位都发生在这条
    // 消息的正文流完之后，若不订阅它们，本 computed 会停在旧消息上——
    // 多步运行整轮不出现复制按钮、单步运行按钮滞后一轮。
    if (m.streaming) continue;
    void m.roundDurationText;
    // 不读代理上的 content：那会让本 computed 订阅所有 assistant 消息内容，
    // 流式增量把整个列表渲染拖下水。toRaw 绕过代理读原始对象，不建立依赖。
    // 中间步骤的 assistant 消息可能没有正文（只有 tool_calls），必须跳过。
    if (String(toRaw(m)?.content || '').trim()) return m;
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
// 当前跟随目标：'bottom' = 常规贴底；'tool-card' = 停在最新工具卡的卡头（见
// scrollToBottom 的 alignToLastToolCard）。即时贴底只服务于前者——卡头模式下
// 内容继续长高是预期的，把视口拉到最底恰好会把卡头推到视口之上，即那段注释里
// 明确要避免的行为。
let followMode = 'bottom';
let scrollRaf = 0;
// 贴底用的哨兵值：交给浏览器夹到真实底部。content-visibility 的占位会让
// scrollHeight 小于真实内容高度，按 scrollHeight 算反而不准。
const BOTTOM_SENTINEL = 999999999;
function scrollViewportToBottom(viewport) {
  scrollbarRef.value?.scrollTo({ top: BOTTOM_SENTINEL });
  if (viewport) viewport.scrollTop = BOTTOM_SENTINEL;
}
// ── 跟随状态（autoFollow）只有这三条改写路径，别处一律动不了它 ──
//   ① 用户手势向上离开底部 → 关闭，且是粘性的：一次手势就关掉，之后一直关着；
//   ② 用户自己滚回底部 / 点「回到底部」/ 新一轮开始 → 恢复；
//   ③ 会话切换的 restoreToBottom → 恢复。
// 「离底 ≤96px」只服务 ② 的恢复判定，绝不用于重新打开 ① 刚关掉的跟随：
// 向上滚一格 ≈100px，减去消息区底部 18px 留白后离底只剩 ~82px，仍在 96px 判定带内，
// 按位置判会当场把跟随重新打开；紧接着任何一次内容高度变化（收尾整篇重渲染、
// mermaid 异步落位、content-visibility 占位换成实测高度）就把人拽回底部——表现
// 就是「慢滚永远滚不上去，只有滚得特别快才行」。贴底回拽自己产生的 scroll 事件
// 也落在手势窗口内，按位置判同样会自锁。
// 因此这里没有「空闲自动恢复」：那个 8s 定时器是第四条恢复路径，会在用户读历史
// 读到一半时把人再拽走一次。
let userIntentUntil = 0;        // 手势窗口：该时间戳前的 scroll 事件视为用户驱动
let lastScrollTop = 0;          // 上次 scroll 事件的位置，用来判这一下是往上还是往下
let restoreRequestId = 0;
const userIntentWindow = 250;   // 手势后 250ms 内的 scroll 事件按用户处理
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
let contentResizeObserver = null;
let userIntentTarget = null;
// 拖动滚动条 thumb 期间为真：拖动通常远长于 250ms 手势窗口，窗口过期后最后那几帧
// 滚动事件会被当成程序化滚动，慢拖就又会自锁，所以拖住期间一直算用户驱动。
let railDragging = false;

// 贴底的"立即"版本：ResizeObserver 回调跑在 rAF 之后、绘制之前，这里写进去的
// 滚动位置就是这一帧画出来的位置。scrollToBottom() 是刻意延后两帧的（rAF 套
// rAF，用来合并一批调用），凡是"高度在这一帧变了"的场景用它，都会先画出一个没
// 贴底的中间态、下一帧再修正——观感就是内容先跳一下再被拉回来。run 结束那一帧
// 恰好三处高度同时变化（最后一批增量、收尾整篇重渲染、composer 状态行卸载），
// 所以那条路径必须走即时贴底。
//
// 这里不做"是否已贴底"的预判：scrollHeight 在 content-visibility 占位下本就小于
// 真实内容高度，按它算出来的"已贴底"是假的，据此跳过这一次写入就等于漏掉一次贴
// 底。写哨兵值是幂等的（浏览器会夹到真实底部），只写滚动位置、不改任何尺寸，因
// 此也不会自激。
function pinToBottomNow() {
  const viewport = getScrollViewport();
  if (!viewport) return;
  scrollViewportToBottom(viewport);
}

// 消息区底部一旦被布局挤压（plan 面板出现/展开、输入框自动增高、窗口
// resize、Tab 从隐藏切回可见），可用高度变小，最新内容会被推到视口之下，
// 看起来像被遮挡。这类变化不经过任何事件处理器，无法靠逐个补滚动覆盖；
// 这里统一观察滚动视口的尺寸变化：只要用户仍处于自动跟随状态就重新贴底。
function ensureViewportResizeObserver() {
  const viewport = getScrollViewport();
  if (!viewport || viewportResizeObserver) return;
  viewportResizeObserver = new ResizeObserver(() => {
    // 卡头模式同样不介入：视口被挤压时把视口拉到底会把卡头推出视口之上。
    if (!autoFollow.value || followMode !== 'bottom') return;
    pinToBottomNow();
  });
  viewportResizeObserver.observe(viewport);
}

// 观察内容本体（.messages）的尺寸：流式增量、收尾的整篇重渲染、折叠组展开、
// 本轮统计行落位……凡是"消息列表变高变矮"都走这里，在同一帧内完成贴底。
// 工具卡卡头模式下不介入（见 followMode 的说明）。
function ensureContentResizeObserver() {
  const root = messagesRootRef.value;
  if (!root || contentResizeObserver) return;
  contentResizeObserver = new ResizeObserver(() => {
    if (!autoFollow.value || followMode !== 'bottom') return;
    pinToBottomNow();
  });
  contentResizeObserver.observe(root);
}

// 记录一次真实用户手势：只有手势后 250ms 内的 scroll 事件才可能改写跟随状态，
// 且只用于「滚回底部」的恢复判定。程序化滚动不经过这里，所以永远改不了跟随。
function markUserIntent() {
  restoreRequestId += 1;
  userIntentUntil = Date.now() + userIntentWindow;
}

// 「用户要离开底部」的唯一写入口：一次向上手势即关闭跟随，之后保持关闭，直到
// 用户自己滚回底部、点「回到底部」，或新一轮开始（见文件顶部的三条路径）。
function leaveBottom() {
  autoFollow.value = false;
  followMode = 'bottom';
  showJumpToBottom.value = true;
}

const UP_KEYS = new Set(['PageUp', 'ArrowUp', 'Home']);
const DOWN_KEYS = new Set(['PageDown', 'ArrowDown', 'End', ' ']);

function onUserKey(e) {
  // Shift+Space 是向上翻页，与 PageUp / ArrowUp / Home 同类。
  if (UP_KEYS.has(e.key) || (e.key === ' ' && e.shiftKey)) {
    markUserIntent();
    leaveBottom();
    return;
  }
  if (DOWN_KEYS.has(e.key)) markUserIntent();
}

function onUserWheel(e) {
  markUserIntent();
  // 向上滚 = 用户明确要离开底部：立即关跟随，不等 scroll 事件的位置判定。
  if (e.deltaY < 0) leaveBottom();
}

// 拖动 naive-ui 滚动条 rail/thumb 不触发 wheel/touch，这里单独识别：按下即开手势
// 窗口，并在整个拖动期间保持开启，松手后再补一个窗口收尾。
function onShellPointerDown(e) {
  if (!e.target?.closest?.('.n-scrollbar-rail')) return;
  railDragging = true;
  markUserIntent();
  window.addEventListener('pointerup', onRailDragEnd, true);
  window.addEventListener('pointercancel', onRailDragEnd, true);
}

function onRailDragEnd() {
  railDragging = false;
  window.removeEventListener('pointerup', onRailDragEnd, true);
  window.removeEventListener('pointercancel', onRailDragEnd, true);
  markUserIntent();
}

onMounted(() => {
  ensureViewportResizeObserver();
  ensureContentResizeObserver();
  const viewport = getScrollViewport();
  if (viewport) {
    viewport.addEventListener('wheel', onUserWheel, { passive: true });
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
  contentResizeObserver?.disconnect();
  contentResizeObserver = null;
  if (scrollRaf) cancelAnimationFrame(scrollRaf);
  for (const id of pendingRafs) cancelAnimationFrame(id);
  pendingRafs.clear();
  if (railDragging) onRailDragEnd();
  if (userIntentTarget) {
    userIntentTarget.viewport?.removeEventListener('wheel', onUserWheel);
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

// 贴底判定必须用实测位置，不能用 scrollHeight 算术：.messages .message 挂了
// content-visibility: auto，离屏消息只占 contain-intrinsic-size 的占位高度，
// scrollHeight 因此小于真实内容高度，算出来的“离底距离”永远偏小——用户明明已经
// 滚回中部，却会被判成“还在底部”而恢复跟随，随后每一帧内容尺寸变化都把人拽回
// 底部。底部锚点是真实渲染的最后一个盒子，它与视口底的实际距离不受占位影响。
function isNearBottom() {
  const viewport = getScrollViewport();
  if (!viewport) return true;
  const anchor = bottomAnchorRef.value;
  if (anchor) {
    const viewportBottom = viewport.getBoundingClientRect().bottom;
    const distance = anchor.getBoundingClientRect().bottom - viewportBottom;
    return distance <= bottomThreshold;
  }
  return viewport.scrollHeight - viewport.scrollTop - viewport.clientHeight <= bottomThreshold;
}

function handleScroll() {
  const viewport = getScrollViewport();
  if (!viewport) return;
  const top = viewport.scrollTop;
  const movedUp = top < lastScrollTop;
  lastScrollTop = top;
  // 手势窗口之外的 scroll 事件一律视为程序化滚动，完全不改变跟随状态
  // （流式增长、贴底回拽、resize 补滚都落在这里）；拖动滚动条期间例外，
  // 见 railDragging。
  if (!railDragging && Date.now() > userIntentUntil) return;
  // 往上走 = 用户要离开底部：拖动滚动条、触屏拖动、键盘翻页都靠这条兜底。
  if (movedUp) {
    leaveBottom();
    return;
  }
  // 只有「用户自己往下滚、并且已经回到贴底位置」才恢复跟随：位置判据仅此一处使用。
  if (!autoFollow.value && isNearBottom()) {
    autoFollow.value = true;
    followMode = 'bottom';
    showJumpToBottom.value = false;
  }
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
        const target = lastToolCardElement();
        if (target && viewport) {
          const viewportTop = viewport.getBoundingClientRect().top;
          const targetTop = target.getBoundingClientRect().top;
          const delta = targetTop - viewportTop - bottomThreshold;
          scrollbarRef.value?.scrollTo({ top: viewport.scrollTop + delta });
          autoFollow.value = true;
          followMode = 'tool-card';
          showJumpToBottom.value = false;
          return;
        }
      }
      // Use a large sentinel value so the browser clamps to the true
      // scrollable bottom. This is robust against content-visibility: auto
      // elements whose contain-intrinsic-size placeholders make scrollHeight
      // smaller than the actual rendered content height.
      scrollViewportToBottom(viewport);
      autoFollow.value = true;
      followMode = 'bottom';
      showJumpToBottom.value = false;
    });
  });
}

// 末尾那张工具卡的节点：对齐滚动在每个流式刷新（120/400/900ms）都要用一次，
// 而 querySelectorAll 会把整棵消息子树（最多 180 行、上百张卡）都走一遍。卡片与
// 消息块是 .messages 的直接子元素，且正在刷新的那张总在末尾附近，从末尾往回走
// 几步即可（最坏 O(兄弟数)，不再付“扫完整棵子树”的代价）。
function lastToolCardElement() {
  const root = messagesRootRef.value;
  if (!root) return null;
  let el = root.lastElementChild;
  while (el && !el.classList.contains('rich-tool-card')) el = el.previousElementSibling;
  return el;
}

function jumpToBottom() {
  autoFollow.value = true;
  followMode = 'bottom';
  showJumpToBottom.value = false;
  scrollToBottom({ force: true });
}

// 会话恢复专用：先滚到底，再在 Vue 更新和浏览器布局完成后补两帧。
// 不依赖内容观察器，也不受之前会话的 autoFollow 状态影响。
async function restoreToBottom() {
  autoFollow.value = true;
  followMode = 'bottom';
  showJumpToBottom.value = false;
  const requestId = ++restoreRequestId;
  await nextTick();
  const apply = () => {
    if (requestId !== restoreRequestId) return;
    const viewport = getScrollViewport();
    if (!viewport) return;
    scrollViewportToBottom(viewport);
    bottomAnchorRef.value?.scrollIntoView({ block: 'end', behavior: 'auto' });
  };
  apply();
  scheduleRaf(() => {
    apply();
    scheduleRaf(apply);
  });
}

// 新一轮开始即恢复贴底：这是「粘性离开」之外唯一的自动恢复路径（用户自己滚回
// 底部、点「回到底部」、会话切换是另外几条）。inFlightRunId 只在 run:start 落位、
// 同一轮内不变，所以整轮只触发一次；用户正在读历史时不会被后续步骤反复拽走。
watch(() => props.inFlightRunId, (runId) => {
  if (!runId) return;
  autoFollow.value = true;
  followMode = 'bottom';
  showJumpToBottom.value = false;
  scrollToBottom({ force: true });
});

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
    // 同一个 content-visibility 陷阱：这里也必须用锚点实测，不能用 scrollHeight。
    const anchor = bottomAnchorRef.value;
    const stale = anchor
      ? anchor.getBoundingClientRect().bottom - viewport.getBoundingClientRect().bottom > bottomThreshold
      : viewport.scrollHeight - viewport.scrollTop - viewport.clientHeight > bottomThreshold;
    if (stale) scrollToBottom({ force: true });
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

  // 瞬时跳转：这里刻意不做平滑滚动——连点时两次平滑会相互打断，长距离平滑
  // 滚动也很慢，观感像“页面在飘”。下方按钮（jumpToBottom）本来就不传 behavior。
  // 自己跳到某条提问 = 主动离开底部，先关跟随，否则下一次内容高度变化会立刻
  // 把人拽回底部；跳完若恰好落在贴底位置，handleScroll 的位置判据会自行恢复。
  // 没有可跳目标时什么也不做，别白关一次跟随。
  if (!target) return;
  leaveBottom();
  target.scrollIntoView({ block: 'start', behavior: 'auto' });
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

/* 前缀漂移提示（context:drift）：一行内联告警，留在对话流里可回看。
   估计高度按单行给（.messages .message 的 120px 估计值会把离屏的空隙算大）。 */
.message.notice {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 6px 10px;
  border: 1px solid color-mix(in srgb, var(--ally-warning) 35%, transparent);
  border-left: 3px solid var(--ally-warning);
  border-radius: 6px;
  background: color-mix(in srgb, var(--ally-warning) 8%, transparent);
  color: var(--ally-warning-text);
  font-size: 12px;
  line-height: 1.5;
  content-visibility: auto;
  contain-intrinsic-size: auto 32px;
}

.notice-icon {
  flex: 0 0 auto;
  width: 14px;
  height: 14px;
}

.notice-text {
  min-width: 0;
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
  gap: 6px;
  margin-right: -28px;
  margin-left: -28px;
  /* The row bleeds 28px into the gutter on both sides. The bleed is
     deliberate: it puts the question text on the same x as the assistant's
     body text while letting the tint run the full column width. Left inset
     arithmetic with the restored caret: 12px rail + 6px gap + 10px padding
     = 28px. */
  padding: 10px 10px 10px 10px;
  /* 纯色带区分用户轮次：只留 hover 灰底，不再画 accent 竖线 ——
     全宽色带加竖线会读成条纹块，与后面无框的 assistant 轮次打架。 */
  background: var(--ally-state-hover);
}

/* 提问行左侧的琥珀箭头：用户轮次的定位符号（旧版 hanging caret 回归，
   竖线移除后它是行首唯一的强调记号） */
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

/* 统计行的可见性跟着本轮收尾消息走（行的样式表在 MessageRoundStats.vue）：
   鼠标压住它前面那条消息（收尾消息）或统计行这块区域本身时浮出。后者要求
   「统计行自己」是可命中的 —— 行的外框因此始终可见，隐藏的是它的 .stats-inner，
   这里显形也只能显形内容盒；若把 visibility:hidden 留在外框上，外框就不接指针，
   鼠标放到统计行区域什么都不会发生。
   占位行（is-placeholder，这一轮还在跑）不在显形之列：它只占高度。 */
.messages > *:hover + .turn-stats:not(.is-placeholder) :deep(.stats-inner),
.turn-stats:not(.is-placeholder):hover :deep(.stats-inner) {
  visibility: visible;
  opacity: 1;
}
</style>
