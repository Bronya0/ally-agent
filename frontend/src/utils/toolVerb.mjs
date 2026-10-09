/*
 * SPDX-License-Identifier: GPL-3.0-only
 *
 * Copyright (C) 2026 tangssst <tangssst@qq.com>
 * GitHub: https://github.com/Bronya0/ally-agent
 *
 * This file is part of ally-agent, licensed under the GNU General
 * Public License v3. See the LICENSE file for details.
 */
// Single source of truth for tool-card verbs, keyed by the REAL backend tool
// name (the authoritative name list lives in App.vue `toolKind`). Values are
// [inProgress, done] English tense forms; the status icon (✓ / ✗ / none) already
// conveys state, so the verb carries the action and no redundant kind label is
// shown next to it. Kept in English regardless of UI locale by design.
//
// The inline subagent card (SubagentInlineCard.vue) is the exception: it shows
// only the localized kind label ("子代理" / "Sub-agent") plus the status icon,
// no verb — the card already names the delegated task and shows its own
// progress, so a "Delegating" verb adds nothing.
//
// Keyed by tool name rather than by `kind` on purpose: `kind` is too coarse
// (web_fetch/http_request fall into
// `other`), which previously produced "Used web_fetch" / mixed CN-EN labels.
// Each entry is [inProgress, done, noun]. The noun is used for the error label
// ("<noun> failed", e.g. "Delete failed") so a failed card still names what failed
// instead of a bare "Failed".
const TOOL_VERBS = {
  // read / list
  read: ['Reading', 'Read', 'Read'],
  // 批形状的读（旧快照里仍有这个名字，result 里带 files[]）；没有它就只能显示通用的 Used。
  batch_read: ['Reading', 'Read', 'Read'],
  remote_read: ['Remote Reading', 'Remote Read', 'Remote Read'],
  list_files: ['Listing', 'Listed', 'List'],
  // write
  edit: ['Editing', 'Edited', 'Edit'],
  replace_exact: ['Editing', 'Edited', 'Edit'],
  replace_lines: ['Editing', 'Edited', 'Edit'],
  remote_edit: ['Remote Editing', 'Remote Edited', 'Remote Edit'],
  create: ['Creating', 'Created', 'Create'],
  remote_create_file: ['Remote Creating', 'Remote Created', 'Remote Create'],
  delete: ['Deleting', 'Deleted', 'Delete'],
  remote_delete_path: ['Remote Deleting', 'Remote Deleted', 'Remote Delete'],
  remote_transfer: ['Remote Transferring', 'Remote Transferred', 'Remote Transfer'],
  // ssh_cluster 一词里 cluster 本身说明了对象，失败时也说得出是什么（“SSH cluster
  // failed”），否则只剩一个无名 Failed。列表/登记的动作词见 SSH_CLUSTER_VERBS，
  // 这里是动作没被捕获时的兜底。
  ssh_cluster: ['Registering server', 'Registered server', 'SSH cluster'],
  // command / process
  command: ['Running', 'Ran', 'Command'],
  remote_run_command: ['Remote Running', 'Remote Ran', 'Remote Command'],
  Bash: ['Running', 'Ran', 'Command'],
  run: ['Running', 'Ran', 'Run'],
  // service is one backend tool multiplexing start/stop/list/read; this entry
  // is the fallback when the action is unknown (e.g. an old card whose args
  // weren't captured). Action-keyed forms live in SERVICE_VERBS below.
  service: ['Starting service', 'Started service', 'Service'],
  start_service: ['Starting service', 'Started service', 'Service'],
  stop_service: ['Stopping service', 'Stopped service', 'Service'],
  list_services: ['Listing services', 'Listed services', 'Service'],
  // search — grep keeps its literal tool name in every status and locale: the
  // name IS the action, so all three forms (inProgress, done, error noun) are
  // the same word and no translated kind label is shown anywhere.
  grep: ['Grep', 'Grep', 'Grep'],
  Glob: ['Matching', 'Matched', 'Match'],
  // network
  web_fetch: ['Fetching', 'Fetched', 'Fetch'],
  http_request: ['Requesting', 'Requested', 'Request'],
  render_html: ['Rendering', 'Rendered', 'Render'],
  // 截图（内置 screenshot 工具）：名字即动作（capture/list 由标题参数区分），
  // 不用时态动词——卡片名位显示 Screenshot，参数摘要走标题括号。
  screenshot: ['Screenshot', 'Screenshot', 'Screenshot'],
  // utility
  calculate: ['Calculating', 'Calculated', 'Calculation'],
  wait: ['Waiting', 'Waited', 'Wait'],
  ask: ['Asking', 'Asked', 'Ask'],
  // Fallback for a plan card that captured no action at all — an event whose
  // arguments never arrived, or a snapshot written before the action was
  // recorded. It names the tool without claiming an action.
  plan: ['Plan', 'Plan', 'Plan'],
  // scheduled_task verb depends on the action (create/list/delete), resolved via
  // SCHEDULED_TASK_VERBS below; this entry is the fallback when the action is
  // unknown (e.g. an old card whose args weren't captured).
  scheduled_task: ['Scheduling', 'Scheduled', 'Schedule'],
  // memory — now managed through read/edit (no dedicated tool)
  // agents / skills
  subagent: ['Delegating', 'Delegated', 'Sub-agent'],
  agent_delegate: ['Delegating', 'Delegated', 'Sub-agent'],
  skill: ['Loading skill', 'Loaded skill', 'Skill'],
  Skill: ['Loading skill', 'Loaded skill', 'Skill'],
};

