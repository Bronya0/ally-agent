// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.

// SSH 节点分组的纯函数：集群页与底部授权弹窗共用这一套口径（分组名归一、分组顺序、
// 表单下拉候选、当前页签回落），两边各写一份就会出现“弹窗里的组和集群页对不上”。
//
// 分组是纯归类（只影响 UI 的页签与筛选），不参与授权、凭据或模型侧的节点身份；
// 节点身份仍是别名。字面量与后端 biz_ssh_cluster.go 的 sshUngroupedGroup 必须一致，
// sshClusterGroups.test.mjs 会读 Go 源码钉住它。
export const SSH_UNGROUPED_GROUP = '未分组';

// 空白分组一律归入未分组；历史节点没有 group 字段时同样落到这里。
export function normalizeSSHGroup(value) {
  const group = String(value ?? '').trim();
  return group || SSH_UNGROUPED_GROUP;
}

// 具名分组按名字排序，未分组固定垫底；组内保持输入顺序（授权置顶等排序由调用方先定）。
export function groupSSHServers(servers) {
  const buckets = new Map();
  for (const server of Array.isArray(servers) ? servers : []) {
    const name = normalizeSSHGroup(server?.group);
    if (!buckets.has(name)) buckets.set(name, []);
    buckets.get(name).push(server);
  }
  const names = [...buckets.keys()].sort((a, b) => {
    if (a === SSH_UNGROUPED_GROUP) return 1;
    if (b === SSH_UNGROUPED_GROUP) return -1;
    return a.localeCompare(b, 'zh-Hans-CN');
  });
  return names.map((name) => ({ name, servers: buckets.get(name) }));
}

// 表单分组下拉的候选：已有分组 + 始终保留的未分组（新增节点的默认值）。
export function sshGroupOptions(servers) {
  const names = groupSSHServers(servers).map((group) => group.name);
  if (!names.includes(SSH_UNGROUPED_GROUP)) names.push(SSH_UNGROUPED_GROUP);
  return names.map((name) => ({ label: name, value: name }));
}

// 搜索口径：别名 / 地址 / 用户名 / 描述 / 分组名，大小写不敏感（分组名对应模型页按 provider 搜）。
// 集群页与授权弹窗共用，两边各写一份就会出现“这边搜得到、那边搜不到”。
export function sshServerMatchesQuery(server, query) {
  const needle = String(query ?? '').trim().toLowerCase();
  if (!needle) return true;
  return [server?.alias, server?.host, server?.username, server?.description, server?.group]
    .some((field) => String(field ?? '').toLowerCase().includes(needle));
}

// 当前页签仍在则保持，否则回落到第一个分组（与模型页的 provider 页签同一规则）。
export function resolveActiveGroup(groups, current) {
  const list = Array.isArray(groups) ? groups : [];
  if (list.some((group) => group.name === current)) return current;
  return list[0]?.name ?? '';
}
