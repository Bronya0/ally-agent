/*
 * SPDX-License-Identifier: GPL-3.0-only
 *
 * Copyright (C) 2026 tangssst <tangssst@qq.com>
 * GitHub: https://github.com/Bronya0/ally-agent
 *
 * This file is part of ally-agent, licensed under the GNU General
 * Public License v3. See the LICENSE file for details.
 */
// Cache positions, never message objects: compaction/replacement must release
// old cards. Validate the array slot on every hit so splice, deletion and
// same-length replacement cannot route a late event to a discarded card.
// Bound each array's cache; WeakMap releases it when the array is replaced.
const toolPositions = new WeakMap();
const TOOL_POSITION_CACHE_LIMIT = 512;

function rememberToolPosition(messages, eventId, index) {
  let positions = toolPositions.get(messages);
  if (!positions) {
    positions = new Map();
    toolPositions.set(messages, positions);
  }
  positions.delete(eventId);
  positions.set(eventId, index);
  if (positions.size > TOOL_POSITION_CACHE_LIMIT) {
    positions.delete(positions.keys().next().value);
  }
}

export function findToolEventMessage(session, eventId) {
  if (!session || !eventId || !Array.isArray(session.messages)) return null;
  const messages = session.messages;
  const positions = toolPositions.get(messages);
  const index = positions?.get(eventId);
  const cached = index === undefined ? null : messages[index];
  if (cached?.role === 'tool_call' && cached.eventId === eventId) return cached;
  positions?.delete(eventId);
  const foundIndex = messages.findIndex(
    (item) => item?.role === 'tool_call' && item.eventId === eventId,
  );
  if (foundIndex < 0) return null;
  rememberToolPosition(messages, eventId, foundIndex);
  return messages[foundIndex];
}

export function commitToolEventMessage(session, eventId, existing, payload) {
  if (!session || !eventId || !Array.isArray(session.messages)) return null;
  // Resolve through the validated position cache, including when a caller
  // holds an object from before compaction or an array replacement.
  let target = findToolEventMessage(session, eventId);

  if (target) {
    for (const key of Object.keys(payload)) {
      if (target[key] !== payload[key]) target[key] = payload[key];
    }
  } else {
    target = payload;
    session.messages.push(target);
    rememberToolPosition(session.messages, eventId, session.messages.length - 1);
  }

  return target;
}

// A stage barrier produces a protocol result, not a visible tool failure.
export function isStageBlockedTool(data) {
  return data?.errorCode === 'E_STAGE_BLOCKED';
}

/**
 * Build the stable event id for a tool-call event payload.
 *
 * The id is deterministic from the agent-loop coordinates (runId + step +
 * index) so the same logical call always maps to the same id across the
 * start/update/result/error burst. When those coordinates are missing (e.g.
 * a transient or malformed event) it falls back to toolCallId, then to a
 * time-based id so a card can still be created and later located.
 *
 * This is the single source of truth: App.vue previously kept its own copy,
 * which drifted from the matching logic and hid bugs. Keep it here.
 */
export function toolEventId(data = {}) {
  if (data.runId && data.toolBatchId && data.toolCallIndex !== undefined && data.toolCallIndex !== null) {
    return `${data.runId}:tool:${data.toolBatchId}:${data.toolCallIndex}`;
  }
  if (data.runId && data.toolCallIndex !== undefined && data.toolCallIndex !== null) {
    return `${data.runId}:tool:${data.toolCallIndex}`;
  }
  return data.toolCallId || `${data.name || 'tool'}-${Date.now()}`;
}

/**
 * Locate the tool-card message for an incoming tool event, matching the
 * backend's identity scheme rather than a single id.
 *
 * Matching precedence:
 *   1. Exact eventId (the deterministic id from toolEventId).
 *   2. toolCallId (covers backends that identify calls by an opaque id).
 *   3. runId + toolCallIndex, further disambiguated by toolBatchId (the
 *      agent-loop step) so a result from step N can never land on a same-index
 *      card from step N-1. When neither side carries a batch id, the
 *      runId+index pair already uniquely identifies the call.
 *
 * Kept pure and dependency-free so it can be unit-tested without a Vue runtime.
 */
export function findToolEventByData(session, data = {}) {
  if (!session || !Array.isArray(session.messages)) return null;
  const eventId = toolEventId(data);
  const toolCallId = data.toolCallId || '';
  const toolBatchId = data.toolBatchId || '';
  const hasIndex = data.runId && data.toolCallIndex !== undefined && data.toolCallIndex !== null;
  return session.messages.find((item) => {
    if (item.role !== 'tool_call') return false;
    if (item.eventId === eventId) return true;
    if (toolCallId && (item.eventId === toolCallId || item.toolCallId === toolCallId)) return true;
    if (!hasIndex || item.runId !== data.runId || Number(item.toolCallIndex) !== Number(data.toolCallIndex)) return false;
    if (toolBatchId || item.toolBatchId) return item.toolBatchId === toolBatchId;
    return true;
  }) || null;
}

/**
 * The status vocabulary: canonical state -> every raw string that means it.
 *
 * The backend and older code have sprinkled in aliases — sub-agent records use
 * `completed`/`failed` (and `timed_out` when a budget-exhausted run never produced
 * a report), streaming uses `info`, plan items use `done`/
 * `in_progress`. Declaring them as data (rather than a chain of tests) makes an
 * alias a single row, and lets every consumer that needs its own per-state
 * artifact index the canonical state instead of re-testing the aliases
 * (see ToolStatusIcon).
 */
const STATUS_STATES = {
  success: ['success', 'done', 'completed'],
  error: ['error', 'failed', 'timed_out'],
  running: ['running', 'in_progress', 'info'],
};

const STATUS_ALIASES = new Map();
for (const [state, aliases] of Object.entries(STATUS_STATES)) {
  for (const alias of aliases) STATUS_ALIASES.set(alias, state);
}

/**
 * Canonicalize the ad-hoc status vocabulary into the four states the render
 * layer understands: success | error | running | default.
 *
 * Collapsing the aliases here (instead of at every call site) means the
 * vocabulary can be tightened in one place and the UI never sees an unknown
 * string. Unknown values fall back to `default`, preserving the previous
 * behavior for unrecognized inputs.
 */
export function normalizeToolStatus(status) {
  return STATUS_ALIASES.get(status) || 'default';
}

/**
 * The single entry point for flipping a tool card's status. Normalizes the
 * incoming value so aliases (`completed`/`failed`/`info`/...) collapse to the
 * canonical set, then writes it. Returns `msg` so calls can be chained, and is
 * a no-op (returns the input untouched) when `msg` is falsy so callers do not
 * need a guard.
 *
 * This is the structural fix for the "card stuck in running" class of bugs:
 * there is now exactly one function that writes `status`, so a missed or
 * duplicate flip is impossible to introduce silently.
 */
export function setToolStatus(msg, status) {
  if (!msg) return msg;
  msg.status = normalizeToolStatus(status);
  return msg;
}
