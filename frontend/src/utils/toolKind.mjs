/*
 * SPDX-License-Identifier: GPL-3.0-only
 *
 * Copyright (C) 2026 tangssst <tangssst@qq.com>
 * GitHub: https://github.com/Bronya0/ally-agent
 *
 * This file is part of ally-agent, licensed under the GNU General
 * Public License v3. See the LICENSE file for details.
 */
// 工具卡「名字 → 展示属性」的唯一来源。一条 = 一个 kind：它包含哪些工具名，以及该
// kind 在卡片上长什么样（类型标签、要不要耗时角标、是否默认折叠、正体是否固定高度）。
//
// 这几件事以前散在 App.vue（toolKind、COLLAPSED_BY_DEFAULT_*）与 ToolCallCard.vue
// （类型标签表、NO_DURATION_*、isFixedKind）的 5 张表里，加一个工具要手改 5 处，
// 漏抄一处就静默显示不一致（远端读卡曾因此比本地读卡多一条耗时角标，没人发现）。
// 现在加工具只改这里。
//
// 两条硬性约束，破坏其一即静默失效：
//   1. 每个 kind 只能出现在一条上（kind → 属性是一张表，重复的后者覆盖前者）；
//   2. 工具名不能重复，且 toolKind() 会产出的每个 kind 都要有一条。
//
// 类型标签是**兜底**：动词表（utils/toolVerb.mjs 的 TOOL_VERBS）已经说清动作的工具
// 不会显示类型标签，所以没写 labelKey 的 kind 正常不会被看到——加工具的默认做法是往
// 动词表加一条动词，而不是加标签。
//
// 本文件不引入 i18n：标签只导出 i18n 键，由调用方自己 t()（utils/*.mjs 顶层拉
// i18n 会连 naive-ui 一起拉进 node --test，见 LESSONS）。
import { isMcpToolName } from './toolVerb.mjs';

const KIND_ENTRIES = [
  // 读 / 搜
  { kind: 'read', names: ['read'], labelKey: 'tools.kind.read', noDuration: true },
  { kind: 'remote_read', names: ['remote_read'], labelKey: 'tools.kind.read', noDuration: true },
  { kind: 'grep', names: ['grep'], labelKey: 'tools.kind.grep', noDuration: true, collapsed: true },
  { kind: 'glob', names: ['Glob'], labelKey: 'tools.kind.glob', noDuration: true },
  { kind: 'list', names: ['list_files'], labelKey: 'tools.kind.list', noDuration: true, collapsed: true },
  // 写
  { kind: 'edit', names: ['edit', 'replace_exact', 'replace_lines', 'remote_edit'], labelKey: 'tools.kind.edit', noDuration: true, fixedBody: true },
  { kind: 'create', names: ['create', 'remote_create_file'], labelKey: 'tools.kind.create', noDuration: true, collapsed: true, fixedBody: true },
  { kind: 'delete', names: ['delete', 'remote_delete_path'], labelKey: 'tools.kind.delete', noDuration: true },
  // 命令 / 进程
  { kind: 'command', names: ['command', 'remote_run_command', 'Bash'], labelKey: 'tools.kind.command', collapsed: true, fixedBody: true },
  { kind: 'run', names: ['run'], labelKey: 'tools.kind.run' },
  { kind: 'service', names: ['service', 'start_service', 'stop_service', 'list_services'], labelKey: 'tools.kind.service', noDuration: true },
  { kind: 'wait', names: ['wait'], labelKey: 'tools.kind.wait' },
  // 网络 / 渲染：http_request 与 web_fetch 没有专属 kind（动作由动词说清），
  // 与所有未知工具同归 other。
  { kind: 'other', names: ['http_request', 'web_fetch'], labelKey: 'tools.kind.tool', noDuration: true, collapsed: true },
  { kind: 'render_html', names: ['render_html'], labelKey: 'tools.kind.renderHtml' },
  // 截图：action 复用工具（capture/list）；结果本体（图片）进模型上下文，
  // 卡片只显示摘要，不做耗时角标
  { kind: 'screenshot', names: ['screenshot'], noDuration: true },
  // 其余：动词已说清动作，不给类型标签（labels 缺这些 kind 是故意的，不是漏抄）
  { kind: 'calculate', names: ['calculate'], labelKey: 'tools.kind.calculate', noDuration: true },
  { kind: 'plan', names: ['plan'], labelKey: 'tools.kind.plan', noDuration: true },
  { kind: 'ask', names: ['ask'], noDuration: true },
  { kind: 'skill', names: ['skill', 'Skill'], noDuration: true },
  { kind: 'ssh_cluster', names: ['ssh_cluster'], noDuration: true },
  { kind: 'scheduled', names: ['scheduled_task'], labelKey: 'tools.kind.scheduled' },
  { kind: 'subagent', names: ['subagent', 'agent_delegate'], labelKey: 'tools.kind.subagent' },
];

const ENTRY_BY_NAME = new Map();
const ENTRY_BY_KIND = new Map();
for (const entry of KIND_ENTRIES) {
  ENTRY_BY_KIND.set(entry.kind, entry);
  for (const name of entry.names) ENTRY_BY_NAME.set(name, entry);
}

// 工具名 → kind。MCP 工具按 `mcp__` 前缀判（唯一的命名约定判定在 toolVerb.mjs），
// 认不出来的落 other。
export function toolKindOf(name) {
  if (isMcpToolName(name)) return 'mcp';
  return ENTRY_BY_NAME.get(name)?.kind || 'other';
}

// kind → 类型标签的 i18n 键；'' 表示这类工具不显示标签（由调用方 t()）。
export function kindLabelKey(kind) {
  return ENTRY_BY_KIND.get(kind)?.labelKey || '';
}

// 这张卡要不要显示耗时角标。结果自身已含规模信息的工具（Read/Grep/Delete/…）与
// 计人不计工具的 ask 都不显示。
export function toolShowsDuration(name) {
  return !ENTRY_BY_NAME.get(name)?.noDuration;
}

// 这张卡是否默认折叠（只留固定几行预览）。
export function toolStartsCollapsed(name) {
  return ENTRY_BY_NAME.get(name)?.collapsed === true;
}

// 正体是否固定高度（内容再长也不撑高卡片，内部滚动）。
export function isFixedBodyKind(kind) {
  return ENTRY_BY_KIND.get(kind)?.fixedBody === true;
}
