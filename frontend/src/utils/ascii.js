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
 * ASCII banner generator — renders text as a 5-line block-letter banner.
 * Uses full-block █ with guaranteed monospace font in CSS.
 */

const FONT = {
  A: [' ███ ', '██ ██', '█████', '██ ██', '██ ██'],
  B: ['████ ', '██ ██', '████ ', '██ ██', '████ '],
  C: [' ████', '██   ', '██   ', '██   ', ' ████'],
  D: ['████ ', '██ ██', '██ ██', '██ ██', '████ '],
  E: ['█████', '██   ', '████ ', '██   ', '█████'],
  F: ['█████', '██   ', '████ ', '██   ', '██   '],
  G: [' ████', '██   ', '██ ██', '██ ██', ' ████'],
  H: ['██ ██', '██ ██', '█████', '██ ██', '██ ██'],
  I: ['█████', '  █  ', '  █  ', '  █  ', '█████'],
  J: ['█████', '   █ ', '   █ ', '██ █ ', ' ███ '],
  K: ['██ ██', '██ █ ', '███  ', '██ █ ', '██ ██'],
  L: ['██   ', '██   ', '██   ', '██   ', '█████'],
  M: ['█   █', '██ ██', '█ █ █', '█   █', '█   █'],
  N: ['█   █', '██ ██', '█ █ █', '█  ██', '█   █'],
  O: [' ███ ', '██ ██', '██ ██', '██ ██', ' ███ '],
  P: ['████ ', '██ ██', '████ ', '██   ', '██   '],
  Q: [' ███ ', '██ ██', '██ ██', ' ███ ', '    █'],
  R: ['████ ', '██ ██', '████ ', '██ █ ', '██ ██'],
  S: [' ████', '██   ', ' ███ ', '   ██', '████ '],
  T: ['█████', '  █  ', '  █  ', '  █  ', '  █  '],
  U: ['██ ██', '██ ██', '██ ██', '██ ██', ' ███ '],
  V: ['██ ██', '██ ██', '██ ██', ' ███ ', '  █  '],
  W: ['█   █', '█   █', '█ █ █', '██ ██', '█   █'],
  X: ['██ ██', ' ███ ', '  █  ', ' ███ ', '██ ██'],
  Y: ['██ ██', ' ███ ', '  █  ', '  █  ', '  █  '],
  Z: ['█████', '   █ ', '  █  ', ' █   ', '█████'],
  ' ': ['     ', '     ', '     ', '     ', '     '],
};

const H = 5;

/**
 * Render text as a 5-line-tall ASCII banner with full-block characters.
 * Uses ```ascii-art code fence for transparent-background rendering.
 */
export function renderBanner(text) {
  const upper = text.toUpperCase();
  const lines = Array.from({ length: H }, () => '');
  for (const ch of upper) {
    const g = FONT[ch] || FONT[' '];
    for (let r = 0; r < H; r++) {
      if (lines[r]) lines[r] += ' ';
      lines[r] += g[r];
    }
  }
  return '```ascii-art\n' + lines.join('\n') + '\n```';
}
