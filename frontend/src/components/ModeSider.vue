<!--
SPDX-License-Identifier: GPL-3.0-only

Copyright (C) 2026 tangssst <tangssst@qq.com>
GitHub: https://github.com/Bronya0/ally-agent

This file is part of ally-agent, licensed under the GNU General
Public License v3. See the LICENSE file for details.
-->
<template>
  <!-- Mode rail following the ES-King aside pattern: a vertical n-menu that is
       icon-only while collapsed (native icon items, hover tooltip with the
       label) and spells out every label once the bottom handle expands it.
       Theme-driven active/hover states instead of hand-rolled buttons. -->
  <nav class="mode-sider" :class="{ 'mode-sider--expanded': expanded, 'mode-sider--resizing': resizing }" aria-label="mode">
    <div class="mode-sider-menu">
      <n-scrollbar style="height: 100%">
        <n-menu
          :value="mode"
          mode="vertical"
          :collapsed="!expanded"
          collapse-mode="width"
          :collapsed-width="44"
          :collapsed-icon-size="20"
          :root-indent="12"
          :theme-overrides="expanded ? EXPANDED_MENU_THEME : undefined"
          :options="modeOptions"
          @update:value="onSelect"
        />
      </n-scrollbar>
    </div>
    <!-- 展开态下的历史工作空间：与顶栏「历史工作空间」下拉同一份数据、同一组事件。
         点击条目新开 Tab，右侧 × 从历史移除，标题行 + 添加工作空间，列表首项创建临时工作空间。 -->
    <section v-if="expanded" class="mode-sider-history" :class="{ 'mode-sider-history--collapsed': historyCollapsed }" :aria-label="t('header.workspaceHistory')">
      <div class="mode-sider-history-header">
        <button
          type="button"
          class="mode-sider-history-heading"
          :aria-expanded="!historyCollapsed"
          @click="historyCollapsed = !historyCollapsed"
        >
          <n-icon :component="FolderOutlined" :size="15" />
          <span>{{ t('header.workspaceHistory') }}</span>
          <n-icon class="mode-sider-history-chevron" :component="historyCollapsed ? RightOutlined : DownOutlined" :size="10" />
        </button>
        <button
          type="button"
          class="mode-sider-history-add"
          :title="t('header.addWorkspace')"
          :aria-label="t('header.addWorkspace')"
          @click="emit('addWorkspace')"
        >
          <n-icon :component="PlusOutlined" :size="13" />
        </button>
      </div>
      <div v-show="!historyCollapsed" class="mode-sider-history-scroll">
        <n-scrollbar content-class="mode-sider-history-list" style="height: 100%">
          <button
            type="button"
            class="mode-sider-history-item mode-sider-history-temp"
            :title="t('header.tempWorkspaceHint')"
            @click="emit('addTempWorkspace')"
          >
            <span class="mode-sider-history-name">{{ t('header.tempWorkspace') }}</span>
          </button>
          <div v-if="!recentWorkspaces.length" class="mode-sider-history-empty">{{ t('app.history.empty') }}</div>
          <div
            v-for="path in recentWorkspaces"
            :key="path"
            class="mode-sider-history-item"
            role="button"
            tabindex="0"
            :title="path"
            @click="emit('openWorkspace', path)"
            @keydown.enter.prevent="emit('openWorkspace', path)"
            @keydown.space.prevent="emit('openWorkspace', path)"
          >
            <span class="mode-sider-history-name">{{ workspaceName(path) }}</span>
            <button
              type="button"
              class="hist-del mode-sider-history-del"
              :title="t('app.history.remove')"
              :aria-label="t('app.history.remove')"
              @click.stop="emit('removeWorkspace', path)"
              @keydown.stop
            >
              <CloseOutlined />
            </button>
          </div>
        </n-scrollbar>
      </div>
    </section>
    <section v-if="expanded" class="mode-sider-history" :class="{ 'mode-sider-history--collapsed': sessionsCollapsed }" :aria-label="t('app.mode.workspaceSessions')">
      <div class="mode-sider-history-header">
        <button
          type="button"
          class="mode-sider-history-heading"
          :aria-expanded="!sessionsCollapsed"
          @click="sessionsCollapsed = !sessionsCollapsed"
        >
          <n-icon :component="HistoryOutlined" :size="15" />
          <span class="mode-sider-history-name">{{ t('app.mode.workspaceSessions') }}</span>
          <n-icon class="mode-sider-history-chevron" :component="sessionsCollapsed ? RightOutlined : DownOutlined" :size="10" />
        </button>
        <button
          type="button"
          class="mode-sider-history-add mode-sider-session-add"
          :title="t('app.sessions.newSession')"
          :aria-label="t('app.sessions.newSession')"
          @click="emit('newSession')"
        >
          <n-icon :component="PlusOutlined" :size="13" />
        </button>
      </div>
      <div v-if="!sessionsCollapsed" class="mode-sider-history-scroll">
        <n-scrollbar content-class="mode-sider-history-list" style="height: 100%">
          <div v-if="!workspaceSessions.length" class="mode-sider-history-empty">{{ t('app.mode.emptySessions') }}</div>
          <div
            v-for="session in workspaceSessions"
            :key="session.id"
            class="mode-sider-history-item"
            :class="{ 'mode-sider-history-item--current': session.id === activeSessionId }"
            role="button"
            tabindex="0"
            :title="sessionTooltip(session)"
            :aria-current="session.id === activeSessionId ? 'true' : undefined"
            @click="emit('openSession', session.id)"
            @keydown.enter.prevent="emit('openSession', session.id)"
            @keydown.space.prevent="emit('openSession', session.id)"
          >
            <span class="mode-sider-history-name">{{ sessionTitle(session) }}</span>
            <span v-if="sessionBusy(session)" class="mode-sider-session-busy" :aria-label="t('app.mode.sessionBusy')">●</span>
            <button
              type="button"
              class="hist-del mode-sider-history-del"
              :title="t('app.sessions.delete')"
              :aria-label="t('app.sessions.delete')"
              :disabled="sessionBusy(session)"
              @click.stop="emit('removeSession', session.id)"
              @keydown.stop
            >
              <CloseOutlined />
            </button>
          </div>
        </n-scrollbar>
      </div>
    </section>
    <button
      type="button"
      class="mode-sider-toggle"
      :title="toggleLabel"
      :aria-label="toggleLabel"
      :aria-expanded="expanded"
      @click="emit('toggle')"
    >
      <n-icon :component="expanded ? DoubleLeftOutlined : DoubleRightOutlined" :size="16" />
    </button>
    <div
      v-if="expanded"
      class="mode-sider-resize-handle"
      role="separator"
      tabindex="0"
      aria-orientation="vertical"
      :aria-label="t('app.mode.resizeRail')"
      :aria-valuemin="RAIL_MIN_WIDTH"
      :aria-valuemax="RAIL_MAX_WIDTH"
      :aria-valuenow="expandedWidth"
      :title="t('app.mode.resizeRail')"
      @pointerdown="startResize"
      @pointermove="moveResize"
      @pointerup="finishResize"
      @pointercancel="finishResize"
      @lostpointercapture="finishResize"
      @keydown="resizeWithKeyboard"
    />
  </nav>
