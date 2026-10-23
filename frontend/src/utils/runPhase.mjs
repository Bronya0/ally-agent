/*
 * SPDX-License-Identifier: GPL-3.0-only
 *
 * Copyright (C) 2026 tangssst <tangssst@qq.com>
 * GitHub: https://github.com/Bronya0/ally-agent
 *
 * This file is part of ally-agent, licensed under the GNU General Public
 * License v3. See the LICENSE file for details.
 */
// Composer status row: one label at a time. MCP discovery carries localized
// waiting text and server names in App.vue; reasoning carries Thinking + tokens.
// Prompt processing, body streaming, and tool execution keep the row quiet.
// State is per session and transient, so a background Tab cannot change the
// active Tab's status. This pure machine stays testable without Vue.

import { fmtTokens } from './format.mjs';
import { toolEventId } from './toolEventState.mjs';

export const RUN_PHASE = {
  // First request waits for configured MCP servers to discover their tools.
  mcp: 'mcp',
  // Waiting for the model's first token of this step (prefill /
  // time-to-first-token). Every step of a tool loop passes through it again.
  prompt: 'prompt',
  // Thinking tokens are streaming.
  reasoning: 'reasoning',
  // The answer body is streaming.
  decoding: 'decoding',
  // At least one tool card is running.
  tools: 'tools',
};

// Reasoning wording is shared here; the MCP phase uses App.vue's i18n labels.
const REASONING_LABEL = 'Thinking';

// The single string for the row's label slot. It is one string rather than the
// word/number span pair it used to be, because the whole label now shares one
// shimmer (see .composer-run-label in style.css) and splitting it would paint
// two gradients at once.
//
// The token count rides along only while actually reasoning: it is estimated
// from streamed characters (reasoningChars / 3), so a stale or zero count would
// otherwise decorate a phase that is not thinking.
export function phaseLabel(phase, reasoningTokens = 0) {
  if (phase !== RUN_PHASE.reasoning) return '';
  if (!(Number(reasoningTokens) > 0)) return REASONING_LABEL;
  return `${REASONING_LABEL} ${fmtTokens(reasoningTokens)} tokens`;
}

// Per-session phase state. `running` holds the tool calls of the current run
// that started but have not finished, so a batch finishing one call at a time
// does not report the step as waiting for the model while siblings still run.
export function createRunPhaseState() {
  return { phase: RUN_PHASE.prompt, running: new Set(), waitingServers: [], mcpTimeoutSeconds: 0 };
}

// Advance the machine by one runtime event; unrelated events are ignored, so
// callers can feed every event through without filtering first.
export function applyRunPhaseEvent(state, eventName, payload = {}) {
  if (!state) return state;
  switch (eventName) {
    case 'run:start':
      state.phase = RUN_PHASE.prompt;
      state.running.clear();
      state.waitingServers = [];
      state.mcpTimeoutSeconds = 0;
      break;
    case 'run:mcp-wait':
      state.phase = payload.state === 'waiting' ? RUN_PHASE.mcp : RUN_PHASE.prompt;
      state.waitingServers = payload.state === 'waiting' && Array.isArray(payload.servers)
        ? payload.servers.map(String) : [];
      state.mcpTimeoutSeconds = Math.max(0, Number(payload.timeoutSeconds) || 0);
      break;
    case 'run:stream':
    case 'run:delta':
    case 'run:reasoning': {
      // The merged run:stream payload may carry content, reasoning or both; a
      // content delta means the body is being written, which is past thinking.
      // Once the body has started, the row never goes back to thinking: a
      // provider that interleaves a late reasoning delta would otherwise flip
      // the label mid-answer (the token counter keeps its own reasoningActive
      // flag, so the two would even disagree).
      const hasContent = Boolean(payload.content) || eventName === 'run:delta';
      const hasReasoning = Boolean(payload.reasoning) || Number(payload.reasoningLen) > 0
        || eventName === 'run:reasoning';
      if (hasContent) state.phase = RUN_PHASE.decoding;
      else if (hasReasoning && state.phase !== RUN_PHASE.decoding) state.phase = RUN_PHASE.reasoning;
      break;
    }
    case 'tool:start':
    case 'tool:update':
      // Cards appear (already marked running) while the model is still
      // streaming the call arguments: the backend sets them up as soon as the
      // tool name is known. Reporting tool execution from that moment matches
      // what is on screen.
      state.running.add(toolEventId(payload));
      state.phase = RUN_PHASE.tools;
      break;
    case 'tool:result':
    case 'tool:error':
      // A finished call seals the assistant message of this step; when no
      // sibling call is left, the loop is back to waiting for the next request.
      state.running.delete(toolEventId(payload));
      state.phase = state.running.size > 0 ? RUN_PHASE.tools : RUN_PHASE.prompt;
      break;
    default:
      break;
  }
  return state;
}
