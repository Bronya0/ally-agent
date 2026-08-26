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
import { formatReadRangeChip } from './toolFormat.mjs';

test('formatReadRangeChip full file has no range', () => {
  assert.equal(formatReadRangeChip(1, 10, 10, false), '· 10 lines');
});

test('formatReadRangeChip partial range shows start-end', () => {
  assert.equal(formatReadRangeChip(8, 12, 100, false), '· 5 lines 8-12');
});

test('formatReadRangeChip appends (truncated)', () => {
  assert.equal(formatReadRangeChip(8, 12, 100, true), '· 5 lines 8-12 · (truncated)');
});

test('formatReadRangeChip invalid input returns empty', () => {
  assert.equal(formatReadRangeChip(0, 0, 0, false), '');
  assert.equal(formatReadRangeChip('', '', '', false), '');
  assert.equal(formatReadRangeChip(NaN, NaN, 0, false), '');
});
