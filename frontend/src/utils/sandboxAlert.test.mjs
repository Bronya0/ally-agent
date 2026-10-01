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

import { showsSandboxAlert } from './sandboxAlert.mjs';

test('service 的告警只跟随 start 动作', () => {
  assert.equal(showsSandboxAlert('service', 'start'), true);
  assert.equal(showsSandboxAlert('service', 'read'), false);
  assert.equal(showsSandboxAlert('service', 'stop'), false);
  assert.equal(showsSandboxAlert('service', 'list'), false);
});

test('service 动作未知时不显示（与动词退化成裸工具名同一个取舍）', () => {
  assert.equal(showsSandboxAlert('service', ''), false);
  assert.equal(showsSandboxAlert('service', undefined), false);
});

test('命令类工具照旧显示（它们的结果就是那次执行）', () => {
  assert.equal(showsSandboxAlert('command', ''), true);
  assert.equal(showsSandboxAlert('remote_run_command', 'stop'), true);
});
