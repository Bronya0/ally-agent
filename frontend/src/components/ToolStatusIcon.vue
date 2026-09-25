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

const props = defineProps({
  // Raw status string: running | success | completed | error | failed | pending...
  status: { type: String, default: '' },
});

// Normalize the status family across the different components that consume
// this icon (tool cards use success/error, sub-agent records also use
// completed/failed, plan items use done/in_progress/pending).
const statusClass = computed(() => {
  const s = props.status;
  if (s === 'success' || s === 'done' || s === 'completed') return 'is-success';
  if (s === 'error' || s === 'failed') return 'is-error';
  if (s === 'running' || s === 'in_progress') return 'is-running';
  return 'is-pending';
});

const icon = computed(() => {
  const s = props.status;
  if (s === 'success' || s === 'done' || s === 'completed') return 'check';
  if (s === 'error' || s === 'failed') return 'close';
  if (s === 'running' || s === 'in_progress') return 'dot';
  return 'hollow';
});
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

/* Filled running dot — the tool card's "in progress" mark, deliberately built in the
   shape of the composer status row's three bars (RunSpinner.vue), the one animation
   next to the message list that never hitches: a real child element absolutely
   positioned in a fixed 16x14 box, animating transform only, with no static
   will-change hint. A transform-only animation is the case every engine promotes on
   its own, and this dot lives in a card that is re-laid out and repainted on every
   tool:update flush, so its frames must not depend on the main thread.
   Do not put opacity back into the keyframes: the previous revision animated
   opacity + scale and visibly hitched while the card streamed. */
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
  /* 盒子恒定，只有 scale 在动（AGENTS.md §4.7）。 */
  animation: tool-svg-pulse 1.1s ease-in-out infinite;
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

@keyframes tool-svg-pulse {
  0%, 100% { transform: scale(0.6); }
  50% { transform: scale(1); }
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
