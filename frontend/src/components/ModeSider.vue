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
import ClusterOutlined from '@vicons/antd/ClusterOutlined';
import BarChartOutlined from '@vicons/antd/BarChartOutlined';
import AppstoreOutlined from '@vicons/antd/AppstoreOutlined';
import SettingOutlined from '@vicons/antd/SettingOutlined';
import BugOutlined from '@vicons/antd/BugOutlined';
import CloudServerOutlined from '@vicons/antd/CloudServerOutlined';
import CodeOutlined from '@vicons/antd/CodeOutlined';
import DatabaseOutlined from '@vicons/antd/DatabaseOutlined';
import DeploymentUnitOutlined from '@vicons/antd/DeploymentUnitOutlined';
import FileTextOutlined from '@vicons/antd/FileTextOutlined';
import GlobalOutlined from '@vicons/antd/GlobalOutlined';
import ProjectOutlined from '@vicons/antd/ProjectOutlined';
import ToolOutlined from '@vicons/antd/ToolOutlined';
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
  // 高级设置里的隐藏页面名单（mode key 小写）：命中即不在菜单渲染。
  hiddenModes: { type: Array, default: () => [] },
  // 已启用插件声明的菜单项：{ mode, title, icon, group }。icon 只是名字，由下面的
  // 白名单解析——插件只能给字符串，不能往宿主的组件树里塞组件。group 非空时该页面
  // 会被合进同一个一级菜单下（key 相同即同组）。
  pluginItems: { type: Array, default: () => [] },
});
const emit = defineEmits(['switch']);

// 插件可选图标白名单（名字 → 组件）。没列进白名单的名字一律回退默认图标。
const PLUGIN_ICON_WHITELIST = {
  ApiOutlined,
  AppstoreOutlined,
  BarChartOutlined,
  BugOutlined,
  CloudServerOutlined,
  ClusterOutlined,
  CodeOutlined,
  DatabaseOutlined,
  DeploymentUnitOutlined,
  FileTextOutlined,
  GlobalOutlined,
  ProjectOutlined,
  RobotOutlined,
  SettingOutlined,
  ThunderboltOutlined,
  ToolOutlined,
};

function pluginIcon(name) {
  return PLUGIN_ICON_WHITELIST[String(name || '').trim()] || AppstoreOutlined;
}

const renderIcon = (icon, dot = false) => () => h('div', { class: 'mode-sider-icon-wrap' }, [
  h(NIcon, null, { default: () => h(icon) }),
  dot ? h('span', { class: 'mode-sider-running-dot', 'aria-label': t('header.running') }) : null,
]);

// 一级菜单（分组）图标上的小点：组里有页面正在显示时点亮。与「运行中」那个点同族但
// 语义不同，所以用独立的类名与 aria 文案，免得读屏软件把两者说成同一回事。
const renderGroupIcon = (icon, active) => () => h('div', { class: 'mode-sider-icon-wrap' }, [
  h(NIcon, null, { default: () => h(icon) }),
  active ? h('span', { class: 'mode-sider-group-dot', 'aria-label': t('app.mode.groupActive') }) : null,
]);

const hiddenSet = computed(() => new Set((props.hiddenModes || []).map((key) => String(key || '').trim().toLowerCase())));

// chat/settings 永不隐藏：后端清洗与 assignConfig 都不会产出它们，这里再加一道
// 最后一道闸——万一名单里混进来，Agent 与设置入口也不能被藏掉。
const baseModeOptions = computed(() => [
  { label: t('app.mode.chat'), key: 'chat', icon: renderIcon(SlackOutlined) },
  { label: t('app.mode.kb'), key: 'kb', icon: renderIcon(BookOutlined, props.kbRunning) },
  { label: t('app.mode.skills'), key: 'skills', icon: renderIcon(ThunderboltOutlined) },
  { label: t('app.mode.mcp'), key: 'mcp', icon: renderIcon(ApiOutlined) },
  { label: t('app.mode.models'), key: 'models', icon: renderIcon(RobotOutlined) },
  { label: t('app.mode.sshCluster'), key: 'ssh', icon: renderIcon(ClusterOutlined) },
  { label: t('header.tokenStats'), key: 'stats', icon: renderIcon(BarChartOutlined) },
  { label: t('header.games'), key: 'games', icon: renderIcon(SwordsIcon) },
  { label: t('app.mode.plugins'), key: 'plugins', icon: renderIcon(AppstoreOutlined) },
  { label: t('header.settings'), key: 'settings', icon: renderIcon(SettingOutlined) },
].filter((item) => item.key === 'chat' || item.key === 'settings' || !hiddenSet.value.has(item.key)));

// 插件页插在设置之前：设置永远排最后。插件项由 App.vue 从后端列表派生（只含启用中
// 且清单合法的插件），这里只负责渲染。
//
// 声明了 group 的插件会被合进同一个一级菜单（子菜单，悬停展开）：合并键是 group.key，
// 所以插件之间不需要知道对方存在；展示用的 title/icon 以第一个声明者为准，同组插件
// 应当写成一样（写得不一致时不会报错，只是菜单上看到的是先出现的那份）。
const modeOptions = computed(() => {
  const items = baseModeOptions.value;
  const pluginItems = (props.pluginItems || [])
    .filter((item) => item && item.mode)
    .map((item) => ({
      label: String(item.title || item.mode || ''),
      key: String(item.mode || ''),
      icon: renderIcon(pluginIcon(item.icon)),
      group: item.group && item.group.key ? item.group : null,
    }));
  if (!pluginItems.length) return items;

  const groups = new Map();
  const topLevel = [];
  for (const item of pluginItems) {
    const child = { label: item.label, key: item.key, icon: item.icon };
    if (!item.group) {
      topLevel.push(child);
      continue;
    }
    const key = String(item.group.key);
    if (!groups.has(key)) {
      groups.set(key, {
        key,
        title: String(item.group.title || item.group.key),
        icon: item.group.icon || '',
        children: [],
      });
    }
    groups.get(key).children.push(child);
  }

  const groupItems = [...groups.values()].map((group) => ({
    label: group.title,
    key: `group:${group.key}`,
    icon: renderGroupIcon(pluginIcon(group.icon), group.children.some((child) => child.key === props.mode)),
    children: group.children,
  }));

  const settingsIndex = items.findIndex((item) => item.key === 'settings');
  const next = [...items];
  next.splice(settingsIndex < 0 ? next.length : settingsIndex, 0, ...groupItems, ...topLevel);
  return next;
});

function onSelect(key) {
  emit('switch', key);
}
</script>
