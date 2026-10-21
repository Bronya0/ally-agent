/*
 * SPDX-License-Identifier: GPL-3.0-only
 *
 * Copyright (C) 2026 tangssst <tangssst@qq.com>
 * GitHub: https://github.com/Bronya0/ally-agent
 *
 * This file is part of ally-agent, licensed under the GNU General
 * Public License v3. See the LICENSE file for details.
 */
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';

import {
  SSH_UNGROUPED_GROUP,
  groupSSHServers,
  normalizeSSHGroup,
  resolveActiveGroup,
  sshGroupOptions,
  sshServerMatchesQuery,
} from './sshClusterGroups.mjs';

const read = (relative) => readFileSync(new URL(relative, import.meta.url), 'utf8');

test('未分组字面量与后端 sshUngroupedGroup 是同一个值', () => {
  // 前端默认值与后端补齐的历史数据必须一致，否则历史节点会单独冒出一个“未分组”页签。
  const match = /sshUngroupedGroup\s*=\s*"([^"]+)"/.exec(read('../../../internal/app/biz_ssh_cluster.go'));
  assert.ok(match, 'Go 侧找不到 sshUngroupedGroup（改名了？两边要一起改）');
  assert.equal(SSH_UNGROUPED_GROUP, match[1]);
});

test('blank or missing group falls back to the ungrouped bucket', () => {
  assert.equal(normalizeSSHGroup(undefined), SSH_UNGROUPED_GROUP);
  assert.equal(normalizeSSHGroup(null), SSH_UNGROUPED_GROUP);
  assert.equal(normalizeSSHGroup('   '), SSH_UNGROUPED_GROUP);
  assert.equal(normalizeSSHGroup('  prod  '), 'prod');
});

test('groups are named-sorted with ungrouped last, members keep input order', () => {
  const groups = groupSSHServers([
    { alias: 'b1', group: 'staging' },
    { alias: 'legacy' }, // 历史节点没有 group 字段
    { alias: 'a1', group: 'prod' },
    { alias: 'b2', group: 'staging' },
  ]);
  assert.deepEqual(groups.map((g) => g.name), ['prod', 'staging', SSH_UNGROUPED_GROUP]);
  assert.deepEqual(groups[1].servers.map((s) => s.alias), ['b1', 'b2']);
});

test('dropdown options always offer the ungrouped default', () => {
  const values = sshGroupOptions([{ alias: 'a', group: 'prod' }]).map((o) => o.value);
  assert.deepEqual(values, ['prod', SSH_UNGROUPED_GROUP]);
  assert.deepEqual(sshGroupOptions([]).map((o) => o.value), [SSH_UNGROUPED_GROUP]);
});

test('search matches alias, endpoint fields and the group name, case-insensitively', () => {
  const server = { alias: 'Dev-Node-1', host: '10.0.0.7', username: 'deploy', description: 'web', group: '生产' };
  assert.equal(sshServerMatchesQuery(server, ''), true);
  assert.equal(sshServerMatchesQuery(server, '   '), true);
  assert.equal(sshServerMatchesQuery(server, 'dev-node'), true);
  assert.equal(sshServerMatchesQuery(server, '10.0.0'), true);
  assert.equal(sshServerMatchesQuery(server, '生产'), true, '分组名要能搜到，对应模型页按 provider 搜');
  assert.equal(sshServerMatchesQuery(server, '未分组'), false);
  // 历史节点没有 group 字段时不能报错也不能误判。
  assert.equal(sshServerMatchesQuery({ alias: 'legacy' }, 'legacy'), true);
});

test('active tab stays when still present, otherwise falls back to the first group', () => {
  const groups = [{ name: 'prod' }, { name: SSH_UNGROUPED_GROUP }];
  assert.equal(resolveActiveGroup(groups, SSH_UNGROUPED_GROUP), SSH_UNGROUPED_GROUP);
  assert.equal(resolveActiveGroup(groups, 'gone'), 'prod');
  assert.equal(resolveActiveGroup([], 'prod'), '');
});
