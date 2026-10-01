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

import { attachmentIcon } from './attachmentIcon.mjs';

test('attachment kind maps to its fallback glyph text', () => {
  assert.equal(attachmentIcon({ kind: 'image' }), 'IMG');
  assert.equal(attachmentIcon({ kind: 'video' }), 'VID');
  assert.equal(attachmentIcon({ kind: 'audio' }), 'AUD');
  assert.equal(attachmentIcon({ kind: 'text' }), 'TXT');
  // file 与任何未识别的类型都落到 FILE，不会渲染出空白。
  assert.equal(attachmentIcon({ kind: 'file' }), 'FILE');
  assert.equal(attachmentIcon({ kind: 'future-kind' }), 'FILE');
  assert.equal(attachmentIcon({}), 'FILE');
  assert.equal(attachmentIcon(null), 'FILE');
});
