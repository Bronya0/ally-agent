export const DEFAULT_TOOL_PREVIEW_LINES = 6;

export function normalizedLines(text) {
  const lines = String(text || '').replace(/\r\n/g, '\n').split('\n');
  if (lines.length > 1 && lines[lines.length - 1] === '') lines.pop();
  return lines;
}

export function codePreviewWindow(code, options = {}) {
  if (!code) {
    return {
      lines: [],
      startLine: 1,
      totalLines: 0,
      omittedBefore: false,
      omittedAfter: false,
    };
  }
  const collapsed = Boolean(options.collapsed);
  const maxLines = Number(options.maxLines || 0);
  const mode = options.mode === 'tail' ? 'tail' : 'head';
  const lines = normalizedLines(code);
  const totalLines = lines.length;

  if (!collapsed || maxLines <= 0 || totalLines <= maxLines) {
    return {
      lines,
      startLine: 1,
      totalLines,
      omittedBefore: false,
      omittedAfter: false,
    };
  }

  if (mode === 'tail') {
    const start = Math.max(0, totalLines - maxLines);
    return {
      lines: lines.slice(start),
      startLine: start + 1,
      totalLines,
      omittedBefore: start > 0,
      omittedAfter: false,
    };
  }

  return {
    lines: lines.slice(0, maxLines),
    startLine: 1,
    totalLines,
    omittedBefore: false,
    omittedAfter: totalLines > maxLines,
  };
}

export function estimateCodePreviewChars(code, options = {}) {
  const window = codePreviewWindow(code, options);
  return window.lines.join('\n').length + 120;
}

export function estimateMessageRenderChars(msg, options = {}) {
  if (!msg) return 0;
  const toolPreviewLines = Number(options.toolPreviewLines || DEFAULT_TOOL_PREVIEW_LINES);
  let total = 0;
  if (msg.content) total += String(msg.content).length;
  if (msg.reasoningBody) total += String(msg.reasoningBody).length;
  if (msg.body) total += String(msg.body).length;
  if (msg.codeContent) {
    if (msg.role === 'tool_call' && msg.kind === 'create' && !msg.expanded) {
      total += estimateCodePreviewChars(msg.codeContent, {
        collapsed: true,
        maxLines: toolPreviewLines,
        mode: 'tail',
      });
    } else {
      total += String(msg.codeContent).length;
    }
  }
  if (msg.editDiff) total += String(msg.editDiff).length;
  if (msg.summary) total += String(msg.summary).length;
  return total || 80;
}

export function isRenderableMessage(msg) {
  return !(msg?.role === 'tool_call' && msg?.kind === 'run');
}

export function displaySourceMessages(session, expandedArchiveSessions, options = {}) {
  const src = (session?.messages || []).filter(isRenderableMessage);
  if (!session) return src;
  const maxMessages = Number(options.maxMessages || 180);
  const maxChars = Number(options.maxChars || 220000);
  const expandedSet = expandedArchiveSessions || new Set();
  const estimate = (msg) => estimateMessageRenderChars(msg, options);
  const totalChars = src.reduce((sum, msg) => sum + estimate(msg), 0);
  if (src.length <= maxMessages && totalChars <= maxChars) return src;

  const expanded = expandedSet.has(session.id);
  const effectiveMaxMessages = expanded
    ? Number(options.expandedMaxMessages || maxMessages * 2)
    : maxMessages;
  const effectiveMaxChars = expanded
    ? Number(options.expandedMaxChars || maxChars * 2)
    : maxChars;
  let chars = 0;
  let keepStart = src.length;
  for (let i = src.length - 1; i >= 0; i--) {
    const nextChars = chars + estimate(src[i]);
    const kept = src.length - i;
    if (kept > effectiveMaxMessages || nextChars > effectiveMaxChars) {
      keepStart = Math.min(i + 1, src.length - 1);
      break;
    }
    chars = nextChars;
    keepStart = i;
  }
  if (keepStart <= 0) return src;
  const archived = src.slice(0, keepStart);
  const archiveMsg = {
    role: 'archive',
    sessionId: session.id,
    expanded,
    count: archived.length,
    tokens: Math.round(archived.reduce((sum, msg) => sum + estimate(msg), 0) / 4),
  };
  return [archiveMsg, ...src.slice(keepStart)];
}