// scheduled_task is one backend tool multiplexing several actions; a single
// "Scheduled" verb hides what actually happened. Key by action so the card reads
// "Created Scheduled Task" / "Deleted Scheduled Task" / "Listed Scheduled Tasks",
// mirroring how every other tool names its action. [inProgress, done, noun].
const SCHEDULED_TASK_VERBS = {
  create: ['Creating Scheduled Task', 'Created Scheduled Task', 'Scheduled task create'],
  delete: ['Deleting Scheduled Task', 'Deleted Scheduled Task', 'Scheduled task delete'],
  list: ['Listing Scheduled Tasks', 'Listed Scheduled Tasks', 'Scheduled task list'],
};

// service multiplexes start/stop/list/read through args.action like
// scheduled_task; key by action so a stop call reads "Stopped service" instead of
// always "Started service". [inProgress, done, noun].
// ssh_cluster 一个工具同时管 list 与 add；只写一个动词会把列表调用说成「登记了
// 服务器」（什么都没登记）。按动作取词，与 service / scheduled_task 同一套。
// [inProgress, done, noun]。
const SSH_CLUSTER_VERBS = {
  list: ['Listing servers', 'Listed servers', 'SSH cluster list'],
  add: ['Registering server', 'Registered server', 'SSH cluster add'],
};

const SERVICE_VERBS = {
  start: ['Starting service', 'Started service', 'Service start'],
  stop: ['Stopping service', 'Stopped service', 'Service stop'],
  list: ['Listing services', 'Listed services', 'Service list'],
  read: ['Reading service output', 'Read service output', 'Service read'],
};

// plan has no action field: which source the model sent IS the action, and the
// backend reads it the same way. Reporting progress ("Finished step") and the
// read-back are the two the card can state from the arguments; naming the last
// step ends the plan but is still a progress report, so it keeps that verb. The
// clear (`steps: []`) is its own verb because the backend reports it as a set
// while the card reads very differently.
// [inProgress, done, noun].
const PLAN_VERBS = {
  set: ['Planning', 'Planned', 'Plan set'],
  clear: ['Clearing plan', 'Cleared plan', 'Plan clear'],
  finish: ['Finishing step', 'Finished step', 'Step finish'],
  read: ['Reading plan', 'Read plan', 'Plan read'],
};

const REMOTE_TRANSFER_VERBS = {
  upload: ['Remote Uploading', 'Remote Uploaded', 'Remote Upload'],
  download: ['Remote Downloading', 'Remote Downloaded', 'Remote Download'],
};

