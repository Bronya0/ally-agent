/*
 * SPDX-License-Identifier: GPL-3.0-only
 *
 * Copyright (C) 2026 tangssst <tangssst@qq.com>
 * GitHub: https://github.com/Bronya0/ally-agent
 *
 * This file is part of ally-agent, licensed under the GNU General
 * Public License v3. See the LICENSE file for details.
 */
export function findToolEventMessage(session, eventId) {
  if (!session || !eventId || !Array.isArray(session.messages)) return null;
  const cached = session._lastToolEventId === eventId ? session._lastToolMsg : null;
  if (cached && cached.eventId === eventId && session.messages.includes(cached)) return cached;

  const found = session.messages.find(
    (item) => item?.role === 'tool_call' && item.eventId === eventId,
  ) || null;
  session._lastToolEventId = eventId;
  session._lastToolMsg = found;
  return found;
}

export function commitToolEventMessage(session, eventId, existing, payload) {
  if (!session || !eventId || !Array.isArray(session.messages)) return null;
  let target = existing;
  if (!target || target.eventId !== eventId || !session.messages.includes(target)) {
    target = findToolEventMessage(session, eventId);
  }

  if (target) {
    for (const key of Object.keys(payload)) {
      if (target[key] !== payload[key]) target[key] = payload[key];
    }
  } else {
    target = payload;
    session.messages.push(target);
  }

  session._lastToolEventId = eventId;
  session._lastToolMsg = target;
  return target;
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
 * Canonical tool-card status vocabulary.
 *
 * Every status flip on a tool card MUST go through setToolStatus(); never
 * assign `msg.status` directly. Routing all flips through one function is what
 * keeps the card from getting stuck in `running` — there is exactly one place
 * that writes the field, so a forgotten or doubled flip cannot hide in some
 * distant handler.
 */
export const TOOL_STATUS = {
  PENDING: 'pending',
  RUNNING: 'running',
  SUCCESS: 'success',
  ERROR: 'error',
};

/**
 * Canonicalize the ad-hoc status vocabulary into the four values the render
 * layer understands: success | error | running | default.
 *
 * The backend and older code have sprinkled in aliases — sub-agent records use
 * `completed`/`failed`, streaming uses `info`, plan items use `done`/
 * `in_progress`. Collapsing them here (instead of at every call site) means the
 * vocabulary can be tightened in one place and the UI never sees an unknown
 * string. Unknown values fall back to `default`, preserving the previous
 * behavior of normalizeToolStatus for unrecognized inputs.
 */
export function normalizeToolStatus(status) {
  if (status === 'success' || status === 'done' || status === 'completed') return 'success';
  if (status === 'error' || status === 'failed') return 'error';
  if (status === 'running' || status === 'in_progress' || status === 'info') return 'running';
  return 'default';
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
