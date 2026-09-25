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
 * Cadence for coalescing the streaming `tool:update` burst into one card
 * re-render.
 *
 * A flush re-parses the whole accumulated payload (streaming args, or live
 * output) and rebuilds the card body, so its cost grows with the payload while
 * a fixed timer keeps firing at the same rate. On a big `create` / `edit` /
 * `command` payload that pins the main thread every tick and drops frames —
 * including anything animating next to the card, which then looks like it
 * stutters whenever the card updates. Large payloads therefore flush less
 * often: the body still arrives complete, just in coarser steps.
 *
 * One place owns the numbers so they can be tuned against a measurement and
 * asserted in toolUpdateFlush.test.mjs.
 */

/** Small payloads: the body follows the stream closely (the legacy cadence). */
export const TOOL_UPDATE_FLUSH_MS = 120;
/** ≥ TOOL_UPDATE_HEAVY_CHARS: often enough to look alive, rarely enough to stay cheap. */
export const TOOL_UPDATE_FLUSH_MS_HEAVY = 400;
/** ≥ TOOL_UPDATE_HUGE_CHARS: the body is a wall of text anyway; favour the frame budget. */
export const TOOL_UPDATE_FLUSH_MS_HUGE = 900;

export const TOOL_UPDATE_HEAVY_CHARS = 32 * 1024;
export const TOOL_UPDATE_HUGE_CHARS = 256 * 1024;

/**
 * Delay before the buffered tool updates are flushed, given the largest
 * accumulated payload buffered for this tick.
 *
 * `payloadChars` is `String.length` of that payload — a cheap size proxy in
 * UTF-16 code units, deliberately not a byte count (measuring real UTF-8 bytes
 * would walk the whole payload every tick, which is the cost we are avoiding).
 */
export function toolUpdateFlushDelay(payloadChars) {
  const chars = Number(payloadChars) || 0;
  if (chars >= TOOL_UPDATE_HUGE_CHARS) return TOOL_UPDATE_FLUSH_MS_HUGE;
  if (chars >= TOOL_UPDATE_HEAVY_CHARS) return TOOL_UPDATE_FLUSH_MS_HEAVY;
  return TOOL_UPDATE_FLUSH_MS;
}
