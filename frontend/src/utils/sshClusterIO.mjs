// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.

// SSH 集群配置的导出/导入载荷（纯函数，不碰 DOM、不碰后端绑定）。
//
// 只搬运可跨机器复用的字段：id / createdAtMs / updatedAtMs 是本机身份与时间戳，
// 导入时留空由后端重新生成（SaveSSHServer 见到空 id 会 newID）。
// password / keyPath 原样导出——导出文件因此是明文凭据文件，UI 必须提示用户妥善保管；
// 导入时若文件里没有 password，则保留为空、由用户进编辑框补填（绝不编造默认值）。
//
// 校验口径与后端 SaveSSHServer 一致（alias / host / username / description 必填）。
// 这里整份校验、整份拒绝，避免导入到一半才逐条失败、留下半套配置。

export const SSH_CLUSTER_SCHEMA_VERSION = 1;

const AUTH_TYPES = new Set(['agent', 'key', 'password']);

function importError(code) {
  return Object.assign(new Error(code), { code });
}

function text(value) {
  if (value == null) return '';
  return String(value).trim();
}

function normalizePort(value) {
  const port = Number.parseInt(value, 10);
  return Number.isFinite(port) && port > 0 && port <= 65535 ? port : 22;
}

// strict=true：导入路径，缺必填字段即整份文件不合法；同时按认证方式清掉不该存在的
// 凭据槽（外来文件可能是 "authType: agent + password" 这种自相矛盾的组合，界面会显示
// 免密而后端真的去用密码）。
// strict=false：导出路径，内存里的节点已由后端校验过，原样搬运（导出是快照，
// 不在这里替用户改配置）。
function normalizeNode(raw, strict) {
  const node = raw && typeof raw === 'object' ? raw : {};
  const alias = text(node.alias);
  const host = text(node.host);
  const username = text(node.username);
  const description = text(node.description);
  if (strict && (!alias || !host || !username || !description)) {
    throw importError('INVALID');
  }
  const authType = text(node.authType).toLowerCase();
  const normalizedAuth = AUTH_TYPES.has(authType) ? authType : 'agent';
  const riskLevel = text(node.riskLevel).toLowerCase();
  const status = text(node.status).toLowerCase();
  const createdBy = text(node.createdBy).toLowerCase();
  // 密码不 trim：首尾空格可能就是密码的一部分，只能原样搬运。
  const rawPassword = typeof node.password === 'string' ? node.password : '';
  const rawKeyPath = text(node.keyPath);
  // key 档保留密码槽：带口令的私钥要把口令存在那里（后端按 keyPath + password
  // 组合成 -i + askpass）；agent 档两个槽都要空，password 档不认密钥路径。
  const credentials = !strict
    ? { keyPath: rawKeyPath, password: rawPassword }
    : normalizedAuth === 'agent'
      ? { keyPath: '', password: '' }
      : normalizedAuth === 'key'
        ? { keyPath: rawKeyPath, password: rawPassword }
        : { keyPath: '', password: rawPassword };
  return {
    alias,
    host,
    port: normalizePort(node.port),
    username,
    authType: normalizedAuth,
    keyPath: credentials.keyPath,
    password: credentials.password,
    description,
    riskLevel: riskLevel === 'high' ? 'high' : 'low',
    status: status === 'pending_approval' ? 'pending_approval' : 'approved',
    createdBy: createdBy === 'agent' ? 'agent' : 'user',
  };
}

export function buildSSHClusterExport(servers) {
  const list = Array.isArray(servers) ? servers : [];
  return {
    version: SSH_CLUSTER_SCHEMA_VERSION,
    exportedAt: new Date().toISOString(),
    servers: list.map((server) => normalizeNode(server, false)),
  };
}

export function parseSSHClusterImport(raw) {
  let parsed;
  try {
    parsed = JSON.parse(String(raw == null ? '' : raw));
  } catch (_) {
    throw importError('INVALID');
  }
  // 兼容本页导出的 { version, servers: [...] } 与手写的裸数组两种形态。
  const list = Array.isArray(parsed)
    ? parsed
    : (parsed && typeof parsed === 'object' && Array.isArray(parsed.servers) ? parsed.servers : null);
  if (!list || list.length === 0) {
    throw importError('INVALID');
  }
  return list.map((entry) => normalizeNode(entry, true));
}
