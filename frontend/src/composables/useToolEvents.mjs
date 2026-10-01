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

import { t, formatDateTime } from '../i18n.mjs';
import {
  setToolStatus,
  findToolEventByData,
  toolEventId,
} from '../utils/toolEventState.mjs';
import { formatReadRangeChip } from '../utils/toolFormat.mjs';
import { deletePathRows, scheduledTaskRow, scheduledTaskRows, serviceListRows, serviceRow, sshClusterNodeRow, sshServerRows } from '../utils/toolPreview.mjs';
import { formatBytes } from '../utils/format.mjs';
import { scheduledStatusKey, scheduledStatusTone, serviceStatusKey, serviceStatusTone } from '../utils/taskStatus.mjs';
import { isActionKeyedTool } from '../utils/toolVerb.mjs';

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
    formatScheduledToolSchedule,
    scrollMessagesToBottomIfStale,
    scrollMessagesToBottom,
    activeSessionId,
  } = ctx;

  // ---- tool:result adapters ------------------------------------------------
  // Each tool's result post-processing is a small named function, keyed by
  // tool name in toolResultAdapters below. Splitting the former 180-line
  // if-chain into named adapters means editing one tool's rendering cannot
  // silently break another, and each adapter can be read in isolation.

  // edit 系工具的耗时以 UI 收到参数的时刻（uiStartedAt）为基准：后端
  // durationMs 只覆盖执行段，参数流式到达的时间不计入，显示偏短。
  const EDIT_TOOL_NAMES = ['edit', 'replace_exact', 'replace_lines', 'remote_edit'];

  function toolDurationMs(existing, data) {
    if (EDIT_TOOL_NAMES.includes(data.name) && existing.uiStartedAt) {
      return Math.max(0, Date.now() - Number(existing.uiStartedAt));
    }
    return Number(data.durationMs || 0);
  }

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
    existing.durationMs = toolDurationMs(existing, data);
    existing.durationText = formatDurationShort(existing.durationMs);
    if (data.mcpServer) existing.mcpServer = data.mcpServer;
    if (data.mcpTool) existing.mcpTool = data.mcpTool;
    existing.time = new Date().toLocaleTimeString();
    // A tool whose verb is keyed by its action (scheduled_task / service / plan)
    // takes that action from the call's arguments; the result names the action
    // the backend took, which is the only source for a card whose arguments never
    // arrived and for plan's read-back call, whose arguments state no action at
    // all. Only a gap is filled — an action the card already captured is never
    // replaced (plan's clear and the backend's set are two readings of one call).
    if (!existing.toolAction && isActionKeyedTool(existing.name)) {
      existing.toolAction = String(resultData?.action || '').trim().toLowerCase();
    }
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
            reused: !!f.reused,
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

  // 删一批路径时卡片正体逐行列出每一条（成/败），与读卡的折叠行同一套行样式。
  // 行只从结果里读：结果才是逐条判定的来源（入参此时只剩一串没判定过的路径）。
  // body 一并清掉——失败详情已经落在行里，两份都留会让同一条错误显示两次。
  function applyDeleteEntries(existing, data, resultData) {
    const rows = deletePathRows(resultData);
    if (!rows.length) return;
    existing.deleteEntries = rows;
    existing.body = '';
  }

  // ssh_cluster 的结果不再倒成通用键值文本：卡片正体是一格格服务器卡（彩色网格，
  // 一行多个、自动换行）。列表结果带 servers 数组，登记 / 授权的结果是单台节点的扁平
  // 字段，两者各一张卡；空清单也接管正体，否则卡片只剩标题、看不出「一台都没有」。
  function applySSHClusterResult(existing, data, resultData) {
    const servers = sshServerRows(resultData);
    if (servers) {
      setCardGrid(existing, servers.map(sshServerCardItem), { emptyText: t('tools.sshCluster.empty') });
      return;
    }
    const node = sshClusterNodeRow(resultData);
    if (node) setCardGrid(existing, [sshClusterNodeCardItem(node)]);
  }

  // scheduled_task 的结果按形状分支：列表（tasks 数组）一个任务一张卡（名字 /
  // 调度+下次执行 / 任务内容 / 状态）；create 返回单条 task，也是一张卡——创建后
  // 当场看到调度、下次执行与状态，与列表里那行同一套字段。delete 的结果只有被删的
  // id，没有任务信息，保持原来的文本体。
  function applyScheduledTaskResult(existing, data, resultData) {
    const tasks = scheduledTaskRows(resultData);
    if (tasks) {
      setCardGrid(existing, tasks.map(scheduledTaskCardItem), { emptyText: t('app.tools.scheduled.none') });
      return;
    }
    const task = scheduledTaskRow(resultData);
    if (task) setCardGrid(existing, [scheduledTaskCardItem(task)]);
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

  // ── 卡片网格：结果行 → CardGrid 条目 ──
  // 行解析是纯函数（utils/toolPreview.mjs，不 import i18n），而徽标文案与色调判定需要
  // i18n / 语义词表，所以「行 → 条目」写在这一层，与其它适配器一样在适配器里产出展示
  // 数据。一次调用只写 msg.cardGrid（条目、标题行、空态文案一起走），卡片正体整块清空。
  function setCardGrid(existing, items, options = {}) {
    existing.cardGrid = {
      items,
      caption: options.caption || '',
      emptyText: options.emptyText || '',
    };
    existing.body = '';
  }

  // 状态串没有对应文案时原样显示：后端新加了一个状态也不能变成空白徽标。
  function statusLabel(key, raw) {
    return (key && t(key)) || raw || '';
  }

  function sshServerCardItem(server) {
    return {
      key: server.alias,
      title: server.alias,
      subtitle: server.endpoint,
      description: server.description,
      badge: server.pending
        ? t('sshCluster.table.pending')
        : (server.riskLevel === 'high' ? t('sshCluster.table.riskHigh') : t('sshCluster.table.riskLow')),
      tone: server.pending ? 'warning' : (server.riskLevel === 'high' ? 'danger' : 'info'),
    };
  }

  // 登记 / 授权的结果：一台机器一张卡，徽标说明这次到底动没动已存状态。别拿「节点已
  // 登记」（alreadyRegistered）代偿：已登记但本次才授权的工作区授权同样是改动。
  function sshClusterNodeCardItem(node) {
    return {
      key: node.alias,
      title: node.alias,
      subtitle: node.endpoint,
      description: node.description,
      badge: node.changed ? t('tools.sshCluster.authorized') : t('tools.sshCluster.unchanged'),
      tone: node.changed ? 'success' : 'neutral',
    };
  }

  function serviceCardItem(service, options = {}) {
    const details = [];
    if (service.id) details.push(service.id);
    if (service.pid) details.push(`pid ${service.pid}`);
    // 缓冲体积与工作目录是旧日志行里就有的两项，换卡片不能丢。
    if (service.outputBytes > 0) details.push(formatBytes(service.outputBytes));
    if (service.cwd) details.push(service.cwd);
    // 启动时间只有 start / stop 那条结果需要补（列表行也有这个字段，但列表已经有
    // id 与 pid，每张卡再挂一个时间会把卡片挤成两行），所以按需给。
    if (options.withStartedAt && service.startedAt) details.push(formatDateTime(service.startedAt * 1000));
    if (service.error) details.push(service.error);
    return {
      key: service.id || service.name,
      title: service.name || service.id,
      subtitle: service.command,
      description: details.join(' · '),
      badge: statusLabel(serviceStatusKey(service.status), service.status),
      tone: serviceStatusTone(service),
    };
  }

  function scheduledTaskCardItem(task) {
    const schedule = task.schedule?.type ? formatScheduledToolSchedule(task.schedule) : '';
    const next = task.nextRunAt ? formatDateTime(task.nextRunAt) : '';
    return {
      key: task.id || task.name,
      title: task.name || task.id,
      subtitle: [schedule, next].filter(Boolean).join(' · '),
      description: task.command || task.instruction,
      badge: statusLabel(scheduledStatusKey(task), task.lastStatus),
      tone: scheduledStatusTone(task),
    };
  }

  // service 的结果按形状分支，不按动作名：列表（services 数组）一个进程一张卡
  // （命令、pid、工作目录都在卡里），active/max 那行数字正体里没处放，走 CardGrid
  // 的 caption；start / stop 返回单条 ServiceInfo，同样一张卡（启动时间只有这条
  // 结果带，卡上补出来）；read 带命令输出正文，保持 formatToolBody 的原生结果体。
  // 动作名（existing.toolAction）只用来显示动词：它来自流式参数，早退的 tool:start
  // 可能没带上，按它分支会漏（早先靠标题前缀兜底，仍是猜）。
  function applyServiceResult(existing, data, resultData) {
    const services = serviceListRows(resultData);
    if (services) {
      const counts = resultData && typeof resultData === 'object' ? resultData : {};
      const caption = typeof counts.activeCount === 'number' && typeof counts.maxActive === 'number'
        ? t('app.tools.services.active', { active: counts.activeCount, max: counts.maxActive })
        : '';
      setCardGrid(existing, services.map((service) => serviceCardItem(service)), { caption, emptyText: t('app.tools.services.none') });
      return;
    }
    const service = serviceRow(resultData);
    if (service) setCardGrid(existing, [serviceCardItem(service, { withStartedAt: true })]);
  }

  const toolResultAdapters = {
    'edit': [applyEditValidation, applyEditDiff],
    'replace_exact': [applyEditValidation, applyEditDiff],
    'replace_lines': [applyEditValidation, applyEditDiff],
    'remote_edit': [applyEditValidation, applyEditDiff],
    'create': [applyCreatePath],
    'remote_create_file': [applyCreatePath],
    'ask': [applyAskResult],
    // plan has no adapter on purpose: the card's parenthetical names the step the
    // call is ABOUT ("Finished step (X)" = the step just reported), and only the
    // arguments state that step — the result only carries the plan, whose current
    // step is the one the work moved ON to. The default title path below fills a
    // title the call never stated, so the arguments' title survives the result.
    'read': [applyReadBatchEntries],
    'remote_read': [applyReadBatchEntries],
    'delete': [applyDeleteEntries],
    'remote_delete_path': [applyDeleteEntries],
    'service': [applyServiceResult],
    'scheduled_task': [applyScheduledTaskResult],
    'ssh_cluster': [applySSHClusterResult],
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
    existing.durationMs = toolDurationMs(existing, data);
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
      if ((data.name === 'read' || data.name === 'remote_read' || data.name === 'batch_read') && Array.isArray(resultData.files) && resultData.files.length === 0) {
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
