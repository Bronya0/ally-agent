/*
 * SPDX-License-Identifier: GPL-3.0-only
 *
 * Copyright (C) 2026 tangssst <tangssst@qq.com>
 * GitHub: https://github.com/Bronya0/ally-agent
 *
 * This file is part of ally-agent, licensed under the GNU General
 * Public License v3. See the LICENSE file for details.
 */
import { normalizeToolStatus } from './toolEventState.mjs';

// Group only adjacent sub-agent cards. Keep the last source index so round
// statistics stay after their original message, and never cross a run or tool-batch boundary.
export function subagentRows(messages, boundaryAfter = () => false) {
  const rows = [];
  for (let index = 0; index < messages.length; index++) {
    const msg = messages[index];
    const previous = rows[rows.length - 1];
    if (msg.kind === 'subagent') {
      if (previous?.agents && previous.msg.runId === msg.runId && (previous.msg.toolBatchId || '') === (msg.toolBatchId || '') && !boundaryAfter(previous.msg, previous.index)) {
        previous.agents.push(msg);
        previous.msg = msg;
        previous.index = index;
      } else {
        rows.push({ msg, index, agents: [msg] });
      }
    } else {
      rows.push({ msg, index, agents: null });
    }
  }
  return rows;
}

// A sub-agent card shows one more state than the generic tool vocabulary:
// `partial` means the child hit its step budget, so its report exists but the
// task may be unfinished. This predicate is the only place that decides it;
// the card and the batch aggregate both read it.
export function subagentDisplayState(agent) {
  if (!agent) return 'default';
  if (agent.error) return 'error';
  const state = normalizeToolStatus(agent.status);
  return state === 'success' && agent.stopReason === 'step_limit' ? 'partial' : state;
}

// A failed child does not finish a batch while siblings are still running.
export function subagentBatchStatus(agents) {
  const states = agents.map(subagentDisplayState);
  for (const state of ['running', 'default', 'error', 'partial']) {
    if (states.includes(state)) return state;
  }
  return states.length ? 'success' : 'default';
}
