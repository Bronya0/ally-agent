/*
 * SPDX-License-Identifier: GPL-3.0-only
 *
 * Copyright (C) 2026 tangssst <tangssst@qq.com>
 * GitHub: https://github.com/Bronya0/ally-agent
 *
 * This file is part of ally-agent, licensed under the GNU General
 * Public License v3. See the LICENSE file for details.
 */
import { formatBytes } from './format.mjs';

export function normalizedLines(text) {
  const lines = String(text || '').replace(/\r\n/g, '\n').split('\n');
  if (lines.length > 1 && lines[lines.length - 1] === '') lines.pop();
  return lines;
}

// 折叠预览与"行数是否超阈值"都只需要文本的**一小部分**，但两者过去都先调
// normalizedLines 把整段切完再说。流式期间这段文本有几 MB（命令输出 / 创建内容
// 都挂在同一个 body 上），而工具卡每拍就重渲一次（小载荷 120ms，见
// utils/toolUpdateFlush.mjs），于是每帧都为一个 4 行预览抄一遍全文：主线程被这件
// 事占满时，卡片旁边正在跑的动画就跟着掉帧（那张卡上的运行圆点尤其明显）。
// 下面两个函数都从文本尾部/头部就地取，代价与文本总长无关。

/**
 * 取末尾 count 行，结果与下面两种写法逐字一致：
 *   skipBlank=false → normalizedLines(text).slice(-count)
 *   skipBlank=true  → normalizedLines(text).filter((line) => line !== '').slice(-count)
 * 归一化只吃掉**真正跟在 \n 前面**的那个 \r（等价于 replace(/\r\n/g, '\n')）；
 * 行内或文本末尾的裸 \r 是内容的一部分，照旧保留。count<=0 返回空数组。
 */
export function tailLines(text, count, options = {}) {
  const src = String(text || '');
  const max = Math.floor(Number(count) || 0);
  const skipBlank = options.skipBlank === true;
  if (!src || max <= 0) return [];
  const out = [];
  let end = src.length;
  let isTailSegment = true;
  while (out.length < max) {
    // end === 0 时不能再问 lastIndexOf('\n', -1)：负的 fromIndex 会被当成 0，于是又
    // 找到索引 0 上那个 \n，end 原地不动 —— 整个循环就成了死循环。
    // 末尾那段（后面没有 \n）为空时就是 normalizedLines 会 pop 掉的那个空元素，
    // 任何模式下都不算一行；只有它才不做结尾 \r 的归一化（裸 \r 是内容）。
    const start = end > 0 ? src.lastIndexOf('\n', end - 1) : -1;
    const raw = src.slice(start + 1, end);
    const line = !isTailSegment && raw.endsWith('\r') ? raw.slice(0, -1) : raw;
    if (line !== '' || (!skipBlank && !isTailSegment)) out.push(line);
    if (start < 0) break;
    end = start;
    isTailSegment = false;
  }
  return out.reverse();
}

/**
 * 行数封顶：卡片只问「是否超过 4 / 6 行」，不必知道精确行数，数够上限就停。
 * 返回 min(真实行数, cap)，所以拿它和任何不超过 cap 的阈值比较，结论与精确行数一致。
 * 语义与 normalizedLines(text).length 对齐（末尾那个空元素只弹一次）。
 */
export function countLinesCapped(text, cap) {
  const src = String(text || '');
  if (!src) return 0;
  const limit = Math.max(1, Math.floor(Number(cap) || 0));
  // 数的是换行个数，不是行数：数满 cap 个换行就已经能下结论了
  // （末尾那个空元素至多把行数减 1，所以真实行数 ≥ cap），
  // 而末尾空行的修正只在没数满时才需要，那时行数才是精确值。
  let newlines = 0;
  let from = 0;
  while (newlines < limit) {
    const at = src.indexOf('\n', from);
    if (at < 0) break;
    newlines += 1;
    from = at + 1;
  }
  if (newlines >= limit) return limit;
  const tailBlank = src.endsWith('\n') ? 1 : 0;
  return Math.max(1, newlines + 1 - tailBlank);
}

// formatHttpToolTitle renders the title shown on http_request / web_fetch tool
// cards. It surfaces enough of the optional fields (method, timeout, body/json
// presence) that two cards with the same URL but different actual arguments can
// be told apart at a glance, instead of every card collapsing to just the URL.
// The URL is still the first segment; everything else is appended after a
// middle-dot separator so the chip stays compact.
export function formatHttpToolTitle(parsed) {
  if (!parsed || typeof parsed !== 'object') return '';
  const url = String(parsed.url || '').trim();
  if (!url) return '';
  const parts = [url];
  const method = String(parsed.method || '').trim().toUpperCase();
  if (method && method !== 'GET') parts.push(method);
  if (parsed.body) parts.push('body');
  else if (parsed.json !== undefined && parsed.json !== null) parts.push('json');
  if (parsed.saveTo) parts.push(`→ ${parsed.saveTo}`);
  if (parsed.timeout && Number(parsed.timeout) !== 60) {
    parts.push(`${parsed.timeout}s`);
  }
  if (parsed.maxBytes && Number(parsed.maxBytes) !== 262144) {
    parts.push(`≤${formatBytes(Number(parsed.maxBytes))}`);
  }
  return parts.join(' · ');
}

