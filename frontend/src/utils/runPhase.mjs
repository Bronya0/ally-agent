/*
 * SPDX-License-Identifier: GPL-3.0-only
 *
 * Copyright (C) 2026 tangssst <tangssst@qq.com>
 * GitHub: https://github.com/Bronya0/ally-agent
 *
 * This file is part of ally-agent, licensed under the GNU General Public
 * License v3. See the LICENSE file for details.
 */
// Composer status row: one label at a time.
//
// A run is a loop (model step -> tools -> model step -> ...), and the row above
// the input used to show a three-dot animation in every one of those phases,
// next to a "Thinking n tokens" label that appeared and disappeared inside the
// same row. Two independently animated elements with no shared optical
// baseline read as clutter, and the dots carried no phase information at all.
// This module reduces the row to a single English label, and only the thinking
// phase gets one. "Prompt Processing" (waiting for the model's first token) and
// "Decoding" (body streaming) name phases that each last for most of a run, so
// they sat in the row for minutes while saying nothing the three bars did not
// already say — and the row is quieter for their absence. Every other phase
// renders an empty string, so at most one shimmering element is ever present.
//
// The remaining label names a wire-level concept (reasoning tokens) and stays
// English regardless of the UI locale, the same convention the tool-card verbs
// follow (see toolVerb.mjs).
//
// Pure and dependency-light on purpose: the machine is unit-testable without a
// Vue runtime, and App.vue only feeds it the runtime events it already handles.

import { fmtTokens } from './format.mjs';
import { toolEventId } from './toolEventState.mjs';

export const RUN_PHASE = {
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

// The row's only wording. The phases themselves stay in the machine: they are
// what hides the label again — a finished thinking burst must not linger into
// the answer body or into tool execution — but none of them has text.
const REASONING_LABEL = 'Reasoning';

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
  return { phase: RUN_PHASE.prompt, running: new Set() };
}

// Advance the machine by one runtime event; unrelated events are ignored, so
// callers can feed every event through without filtering first.
export function applyRunPhaseEvent(state, eventName, payload = {}) {
  if (!state) return state;
  switch (eventName) {
    case 'run:start':
      state.phase = RUN_PHASE.prompt;
      state.running.clear();
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
