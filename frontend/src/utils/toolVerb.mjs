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
  // utility
  calculate: ['Calculating', 'Calculated', 'Calculation'],
  wait: ['Waiting', 'Waited', 'Wait'],
  ask: ['Asking', 'Asked', 'Ask'],
  plan: ['Next step', 'Next step', 'Next step'],
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
const SERVICE_VERBS = {
  start: ['Starting service', 'Started service', 'Service start'],
  stop: ['Stopping service', 'Stopped service', 'Service stop'],
  list: ['Listing services', 'Listed services', 'Service list'],
  read: ['Reading service output', 'Read service output', 'Service read'],
};

// Fallback verbs by kind, for names not in the table above (e.g. MCP tools whose
// name is `mcp__server__tool`, or genuinely unknown tools bucketed as `other`).
const KIND_VERBS = {
  mcp: ['Calling', 'Called', 'Call'],
};

function isDone(status) {
  return status === 'success' || status === 'completed';
}

function isError(status) {
  return status === 'error' || status === 'failed';
}

// Returns the verb to show for a tool call. `name` is the raw backend tool name,
// `kind` the derived kind (used only as a fallback), `status` the call status.
// `action` disambiguates multi-action tools (currently scheduled_task and
// service); pass the parsed args.action when available, omit otherwise.
// On error the label names the action ("Delete failed") rather than a bare "Failed".
export function toolVerbLabel(name, kind, status, action) {
  let forms = TOOL_VERBS[name] || KIND_VERBS[kind] || null;
  if (name === 'scheduled_task' || name === 'service') {
    const key = String(action || '').trim().toLowerCase();
    const table = name === 'scheduled_task' ? SCHEDULED_TASK_VERBS : SERVICE_VERBS;
    forms = table[key] || forms;
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