// deletePathRows 读出一条删除调用的路径，一条路径一行：{path, ok, absent, error, errorCode}。
// absent 是「这条本来就不存在」（后端报的 absent 槽）：它 ok 仍为真，所以行要单独
// 认得出来，否则卡片会把「什么都没删」写成一次成功删除。
// 三种形状都认：入参的 path（单个）与 paths（一串）、结果的 paths[]（每个槽一条，新
// 形状）、以及老会话里存下来的 deleted / path 字符串（单路径时代的形状）。卡片
// 标题的摘要与卡片的逐行明细都建在它上面，所以「哪些形状算一条路径」只有这一份。
//
// errorCode 是这一条的失败原因身份（结果槽里就有，本地删除逐条给出；远端没有
// 这种东西）。行上显示的原因按它取本地化句子，原始文本只是看不懂时的退路，所以
// 两样都带出去、判定留在渲染侧；在这里截断原文就等于把原因身份丢了。
export function deletePathRows(source) {
  if (!source || typeof source !== 'object') return [];
  const rows = [];
  const push = (path, ok, error, errorCode, absent) => {
    const value = String(path || '').trim();
    if (!value) return;
    rows.push({ path: value, ok: ok !== false, error: String(error || ''), errorCode: String(errorCode || ''), absent: absent === true });
  };
  if (Array.isArray(source.paths)) {
    for (const item of source.paths) {
      if (typeof item === 'string') push(item, true, '', '');
      else if (item && typeof item === 'object') push(item.path || item.deleted, item.ok !== false, item.error, item.errorCode, item.absent);
    }
    return rows;
  }
  push(source.path || source.deleted, true, '', '');
  return rows;
}

// deletePathList 是同一份判定里只取路径的那一面：卡片标题的摘要只关心有哪些路径。
export function deletePathList(source) {
  return deletePathRows(source).map(row => row.path);
}

// deletePathSummary 是卡片标题里的路径摘要：一条就直接写它，多条写成「N paths」
// ——与 remote_read 的「N files」同一套写法。
export function deletePathSummary(source) {
  const paths = deletePathList(source);
  if (!paths.length) return '';
  return paths.length === 1 ? paths[0] : `${paths.length} paths`;
}

// deleteFailedCount 报出这一次删除里有几条失败。失败槽不显眼就等于没提示：
// 一批里失败一条时，卡片上必须看得到。
export function deleteFailedCount(source) {
  if (!source || typeof source !== 'object') return 0;
  const failed = Number(source.failedCount || 0);
  return Number.isFinite(failed) && failed > 0 ? failed : 0;
}

// deleteAbsentCount 报出这一次删除里有几条「本来就不存在」。空删不是失败，但写成
// 「Deleted x」也不对：卡片要能说出这一条什么都没删。
export function deleteAbsentCount(source) {
  if (!source || typeof source !== 'object') return 0;
  const absent = Number(source.absentCount || 0);
  return Number.isFinite(absent) && absent > 0 ? absent : 0;
}

// sshEndpointOf 是节点在 UI 上的端点写法（与 SSH 面板的 endpointOf 一致）：
// username@host:port，缺端口按 22；连主机都没有时返回空串而不是 ":22"。
function sshEndpointOf(node) {
  const host = String(node?.host || '').trim();
  const username = String(node?.username || '').trim();
  const target = username && host ? `${username}@${host}` : (host || username);
  return target ? `${target}:${Number(node?.port || 0) || 22}` : '';
}

// sshServerRows 读出一条 ssh_cluster 列表结果的服务器清单，一条一台机器。返回 null
// 表示「这不是列表结果」（add / 授权的结果是扁平字段，走 sshClusterNodeRow），调用
// 方据此决定要不要接管卡片正体；返回 [] 才是「确实是列表，但一台都没有」。结果的
// 形状见 internal/app/biz_ssh_cluster.go 的 SSHServerSummary —— 密码与私钥从不下发，
// 所以这里也读不到。
export function sshServerRows(source) {
  if (!source || typeof source !== 'object' || !Array.isArray(source.servers)) return null;
  const rows = [];
  for (const item of source.servers) {
    if (!item || typeof item !== 'object') continue;
    const host = String(item.host || '').trim();
    const name = String(item.alias || '').trim() || host || String(item.username || '').trim();
    // 三个身份字段全空的槽没有任何可展示的身份，整条丢掉。
    if (!name) continue;
    rows.push({
      alias: name,
      endpoint: sshEndpointOf(item),
      description: String(item.description || '').trim(),
      riskLevel: String(item.riskLevel || '').trim().toLowerCase(),
      pending: String(item.status || '').trim().toLowerCase() === 'pending_approval',
    });
  }
  return rows;
}

