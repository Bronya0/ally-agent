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
  buildHtmlRenderDocument,
  normalizeHtmlFrameHeight,
} from './htmlRender.mjs';

test('render document height measurement does not grow with iframe viewport height', () => {
  const document = buildHtmlRenderDocument('<div>content</div>', 'frame-token');

  assert.doesNotMatch(document, /html, body\s*\{[^}]*min-height:\s*100%/s);
  assert.match(document, /document\.body\.getBoundingClientRect\(\)\.height/);
  assert.match(document, /echarts/);
  assert.match(document, /window\.echarts\.init/);
  assert.equal(normalizeHtmlFrameHeight(201.2), 202);
  assert.equal(normalizeHtmlFrameHeight(600), 600);
  assert.equal(normalizeHtmlFrameHeight(601), 600);
});
