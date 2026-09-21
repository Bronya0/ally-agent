<!--
SPDX-License-Identifier: GPL-3.0-only

Copyright (C) 2026 tangssst <tangssst@qq.com>
GitHub: https://github.com/Bronya0/ally-agent

This file is part of ally-agent, licensed under the GNU General
Public License v3. See the LICENSE file for details.
-->
<template>
  <!-- Mode rail following the ES-King aside pattern: a permanently collapsed
       vertical n-menu — native icon items, hover tooltip with the label, and
       theme-driven active/hover states instead of hand-rolled buttons. -->
  <nav class="mode-sider" aria-label="mode">
    <n-menu
      :value="mode"
      mode="vertical"
      :collapsed="true"
      :collapsed-width="44"
      :collapsed-icon-size="20"
      :options="modeOptions"
      @update:value="onSelect"
    />
  </nav>
</template>

<script setup>
import { computed, h } from 'vue';
import { NIcon } from 'naive-ui';
import SlackOutlined from '@vicons/antd/SlackOutlined';
import BookOutlined from '@vicons/antd/BookOutlined';
import ThunderboltOutlined from '@vicons/antd/ThunderboltOutlined';
import ApiOutlined from '@vicons/antd/ApiOutlined';
import RobotOutlined from '@vicons/antd/RobotOutlined';
import BarChartOutlined from '@vicons/antd/BarChartOutlined';
import AppstoreOutlined from '@vicons/antd/AppstoreOutlined';
import SettingOutlined from '@vicons/antd/SettingOutlined';
import { t } from '../i18n.mjs';

// Crossed-swords icon for the games (play-vs-AI) entry: no swords glyph in
// @vicons/antd, so render an inline lucide-style SVG instead.
const SwordsIcon = () => h('svg', {
  viewBox: '0 0 24 24',
  fill: 'none',
  stroke: 'currentColor',
  'stroke-width': 2,
  'stroke-linecap': 'round',
  'stroke-linejoin': 'round',
}, [
  h('polyline', { points: '14.5 17.5 3 6 3 3 6 3 17.5 14.5' }),
  h('line', { x1: '13', y1: '19', x2: '19', y2: '13' }),
  h('line', { x1: '16', y1: '16', x2: '20', y2: '20' }),
  h('line', { x1: '19', y1: '21', x2: '21', y2: '19' }),
  h('polyline', { points: '14.5 6.5 18 3 21 3 21 6 17.5 10' }),
  h('line', { x1: '5', y1: '14', x2: '9', y2: '18' }),
  h('line', { x1: '7', y1: '17', x2: '4', y2: '20' }),
  h('line', { x1: '3', y1: '19', x2: '5', y2: '21' }),
]);

const props = defineProps({
  mode: { type: String, default: 'chat' },
  kbRunning: { type: Boolean, default: false },
});
const emit = defineEmits(['switch']);

const renderIcon = (icon, dot = false) => () => h('div', { class: 'mode-sider-icon-wrap' }, [
  h(NIcon, null, { default: () => h(icon) }),
  dot ? h('span', { class: 'mode-sider-running-dot', 'aria-label': t('header.running') }) : null,
]);

const modeOptions = computed(() => [
  { label: t('app.mode.chat'), key: 'chat', icon: renderIcon(SlackOutlined) },
  { label: t('app.mode.kb'), key: 'kb', icon: renderIcon(BookOutlined, props.kbRunning) },
  { label: t('app.mode.skills'), key: 'skills', icon: renderIcon(ThunderboltOutlined) },
  { label: t('app.mode.mcp'), key: 'mcp', icon: renderIcon(ApiOutlined) },
  { label: t('app.mode.models'), key: 'models', icon: renderIcon(RobotOutlined) },
  { label: t('header.tokenStats'), key: 'stats', icon: renderIcon(BarChartOutlined) },
  { label: t('header.games'), key: 'games', icon: renderIcon(SwordsIcon) },
  { label: t('header.settings'), key: 'settings', icon: renderIcon(SettingOutlined) },
]);

function onSelect(key) {
  emit('switch', key);
}
</script>
