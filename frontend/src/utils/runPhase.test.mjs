/*
 * SPDX-License-Identifier: GPL-3.0-only
 *
 * Copyright (C) 2026 tangssst <tangssst@qq.com>
 * GitHub: https://github.com/Bronya0/ally-agent
 *
 * This file is part of ally-agent, licensed under the GNU General Public
 * License v3. See the LICENSE file for details.
 */
import assert from 'node:assert/strict';
import test from 'node:test';
import {
  RUN_PHASE,
  applyRunPhaseEvent,
  createRunPhaseState,
  phaseLabel,
} from './runPhase.mjs';

function drive(events) {
  const state = createRunPhaseState();
  for (const [name, payload] of events) applyRunPhaseEvent(state, name, payload);
  return state;
}

test('a run opens with no label', () => {
  const state = drive([['run:start', { sessionId: 's1' }]]);
  assert.equal(state.phase, RUN_PHASE.prompt);
  assert.equal(phaseLabel(state.phase, 0), '');
});

test('the merged stream event routes reasoning and content', () => {
  const state = createRunPhaseState();
  applyRunPhaseEvent(state, 'run:stream', { sessionId: 's1', reasoning: 'hmm' });
  assert.equal(state.phase, RUN_PHASE.reasoning);
  applyRunPhaseEvent(state, 'run:stream', { sessionId: 's1', reasoningLen: 20 });
  assert.equal(state.phase, RUN_PHASE.reasoning, 'length-only deltas are still reasoning');
  applyRunPhaseEvent(state, 'run:stream', { sessionId: 's1', content: 'answer' });
  assert.equal(state.phase, RUN_PHASE.decoding, 'the first body delta ends thinking');
  applyRunPhaseEvent(state, 'run:stream', { sessionId: 's1', reasoning: 'x', content: 'y' });
  assert.equal(state.phase, RUN_PHASE.decoding, 'a payload carrying both is writing the body');
});

test('legacy single-field delta events map to their own phase', () => {
  const state = createRunPhaseState();
  applyRunPhaseEvent(state, 'run:reasoning', { reasoning: 'x' });
  assert.equal(state.phase, RUN_PHASE.reasoning);
  applyRunPhaseEvent(state, 'run:delta', { content: 'x' });
  assert.equal(state.phase, RUN_PHASE.decoding);
});

test('empty deltas leave the row waiting for the model', () => {
  const state = createRunPhaseState();
  applyRunPhaseEvent(state, 'run:stream', { content: '', reasoning: '' });
  assert.equal(state.phase, RUN_PHASE.prompt);
});

test('a late reasoning delta never sends the row back to thinking', () => {
  const state = createRunPhaseState();
  applyRunPhaseEvent(state, 'run:stream', { content: 'answer' });
  applyRunPhaseEvent(state, 'run:stream', { reasoning: 'late' });
  assert.equal(state.phase, RUN_PHASE.decoding);
  // A new step after the tools may think again, though.
  applyRunPhaseEvent(state, 'tool:start', { sessionId: 's1', runId: 'r1', toolCallIndex: 0, name: 'read' });
  applyRunPhaseEvent(state, 'tool:result', { sessionId: 's1', runId: 'r1', toolCallIndex: 0, name: 'read' });
  applyRunPhaseEvent(state, 'run:stream', { reasoning: 'again' });
  assert.equal(state.phase, RUN_PHASE.reasoning);
});

test('a tool batch reports execution until the last call of the batch ends', () => {
  const base = { sessionId: 's1', runId: 'r1', toolBatchId: 'b1' };
  const state = createRunPhaseState();
  applyRunPhaseEvent(state, 'tool:start', { ...base, toolCallIndex: 0, name: 'read' });
  applyRunPhaseEvent(state, 'tool:update', { ...base, toolCallIndex: 0, name: 'read' });
  applyRunPhaseEvent(state, 'tool:start', { ...base, toolCallIndex: 1, name: 'grep' });
  assert.equal(state.phase, RUN_PHASE.tools);
  applyRunPhaseEvent(state, 'tool:result', { ...base, toolCallIndex: 0, name: 'read' });
  assert.equal(state.phase, RUN_PHASE.tools, 'a sibling call is still running');
  applyRunPhaseEvent(state, 'tool:error', { ...base, toolCallIndex: 1, name: 'grep' });
  assert.equal(state.phase, RUN_PHASE.prompt, 'the last call of the step hands back to the model');
});

test('call identity survives events without agent-loop coordinates', () => {
  const state = createRunPhaseState();
  applyRunPhaseEvent(state, 'tool:start', { toolCallId: 'call_1', name: 'command' });
  applyRunPhaseEvent(state, 'tool:result', { toolCallId: 'call_1', name: 'command' });
  assert.equal(state.phase, RUN_PHASE.prompt);
  assert.equal(state.running.size, 0);
});

test('a new run clears a stale running set', () => {
  const state = createRunPhaseState();
  applyRunPhaseEvent(state, 'tool:start', { sessionId: 's1', runId: 'r1', toolCallIndex: 0, name: 'read' });
  assert.equal(state.running.size, 1);
  applyRunPhaseEvent(state, 'run:start', { sessionId: 's1', runId: 'r2' });
  assert.equal(state.running.size, 0);
  assert.equal(state.phase, RUN_PHASE.prompt);
});

test('unrelated events leave the phase untouched', () => {
  const state = createRunPhaseState();
  applyRunPhaseEvent(state, 'run:stream', { content: 'x' });
  applyRunPhaseEvent(state, 'plan:update', {});
  applyRunPhaseEvent(state, 'mcp:status', {});
  applyRunPhaseEvent(state, 'compact:start', {});
  assert.equal(state.phase, RUN_PHASE.decoding);
});

test('only the thinking phase has a label', () => {
  assert.equal(phaseLabel(RUN_PHASE.reasoning, 12400), 'Reasoning 12.4k tokens');
  assert.equal(phaseLabel(RUN_PHASE.prompt, 0), '', 'waiting for the first token says nothing');
  assert.equal(phaseLabel(RUN_PHASE.decoding, 0), '', 'the body speaks for itself');
  assert.equal(phaseLabel(RUN_PHASE.tools, 0), '', 'tool cards speak for themselves');
  assert.equal(phaseLabel('nonsense', 0), '');
});

test('the token detail never rides along into another phase', () => {
  // A stale counter must not decorate a phase that is not thinking.
  assert.equal(phaseLabel(RUN_PHASE.decoding, 12400), '');
  assert.equal(phaseLabel(RUN_PHASE.prompt, 12400), '');
  // A zero count would read as a claim ("Reasoning 0 tokens") the estimate
  // cannot make, so the word stands alone until a count exists.
  assert.equal(phaseLabel(RUN_PHASE.reasoning, 0), 'Reasoning');
  assert.equal(phaseLabel(RUN_PHASE.reasoning, undefined), 'Reasoning');
});
