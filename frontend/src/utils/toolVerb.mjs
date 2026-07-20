// Single source of truth for tool-card verbs, keyed by the REAL backend tool
// name (the authoritative name list lives in App.vue `toolKind`). Values are
// [inProgress, done] English tense forms; the status icon (✓ / ✗ / none) already
// conveys state, so the verb carries the action and no redundant kind label is
// shown next to it. Kept in English regardless of UI locale by design.
//
// Keyed by tool name rather than by `kind` on purpose: `kind` is too coarse
// (memory_read vs memory_write collapse; web_fetch/http_request fall into
// `other`), which previously produced "Used web_fetch" / mixed CN-EN labels.
// Each entry is [inProgress, done, noun]. The noun is used for the error label
// ("<noun> failed", e.g. "Delete failed") so a failed card still names what failed
// instead of a bare "Failed".
const TOOL_VERBS = {
  // read / list
  read_file: ['Reading', 'Read', 'Read'],
  remote_read_file: ['Reading', 'Read', 'Read'],
  batch_read: ['Reading', 'Read', 'Read'],
  document_read: ['Reading', 'Read', 'Read'],
  list_files: ['Listing', 'Listed', 'List'],
  remote_list_files: ['Listing', 'Listed', 'List'],
  // write
  edit: ['Editing', 'Edited', 'Edit'],
  replace_exact: ['Editing', 'Edited', 'Edit'],
  replace_lines: ['Editing', 'Edited', 'Edit'],
  remote_edit: ['Editing', 'Edited', 'Edit'],
  create_file: ['Creating', 'Created', 'Create'],
  remote_create_file: ['Creating', 'Created', 'Create'],
  delete_path: ['Deleting', 'Deleted', 'Delete'],
  remote_delete_path: ['Deleting', 'Deleted', 'Delete'],
  // command / process
  run_command: ['Running', 'Ran', 'Command'],
  remote_run_command: ['Running', 'Ran', 'Command'],
  Bash: ['Running', 'Ran', 'Command'],
  run: ['Running', 'Ran', 'Run'],
  background_process: ['Starting service', 'Started service', 'Service'],
  start_service: ['Starting service', 'Started service', 'Service'],
  stop_service: ['Stopping service', 'Stopped service', 'Service'],
  list_services: ['Listing services', 'Listed services', 'Service'],
  // search
  grep_files: ['Searching', 'Searched', 'Search'],
  Glob: ['Matching', 'Matched', 'Match'],
  // network
  web_fetch: ['Fetching', 'Fetched', 'Fetch'],
  http_request: ['Requesting', 'Requested', 'Request'],
  render_html: ['Rendering', 'Rendered', 'Render'],
  // utility
  calculate: ['Calculating', 'Calculated', 'Calculation'],
  wait: ['Waiting', 'Waited', 'Wait'],
  ask: ['Asking', 'Asked', 'Ask'],
  todo_write: ['Updating plan', 'Updated plan', 'Plan update'],
  scheduled_task: ['Scheduling', 'Scheduled', 'Schedule'],
  // memory / goal — noun embedded because the verb alone is ambiguous
  memory_read: ['Reading memory', 'Read memory', 'Memory read'],
  memory_write: ['Saving memory', 'Saved memory', 'Memory write'],
  create_goal: ['Setting goal', 'Set goal', 'Goal'],
  update_goal: ['Updating goal', 'Updated goal', 'Goal'],
  get_goal: ['Reading goal', 'Read goal', 'Goal'],
  // agents / skills
  subagent: ['Delegating', 'Delegated', 'Sub-agent'],
  agent_delegate: ['Delegating', 'Delegated', 'Sub-agent'],
  Skill: ['Loading skill', 'Loaded skill', 'Skill'],
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
// On error the label names the action ("Delete failed") rather than a bare "Failed".
export function toolVerbLabel(name, kind, status) {
  const forms = TOOL_VERBS[name] || KIND_VERBS[kind] || null;
  if (isError(status)) return forms ? `${forms[2]} failed` : 'Failed';
  if (forms) return isDone(status) ? forms[1] : forms[0];
  return isDone(status) ? 'Used' : 'Using';
}

// True when the tool has a dedicated verb, i.e. the verb already names the action
// and the card should NOT repeat a kind label in the name slot.
export function hasNamedVerb(name) {
  return Object.prototype.hasOwnProperty.call(TOOL_VERBS, name);
}
