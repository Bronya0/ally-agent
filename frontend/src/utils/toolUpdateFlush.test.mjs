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

import {
  TOOL_UPDATE_FLUSH_MS,
  TOOL_UPDATE_FLUSH_MS_HEAVY,
  TOOL_UPDATE_FLUSH_MS_HUGE,
  TOOL_UPDATE_HEAVY_CHARS,
  TOOL_UPDATE_HUGE_CHARS,
  toolUpdateFlushDelay,
} from './toolUpdateFlush.mjs';

test('small payloads keep the fast cadence', () => {
  assert.equal(toolUpdateFlushDelay(0), TOOL_UPDATE_FLUSH_MS);
  assert.equal(toolUpdateFlushDelay(1024), TOOL_UPDATE_FLUSH_MS);
  assert.equal(toolUpdateFlushDelay(TOOL_UPDATE_HEAVY_CHARS - 1), TOOL_UPDATE_FLUSH_MS);
});

test('payloads at the heavy threshold slow down', () => {
  assert.equal(toolUpdateFlushDelay(TOOL_UPDATE_HEAVY_CHARS), TOOL_UPDATE_FLUSH_MS_HEAVY);
  assert.equal(toolUpdateFlushDelay(TOOL_UPDATE_HUGE_CHARS - 1), TOOL_UPDATE_FLUSH_MS_HEAVY);
});

test('huge payloads slow down further', () => {
  assert.equal(toolUpdateFlushDelay(TOOL_UPDATE_HUGE_CHARS), TOOL_UPDATE_FLUSH_MS_HUGE);
  assert.equal(toolUpdateFlushDelay(8 * 1024 * 1024), TOOL_UPDATE_FLUSH_MS_HUGE);
});

test('cadence never gets faster as the payload grows', () => {
  const sizes = [0, 1024, TOOL_UPDATE_HEAVY_CHARS, TOOL_UPDATE_HUGE_CHARS, 4 * 1024 * 1024];
  const delays = sizes.map(toolUpdateFlushDelay);
  for (let i = 1; i < delays.length; i++) {
    assert.ok(delays[i] >= delays[i - 1], `${sizes[i]} must not flush faster than ${sizes[i - 1]}`);
  }
});

test('missing or non-numeric sizes fall back to the fast cadence', () => {
  for (const value of [undefined, null, '', NaN, 'not-a-number']) {
    assert.equal(toolUpdateFlushDelay(value), TOOL_UPDATE_FLUSH_MS);
  }
});
