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
// 锁死不可停用，其余内置工具可由用户开关。
// 默认已知名单作为离线/初始化底座，运行时通过后端 ListTools 动态扫描发现实际已注册的内置工具。
// 停用名单保存在 config.disabledTools，后端只在**新会话**注入 schema 时过滤。
export const protectedToolNames = ['read', 'edit', 'create', 'delete', 'command'];

export const defaultToggleableToolNames = [
  'list_files', 'grep', 'screenshot', 'service', 'wait', 'ask', 'suggest',
  'scheduled_task', 'http_request', 'web_fetch',
  'remote_read', 'remote_edit', 'remote_create_file', 'remote_delete_path', 'remote_run_command',
  'remote_transfer',
  'ssh_cluster', 'calculate', 'render_html', 'plan', 'subagent', 'skill',
];

const discoveredToolNames = new Set(defaultToggleableToolNames);

// 保持对旧引用兼容的导出数组，动态扫描发现新工具时会同步追加
export const toggleableToolNames = [...defaultToggleableToolNames];

export function isProtectedTool(name) {
  const clean = String(name || '').trim().toLowerCase();
  return protectedToolNames.includes(clean);
}

export function isToggleableTool(name) {
  const clean = String(name || '').trim().toLowerCase();
  if (!clean || isProtectedTool(clean)) return false;
  return discoveredToolNames.has(clean) || defaultToggleableToolNames.includes(clean);
}

export function registerDiscoveredTools(tools) {
  if (!Array.isArray(tools)) return;
  for (const item of tools) {
    const name = typeof item === 'string' ? item : item?.name;
    const clean = String(name || '').trim().toLowerCase();
    if (!clean || isProtectedTool(clean)) continue;
    if (!discoveredToolNames.has(clean)) {
      discoveredToolNames.add(clean);
      if (!toggleableToolNames.includes(clean)) {
        toggleableToolNames.push(clean);
      }
    }
  }
}

export function getToggleableToolNames() {
  return Array.from(discoveredToolNames);
}

export function resetDiscoveredTools() {
  discoveredToolNames.clear();
  for (const name of defaultToggleableToolNames) {
    discoveredToolNames.add(name);
  }
  toggleableToolNames.length = 0;
  toggleableToolNames.push(...defaultToggleableToolNames);
}

export function formatBuiltinToolLabel(name, translate) {
  const clean = String(name || '').trim();
  if (!clean) return '';
  const key = `settings.tool.${clean}`;
  if (typeof translate === 'function') {
    const localized = translate(key);
    if (localized && localized !== key) {
      return localized;
    }
  }
  return clean
    .split('_')
    .filter(Boolean)
    .map((word) => word.charAt(0).toUpperCase() + word.slice(1))
    .join(' ');
}
