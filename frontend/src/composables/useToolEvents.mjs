// useToolEvents.mjs
// -----------------------------------------------------------------------------
// Tool-card subsystem, extracted from App.vue.
//
// Owns the `tool:result` / `tool:error` runtime-event handlers and the
// per-tool rendering adapters. Historically this was the most fragile part of
// the frontend (card stuck in "running", cross-step mismatches, status
// vocabulary drift). Pulling it into one isolated, dependency-injected
// composable keeps the state transitions in a single reviewable place and
// makes the App.vue wiring trivial.
//
// Dependency strategy:
//   - Pure utilities (setToolStatus, findToolEventByData, toolEventId,
//     formatReadRangeChip) and i18n `t` are imported directly.
//   - App.vue-local state/functions are passed through `ctx`, so this module
//     never reaches into component scope and stays unit-testable.
// -----------------------------------------------------------------------------

import { t } from '../i18n.mjs';
import {
  setToolStatus,
  findToolEventByData,
  toolEventId,
} from '../utils/toolEventState.mjs';
import { formatReadRangeChip } from '../utils/toolFormat.mjs';

export function useToolEvents(ctx) {
  const {
    onRuntimeEvent,
    sessionByEvent,
    appendToolEventFallback,
    scheduleSaveSessions,
    flushStreamBuffer,
    flushToolUpdateBuffer,
    finalizeStreamingMessageForRun,
    refreshContextTokens,
    isHiddenTool,
    parseToolResultData,
    formatToolBody,
    formatToolChip,
    formatDurationShort,
    makeToolResultTitle,
    formatTodoNextStep,
    scrollMessagesToBottomIfStale,
    scrollMessagesToBottom,
    activeSessionId,
  } = ctx;

  // ---- tool:result adapters ------------------------------------------------
  // Each tool's result post-processing is a small named function, keyed by
  // tool name in toolResultAdapters below. Splitting the former 180-line
  // if-chain into named adapters means editing one tool's rendering cannot
  // silently break another, and each adapter can be read in isolation.

  function applyToolResultCommon(existing, data, resultData) {
    setToolStatus(existing, 'success');
    // ESC 终止的命令不应显示绿色 √
    if (data.name === 'command' || data.name === 'remote_run_command') {
      try {
        const parsed = JSON.parse(data.result);
        if (parsed?.data?.cancelled) setToolStatus(existing, 'error');
      } catch (e) {
        console.error('[tool:result] failed to parse command cancel state', data?.name, data?.toolCallId, e);
      }
    }
    existing.body = formatToolBody(data.name, data.result);
    existing.chip = formatToolChip(data.name, data.result);
    existing.durationMs = Number(data.durationMs || 0);
    existing.durationText = formatDurationShort(existing.durationMs);
    if (data.mcpServer) existing.mcpServer = data.mcpServer;
    if (data.mcpTool) existing.mcpTool = data.mcpTool;
    existing.time = new Date().toLocaleTimeString();
  }

  function applyDefaultToolResultTitle(existing, data, resultData) {
    if (!existing.title) existing.title = makeToolResultTitle(data.name, data.result, data);
  }

  function applyEditValidation(existing, data, resultData) {
    existing.validation = typeof resultData.validation === 'string' ? resultData.validation : '';
  }

  function applyAskResult(existing, data, resultData) {
    existing.askReady = false;
    existing.askSubmitting = false;
    existing.askSubmitted = true;
    existing.askAnswers = Array.isArray(resultData.answers) ? resultData.answers : existing.askAnswers || [];
  }

  function applyPlanTitle(existing, data, resultData) {
    if (Array.isArray(resultData.todos)) existing.title = formatTodoNextStep(resultData.todos);
  }

  function applyCreatePath(existing, data, resultData) {
    if (resultData.path) {
      existing.editFilePath = resultData.path;
      if (!existing.title) existing.title = resultData.target ? `${resultData.target} · ${resultData.path}` : resultData.path;
    }
  }

  function applyReadFileMeta(existing, data, resultData) {
    try {
      const rp = JSON.parse(data.result);
      if (rp.data) {
        const d = rp.data;
        const s = d.startLine || 1;
        const e = d.endLine || d.totalLines || 0;
        existing.readLineCount = e >= s ? e - s + 1 : 0;
        existing.readTotalLines = d.totalLines || e;
        // read_file returns a single ReadFileResult with path at top level;
        // batch-shaped results (read/batch_read) carry it in files[0].path.
        // The running-stage title comes from streaming args and may never
        // resolve if the path field arrives truncated or tool:update is
        // skipped (then tool:result falls back to makeToolResultTitle,
        // which returns '' for read_file). Fall back to the result path so
        // the read-group entry shows a real name instead of "(未命名)".
        const resultPath = d.path || (Array.isArray(d.files) && d.files[0]?.path) || '';
        if (resultPath && !existing.title) existing.title = resultPath;
      }
    } catch (e) {
      console.error('[tool:result] failed to parse read_file meta', data?.name, data?.toolCallId, e);
    }
  }

  function applyReadBatchEntries(existing, data, resultData) {
    try {
      const rp = JSON.parse(data.result);
      if (rp.data && rp.data.files) {
        const entries = [];
        for (const f of rp.data.files) {
          entries.push({
            title: f.path || '',
            startLine: f.startLine || 1,
            endLine: f.endLine || f.totalLines || 0,
            totalLines: f.totalLines || 0,
            truncated: !!f.truncated,
            lineCount: (f.endLine && f.startLine) ? (f.endLine - f.startLine + 1) : (f.totalLines || 0),
            chip: f.error ? `failed: ${f.error}` : formatReadRangeChip(f.startLine || 1, f.endLine || f.totalLines || 0, f.totalLines || 0, !!f.truncated),
            status: f.error ? 'error' : 'success',
          });
        }
        existing.batchEntries = entries;
      }
    } catch (e) {
      console.error('[tool:result] failed to parse batch read entries', data?.name, data?.toolCallId, e);
    }
  }

  function applyEditDiff(existing, data, resultData) {
    try {
      const resultParsed = JSON.parse(data.result);
      const editData = resultParsed.data || resultParsed;
      const editedFiles = Array.isArray(editData.files) ? editData.files : [];
      if (editedFiles.length) {
        existing.editEntries = editedFiles.map((file, index) => ({
          ...(existing.editEntries?.[index] || {}), path: file.path || existing.editEntries?.[index]?.path || '',
          changes: file.diff ? [] : (existing.editEntries?.[index]?.changes || []), diff: file.diff || '',
          added: file.addedLines || 0, removed: file.removedLines || 0,
        }));
      }
      const combinedDiff = editedFiles.map((file) => file?.diff || '').filter(Boolean).join('\n');
      if (editedFiles.length) {
        existing.editDiff = '';
        existing.editOldString = '';
        existing.editNewString = '';
        existing.body = '';
      } else if (editData.diff || combinedDiff) {
        existing.editDiff = editData.diff || combinedDiff;
      }
      if (editData.addedLines !== undefined || editData.removedLines !== undefined) {
        existing.editAdded = editData.addedLines || 0;
        existing.editRemoved = editData.removedLines || 0;
        const parts = [];
        if (existing.editAdded > 0) parts.push('+' + existing.editAdded);
        if (existing.editRemoved > 0) parts.push('-' + existing.editRemoved);
        existing.editStats = parts.join(' ');
      }
      if (editData.path) existing.editFilePath = editData.path;
      else if (editedFiles.length === 1) existing.editFilePath = editedFiles[0]?.path || '';
      else if (editedFiles.length > 1) existing.editFilePath = `${editedFiles.length} files`;
      if (editData.warnings) existing.editWarnings = editData.warnings;
      if (editData.changedLinesBlock) existing.editChangedLinesBlock = editData.changedLinesBlock;
    } catch (e) {
      console.error('[tool:result] failed to parse edit diff', data?.name, data?.toolCallId, e);
    }
  }

  const toolResultAdapters = {
    'edit': [applyEditValidation, applyEditDiff],
    'replace_exact': [applyEditValidation, applyEditDiff],
    'replace_lines': [applyEditValidation, applyEditDiff],
    'remote_edit': [applyEditValidation, applyEditDiff],
    'create': [applyCreatePath],
    'remote_create_file': [applyCreatePath],
    'ask': [applyAskResult],
    'plan': [applyPlanTitle],
    'read_file': [applyReadFileMeta],
    'remote_read_file': [applyReadFileMeta],
    'read': [applyReadBatchEntries],
    'batch_read': [applyReadBatchEntries],
  };

  function applySubagentResult(existing, data, resultData) {
    existing.subagentId = resultData.agentId || existing.subagentId || '';
    existing.subagentRole = resultData.role || existing.subagentRole || '';
    setToolStatus(existing, resultData.status || 'success');
    existing.description = resultData.description || existing.description || '';
    existing.summary = resultData.summary || existing.summary || '';
    existing.filesRead = resultData.filesRead || existing.filesRead || [];
    existing.filesEdited = resultData.filesEdited || existing.filesEdited || [];
    existing.steps = resultData.steps || existing.steps || 0;
    existing.error = resultData.error || '';
    existing.durationMs = Number(data.durationMs || existing.durationMs || 0);
    existing.durationText = formatDurationShort(existing.durationMs);
    existing.time = new Date().toLocaleTimeString();
  }

  function applySubagentError(existing, data) {
    setToolStatus(existing, 'failed');
    existing.error = data.error || '';
    existing.body = '';
    existing.errorCode = data.errorCode || '';
    existing.durationMs = Number(data.durationMs || existing.durationMs || 0);
    existing.durationText = formatDurationShort(existing.durationMs);
    existing.time = new Date().toLocaleTimeString();
  }

  function applyToolErrorCommon(existing, data) {
    setToolStatus(existing, 'error');
    existing.body = data.error || '';
    existing.errorCode = data.errorCode || '';
    if (data.name === 'ask') {
      existing.askReady = false;
      existing.askSubmitting = false;
      if (existing.errorCode === 'E_ASK_CANCELLED') existing.body = t('app.ask.cancelled');
    }
    existing.durationMs = Number(data.durationMs || 0);
    existing.durationText = formatDurationShort(existing.durationMs);
    if (data.mcpServer) existing.mcpServer = data.mcpServer;
    if (data.mcpTool) existing.mcpTool = data.mcpTool;
    existing.time = new Date().toLocaleTimeString();
  }

  onRuntimeEvent('tool:result', (data) => {
    flushStreamBuffer(data.runId);
    flushToolUpdateBuffer();
    const session = sessionByEvent(data);
    if (!session) return;
    // 工具批次执行完成意味着当前 assistant 消息已经结束(模型在发起工具
    // 调用后不会再输出正文)。封口它,使下一次流式回答新建消息,而不是
    // 被 flushStreamBuffer 扫描复用、把工具卡片后生成的内容并进旧消息。
    finalizeStreamingMessageForRun(session, data.runId);
    // Tool results grow the live context (tool result messages get appended to
    // the next model request). Refresh the footer counter so it tracks the
    // agent loop while it works, not only after the run ends.
    if (session.id === activeSessionId.value) refreshContextTokens(session.id);
    // suggest: 不渲染为 tool card，直接把 items 注入前一条 assistant 消息
    if (isHiddenTool(data.name)) {
      const resultData = parseToolResultData(data.result);
      const items = Array.isArray(resultData.items) ? resultData.items : [];
      for (let i = session.messages.length - 1; i >= 0; i--) {
        if (session.messages[i].role === 'assistant') {
          session.messages[i].suggestions = items;
          break;
        }
      }
      scheduleSaveSessions();
      return;
    }
    const eventId = toolEventId(data);
    let existing = findToolEventByData(session, data);
    if (!existing) existing = appendToolEventFallback(session, data, 'running');
    if (existing) {
      existing.eventId = eventId;
      existing.runId = data.runId || existing.runId || '';
      existing.toolBatchId = data.toolBatchId || existing.toolBatchId || '';
      existing.toolCallId = data.toolCallId || existing.toolCallId || '';
      if (data.toolCallIndex !== undefined && data.toolCallIndex !== null) existing.toolCallIndex = data.toolCallIndex;
      const resultData = parseToolResultData(data.result);
      // The backend silently filters directory and stale/missing paths from a
      // batch read. If every requested path was filtered, remove the transient
      // running card as well so the UI shows neither an error nor an empty read.
      if ((data.name === 'read' || data.name === 'batch_read') && Array.isArray(resultData.files) && resultData.files.length === 0) {
        const messageIndex = session.messages.indexOf(existing);
        if (messageIndex >= 0) session.messages.splice(messageIndex, 1);
        scheduleSaveSessions();
        return;
      }
      if ((data.name === 'subagent' || data.name === 'agent_delegate') && existing.kind === 'subagent') {
        applySubagentResult(existing, data, resultData);
        scheduleSaveSessions();
        return;
      }
      // Common fields for every successful tool card, then per-tool adapters
      // dispatched by name (see toolResultAdapters above). Splitting the
      // former 180-line if-chain into named adapters means editing one tool's
      // rendering cannot silently break another.
      applyToolResultCommon(existing, data, resultData);
      for (const applyAdapter of toolResultAdapters[data.name] || []) applyAdapter(existing, data, resultData);
      applyDefaultToolResultTitle(existing, data, resultData);
    }
    // 详情已写入卡片（status/body/diff 等）。flushToolUpdateBuffer 的
    // alignToLastToolCard 只保证卡片头部可见，详情可能仍在折叠线下，
    // 这里滚动到容器真实底部让最新结果完整露出。大 diff（Edit）渲染
    // 跨帧展开，首帧可能未达真实底部，由组件内补一次复查滚底。
    if (session.id === activeSessionId.value) scrollMessagesToBottomIfStale();
  });
  onRuntimeEvent('tool:error', (data) => {
    flushStreamBuffer(data.runId);
    flushToolUpdateBuffer();
    const session = sessionByEvent(data);
    if (!session) return;
    // A failed tool still terminates the assistant's tool-call response. Keep
    // the failed path identical to tool:result so the next model response gets
    // its own assistant message and run-level metadata stays at the bottom.
    finalizeStreamingMessageForRun(session, data.runId);
    // Failed tool calls also become part of the context; keep the footer fresh.
    if (session.id === activeSessionId.value) refreshContextTokens(session.id);
    // suggest: 静默忽略错误，不渲染任何 card
    if (isHiddenTool(data.name)) return;
    const eventId = toolEventId(data);
    let existing = findToolEventByData(session, data);
    if (!existing) existing = appendToolEventFallback(session, data, 'error');
    if (existing) {
      existing.eventId = eventId;
      existing.runId = data.runId || existing.runId || '';
      existing.toolBatchId = data.toolBatchId || existing.toolBatchId || '';
      existing.toolCallId = data.toolCallId || existing.toolCallId || '';
      if (data.toolCallIndex !== undefined && data.toolCallIndex !== null) existing.toolCallIndex = data.toolCallIndex;
      if ((data.name === 'subagent' || data.name === 'agent_delegate') && existing.kind === 'subagent') {
        applySubagentError(existing, data);
        scheduleSaveSessions();
        return;
      }
      applyToolErrorCommon(existing, data);
    }
    // 错误详情已写入卡片，滚动到可见区域。
    if (session.id === activeSessionId.value) scrollMessagesToBottom();
  });
}
