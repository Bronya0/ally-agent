<!--
SPDX-License-Identifier: GPL-3.0-only

Copyright (C) 2026 tangssst <tangssst@qq.com>
GitHub: https://github.com/Bronya0/ally-agent

This file is part of ally-agent, licensed under the GNU General
Public License v3. See the LICENSE file for details.
-->
<template>
  <!-- Unified header: brand + tabs + actions + meta -->
  <n-layout-header bordered class="app-header">
    <div class="brand">
      <AllyWordmark class="brand-wordmark" />
    </div>
    <div class="header-tabs-area">
      <div
        ref="workspaceTabsRef"
        :class="['workspace-tabs-host', { 'drag-preview-active': hasDragShift }]"
        :style="{ '--workspace-drag-offset': `${draggedTabWidth}px` }"
        @dragover.prevent="onWorkspaceTabsDragOver"
        @drop.prevent="onWorkspaceTabsDrop"
      >
        <n-tabs
          class="workspace-tabs"
          type="bar"
          size="small"
          :value="activeWorkspaceId"
          @update:value="onWorkspaceTabUpdate"
        >
          <n-tab
            v-for="tab in workspaceTabs"
            :key="tab.id"
            :name="tab.id"
            :class="['workspace-tab', { active: tab.id === activeWorkspaceId, running: tab.isRunning, dragging: tab.id === draggedWorkspaceId }, dragShiftClass(tab.id)]"
            :data-tab-id="tab.id"
            :draggable="workspaceTabs.length > 1"
            @dragstart="onWorkspaceDragStart($event, tab.id)"
            @dragover.prevent.stop="onWorkspaceDragOver($event, tab.id)"
            @drop.prevent.stop="onWorkspaceDrop($event, tab.id)"
            @dragend="onWorkspaceDragEnd"
          >
            <span v-if="tab.isRunning" class="tab-running-dot" :aria-label="$t('header.running')"></span>
            <span class="tab-label">{{ tab.label }}</span>
            <button type="button" class="tab-close" :title="$t('header.close')" :aria-label="$t('header.close')" @click.stop="$emit('closeWorkspace', tab.id)">
              <CloseOutlined class="tab-close-icon" />
            </button>
          </n-tab>
        </n-tabs>
      </div>
      <div class="header-tabs-actions">
        <n-button class="header-icon-button tabs-action-button" size="small" quaternary @click="$emit('addWorkspace')" :title="$t('header.addWorkspace')" :aria-label="$t('header.addWorkspace')">
          <PlusOutlined class="header-icon" />
        </n-button>
        <n-dropdown
          trigger="click"
          scrollable
          :options="historyOptions"
          :menu-props="historyMenuProps"
          @select="onHistorySelect"
        >
          <n-button class="header-icon-button tabs-action-button" size="small" quaternary :title="$t('header.workspaceHistory')" :aria-label="$t('header.workspaceHistory')">
            <HistoryOutlined class="header-icon" />
          </n-button>
        </n-dropdown>
      </div>
    </div>
    <div class="header-actions">
      <n-button class="header-icon-button" size="small" quaternary @click="onOpenTokenStats" :title="$t('header.tokenStats')" :aria-label="$t('header.tokenStats')">
        <BarChartOutlined class="header-icon" />
      </n-button>
      <n-button
        :class="['header-icon-button', 'repository-button', { 'update-available': updateAvailable }]"
        size="small"
        quaternary
        :title="updateAvailable ? (updateAutoSupported ? $t('header.updateAuto', { version: latestVersion }) : $t('header.update', { version: latestVersion })) : $t('header.github')"
        :aria-label="updateAvailable ? $t('header.updateAria') : $t('header.githubAria')"
        @click="onRepositoryClick"
      >
        <DownloadOutlined v-if="updateAvailable" class="header-icon" />
        <GithubOutlined v-else class="header-icon github-icon" />
      </n-button>
      <n-button class="header-icon-button" size="small" quaternary @click="onOpenSettings" :title="$t('header.settings')" :aria-label="$t('header.settings')">
        <SettingOutlined class="header-icon" />
      </n-button>
    </div>
    <div class="window-controls">
      <button class="window-control-btn" @click="$emit('minimise')" :title="$t('header.minimize')" :aria-label="$t('header.minimize')">
        <MinusOutlined class="window-icon" />
      </button>
      <button class="window-control-btn" @click="toggleMaximise" :title="isMaximised ? $t('header.restore') : $t('header.maximize')" :aria-label="isMaximised ? $t('header.restore') : $t('header.maximize')">
        <SwitcherOutlined v-if="isMaximised" class="window-icon" />
        <BorderOutlined v-else class="window-icon" />
      </button>
      <button class="window-control-btn close" @click="$emit('closeWindow')" :title="$t('header.close')" :aria-label="$t('header.close')">
        <CloseOutlined class="window-icon window-close-icon" />
      </button>
    </div>
  </n-layout-header>