// sshClusterNodeRow 读 ssh_cluster action=add / 授权的结果：一台节点的扁平结果（列表
// 结果带 servers 数组，走 sshServerRows）。别名是唯一身份，缺别名就不是节点结果。
// changed 是「这次调用到底动没动已存状态」——新登记与「已登记但本次才授权」都改了东西，
// 只有后端明确回 false（该节点本来就已授权给当前工作区）才算没动，所以判据写成
// `!== false`：字段缺失时按「动了」显示，不能拿已存在当成没改动。已登记节点的结果里
// 没有 host/username，端点自然为空。
export function sshClusterNodeRow(source) {
  if (!source || typeof source !== 'object' || Array.isArray(source.servers)) return null;
  const alias = String(source.alias || '').trim();
  if (!alias) return null;
  return {
    alias,
    endpoint: sshEndpointOf(source),
    description: String(source.description || '').trim(),
    changed: source.changed !== false,
  };
}

// serviceRowOf 把一条服务记录映射成卡片行：列表元素与单条结果（start / stop）共用
// 这一份映射，免得两处的字段各自漂移。id 与 name 全空的行没有任何可展示的身份，
// 返回 null 由调用方丢掉。状态文案与色调不在这里判（那需要 i18n 与语义词表，见
// utils/taskStatus.mjs）。
function serviceRowOf(item) {
  if (!item || typeof item !== 'object') return null;
  const id = String(item.id || '').trim();
  const name = String(item.name || '').trim();
  if (!id && !name) return null;
  return {
    id,
    name,
    command: String(item.command || '').trim(),
    cwd: String(item.cwd || '').trim(),
    pid: Number(item.pid || 0) || 0,
    status: String(item.status || '').trim().toLowerCase(),
    exitCode: Number(item.exitCode || 0) || 0,
    outputBytes: Number(item.outputBytes || 0) || 0,
    startedAt: Number(item.startedAt || 0) || 0,
    error: String(item.error || '').trim(),
  };
}

// serviceListRows 读出一条 service action=list 结果的进程清单；null / [] 的约定
// 同上（null = 这不是列表结果）。
export function serviceListRows(source) {
  if (!source || typeof source !== 'object' || !Array.isArray(source.services)) return null;
  const rows = [];
  for (const item of source.services) {
    const row = serviceRowOf(item);
    if (row) rows.push(row);
  }
  return rows;
}

// serviceRow 读一条 service start / stop 结果的单条 ServiceInfo（见 internal/app 的
// ServiceInfo）。read 结果同样带 id，但它的正体是命令输出，必须整条挡在外面——否则
// 一次 service read 会被换成一张没有输出的服务卡，输出正文在卡片上再也看不到。
// 判据是 read 独有的 output（app.go 的 ServiceOutputResult 是 id/output/bytes/truncated，
// ServiceInfo 只有 outputTail），不是有则加一条的字段名猜测。
export function serviceRow(source) {
  if (!source || typeof source !== 'object' || Array.isArray(source.services)) return null;
  if (typeof source.output === 'string') return null;
  return serviceRowOf(source);
}

// scheduledTaskRowOf 把一个任务映射成卡片行：列表元素与 create 结果的单条任务本就
// 同形，共用这一份映射。schedule 原样带出——怎么写是 UI 文案，纯函数不碰；
// nextRunAt 是毫秒。
function scheduledTaskRowOf(item) {
  if (!item || typeof item !== 'object') return null;
  const id = String(item.id || '').trim();
  const name = String(item.name || '').trim();
  if (!id && !name) return null;
  return {
    id,
    name,
    schedule: item.schedule && typeof item.schedule === 'object' ? { ...item.schedule } : {},
    command: String(item.command || '').trim(),
    instruction: String(item.instruction || '').trim(),
    lastStatus: String(item.lastStatus || '').trim().toLowerCase(),
    running: item.running === true,
    nextRunAt: Number(item.nextRunAt || 0) || 0,
  };
}

