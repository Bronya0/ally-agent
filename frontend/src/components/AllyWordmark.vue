<!--
SPDX-License-Identifier: GPL-3.0-only

Copyright (C) 2026 tangssst <tangssst@qq.com>
GitHub: https://github.com/Bronya0/ally-agent

This file is part of ally-agent, licensed under the GNU General
Public License v3. See the LICENSE file for details.
-->
<template>
  <span class="ally-wordmark" :aria-label="text">{{ text }}</span>
</template>

<script setup>
defineProps({
  text: { type: String, default: 'Ally' },
});
</script>

<style scoped>
/* 使用固定的银白→暖金→浅灰蓝→银白渐变，避免不同 WebView 的字体合成造成色带漂移。 */
.ally-wordmark {
  background: var(--ally-wordmark-gradient);
  background-repeat: no-repeat;
  background-size: 100% 100%;
  -webkit-background-clip: text;
  background-clip: text;
  color: transparent;
  -webkit-text-fill-color: transparent;
  font-family: 'Inter', sans-serif;
  font-weight: 700;
  font-synthesis: none;
  letter-spacing: -0.5px;
}
</style>
