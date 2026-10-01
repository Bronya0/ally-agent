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
import test from 'node:test';

import {
  isServiceActive,
  scheduledStatusKey,
  scheduledStatusTone,
  serviceStatusKey,
  serviceStatusTone,
} from './taskStatus.mjs';

test('service status maps to its label key', () => {
  assert.equal(serviceStatusKey('running'), 'common.running');
  assert.equal(serviceStatusKey('STARTING'), 'service.status.starting');
  assert.equal(serviceStatusKey('exited'), 'service.status.exited');
  // 表外的状态没有 key，调用方回落成原样显示状态串。
  assert.equal(serviceStatusKey('zombie'), '');
  assert.equal(serviceStatusKey(''), '');
  assert.equal(serviceStatusKey(null), '');
});

test('service tone separates a real failure from a clean exit', () => {
  assert.equal(serviceStatusTone({ status: 'running' }), 'success');
  assert.equal(serviceStatusTone({ status: 'starting' }), 'info');
  assert.equal(serviceStatusTone({ status: 'interrupted' }), 'warning');
  // 退出码才是成败判据：0 是中性的已退出，非 0 才是失败。
  assert.equal(serviceStatusTone({ status: 'exited', exitCode: 0 }), 'neutral');
  assert.equal(serviceStatusTone({ status: 'exited', exitCode: 2 }), 'danger');
  assert.equal(serviceStatusTone({ status: 'stopped' }), 'neutral');
  assert.equal(serviceStatusTone({}), 'neutral');
});

test('scheduled status maps to its label key', () => {
  assert.equal(scheduledStatusKey({ lastStatus: 'scheduled' }), 'scheduled.status.waiting');
  assert.equal(scheduledStatusKey({ lastStatus: 'timed_out' }), 'scheduled.status.timedOut');
  // 正在跑的覆盖上次结果：显示的就是「运行中」。
  assert.equal(scheduledStatusKey({ running: true, lastStatus: 'failed' }), 'common.running');
  assert.equal(scheduledStatusKey({ lastStatus: 'whatever' }), '');
  assert.equal(scheduledStatusKey(null), '');
});

test('scheduled tone never reports an unfinished run as success', () => {
  assert.equal(scheduledStatusTone({ running: true, lastStatus: 'completed' }), 'info');
  assert.equal(scheduledStatusTone({ lastStatus: 'completed' }), 'success');
  assert.equal(scheduledStatusTone({ lastStatus: 'timed_out' }), 'danger');
  assert.equal(scheduledStatusTone({ lastStatus: 'missed' }), 'warning');
  assert.equal(scheduledStatusTone({ lastStatus: 'scheduled' }), 'neutral');
  assert.equal(scheduledStatusTone({}), 'neutral');
});

// 「此刻还活着」的集合只有一处定义：徽标计数、事件合流、排序、任务中心都问它。
test('only starting and running count as an active service', () => {
  assert.ok(isServiceActive({ status: 'starting' }));
  assert.ok(isServiceActive({ status: 'running' }));
  assert.ok(isServiceActive({ status: 'RUNNING' }));
  assert.ok(!isServiceActive({ status: 'exited' }));
  assert.ok(!isServiceActive({ status: 'stopped' }));
  assert.ok(!isServiceActive({ status: 'interrupted' }));
  assert.ok(!isServiceActive({}));
  assert.ok(!isServiceActive(null));
});
