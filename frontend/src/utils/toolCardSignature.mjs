/*
 * SPDX-License-Identifier: GPL-3.0-only
 *
 * Copyright (C) 2026 tangssst <tangssst@qq.com>
 * GitHub: https://github.com/Bronya0/ally-agent
 *
 * This file is part of ally-agent, licensed under the GNU General
 * Public License v3. See the LICENSE file for details.
 */

/**
 * Render signature for a tool card.
 *
 * ChatMessages keys its v-for with v-memo; the memo tuple must change whenever
 * the rendered card can change, otherwise Vue reuses the stale vnode and the
 * card appears frozen (e.g. stuck on "Reading" after the tool result flips
 * status to success). Keeping every render-affecting field in one place means
 * a new field only needs adding here once, and toolCardSignature.test.mjs
 * asserts the signature changes on a status transition so the trap is caught
 * in tests instead of in production.
 *
 * Non-tool messages return an empty string so they add nothing to the memo.
 *
 * 内容类的大字段（body / codeContent）只以**形状指纹**进签名，不整段拼进来。
 * 签名每拍都要算一次（ChatMessages 的 v-memo，以及 App.vue 那个跨整段会话的
 * buildDisplayMessagesSignature），而 body 是流式累积出来的完整输出，可以到几 MB：
 * 把它拼进签名字符串等于每拍给整段会话的文本做一次 O(n) 拷贝，主线程被这件事
 * 占满，卡片旁边的动画（工具卡上那个运行圆点）就跟着掉帧。
 * 指纹保留了「每一拍的内容变化都会改签名」，成本却是 O(1)。
 */
// 长文本的廉价指纹：长度 + 首尾片段。V8 的 slice 返回 SlicedString，不复制内容；
// 短文本直接带全文，避免任何碰撞。
function contentShape(text) {
  const src = String(text || '');
  if (!src) return '0';
  if (src.length <= 64) return `${src.length}:${src}`;
  return `${src.length}:${src.slice(0, 32)}:${src.slice(-32)}`;
}

export function toolCardRenderSignature(msg) {
  if (!msg || msg.role !== 'tool_call') return '';
  const len = (v) => (Array.isArray(v) ? v.length : 0);
  return [
    msg.kind,
    msg.status,
    // The verb is keyed by the action for scheduled_task / service / plan, so an
    // action that arrives after the card renders must change the tuple or the
    // card keeps the verb it showed first.
    msg.toolAction,
    msg.title,
    contentShape(msg.body),
    msg.error,
    // 沙箱拒绝同时决定状态标记与那行醒目报错：漏掉它 v-memo 会把卡片冻在绿色 √ 上。
    msg.sandboxDenied,
    msg.expanded,
    msg.eventId,
    msg.toolCallId,
    msg.mcpServer,
    msg.mcpTool,
    msg.validation,
    msg.chip,
    msg.askReady,
    msg.askSubmitted,
    len(msg.askQuestions),
    len(msg.editEntries),
    len(msg.deleteEntries),
    // 工具结果渲染成卡片网格（CardGrid）时，条目数是渲染内容本身：结果到达后条目
    // 从无到有，漏掉这条备忘录就把卡片冻在空态上。
    len(msg.cardGrid?.items),
    len(msg.batchEntries),
    msg.readLineCount,
    msg.readTotalLines,
    msg.readCount,
    msg.grepCount,
    msg.grepTotalHits,
    msg.listCount,
    msg.listTotalItems,
    // 截图卡正体是图像本体：dataUrl 从无到有（结果到达）必须翻签名，否则
    // v-memo 把卡片冻在 running 态。只记有无与长度，不整段拼 base64。
    msg.screenshotDataUrl ? msg.screenshotDataUrl.length : 0,
    msg.durationText,
    msg.subagentId,
    msg.subagentRole,
    msg.description,
    msg.summary,
    msg.steps,
    len(msg.filesRead),
    len(msg.filesEdited),
    msg.editFilePath || '',
    msg.editAdded || 0,
    msg.editRemoved || 0,
    contentShape(msg.codeContent),
  ].join('|');
}
