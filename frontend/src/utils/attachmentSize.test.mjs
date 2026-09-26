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

import { compactBytes, formatAttachmentSize } from './attachmentSize.mjs';

test('compactBytes spells a size without padding, for limits inside prose', () => {
  assert.equal(compactBytes(20 * 1024 * 1024), '20MB');
  assert.equal(compactBytes(412 * 1024), '412KB');
  assert.equal(compactBytes(300), '300B');
});

// The bracket label is supplied by the caller (the UI passes the i18n string),
// so these cases pin the zh spelling plus the en one used by the en table.
test('shows the sent size with the original in brackets', () => {
  assert.equal(formatAttachmentSize({ size: 6 * 1024 * 1024, sentSize: 412 * 1024 }, '原'), '412KB(原6MB)');
  assert.equal(
    formatAttachmentSize({ size: Math.round(1.1 * 1024 * 1024), sentSize: 200 * 1024 }, '原'),
    '200KB(原1.1MB)',
  );
  assert.equal(formatAttachmentSize({ size: 6 * 1024 * 1024, sentSize: 412 * 1024 }, 'was '), '412KB(was 6MB)');
});

test('shows a single size when nothing was saved', () => {
  // Native passthrough (<=256KB): the sent bytes are the file bytes.
  assert.equal(formatAttachmentSize({ size: 20 * 1024, sentSize: 20 * 1024 }), '20KB');
  // Text attachments and model-generated images carry no sent size at all.
  assert.equal(formatAttachmentSize({ size: 5 * 1024 }), '5KB');
  assert.equal(formatAttachmentSize({ size: 300 }), '300B');
});

test('never repeats the original when both sizes render the same', () => {
  assert.equal(formatAttachmentSize({ size: 18 * 1024, sentSize: 18 * 1024 - 1 }), '18KB');
});

test('ignores a sent size that is not smaller', () => {
  assert.equal(formatAttachmentSize({ size: 1024, sentSize: 2048 }), '1KB');
  assert.equal(formatAttachmentSize({}), '0B');
  assert.equal(formatAttachmentSize(null), '0B');
});