</template>

<script setup>
import { computed, h, onBeforeUnmount, ref, watch } from 'vue';
import { NIcon } from 'naive-ui';
import SlackOutlined from '@vicons/antd/SlackOutlined';
import BookOutlined from '@vicons/antd/BookOutlined';
import ThunderboltOutlined from '@vicons/antd/ThunderboltOutlined';
import ApiOutlined from '@vicons/antd/ApiOutlined';
import RobotOutlined from '@vicons/antd/RobotOutlined';
import ClusterOutlined from '@vicons/antd/ClusterOutlined';
import BarChartOutlined from '@vicons/antd/BarChartOutlined';
import AppstoreOutlined from '@vicons/antd/AppstoreOutlined';
import DoubleLeftOutlined from '@vicons/antd/DoubleLeftOutlined';
import DoubleRightOutlined from '@vicons/antd/DoubleRightOutlined';
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
import FolderOutlined from '@vicons/antd/FolderOutlined';
import PlusOutlined from '@vicons/antd/PlusOutlined';
import CloseOutlined from '@vicons/antd/CloseOutlined';
import RightOutlined from '@vicons/antd/RightOutlined';
import DownOutlined from '@vicons/antd/DownOutlined';
import HistoryOutlined from '@vicons/antd/HistoryOutlined';
import { loadRailSectionCollapsed, saveRailSectionCollapsed } from '../utils/railSections.mjs';
import { t } from '../i18n.mjs';
import { normalizeRailWidth, RAIL_DEFAULT_WIDTH, RAIL_MIN_WIDTH, RAIL_MAX_WIDTH } from '../utils/railWidth.mjs';

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
  // 展开态由 App 持有：那个宽度变量还被三处按它定位的遮罩消费，其中两处被 Teleport
  // 到 body，只能从 <html> 读值，所以状态不能在组件内自闭环。
  expanded: { type: Boolean, default: false },
  expandedWidth: { type: Number, default: RAIL_DEFAULT_WIDTH },
  // 最近工作区列表：从 App 传入，复用 header 里的历史列表逻辑
  recentWorkspaces: { type: Array, default: () => [] },
  workspaceSessions: { type: Array, default: () => [] },
  activeSessionId: { type: String, default: '' },
  sessionTitle: { type: Function, default: (session) => session.title || '' },
  sessionTooltip: { type: Function, default: (session) => session.title || '' },
  sessionBusy: { type: Function, default: () => false },
});
const emit = defineEmits(['switch', 'toggle', 'openWorkspace', 'addWorkspace', 'addTempWorkspace', 'removeWorkspace', 'openSession', 'removeSession', 'newSession', 'openTaskCenter', 'resize', 'resizeEnd']);

