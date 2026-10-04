/*
 * SPDX-License-Identifier: GPL-3.0-only
 *
 * Copyright (C) 2026 tangssst <tangssst@qq.com>
 * GitHub: https://github.com/Bronya0/ally-agent
 *
 * This file is part of ally-agent, licensed under the GNU General
 * Public License v3. See the LICENSE file for details.
 */

// 内置工具启停名单：本地读/写/命令核心五件（AGENTS.md 工具分层里的基本工作面）
// 锁死不可停用，其余内置工具可由用户开关。名单是静态的（工具集随版本走），
// 停用名单保存在 config.disabledTools，后端只在**新会话**注入 schema 时过滤
// （已有会话冻结）。MCP 页开关层与设置侧草稿兜底过滤共用这一份。
export const protectedToolNames = ['read', 'edit', 'create', 'delete', 'command'];

export const toggleableToolNames = [
  'list_files', 'grep', 'screenshot', 'service', 'wait', 'ask', 'suggest',
  'scheduled_task', 'http_request', 'web_fetch',
  'remote_read', 'remote_edit', 'remote_create_file', 'remote_delete_path', 'remote_run_command',
  'ssh_cluster', 'calculate', 'render_html', 'plan', 'subagent', 'skill',
];