</template>

<script setup>
import { computed, h, ref } from 'vue';
import { NDropdown } from 'naive-ui';
import AllyWordmark from './AllyWordmark.vue';
import PlusOutlined from '@vicons/antd/PlusOutlined';
import HistoryOutlined from '@vicons/antd/HistoryOutlined';
import BarChartOutlined from '@vicons/antd/BarChartOutlined';
import DownloadOutlined from '@vicons/antd/DownloadOutlined';
import GithubOutlined from '@vicons/antd/GithubOutlined';
import SettingOutlined from '@vicons/antd/SettingOutlined';
import CloseOutlined from '@vicons/antd/CloseOutlined';
import MinusOutlined from '@vicons/antd/MinusOutlined';
import BorderOutlined from '@vicons/antd/BorderOutlined';
import SwitcherOutlined from '@vicons/antd/SwitcherOutlined';

const props = defineProps({
  workspaceTabs: { type: Array, required: true },
  activeWorkspaceId: { type: String, required: true },
  updateAvailable: { type: Boolean, default: false },
  updateAutoSupported: { type: Boolean, default: false },
  latestVersion: { type: String, default: '' },
  isMaximised: { type: Boolean, default: false },
  historyOptions: { type: Array, default: () => [] },
});

const emit = defineEmits([
  'switchWorkspace',
  'closeWorkspace',
  'reorderWorkspace',
  'addWorkspace',
  'historySelect',
  'openRepository',
  'startUpdate',
  'openSettings',
  'openTokenStats',
  'minimise',
  'toggleMaximise',
  'closeWindow',
]);

const workspaceTabsRef = ref(null);
const draggedWorkspaceId = ref('');
const dragPreview = ref(null);
const draggedTabWidth = ref(0);

// The dragged tab keeps its layout slot. The tabs between the source and the
// drop position move into/out of that slot so the drop position becomes a
// real, visible gap instead of a separate guide line.
const dragShiftClassById = computed(() => {
  const preview = dragPreview.value;
  const tabs = props.workspaceTabs;
  const sourceIndex = tabs.findIndex((tab) => tab.id === draggedWorkspaceId.value);
  const targetIndex = tabs.findIndex((tab) => tab.id === preview?.targetId);
  if (sourceIndex < 0 || targetIndex < 0 || sourceIndex === targetIndex) return new Map();

  const movingRight = sourceIndex < targetIndex;
  const first = movingRight
    ? sourceIndex + 1
    : targetIndex + (preview.after ? 1 : 0);
  const last = movingRight
    ? targetIndex - (preview.after ? 0 : 1)
    : sourceIndex - 1;
  const result = new Map();
  for (let index = first; index <= last; index += 1) {
    const id = tabs[index]?.id;
    if (id) result.set(id, movingRight ? 'drag-shift-left' : 'drag-shift-right');
  }
  return result;
});
const hasDragShift = computed(() => dragShiftClassById.value.size > 0);

function dragShiftClass(id) {
  return dragShiftClassById.value.get(id) || '';
}

function clearDragPreview() {
  dragPreview.value = null;
}

function resetDragState() {
  draggedWorkspaceId.value = '';
  clearDragPreview();
  draggedTabWidth.value = 0;
}

