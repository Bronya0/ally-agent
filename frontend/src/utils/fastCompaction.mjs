// SPDX-License-Identifier: GPL-3.0-only

// Model results and UI cards are different representations. Match only on the
// stable call id; preserve message positions so scroll/drift anchors stay valid.
export function applyFastCompaction(session, result, chip) {
  const summaries = result?.toolSummaries || {};
  // UI assistant rows have no tool-call id. Match exact original text plus its
  // occurrence counted from the end, so repeated answers in the retained tail
  // cannot be mistaken for an older answer that was shortened.
  const edits = new Map();
  for (const edit of result?.assistantEdits || []) {
    if (!edits.has(edit.before)) edits.set(edit.before, new Map());
    edits.get(edit.before).set(edit.fromEnd, edit.after);
  }
  const occurrences = new Map();
  const answerUpdates = new Map();
  for (let i = (session.messages || []).length - 1; i >= 0; i--) {
    const msg = session.messages[i];
    if (msg.role !== 'assistant' || msg.system || msg.error || msg.welcome || msg.transientTurn) continue;
    // Legacy fast compaction appended this reasoning suffix only in backend
    // history. Normalize both old UI text and newly synchronized excerpts.
    const marker = '[Earlier reasoning excerpt; incomplete]\n';
    const content = String(msg.content || '');
    const before = content.startsWith(marker) ? '' : content.split('\n\n' + marker, 1)[0];
    const matches = edits.get(before);
    if (!matches) continue;
    const occurrence = (occurrences.get(before) || 0) + 1;
    occurrences.set(before, occurrence);
    if (matches.has(occurrence)) answerUpdates.set(msg, matches.get(occurrence));
  }
  session.messages = (session.messages || []).map((msg) => {
    if (answerUpdates.has(msg)) {
      const content = answerUpdates.get(msg);
      return { ...msg, content, hasBody: !!content.trim(), fastCompacted: true };
    }
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
