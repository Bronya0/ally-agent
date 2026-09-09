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
      :collapsed-icon-size="22"
      :options="modeOptions"
      @update:value="onSelect"
    />
  </nav>
</template>

<script setup>
import { computed, h } from 'vue';
import { NIcon } from 'naive-ui';
import HomeOutlined from '@vicons/antd/HomeOutlined';
import BookOutlined from '@vicons/antd/BookOutlined';
import ThunderboltOutlined from '@vicons/antd/ThunderboltOutlined';
import ApiOutlined from '@vicons/antd/ApiOutlined';
import RobotOutlined from '@vicons/antd/RobotOutlined';
import BarChartOutlined from '@vicons/antd/BarChartOutlined';
import AppstoreOutlined from '@vicons/antd/AppstoreOutlined';
import SettingOutlined from '@vicons/antd/SettingOutlined';
import { t } from '../i18n.mjs';

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
  { label: t('app.mode.chat'), key: 'chat', icon: renderIcon(HomeOutlined) },
  { label: t('app.mode.kb'), key: 'kb', icon: renderIcon(BookOutlined, props.kbRunning) },
  { label: t('app.mode.skills'), key: 'skills', icon: renderIcon(ThunderboltOutlined) },
  { label: t('app.mode.mcp'), key: 'mcp', icon: renderIcon(ApiOutlined) },
  { label: t('app.mode.models'), key: 'models', icon: renderIcon(RobotOutlined) },
  { label: t('header.tokenStats'), key: 'stats', icon: renderIcon(BarChartOutlined) },
  { label: t('header.games'), key: 'games', icon: renderIcon(AppstoreOutlined) },
  { label: t('header.settings'), key: 'settings', icon: renderIcon(SettingOutlined) },
]);

function onSelect(key) {
  emit('switch', key);
}
</script>