// scheduledTaskRows 读出一条 scheduled_task action=list 结果的任务清单；null / [] 的
// 约定同上。
export function scheduledTaskRows(source) {
  if (!source || typeof source !== 'object' || !Array.isArray(source.tasks)) return null;
  const rows = [];
  for (const item of source.tasks) {
    const row = scheduledTaskRowOf(item);
    if (row) rows.push(row);
  }
  return rows;
}

// scheduledTaskRow 读一条 scheduled_task action=create 结果里的单条任务
// （ScheduledTaskToolResult.Task）：创建后当场给出这条任务的调度、下次执行与状态，
// 与列表里那行同一套字段。delete 的结果只有被删的 id，没有任务信息，返回 null
// （卡片保留文本体）。
export function scheduledTaskRow(source) {
  if (!source || typeof source !== 'object') return null;
  return scheduledTaskRowOf(source.task);
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

  // 折叠 + 尾部（create 卡流式时的正文预览）：只从末尾取需要的行，行数用只数换行的
  // 计数补出来，不切行数组。流式期间这个函数每拍都要跑一次，而内容会累积到几 MB：
  // 为末尾 6 行把整段切一遍会顺带产生几万个短命字符串。
  if (collapsed && mode === 'tail' && maxLines > 0) {
    const totalLines = countLinesCapped(code, Number.POSITIVE_INFINITY);
    if (totalLines <= maxLines) {
      return { lines: normalizedLines(code), startLine: 1, totalLines, omittedBefore: false, omittedAfter: false };
    }
    return {
      lines: tailLines(code, maxLines),
      startLine: totalLines - maxLines + 1,
      totalLines,
      omittedBefore: true,
      omittedAfter: false,
    };
  }

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


export function isRenderableMessage(msg) {
  if (msg?.role === 'tool_call' && msg?.kind === 'run') return false;
  if (msg?.role !== 'assistant') return true;
  return !assistantRowRenderState(msg).empty;
}

// 决定 assistant 行是否有可显示内容的字段收口于此：key 驱动显示列表缓存失效，
// empty 决定消息是否进入正文列表、归档额度与落盘保留。思考计数只在 composer 显示，
// 因而不属于消息行状态。
export function assistantRowRenderState(msg) {
  if (msg?.role !== 'assistant') return { key: '', empty: false };
  const hasBody = msg.hasBody === true;
  const error = !!msg.error;
  const system = !!msg.system;
  const welcome = !!msg.welcome;
  const attachments = Array.isArray(msg.attachments) ? msg.attachments.length : 0;
  const suggestions = Array.isArray(msg.suggestions) ? msg.suggestions.length : 0;
  // 本轮统计行（时长/cache/tokens/导出按钮）就挂在这根行上，悬停可见，不能藏
  const roundStats = !!msg.roundDurationText;
  const key = [hasBody, error, system, welcome, attachments, suggestions, roundStats].join(',');
  // 正文兜底读一把：老快照里存在“有正文但没有 hasBody 标志”的行，不能误隐藏；
  // 正文不进 key，写入正文的地方会同步置 hasBody，避免父级订阅每个流式增量。
  const empty = !error && !system && !welcome && !hasBody
    && !attachments && !suggestions && !roundStats
    && String(msg.content || '').trim() === '';
  return { key, empty };
}

export function displaySourceMessages(session, expandedArchiveSessions, options = {}) {
  const src = (session?.messages || []).filter(isRenderableMessage);
  if (!session) return src;
  const maxMessages = Number(options.maxMessages || 180);
  const expandedSet = expandedArchiveSessions || new Set();
  const expanded = expandedSet.has(session.id);
  const effectiveMaxMessages = expanded
    ? Number(options.expandedMaxMessages || maxMessages * 2)
    : maxMessages;
  if (src.length <= effectiveMaxMessages) return src;

  const keepStart = Math.max(0, src.length - effectiveMaxMessages);
  const archived = src.slice(0, keepStart);
  const archiveMsg = {
    role: 'archive',
    sessionId: session.id,
    expanded,
    count: archived.length,
  };
  return [archiveMsg, ...src.slice(keepStart)];
}

// formatPlanArgsTitle renders the title of a running plan card from the model's
// ARGUMENTS — deliberately not from the tool result, which is a different shape
// (a result carries {title,status} entries, while arguments carry step titles and
// a finish value). Reading a result key here left every running plan card with no
// title at all, in both the main chat and the sub-agent card.
export function formatPlanArgsTitle(parsed) {
  if (!parsed || typeof parsed !== 'object') return '';
  if (Array.isArray(parsed.steps)) {
    return parsed.steps.map((step) => String(step || '').trim()).find(Boolean) || '';
  }
  // A report names the step the work reached, which is what the card should read.
  // Anything else names no step and leaves the title to the result.
  return typeof parsed.finish === 'string' ? parsed.finish.trim() : '';
}