function setDragPreview(targetId, after) {
  if (!targetId || targetId === draggedWorkspaceId.value) {
    clearDragPreview();
    return;
  }
  const current = dragPreview.value;
  if (current?.targetId === targetId && current.after === after) return;
  dragPreview.value = { targetId, after };
}

function onWorkspaceTabUpdate(id) {
  if (id && id !== props.activeWorkspaceId) emit('switchWorkspace', id);
}

function onWorkspaceDragStart(event, id) {
  draggedWorkspaceId.value = id;
  clearDragPreview();
  const rect = event.currentTarget?.getBoundingClientRect?.();
  if (rect?.width) draggedTabWidth.value = rect.width;
  if (event.dataTransfer) {
    event.dataTransfer.effectAllowed = 'move';
    event.dataTransfer.setData('text/plain', id);
  }
}

function onWorkspaceDragOver(event, targetId) {
  if (event.dataTransfer) event.dataTransfer.dropEffect = 'move';
  if (!draggedWorkspaceId.value || draggedWorkspaceId.value === targetId) {
    clearDragPreview();
    return;
  }
  const rect = event.currentTarget.getBoundingClientRect();
  setDragPreview(targetId, event.clientX > rect.left + rect.width / 2);
}

function onWorkspaceTabsDragOver(event) {
  if (event.dataTransfer) event.dataTransfer.dropEffect = 'move';
  const tabs = Array.from(workspaceTabsRef.value?.querySelectorAll('.workspace-tab') || []);
  let target = null;
  let after = false;
  for (const tab of tabs) {
    const rect = tab.getBoundingClientRect();
    if (event.clientX < rect.left + rect.width / 2) {
      target = tab;
      break;
    }
  }
  if (!target && tabs.length) {
    target = tabs[tabs.length - 1];
    after = true;
  }
  setDragPreview(target?.dataset.tabId || '', after);
}

function onWorkspaceDrop(event, targetId) {
  const sourceId = draggedWorkspaceId.value || event.dataTransfer?.getData('text/plain');
  if (sourceId && sourceId !== targetId) {
    const rect = event.currentTarget.getBoundingClientRect();
    emit('reorderWorkspace', {
      sourceId,
      targetId,
      after: event.clientX > rect.left + rect.width / 2,
    });
  }
  resetDragState();
}

function onWorkspaceTabsDrop(event) {
  onWorkspaceTabsDragOver(event);
  const sourceId = draggedWorkspaceId.value || event.dataTransfer?.getData('text/plain');
  const preview = dragPreview.value;
  if (sourceId && preview && sourceId !== preview.targetId) {
    emit('reorderWorkspace', { sourceId, ...preview });
  }
  resetDragState();
}

function onWorkspaceDragEnd() {
  resetDragState();
}

function onOpenTokenStats(event) {
  // Blur immediately so the button doesn't retain :focus after the modal opens
  // or after the modal is dismissed via mask click.
  const el = event?.currentTarget;
  if (el && typeof el.blur === 'function') el.blur();
  emit('openTokenStats');
}

function onOpenSettings(event) {
  // Blur immediately so the button doesn't retain :focus after the modal opens
  // or after the modal is dismissed via mask click.
  const el = event?.currentTarget;
  if (el && typeof el.blur === 'function') el.blur();
  emit('openSettings');
}

function onRepositoryClick() {
  if (props.updateAvailable && props.updateAutoSupported) {
    emit('startUpdate');
  } else {
    emit('openRepository');
  }
}

function toggleMaximise() {
  emit('toggleMaximise');
}

function onHistorySelect(key) {
  if (key && key !== '__empty__') {
    emit('historySelect', key);
  }
}

function historyMenuProps() {
  return {
    class: 'workspace-history-menu',
    style: { maxHeight: '360px' },
  };
}
</script>

<style scoped>
/* ── Header (single row with tabs) ── */

.app-header {
  height: 44px;
  flex-shrink: 0;
  padding: 0 0 0 10px;
  display: flex;
  align-items: stretch;
  background: #202020 !important;
  border-bottom: none;
  box-shadow: inset 0 -1px rgba(255, 255, 255, 0.08);
  --wails-draggable: drag;
  gap: 6px;
  --header-muted: #8d8d8d;
  --header-text: #cfcfcf;
  --header-strong: #f2f2f2;
  --header-hover-bg: rgba(255, 255, 255, 0.07);
  --header-active-bg: #171717;
}