// 展开态是紧凑列表：行高从 Naive 默认 42px 压到 36px；收起态保持默认，图标格不变。
const EXPANDED_MENU_THEME = { itemHeight: '36px' };
function rememberedCollapsed(section) {
  const collapsed = ref(loadRailSectionCollapsed(section));
  watch(collapsed, (value) => saveRailSectionCollapsed(section, value), { flush: 'sync' });
  return collapsed;
}
const historyCollapsed = rememberedCollapsed('workspaces');
const sessionsCollapsed = rememberedCollapsed('sessions');
const resizing = ref(false);
let resizeDrag = null;
let resizeFrame = 0;

function startResize(event) {
  if (event.button !== 0 || !event.isPrimary || resizeDrag) return;
  event.preventDefault();
  event.currentTarget.setPointerCapture(event.pointerId);
  resizeDrag = {
    target: event.currentTarget,
    pointerId: event.pointerId,
    startX: event.clientX,
    startWidth: props.expandedWidth,
    width: props.expandedWidth,
  };
  resizing.value = true;
}

function moveResize(event) {
  if (!resizeDrag || event.pointerId !== resizeDrag.pointerId) return;
  resizeDrag.width = normalizeRailWidth(resizeDrag.startWidth + event.clientX - resizeDrag.startX);
  if (!resizeFrame) {
    resizeFrame = requestAnimationFrame(() => {
      resizeFrame = 0;
      if (resizeDrag) emit('resize', resizeDrag.width);
    });
  }
}

function finishResize(event) {
  if (!resizeDrag || (event && event.pointerId !== resizeDrag.pointerId)) return;
  if (event?.type === 'pointerup') {
    resizeDrag.width = normalizeRailWidth(resizeDrag.startWidth + event.clientX - resizeDrag.startX);
  }
  // Flush the final width even when pointerup precedes the next animation frame.
  const drag = resizeDrag;
  resizeDrag = null;
  cancelAnimationFrame(resizeFrame);
  resizeFrame = 0;
  resizing.value = false;
  if (drag.target.hasPointerCapture(drag.pointerId)) {
    drag.target.releasePointerCapture(drag.pointerId);
  }
  emit('resizeEnd', drag.width);
}

function resizeWithKeyboard(event) {
  let width;
  if (event.key === 'ArrowLeft') width = props.expandedWidth - 8;
  else if (event.key === 'ArrowRight') width = props.expandedWidth + 8;
  else if (event.key === 'Home') width = RAIL_MIN_WIDTH;
  else if (event.key === 'End') width = RAIL_MAX_WIDTH;
  else return;
  event.preventDefault();
  emit('resizeEnd', normalizeRailWidth(width));
}

watch(() => props.expanded, (expanded) => {
  if (!expanded) finishResize();
});
onBeforeUnmount(() => finishResize());

// 底部展开/收起按钮的文案与 aria：两个动作各一个名字，读屏与悬停提示共用。
const toggleLabel = computed(() => t(props.expanded ? 'app.mode.collapseRail' : 'app.mode.expandRail'));

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
const railActions = {
  'action:task-center': { label: 'taskCenter.title', icon: CloudServerOutlined, event: 'openTaskCenter', argument: 'services' },
};

const baseModeOptions = computed(() => [
  { label: t('app.mode.chat'), key: 'chat', icon: renderIcon(SlackOutlined) },
  { label: t('app.mode.kb'), key: 'kb', icon: renderIcon(BookOutlined, props.kbRunning) },
  { label: t('app.mode.skills'), key: 'skills', icon: renderIcon(ThunderboltOutlined) },
  { label: t('app.mode.mcp'), key: 'mcp', icon: renderIcon(ApiOutlined) },
  { label: t('app.mode.models'), key: 'models', icon: renderIcon(RobotOutlined) },
  { label: t('app.mode.sshCluster'), key: 'ssh', icon: renderIcon(ClusterOutlined) },
  ...Object.entries(railActions).map(([key, action]) => ({ label: t(action.label), key, icon: renderIcon(action.icon) })),
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
  const action = Object.hasOwn(railActions, key) ? railActions[key] : null;
  if (action) {
    emit(action.event, action.argument);
    return;
  }
  emit('switch', key);
}

function workspaceName(path) {
  const parts = String(path || '').split(/[\\/]/).filter(Boolean);
  return parts[parts.length - 1] || path;
}
</script>
