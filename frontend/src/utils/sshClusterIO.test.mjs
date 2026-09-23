/*
 * SPDX-License-Identifier: GPL-3.0-only
 *
 * Copyright (C) 2026 tangssst <tangssst@qq.com>
 * GitHub: https://github.com/Bronya0/ally-agent
 *
 * This file is part of ally-agent, licensed under the GNU General
 * Public License v3. See the LICENSE file for details.
 */
import test from 'node:test';
import assert from 'node:assert/strict';

import { buildSSHClusterExport, parseSSHClusterImport } from './sshClusterIO.mjs';

const node = (extra = {}) => ({
  alias: 'prod-web',
  host: '10.0.0.7',
  port: 2222,
  username: 'deploy',
  authType: 'password',
  keyPath: '',
  password: 'pw',
  description: 'web frontend',
  riskLevel: 'high',
  status: 'approved',
  createdBy: 'user',
  ...extra,
});

test('import normalizes the password field without trimming it', () => {
  const [parsed] = parseSSHClusterImport(JSON.stringify([node({ password: ' pw ' })]));
  assert.equal(parsed.password, ' pw ', 'surrounding spaces can be part of the password');
});

test('import clears credential slots that the auth type does not use', () => {
  // authType 是唯一真实源：agent 档带密码会让界面显示“免密”而后端真的去用密码。
  const [agent] = parseSSHClusterImport(JSON.stringify([node({ authType: 'agent', password: 'stale', keyPath: '/keys/k.pem' })]));
  assert.equal(agent.authType, 'agent');
  assert.equal(agent.password, '');
  assert.equal(agent.keyPath, '');

  const [byPassword] = parseSSHClusterImport(JSON.stringify([node({ authType: 'password', keyPath: '/keys/k.pem' })]));
  assert.equal(byPassword.keyPath, '', 'password auth must not keep a key path');
  assert.equal(byPassword.password, 'pw');

  // key 档保留密码槽：带口令的私钥要把口令存在那里（后端组合成 -i + askpass）。
  const [byKey] = parseSSHClusterImport(JSON.stringify([node({ authType: 'key', keyPath: '/keys/k.pem', password: 'passphrase' })]));
  assert.equal(byKey.keyPath, '/keys/k.pem');
  assert.equal(byKey.password, 'passphrase');
});

test('import rejects a file whose entries miss required fields', () => {
  assert.throws(() => parseSSHClusterImport(JSON.stringify([node({ description: '' })])), /INVALID/);
  assert.throws(() => parseSSHClusterImport('not json'), /INVALID/);
  assert.throws(() => parseSSHClusterImport(JSON.stringify([])), /INVALID/);
});

test('export is a faithful snapshot of stored nodes', () => {
  // 导出不替用户改配置：内存里是什么就搬什么（导入侧才做一致性归一）。
  const exported = buildSSHClusterExport([node({ authType: 'agent', password: 'stored' })]);
  assert.equal(exported.servers[0].authType, 'agent');
  assert.equal(exported.servers[0].password, 'stored');
});

test('round trip keeps the node identity fields', () => {
  const exported = buildSSHClusterExport([node()]);
  const [reimported] = parseSSHClusterImport(JSON.stringify(exported));
  assert.equal(reimported.alias, 'prod-web');
  assert.equal(reimported.host, '10.0.0.7');
  assert.equal(reimported.port, 2222);
  assert.equal(reimported.username, 'deploy');
  assert.equal(reimported.riskLevel, 'high');
  assert.equal(reimported.password, 'pw');
});