.app-header button,
.app-header input,
.app-header textarea,
.n-button,
.n-input,
.n-modal {
  --wails-draggable: no-drag;
}

/* Remove Naive UI button white focus border */
.header-icon-button:focus,
.header-icon-button:focus-visible,
.header-icon-button:hover {
  box-shadow: none !important;
}

.header-actions {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  flex-shrink: 0;
  height: 100%;
  --wails-draggable: no-drag;
}

.header-icon-button {
  width: 30px !important;
  height: 30px !important;
  min-width: 30px !important;
  padding: 0 !important;
  color: var(--header-muted) !important;
  border-radius: 6px !important;
  --wails-draggable: no-drag;
}

.header-icon-button:hover,
.header-icon-button:focus-visible {
  color: var(--header-strong) !important;
  background: var(--header-hover-bg) !important;
}

.header-icon-button :deep(.n-button__content) {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 100%;
  height: 100%;
  line-height: 1;
}

.header-icon,
.window-icon,
.tab-close-icon {
  display: block;
  flex: none;
  width: 16px;
  height: 16px;
}

.tab-close-icon {
  width: 14px;
  height: 14px;
}

.github-icon {
  width: 16px;
  height: 16px;
}

.repository-button.update-available {
  color: #67d99b !important;
  background: rgba(74, 222, 128, 0.1) !important;
}

.repository-button.update-available:hover {
  color: #9af0bd !important;
  background: rgba(74, 222, 128, 0.18) !important;
}

.brand {
  display: flex;
  align-items: center;
  flex-shrink: 0;
  padding: 0 10px 0 2px;
  --wails-draggable: drag;
}

.brand-wordmark {
  font-size: 25px;
  line-height: 1;
  white-space: nowrap;
}

body.platform-darwin .brand-wordmark {
  font-size: 29px;
}

/* ── Tabs area (flex-fill in header) ── */

.header-tabs-area {
  flex: 1;
  min-width: 0;
  display: flex;
  align-items: center;
  overflow: hidden;
}

.workspace-tabs-host {
  display: flex;
  align-items: center;
  overflow: hidden;
  height: 100%;
  flex: 1;
  min-width: 0;
  --wails-draggable: drag;
}

.workspace-tabs {
  width: 100%;
  min-width: 0;
  height: 100%;
  --n-tab-gap: 0 !important;
  --n-tab-padding: 0 !important;
  --n-bar-color: var(--ally-accent-bright) !important;
  --wails-draggable: drag;
}

.workspace-tabs :deep(.n-tabs-nav),
.workspace-tabs :deep(.n-tabs-nav-scroll-wrapper),
.workspace-tabs :deep(.v-x-scroll),
.workspace-tabs :deep(.n-tabs-nav-scroll-content),
.workspace-tabs :deep(.n-tabs-wrapper),
.workspace-tabs :deep(.n-tabs-tab-wrapper) {
  height: 100%;
}

.workspace-tabs :deep(.n-tabs-nav-scroll-content),
.workspace-tabs :deep(.n-tabs-wrapper) {
  align-items: stretch;
}

.workspace-tabs :deep(.n-tabs-tab-pad),
.workspace-tabs :deep(.n-tabs-scroll-padding) {
  width: 0 !important;
}

/* 活跃 tab 指示：隐藏 naive-ui 的 bar（其 1px 半透明圆角在缩放屏上呈点状，
   且定位时机不稳会偶发消失），改用 tab 自身底部 2px 实线，纯 CSS 实现。 */
.workspace-tabs :deep(.n-tabs-bar) {
  display: none;
}

.workspace-tabs :deep(.workspace-tab.active)::after {
  content: '';
  position: absolute;
  left: 0;
  right: 0;
  bottom: 0;
  height: 2px;
  border-radius: 1px;
  background: var(--n-bar-color);
  opacity: 0.82;
  pointer-events: none;
}

.workspace-tabs :deep(.workspace-tab .n-tabs-tab__label) {
  display: flex;
  align-items: center;
  gap: 6px;
  width: 100%;
  min-width: 0;
}

