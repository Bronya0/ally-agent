<!--
SPDX-License-Identifier: GPL-3.0-only

Copyright (C) 2026 tangssst <tangssst@qq.com>
GitHub: https://github.com/Bronya0/ally-agent

This file is part of ally-agent, licensed under the GNU General
Public License v3. See the LICENSE file for details.
-->

<!--
Shared tool/status icons from @vicons/antd. Replaces the previous Unicode
glyphs (U+2713 / U+2717 / U+25CF / U+25CB) whose rendering depended on
unpredictable system-font fallback inside WebView2 — different machines drew
them at different weights and baselines. SVG icons render identically
everywhere.

Usage:
  <ToolStatusIcon status="success" />
  <ToolStatusIcon :status="msg.status" />
-->
<template>
  <CheckOutlined v-if="icon === 'check'" :class="['tool-svg-icon', statusClass]" />
  <CloseOutlined v-else-if="icon === 'close'" :class="['tool-svg-icon', statusClass]" />
  <span v-else-if="icon === 'dot'" :class="['tool-svg-icon', 'running-dot', statusClass]" aria-hidden="true"><i class="running-dot-core"></i></span>
  <span v-else-if="icon === 'hollow'" :class="['tool-svg-icon', 'hollow-dot', statusClass]" aria-hidden="true"></span>
</template>

<script setup>
import { computed } from 'vue';
import CheckOutlined from '@vicons/antd/CheckOutlined';
import CloseOutlined from '@vicons/antd/CloseOutlined';
import { normalizeToolStatus } from '../utils/toolEventState.mjs';

const props = defineProps({
  // Raw status string: running | success | completed | error | failed | pending...
  status: { type: String, default: '' },
});

// Which raw strings mean success/error/running is decided once, in
// utils/toolEventState.mjs — the same function every tool card flips its status
// through. This component owns only the mapping from that canonical state to its
// own artifacts (a CSS class, a glyph), so a new alias is never taught twice and
// the two can no longer disagree about e.g. `completed`.
const state = computed(() => normalizeToolStatus(props.status));

const STATE_CLASS = { success: 'is-success', error: 'is-error', running: 'is-running', default: 'is-pending' };
const STATE_ICON = { success: 'check', error: 'close', running: 'dot', default: 'hollow' };

const statusClass = computed(() => STATE_CLASS[state.value] || STATE_CLASS.default);
const icon = computed(() => STATE_ICON[state.value] || STATE_ICON.default);
</script>

<style>
.tool-svg-icon {
  display: inline-flex;
  flex-shrink: 0;
  /* 16px for every status shape (check / close / dot / hollow). The running
     dot previously reserved 16px while check/close used 14px, so tool lines
     shifted 2px left on completion — the read-grep fold row made this visible
     right next to its shimmer animation. One width here keeps every status
     transition layout-stable; each glyph stays centered in its 16px box. */
  width: 16px;
  height: 14px;
  justify-content: center;
  align-items: center;
}

/* Filled running dot — the tool card's "in progress" mark: a solid 6px dot that
   fades softly in and out (opacity 1 ↔ 0.3, one cycle per 1.2s). Same signal the
   terminal coding agents put on their "Running…" line (Claude Code / Codex), but
   deliberately softer than their hard on/off blink: several tool cards can be
   running at once, and hard blinks out of phase read as urgency, while the 0.3
   floor keeps the dot visible at its dimmest so it never looks like it vanished.
   Opacity only — no scale and no rotation: the mark has to say "still working,
   nothing is wrong", and a dot that grows/shrinks or spins reads as urgency
   instead. Because the property is opacity, the box stays exactly 16x14 and the
   swap to the green check never shifts the line.
   The will-change below is what keeps the fade smooth: this dot lives in a card
   that is re-laid out and repainted on every tool:update flush, and a mark this
   small is not promoted to its own layer on its own, so without the hint its
   frames are drawn by the main thread and it hitches whenever the main thread is
   busy — which reads as a stutter even while nothing on the card is changing.
   The hint is dynamic: the class exists only while a card is running and is
   dropped when it finishes, so a long list never carries one layer per card
   (AGENTS.md §4.7). Reveal/blink tuning is a taste call; re-run the
   Rendering-panel comparison while the card streams before touching either. */
.tool-svg-icon.running-dot {
  position: relative;
  color: transparent;
}

.tool-svg-icon.running-dot .running-dot-core {
  position: absolute;
  inset: 0;
  margin: auto;
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: var(--ally-text-tertiary);
  /* 只动透明度：1.2s 一轮的软闪（亮 1 ↔ 暗 0.3 平滑渐变，不硬切），盒恒定、不缩放
     （AGENTS.md §4.7）。 */
  animation: tool-svg-blink 1.2s ease-in-out infinite;
  /* 提层提示：随“正在运行”这个类动态挂上、跑完即撤，不静态摊在长列表每一项上
     （AGENTS.md §4.7）。同一原则的另一处落点见 style.css 的 .subagent-name-dot。 */
  will-change: opacity;
}

/* 软闪：亮暗之间平滑来回，两端各一个半程（0.6s 亮 → 0.6s 暗），不做硬切。
   最暗留 0.3：暗下去仍然看得见，才不会被误认成“点没了 / 卡死了”。 */
@keyframes tool-svg-blink {
  0%, 100% { opacity: 1; }
  50% { opacity: 0.3; }
}

/* 减弱动效：停成一颗不动的灰点，并撤掉提层（不再动画就不占那一层）。 */
@media (prefers-reduced-motion: reduce) {
  .tool-svg-icon.running-dot .running-dot-core {
    animation: none;
    will-change: auto;
  }
}

/* Hollow pending dot */
.tool-svg-icon.hollow-dot {
  position: relative;
}

.tool-svg-icon.hollow-dot::after {
  content: '';
  position: absolute;
  inset: 0;
  margin: auto;
  width: 6px;
  height: 6px;
  border-radius: 50%;
  border: 1px solid currentColor;
}

/* Status colors — matches the previous .tool-status-icon.* palette in style.css */
.tool-svg-icon.is-success {
  color: #22c55e;
}

.tool-svg-icon.is-error {
  color: var(--ally-danger);
}

.tool-svg-icon.is-pending {
  color: var(--ally-text-ghost);
}

.tool-svg-icon.is-running .hollow-dot,
.tool-svg-icon.is-pending .hollow-dot {
  color: inherit;
}
</style>
