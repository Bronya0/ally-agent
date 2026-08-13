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

import { fmtNum, fmtTokens } from './format.mjs';

test('fmtTokens renders compact token counts', () => {
  assert.equal(fmtTokens(0), '0');
  assert.equal(fmtTokens(999), '999');
  assert.equal(fmtTokens(1234), '1.2k');
  assert.equal(fmtTokens(9999), '10.0k');
  assert.equal(fmtTokens(1234567), '1.23M');
  assert.equal(fmtTokens(1234567890), '1.23B');
});

test('fmtTokens tolerates falsy and string inputs', () => {
  assert.equal(fmtTokens(null), '0');
  assert.equal(fmtTokens(undefined), '0');
  assert.equal(fmtTokens(''), '0');
  assert.equal(fmtTokens('5200'), '5.2k');
});

test('fmtNum uses thousands separators (locale-independent)', () => {
  assert.equal(fmtNum(0), '0');
  assert.equal(fmtNum(1234567).replace(/[.,\s\u00a0]/g, ''), '1234567');
});
