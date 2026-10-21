// SPDX-License-Identifier: GPL-3.0-only

// Model results and UI cards are different representations. Match only on the
// stable call id; preserve message positions so scroll/drift anchors stay valid.
export function applyFastCompaction(session, result, chip) {
  const summaries = result?.toolSummaries || {};
  session.messages = (session.messages || []).map((msg) => {
    if (msg.role !== 'tool_call' || !Object.hasOwn(summaries, msg.toolCallId)) return msg;
    return {
      role: 'tool_call', kind: 'generic', name: msg.name,
      title: msg.title, status: msg.status, eventId: msg.eventId,
      toolCallId: msg.toolCallId, runId: msg.runId,
      toolBatchId: msg.toolBatchId, toolCallIndex: msg.toolCallIndex,
      body: summaries[msg.toolCallId], chip,
      fastCompacted: true, toolAction: msg.toolAction,
      mcpServer: msg.mcpServer, mcpTool: msg.mcpTool,
      durationMs: msg.durationMs, durationText: msg.durationText,
      error: msg.error, errorCode: msg.errorCode,
      pendingTurn: msg.pendingTurn, transientTurn: msg.transientTurn,
    };
  });
  // Tool-event lookup caches can otherwise keep the discarded large card alive.
  delete session._lastToolEventId;
  delete session._lastToolMsg;
}
