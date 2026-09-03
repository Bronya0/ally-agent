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

import { ANSI_RENDER_LIMIT, hasAnsi, renderAnsiToHtml, stripAnsi } from './ansi.mjs';

const ESC = String.fromCharCode(27);

test('hasAnsi detects SGR escapes and ignores plain text', () => {
  assert.equal(hasAnsi(`${ESC}[32mgreen${ESC}[0m`), true);
  assert.equal(hasAnsi(`${ESC}[1;38;5;208mbold orange${ESC}[39m`), true);
  assert.equal(hasAnsi('VITE v5.4.21 ready in 310 ms'), false);
  assert.equal(hasAnsi(''), false);
  assert.equal(hasAnsi(null), false);
});

test('stripAnsi removes CSI/OSC sequences but keeps the text', () => {
  assert.equal(stripAnsi(`${ESC}[32m${ESC}[1mVITE${ESC}[22m v5.4.21${ESC}[39m`), 'VITE v5.4.21');
  assert.equal(stripAnsi(`${ESC}]8;;http://x${ESC}\\link${ESC}]8;;${ESC}\\`), 'link');
  assert.equal(stripAnsi('plain'), 'plain');
  assert.equal(stripAnsi(undefined), '');
});

test('renderAnsiToHtml converts SGR colors to styled spans', () => {
  const { html } = renderAnsiToHtml(`${ESC}[31mboom${ESC}[0m`);
  assert.match(html, /<span style="color:rgb\(187,0,0\)">boom<\/span>/);
});

test('renderAnsiToHtml escapes HTML from untrusted subprocess output', () => {
  const { html } = renderAnsiToHtml('<img src=x onerror=alert(1)>');
  assert.doesNotMatch(html, /<img/);
  assert.match(html, /&lt;img/);
});

test('renderAnsiToHtml drops the head of oversized buffers', () => {
  const line = `${ESC}[32mok${ESC}[0m\n`;
  const huge = line.repeat(ANSI_RENDER_LIMIT);
  const { html, renderedChars, droppedChars } = renderAnsiToHtml(huge);
  assert.ok(droppedChars > 0, 'expected dropped characters');
  assert.ok(renderedChars <= ANSI_RENDER_LIMIT, 'rendered window must respect the limit');
  assert.equal(renderedChars + droppedChars, huge.length);
  assert.ok(html.length < huge.length, 'rendered markup should be smaller than input');
  assert.ok(!html.startsWith('ok'), 'partial line at the cut should be trimmed');
});

test('renderAnsiToHtml tolerates empty and non-string input', () => {
  assert.deepEqual(renderAnsiToHtml(''), { html: '', renderedChars: 0, droppedChars: 0 });
  assert.deepEqual(renderAnsiToHtml(null), { html: '', renderedChars: 0, droppedChars: 0 });
});
