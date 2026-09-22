/*
 * SPDX-License-Identifier: GPL-3.0-only
 *
 * Copyright (C) 2026 tangssst <tangssst@qq.com>
 * GitHub: https://github.com/Bronya0/ally-agent
 *
 * This file is part of ally-agent, licensed under the GNU General
 * Public License v3. See the LICENSE file for details.
 */
export function normalizedLines(text) {
  const lines = String(text || '').replace(/\r\n/g, '\n').split('\n');
  if (lines.length > 1 && lines[lines.length - 1] === '') lines.pop();
  return lines;
}

// formatBytes renders a byte count with a proper unit (B/KB/MB/GB), e.g.
// 50000 -> "48.8 KB". Used by formatHttpToolTitle for the maxBytes limit chip.
export function formatBytes(bytes) {
  const n = Number(bytes);
  if (!Number.isFinite(n) || n <= 0) return '0 B';
  const units = ['B', 'KB', 'MB', 'GB'];
  let i = 0;
  let v = n;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return `${v.toFixed(i ? 1 : 0)} ${units[i]}`;
}

// formatHttpToolTitle renders the title shown on http_request / web_fetch tool
// cards. It surfaces enough of the optional fields (method, timeout, body/json
// presence) that two cards with the same URL but different actual arguments can
// be told apart at a glance, instead of every card collapsing to just the URL.
// The URL is still the first segment; everything else is appended after a
// middle-dot separator so the chip stays compact.
export function formatHttpToolTitle(parsed) {
  if (!parsed || typeof parsed !== 'object') return '';
  const url = String(parsed.url || '').trim();
  if (!url) return '';
  const parts = [url];
  const method = String(parsed.method || '').trim().toUpperCase();
  if (method && method !== 'GET') parts.push(method);
  if (parsed.body) parts.push('body');
  else if (parsed.json !== undefined && parsed.json !== null) parts.push('json');
  if (parsed.saveTo) parts.push(`→ ${parsed.saveTo}`);
  if (parsed.timeout && Number(parsed.timeout) !== 60) {
    parts.push(`${parsed.timeout}s`);
  }
  if (parsed.maxBytes && Number(parsed.maxBytes) !== 262144) {
    parts.push(`≤${formatBytes(Number(parsed.maxBytes))}`);
  }
  return parts.join(' · ');
}

export function codePreviewWindow(code, options = {}) {
  if (!code) {
    return {
      lines: [],
      startLine: 1,
      totalLines: 0,
      omittedBefore: false,
      omittedAfter: false,
    };
  }
  const collapsed = Boolean(options.collapsed);
  const maxLines = Number(options.maxLines || 0);
  const mode = options.mode === 'tail' ? 'tail' : 'head';
  const lines = normalizedLines(code);
  const totalLines = lines.length;

  if (!collapsed || maxLines <= 0 || totalLines <= maxLines) {
    return {
      lines,
      startLine: 1,
      totalLines,
      omittedBefore: false,
      omittedAfter: false,
    };
  }

  if (mode === 'tail') {
    const start = Math.max(0, totalLines - maxLines);
    return {
      lines: lines.slice(start),
      startLine: start + 1,
      totalLines,
      omittedBefore: start > 0,
      omittedAfter: false,
    };
  }

  return {
    lines: lines.slice(0, maxLines),
    startLine: 1,
    totalLines,
    omittedBefore: false,
    omittedAfter: totalLines > maxLines,
  };
}


export function isRenderableMessage(msg) {
  if (msg?.role === 'tool_call' && msg?.kind === 'run') return false;
  if (msg?.role !== 'assistant') return true;
  return !assistantRowRenderState(msg).blank;
}

// 思考浮层是否已收起（行内那行 "Thinking" 不再显示）。行内 class 与“空壳行”
// 判定共用这一处，避免两处各写一遍条件后静默跑偏。
export function isReasoningHidden(msg) {
  if (msg?.reasoningEndedAt) return true;
  return !(msg?.reasoningChars > 0 || msg?.reasoningStartedAt);
}

// 一根 assistant 行“会不会渲染出东西”的全部输入，收口在这里：
//   key   —— 显示列表缓存的失效判据（这些字段一变，缓存过的列表就得重算）；
//   empty —— 此刻一个像素都渲染不出来：没有正文/附件/建议/本轮统计，思考也已收起；
//   blank —— 渲染不出东西且已封口（不流式、done）：连“这是一条消息”都不算。
// 三处必须共用这一份字段清单：漏一个字段，缓存过的列表就会跟真实渲染静默分叉。
// 刻意只读终态/一次性字段，**不把正文放进 key**：正文由子组件按增量渲染，父级
// 渲染 effect 一旦订阅它，每个流式增量都要重算整张列表。
// 空壳行不只是白占 DOM：`.message` 上的 content-visibility 会让 0 高行不再自折叠
// 外边距，一根就把下面的内容顶下去一行间距（多步工具批次前叠几根就是折叠组上方
// 那一大片空白，实测 5 根 = 56px）。
export function assistantRowRenderState(msg) {
  if (msg?.role !== 'assistant') return { key: '', blank: false, empty: false };
  const streaming = msg.streaming === true;
  const done = msg.done === true;
  const hasBody = msg.hasBody === true;
  const error = !!msg.error;
  const system = !!msg.system;
  const welcome = !!msg.welcome;
  const reasoningLive = !isReasoningHidden(msg);
  const attachments = Array.isArray(msg.attachments) ? msg.attachments.length : 0;
  const suggestions = Array.isArray(msg.suggestions) ? msg.suggestions.length : 0;
  // 本轮统计行（时长/cache/tokens/导出按钮）就挂在这根行上，悬停可见，不能藏
  const roundStats = !!msg.roundDurationText;
  const key = [streaming, done, hasBody, error, system, welcome, reasoningLive, attachments, suggestions, roundStats].join(',');
  // 正文兜底读一把：老快照里存在“有正文但没有 hasBody 标志”的行（实测 5730 条），
  // 少这一道就会把真消息藏掉。它不进 key：能写正文的地方都同步置 hasBody，
  // 两者的翻转必然同帧发生（已定稿的行的正文不会再变）。
  const empty = !error && !system && !welcome && !hasBody && !reasoningLive
    && !attachments && !suggestions && !roundStats
    && String(msg.content || '').trim() === '';
  // blank 比 empty 多两道闸门：只有已封口的空壳行才连消息都不算（显示列表、归档
  // 额度与计数、运行时/落盘保留共用这一条）。运行中尚未封口的空壳行仍是一条消息，
  // 只是此刻渲染不出东西 —— 那种行样式上要退出尺寸估计（见 .is-empty-row），否则
  // 浏览器把它当离屏内容时会拿 120px 估计值占位。
  const blank = empty && !streaming && done;
  return { key, blank, empty };
}

export function displaySourceMessages(session, expandedArchiveSessions, options = {}) {
  const src = (session?.messages || []).filter(isRenderableMessage);
  if (!session) return src;
  const maxMessages = Number(options.maxMessages || 180);
  const expandedSet = expandedArchiveSessions || new Set();
  const expanded = expandedSet.has(session.id);
  const effectiveMaxMessages = expanded
    ? Number(options.expandedMaxMessages || maxMessages * 2)
    : maxMessages;
  if (src.length <= effectiveMaxMessages) return src;

  const keepStart = Math.max(0, src.length - effectiveMaxMessages);
  const archived = src.slice(0, keepStart);
  const archiveMsg = {
    role: 'archive',
    sessionId: session.id,
    expanded,
    count: archived.length,
  };
  return [archiveMsg, ...src.slice(keepStart)];
}
