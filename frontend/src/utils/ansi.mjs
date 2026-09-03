/*
 * SPDX-License-Identifier: GPL-3.0-only
 *
 * Copyright (C) 2026 tangssst <tangssst@qq.com>
 * GitHub: https://github.com/Bronya0/ally-agent
 *
 * This file is part of ally-agent, licensed under the GNU General
 * Public License v3. See the LICENSE file for details.
 */
/**
 * ANSI escape handling for terminal output (background service buffers,
 * command results).
 *
 * Terminal programs emit SGR escape sequences for color and style. Showing the
 * raw text renders control characters as garbage; stripping them throws away
 * signal the program deliberately encoded. This module converts them to styled
 * HTML through `ansi_up`, which escapes HTML by default — service/command
 * output is untrusted subprocess text and must never be injected verbatim.
 */
import { AnsiUp } from 'ansi_up';

const ESC = String.fromCharCode(27);

// CSI sequences: ESC [ ... final byte (@-~). Covers SGR (m), cursor moves, erase.
const CSI_PATTERN = new RegExp(`${ESC}\\[[0-9;?]*[ -/]*[@-~]`, 'g');
// OSC sequences: ESC ] ... terminated by BEL (0x07) or ST (ESC \). The body is
// matched lazily so a terminated sequence never swallows following text; the
// trailing branch handles an unterminated sequence at the end of a buffer.
const OSC_PATTERN = new RegExp(`${ESC}\\](?:[^\\u0007]*?(?:\\u0007|${ESC}\\\\)|[^\\u0007]*$)`, 'g');
// Charset selection and other two-byte escapes: ESC ( X / ESC ) X.
const SIMPLE_PATTERN = new RegExp(`${ESC}[()#][0-9A-Za-z]`, 'g');
// Non-global copy so `hasAnsi()` can test without mutating shared lastIndex.
const ANSI_DETECT_PATTERN = new RegExp(`${ESC}(?:\\[[0-9;?]*[ -/]*[@-~]|\\])`);

/**
 * Large buffers can reach 512 KiB. Converting all of it to styled HTML on every
 * refresh (1.5s while a service runs) is wasted work — only the tail is ever on
 * screen. Anything beyond this is dropped from the *rendered* markup; the copy
 * button still yields the full text.
 */
export const ANSI_RENDER_LIMIT = 128 * 1024;

const converter = new AnsiUp();
converter.escape_html = true;
converter.use_classes = false;

/**
 * True when the text contains ANSI escape sequences worth converting.
 * Used to skip HTML conversion (and its DOM cost) for plain-text buffers.
 */
export function hasAnsi(text) {
  const value = String(text ?? '');
  if (!value) return false;
  if (value.indexOf(ESC) === -1) return false;
  return ANSI_DETECT_PATTERN.test(value);
}

/**
 * Remove ANSI escape sequences, leaving plain text.
 * Used where HTML rendering is unavailable (clipboard, tool-call bodies).
 */
export function stripAnsi(text) {
  return String(text ?? '')
    .replace(OSC_PATTERN, '')
    .replace(CSI_PATTERN, '')
    .replace(SIMPLE_PATTERN, '');
}

/**
 * Convert terminal text (with SGR sequences) to HTML.
 *
 * @returns {{ html: string, renderedChars: number, droppedChars: number }}
 * `droppedChars` is how many leading characters were omitted because the input
 * exceeded `maxChars`; the caller should surface that to the user. The copy
 * button still yields the full text.
 */
export function renderAnsiToHtml(text, { maxChars = ANSI_RENDER_LIMIT } = {}) {
  const raw = String(text ?? '');
  if (!raw) return { html: '', renderedChars: 0, droppedChars: 0 };

  let body = raw;
  let droppedChars = 0;
  if (body.length > maxChars) {
    droppedChars = body.length - maxChars;
    body = body.slice(droppedChars);
    // Drop the partial line at the cut so the rendered buffer starts clean.
    const firstBreak = body.indexOf('\n');
    if (firstBreak > -1 && firstBreak < 4096) {
      droppedChars += firstBreak + 1;
      body = body.slice(firstBreak + 1);
    }
  }

  return { html: converter.ansi_to_html(body), renderedChars: body.length, droppedChars };
}
