/*
 * SPDX-License-Identifier: GPL-3.0-only
 *
 * Copyright (C) 2026 tangssst <tangssst@qq.com>
 * GitHub: https://github.com/Bronya0/ally-agent
 *
 * This file is part of ally-agent, licensed under the GNU General
 * Public License v3. See the LICENSE file for details.
 */
import { formatBytes } from './format.mjs';

export function normalizedLines(text) {
  const lines = String(text || '').replace(/\r\n/g, '\n').split('\n');
  if (lines.length > 1 && lines[lines.length - 1] === '') lines.pop();
  return lines;
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
  return !assistantRowRenderState(msg).empty;
}

// 决定 assistant 行是否有可显示内容的字段收口于此：key 驱动显示列表缓存失效，
// empty 决定消息是否进入正文列表、归档额度与落盘保留。思考计数只在 composer 显示，
// 因而不属于消息行状态。
export function assistantRowRenderState(msg) {
  if (msg?.role !== 'assistant') return { key: '', empty: false };
  const hasBody = msg.hasBody === true;
  const error = !!msg.error;
  const system = !!msg.system;
  const welcome = !!msg.welcome;
  const attachments = Array.isArray(msg.attachments) ? msg.attachments.length : 0;
  const suggestions = Array.isArray(msg.suggestions) ? msg.suggestions.length : 0;
  // 本轮统计行（时长/cache/tokens/导出按钮）就挂在这根行上，悬停可见，不能藏
  const roundStats = !!msg.roundDurationText;
  const key = [hasBody, error, system, welcome, attachments, suggestions, roundStats].join(',');
  // 正文兜底读一把：老快照里存在“有正文但没有 hasBody 标志”的行，不能误隐藏；
  // 正文不进 key，写入正文的地方会同步置 hasBody，避免父级订阅每个流式增量。
  const empty = !error && !system && !welcome && !hasBody
    && !attachments && !suggestions && !roundStats
    && String(msg.content || '').trim() === '';
  return { key, empty };
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