// The tools whose verb comes from the action the call took rather than from the
// tool name alone, keyed by tool name. One table, so toolVerbLabel and
// isActionKeyedTool cannot disagree about which tools those are.
const ACTION_VERBS = {
  scheduled_task: SCHEDULED_TASK_VERBS,
  service: SERVICE_VERBS,
  plan: PLAN_VERBS,
  ssh_cluster: SSH_CLUSTER_VERBS,
  remote_transfer: REMOTE_TRANSFER_VERBS,
};

// Fallback verbs by kind, for names not in the table above (e.g. MCP tools whose
// name is `mcp__server__tool`, or genuinely unknown tools bucketed as `other`).
// MCP 动词带上 "MCP" 字样：紧跟的参数是 server/tool，不点明来源会读成一次普通工具调用。
const KIND_VERBS = {
  mcp: ['Calling MCP', 'Called MCP', 'Call MCP'],
};

function isDone(status) {
  return status === 'success' || status === 'completed';
}

function isError(status) {
  return status === 'error' || status === 'failed';
}

// Returns the verb to show for a tool call. `name` is the raw backend tool name,
// `kind` the derived kind (used only as a fallback), `status` the call status.
// `action` disambiguates the tools whose verb is keyed by the action they took
// (scheduled_task / service / plan) — pass toolActionFromArgs(...) for it, and
// omit it for every other tool.
// On error the label names the action ("Delete failed") rather than a bare "Failed".
export function toolVerbLabel(name, kind, status, action) {
  let forms = TOOL_VERBS[name] || KIND_VERBS[kind] || null;
  const actionVerbs = ACTION_VERBS[name];
  if (actionVerbs) {
    const key = String(action || '').trim().toLowerCase();
    forms = actionVerbs[key] || forms;
  }
  if (isError(status)) return forms ? `${forms[2]} failed` : 'Failed';
  if (forms) return isDone(status) ? forms[1] : forms[0];
  return isDone(status) ? 'Used' : 'Using';
}

// True when the tool has a dedicated verb, i.e. the verb already names the action
// and the card should NOT repeat a kind label in the name slot.
export function hasNamedVerb(name) {
  return Object.prototype.hasOwnProperty.call(TOOL_VERBS, name);
}

// MCP tools arrive as `mcp__server__tool` (see KIND_VERBS for the verb they get).
// The convention is spelled out here once: App.vue and SubagentInlineCard both
// branch on it, and a string prefix written twice is one copy too many.
export function isMcpToolName(name) {
  return typeof name === 'string' && name.startsWith('mcp__');
}

// True when the tool's verb is keyed by the action the call took, i.e. the
// caller has to capture that action and hand it to toolVerbLabel.
export function isActionKeyedTool(name) {
  return Object.prototype.hasOwnProperty.call(ACTION_VERBS, name);
}

// The action a call took, read from the model's own arguments — never from a
// tool result, whose shape is different. '' means the arguments state no action,
// so the caller keeps whatever it captured before.
export function toolActionFromArgs(name, parsed) {
  if (!isActionKeyedTool(name)) return '';
  if (name === 'plan') return planActionFromArgs(parsed);
  return String(parsed?.action || '').trim().toLowerCase();
}

// plan states its action by which source it carries — the same two the backend
// reads, and `finish` only when it names a step. A call carrying no source states
// nothing, so the answer stays '' and the card falls back to the plain tool name;
// that is deliberate, because arguments that have not arrived yet parse to the
// same {} a read-back call sends, and claiming "read" here would label every plan
// card that way until its arguments show up. The result names the action the
// backend took and fills that gap (useToolEvents).
function planActionFromArgs(parsed) {
  const args = parsed && typeof parsed === 'object' ? parsed : null;
  if (!args) return '';
  if (Array.isArray(args.steps)) return args.steps.length ? 'set' : 'clear';
  if (typeof args.finish === 'string') return args.finish.trim() ? 'finish' : '';
  return '';
}
