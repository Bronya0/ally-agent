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
 */
export function toolCardRenderSignature(msg) {
  if (!msg || msg.role !== 'tool_call') return '';
  const len = (v) => (Array.isArray(v) ? v.length : 0);
  return [
    msg.kind,
    msg.status,
    msg.title,
    msg.body,
    msg.error,
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
    len(msg.batchEntries),
    msg.readLineCount,
    msg.readTotalLines,
    msg.readCount,
    msg.grepCount,
    msg.grepTotalHits,
    msg.listCount,
    msg.listTotalItems,
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
    msg.codeContent ? msg.codeContent.length : 0,
  ].join('|');
}