.workspace-tabs :deep(.n-tabs-nav-scroll-wrapper)::-webkit-scrollbar {
  height: 0;
}

.header-tabs-actions {
  display: flex;
  align-items: center;
  gap: 4px;
  margin-left: 4px;
  flex-shrink: 0;
}

/* ── Window control buttons (frameless) ── */

.window-controls {
  display: flex;
  align-items: stretch;
  height: 100%;
  margin-left: 2px;
  --wails-draggable: no-drag;
  flex-shrink: 0;
}

.window-control-btn {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 46px;
  height: 100%;
  border: none;
  background: transparent;
  color: var(--header-muted);
  cursor: pointer;
  font-size: 14px;
  transition: background 0.12s, color 0.12s;
  padding: 0;
  line-height: 1;
  --wails-draggable: no-drag;
}

.window-close-icon {
  width: 16px;
  height: 16px;
  flex: none;
}

body.platform-darwin .window-close-icon {
  width: 18px;
  height: 18px;
}

.window-control-btn:hover {
  background: var(--header-hover-bg);
  color: var(--header-strong);
}

.window-control-btn.close:hover {
  background: #e81123;
  color: #fff;
}

.workspace-tabs :deep(.workspace-tab) {
  position: relative;
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 0 7px 0 11px !important;
  height: 100%;
  border-radius: 0;
  cursor: grab;
  color: var(--header-muted);
  font-size: 12px;
  line-height: 1;
  white-space: nowrap;
  user-select: none;
  transition: transform 0.14s ease, background 0.12s, color 0.12s;
  flex-shrink: 0;
  min-width: 108px;
  max-width: 180px;
  border: 0;
  --wails-draggable: no-drag;
}

.workspace-tabs :deep(.workspace-tab.drag-shift-left) {
  transform: translateX(calc(0px - var(--workspace-drag-offset, 0px)));
}

.workspace-tabs :deep(.workspace-tab.drag-shift-right) {
  transform: translateX(var(--workspace-drag-offset, 0px));
}

.workspace-tabs-host.drag-preview-active .workspace-tabs :deep(.workspace-tab.dragging) {
  visibility: hidden;
  opacity: 0;
}

.workspace-tabs :deep(.workspace-tab:active) {
  cursor: grabbing;
}

.workspace-tabs :deep(.workspace-tab.dragging) {
  opacity: 0.45;
}

.workspace-tabs :deep(.workspace-tab:hover) {
  background: var(--header-hover-bg);
  color: var(--header-text);
}

.workspace-tabs :deep(.workspace-tab.active) {
  color: var(--header-strong);
  background: var(--header-active-bg);
  border-color: transparent;
  box-shadow: none;
}

.workspace-tabs :deep(.workspace-tab.running) {
  color: #d4d4d4;
}

.workspace-tabs :deep(.workspace-tab.running.active) {
  color: #f5f5f5;
}

.workspace-tabs :deep(.workspace-tab.running .tab-label) {
  font-weight: 500;
}

.workspace-tabs :deep(.workspace-tab .tab-label) {
  display: inline-flex;
  align-items: center;
  flex: 1;
  min-width: 0;
  height: 18px;
  overflow: hidden;
  text-overflow: ellipsis;
}

.tab-running-dot {
  width: 6px;
  height: 6px;
  border-radius: 999px;
  background: var(--ally-accent);
  opacity: 0.9;
  flex: none;
}

.workspace-tabs :deep(.workspace-tab .tab-close) {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 18px;
  height: 18px;
  padding: 0;
  border: 0;
  border-radius: 4px;
  background: transparent;
  color: #737373;
  cursor: pointer;
  transition: background 0.12s, color 0.12s;
  margin-left: auto;
  flex-shrink: 0;
}

.workspace-tabs :deep(.workspace-tab .tab-close:hover) {
  background: rgba(255, 255, 255, 0.12);
  color: #f5f5f5;
}

.workspace-tabs :deep(.workspace-tab.running .tab-close) {
  color: rgba(216, 248, 231, 0.7);
}

</style>
