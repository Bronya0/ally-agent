<!--
SPDX-License-Identifier: GPL-3.0-only

Copyright (C) 2026 tangssst <tangssst@qq.com>
GitHub: https://github.com/Bronya0/ally-agent

This file is part of ally-agent, licensed under the GNU General
Public License v3. See the LICENSE file for details.
-->
<template>
  <!-- Unified header: brand + tabs + actions + meta -->
  <n-layout-header ref="headerRef" bordered class="app-header">
    <div class="brand">
      <AllyWordmark class="brand-wordmark" />
    </div>
    <div class="header-tabs-area">
      <div
        ref="workspaceTabsRef"
        :class="['workspace-tabs-host', { 'drag-preview-active': hasDragShift, dragging: !!draggedWorkspaceId, 'dragging-active': hasDragged }]"
        :style="{ '--workspace-drag-offset': `${draggedTabWidth}px` }"
        @click.capture="onHostCaptureClick"
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
            @pointerdown="onWorkspacePointerDown($event, tab.id)"
          >
            <span v-if="tab.isRunning" class="tab-running-dot" :aria-label="$t('header.running')"></span>
            <span class="tab-label">{{ tab.label }}</span>
            <button type="button" class="tab-close" :title="$t('header.close')" :aria-label="$t('header.close')" @click.stop="$emit('closeWorkspace', tab.id)">
              <CloseOutlined class="tab-close-icon" />
            </button>
          </n-tab>
        </n-tabs>
        <div v-if="dropIndicatorStyle" class="workspace-drop-indicator" :style="dropIndicatorStyle"></div>
      </div>
      <Teleport to="body">
        <div v-if="dragGhostStyle" class="workspace-drag-ghost" :style="dragGhostStyle">{{ draggedTabLabel }}</div>
      </Teleport>
      <div class="header-tabs-actions">
        <n-dropdown
          trigger="click"
          scrollable
          :options="historyOptions"
          :menu-props="historyMenuProps"
          @select="onHistorySelect"
        >
          <n-button class="header-icon-button tabs-action-button history-action-button" size="small" quaternary :title="$t('header.workspaceHistory')" :aria-label="$t('header.workspaceHistory')">
            <HistoryOutlined class="header-icon" />
          </n-button>
        </n-dropdown>
      </div>
    </div>
    <div class="header-actions">
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
import { computed, h, onBeforeUnmount, ref } from 'vue';
import { NDropdown } from 'naive-ui';
import AllyWordmark from './AllyWordmark.vue';
import HistoryOutlined from '@vicons/antd/HistoryOutlined';
import DownloadOutlined from '@vicons/antd/DownloadOutlined';
import GithubOutlined from '@vicons/antd/GithubOutlined';
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
  'minimise',
  'toggleMaximise',
  'closeWindow',
]);

const headerRef = ref(null);
const workspaceTabsRef = ref(null);
const draggedWorkspaceId = ref('');
const dragPreview = ref(null);
const draggedTabWidth = ref(0);
const dropIndicatorLeft = ref(null);
const hasDragged = ref(false);
const dragGhostPos = ref(null);
const dragPointerId = ref(null);
let dragStartX = 0;
let dragStartY = 0;
let suppressClick = false;

const dropIndicatorStyle = computed(() => {
  if (!dragPreview.value || dropIndicatorLeft.value == null) return null;
  return { left: `${dropIndicatorLeft.value}px` };
});

const draggedTabLabel = computed(() => {
  const id = draggedWorkspaceId.value;
  if (!id) return '';
  return props.workspaceTabs.find((t) => t.id === id)?.label || id;
});

const dragGhostStyle = computed(() => {
  if (!dragGhostPos.value || !draggedWorkspaceId.value || !hasDragged.value) return null;
  return {
    left: `${dragGhostPos.value.x + 12}px`,
    top: `${dragGhostPos.value.y + 12}px`,
  };
});

function updateDropIndicatorPosition(targetId, after) {
  const host = workspaceTabsRef.value;
  if (!host || !targetId) {
    dropIndicatorLeft.value = null;
    return;
  }
  const targetEl = host.querySelector(`.workspace-tab[data-tab-id="${targetId}"]`);
  if (!targetEl) {
    dropIndicatorLeft.value = null;
    return;
  }
  const hostRect = host.getBoundingClientRect();
  const rect = targetEl.getBoundingClientRect();
  // 2px 竖线以选中目标边缘为基准居中（-1 偏移 = 半宽，避免再 translate 或被边缘裁剪）
  const edge = after ? rect.right - hostRect.left : rect.left - hostRect.left;
  let left = Math.round(edge - 1);
  const maxLeft = Math.max(0, Math.round(hostRect.width - 2));
  if (left < 0) left = 0;
  if (left > maxLeft) left = maxLeft;
  dropIndicatorLeft.value = left;
}

// 被拖 tab 保持在布局槽内；源与落点之间的 tab 平移出真实缝隙，落点即缝隙。
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
  dropIndicatorLeft.value = null;
}

function applyDragPreview(targetId, after) {
  if (!targetId || targetId === draggedWorkspaceId.value) {
    clearDragPreview();
    return;
  }
  const cur = dragPreview.value;
  if (cur?.targetId === targetId && cur.after === after) return;
  dragPreview.value = { targetId, after };
  updateDropIndicatorPosition(targetId, after);
}

function headerDragEl() {
  const h = headerRef.value;
  if (!h) return null;
  // naive-ui 的 n-layout-header 可能是组件实例，真实 DOM 在 $el 上
  return h.$el || h;
}
function resetDragState() {
  window.removeEventListener('pointermove', onWindowPointerMove);
  window.removeEventListener('pointerup', onWindowPointerUp);
  window.removeEventListener('pointercancel', onWindowPointerCancel);
  // 同步恢复窗口拖动区：WebView2 在 pointerdown 时刻判定 --wails-draggable，
  // 响应式 class 切换有下一帧延迟，必须直接写 style 才能让后续 move 不被当成窗口拖动
  try { workspaceTabsRef.value?.style?.removeProperty('--wails-draggable'); } catch { /* ignore */ }
  try { headerDragEl()?.style?.removeProperty('--wails-draggable'); } catch { /* ignore */ }
  draggedWorkspaceId.value = '';
  dragPointerId.value = null;
  dragPreview.value = null;
  dropIndicatorLeft.value = null;
  dragGhostPos.value = null;
  hasDragged.value = false;
  draggedTabWidth.value = 0;
}

// 拖拽中途失焦（Alt+Tab 切走/弹窗抢占）或组件卸载时兜底清理：
// 否则 window 级监听与 header 上的 no-drag 内联样式会永久残留，窗口将无法拖动。
function onWindowBlur() {
  resetDragState();
}
window.addEventListener('blur', onWindowBlur);
onBeforeUnmount(() => {
  window.removeEventListener('blur', onWindowBlur);
  resetDragState();
});

function onWorkspaceTabUpdate(id) {
  if (id && id !== props.activeWorkspaceId) emit('switchWorkspace', id);
}

// WebView2 对页面内部 HTML5 drag-and-drop（dragover/drop）支持不完整，
// 因此 tab 排序用 Pointer Events 模拟：pointerdown 捕获 + window 级 move/up。
function onWorkspacePointerDown(event, id) {
  if (props.workspaceTabs.length <= 1 || event.button !== 0) return;
  // 重入防护：已在拖拽中（多指/异常输入）忽略后续 pointerdown，避免重复挂 window 监听
  if (draggedWorkspaceId.value) return;
  const target = event.target;
  if (target instanceof Element && target.closest('.tab-close')) return;
  const rect = event.currentTarget?.getBoundingClientRect?.();
  if (rect?.width) draggedTabWidth.value = rect.width;
  else draggedTabWidth.value = 112;
  draggedWorkspaceId.value = id;
  dragPointerId.value = event.pointerId;
  dragStartX = event.clientX;
  dragStartY = event.clientY;
  hasDragged.value = false;
  clearDragPreview();
  // WebView2 的 --wails-draggable 判定在 pointerdown 时机，响应式 class 要下一帧才生效，
  // 必须同步把 host 与 header 切到 no-drag，否则后续 pointermove 会被当成窗口拖动，原 tab 看似“一动不动”
  try { workspaceTabsRef.value?.style?.setProperty('--wails-draggable', 'no-drag'); } catch { /* ignore */ }
  try { headerDragEl()?.style?.setProperty('--wails-draggable', 'no-drag'); } catch { /* ignore */ }
  // 捕获指针：鼠标移出 tab/窗口也持续收到 move，不受 --wails-draggable 窗口拖动区影响
  try { event.currentTarget?.setPointerCapture?.(event.pointerId); } catch { /* ignore */ }
  window.addEventListener('pointermove', onWindowPointerMove);
  window.addEventListener('pointerup', onWindowPointerUp);
  window.addEventListener('pointercancel', onWindowPointerCancel);
  event.preventDefault();
}

function computeDropTarget(clientX) {
  const host = workspaceTabsRef.value;
  if (!host || !draggedWorkspaceId.value) return null;
  const tabs = Array.from(host.querySelectorAll('.workspace-tab') || []).filter(
    (el) => el.dataset.tabId !== draggedWorkspaceId.value,
  );
  if (!tabs.length) return null;
  for (const tab of tabs) {
    const rect = tab.getBoundingClientRect();
    if (clientX < rect.left + rect.width / 2) {
      return { targetId: tab.dataset.tabId || '', after: false };
    }
  }
  const last = tabs[tabs.length - 1];
  return { targetId: last.dataset.tabId || '', after: true };
}

function onWindowPointerMove(event) {
  if (event.pointerId !== undefined && event.pointerId !== dragPointerId.value) return;
  // WebView2 窗口拖动会先于 JS 消费 move，必须 preventDefault 阻止默认拖动/选区
  try { event.preventDefault(); } catch { /* ignore */ }
  const dx = event.clientX - dragStartX;
  const dy = event.clientY - dragStartY;
  if (!hasDragged.value && Math.hypot(dx, dy) < 4) return;
  hasDragged.value = true;
  dragGhostPos.value = { x: event.clientX, y: event.clientY };
  const drop = computeDropTarget(event.clientX);
  if (!drop) {
    clearDragPreview();
    return;
  }
  applyDragPreview(drop.targetId, drop.after);
}

function onWindowPointerUp() {
  const sourceId = draggedWorkspaceId.value;
  const preview = dragPreview.value;
  // 只要发生过实际拖拽位移就抑制随后的 click，避免拖回原位时误触发 tab 切换
  if (hasDragged.value) {
    suppressClick = true;
    if (sourceId && preview && sourceId !== preview.targetId) {
      emit('reorderWorkspace', { sourceId, ...preview });
    }
  }
  resetDragState();
}

function onWindowPointerCancel() {
  // 系统取消拖拽（手势被抢占等）：只清理状态，不触发排序也不抑制 click
  resetDragState();
}

function onHostCaptureClick(event) {
  if (suppressClick) {
    suppressClick = false;
    event.stopPropagation();
    event.preventDefault();
  }
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
  if (key === '__add__') {
    emit('addWorkspace');
    return;
  }
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
  padding: 0 0 0 8px;
  display: flex;
  align-items: stretch;
  background: var(--ally-surface-chrome) !important;
  /* A real border, not an inset shadow. The shadow was painted inside the
     padding box while the mode rail and the explorer use borders on the
     border box, so the three edges could not line up at 1px. */
  border-bottom: 1px solid var(--ally-border);
  --wails-draggable: drag;
  gap: 8px;
  --header-muted: var(--ally-text-muted);
  --header-strong: var(--ally-text-primary);
  /* Hover and selected both lift the surface; selected simply lifts further.
     The old active background was #171717 — darker than the header itself,
     which punched a hole in the surface scale instead of sitting on it. */
  --header-hover-bg: var(--ally-state-hover);
  --header-active-bg: var(--ally-state-selected);
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
  color: var(--ally-success-deep) !important;
  background: rgba(74, 222, 128, 0.1) !important;
}

.repository-button.update-available:hover {
  color: var(--ally-success-pale) !important;
  background: rgba(74, 222, 128, 0.18) !important;
}

/* 历史工作空间按钮：琥珀色图标与其他 header 图标区分（参考底部会话按钮 #e0a070） */
.history-action-button {
  color: var(--ally-accent) !important;
}

.history-action-button:hover,
.history-action-button:focus-visible {
  color: var(--ally-accent-strong) !important;
}

.brand {
  display: flex;
  align-items: center;
  flex-shrink: 0;
  /* The header gap already separates brand from tabs; this adds room so the
     mark does not crowd the first tab. Everything here is on a 4px grid. */
  padding: 0 8px 0 0;
  --wails-draggable: drag;
}

/* Sized against the 16px header icons: at 25px the mark carried more visual
   weight than every control next to it and read as the header's subject. */
.brand-wordmark {
  font-size: 20px;
  line-height: 1;
  white-space: nowrap;
}

body.platform-darwin .brand-wordmark {
  font-size: 24px;
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
  position: relative;
  display: flex;
  align-items: center;
  overflow: visible;
  height: 100%;
  flex: 1;
  min-width: 0;
  --wails-draggable: drag;
}

.workspace-tabs-host.dragging,
.workspace-tabs-host.dragging .workspace-tabs,
.workspace-tabs-host.dragging-active,
.workspace-tabs-host.dragging-active .workspace-tabs {
  --wails-draggable: no-drag;
  touch-action: none;
  user-select: none;
}

.workspace-drop-indicator {
  position: absolute;
  top: 4px;
  bottom: 4px;
  width: 2px;
  background: var(--ally-accent-bright);
  border-radius: 1px;
  pointer-events: none;
  z-index: 10;
  box-shadow: 0 0 8px var(--ally-accent-bright), 0 0 2px rgba(0, 0, 0, 0.6);
}

.workspace-drag-ghost {
  position: fixed;
  max-width: 200px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  padding: 7px 12px;
  border-radius: 8px;
  background: var(--ally-surface-raised);
  color: var(--ally-text-primary);
  font-size: 12px;
  font-weight: 600;
  line-height: 1;
  border: 1px solid var(--ally-border-strong);
  box-shadow: var(--ally-overlay-shadow);
  pointer-events: none;
  z-index: 99999;
  opacity: 1;
  will-change: left, top;
  transform: translateZ(0);
}

.workspace-tabs {
  width: 100%;
  min-width: 0;
  height: 100%;
  --n-tab-gap: 0 !important;
  --n-tab-padding: 0 !important;
  /* 与用户消息左侧竖线同一档色（accent-dim）：同族更协调，且不像全亮
     accent 那样在 header 顶部抢视线。 */
  --n-bar-color: var(--ally-accent-dim) !important;
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
  /* Solid full-bleed rule: at 2px tall a 1px radius just blurs the ends,
     and the old 0.82 opacity let the header surface bleed through. */
  height: 2px;
  background: var(--n-bar-color);
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
  margin-left: 8px;
  flex-shrink: 0;
}

/* ── Window control buttons (frameless) ── */

.window-controls {
  display: flex;
  align-items: stretch;
  height: 100%;
  /* No extra margin: the header gap separates these from the actions, and the
     buttons stay flush to the window edge as the platform expects. */
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
  padding: 0 8px 0 12px !important;
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
  min-width: 112px;
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

.workspace-tabs-host.dragging-active .workspace-tabs :deep(.workspace-tab.dragging) {
  /* 有跟手位移后立即虚线占位：即使尚未产生缝隙（hasDragShift 之前）也必须有视觉反馈，否则原位看似“一动不动” */
  background: var(--ally-state-hover) !important;
  color: var(--ally-text-faint) !important;
  border: 1px dashed var(--ally-border-strong) !important;
  box-shadow: none !important;
  pointer-events: none;
  opacity: 0.62 !important;
}
.workspace-tabs-host.drag-preview-active .workspace-tabs :deep(.workspace-tab.dragging) {
  /* 有缝隙时再压淡文字，清晰内容由跟手卡片展示 */
  visibility: visible !important;
  opacity: 0.62 !important;
  background: var(--ally-state-hover) !important;
  color: var(--ally-text-faint) !important;
  box-shadow: none !important;
  border: 1px dashed var(--ally-border-strong) !important;
  pointer-events: none;
}

/* 拖拽落点指示线：独立悬浮竖线，随鼠标实时定位到目标 tab 边缘（2 tab 相邻时也可见） */
.workspace-tabs :deep(.workspace-tab:active) {
  cursor: grabbing;
}

.workspace-tabs :deep(.workspace-tab.dragging) {
  /* 未产生位移前（2 tab 相邻互换等）保持清晰可读 */
  opacity: 1 !important;
  z-index: 3;
  touch-action: none;
  user-select: none;
}

.workspace-tabs :deep(.workspace-tab:hover) {
  /* Capped at the secondary level so hovering an *inactive* tab can never
     out-shine the active one now that active uses the same token; the
     background lift is the hover signal. */
  background: var(--header-hover-bg);
  color: var(--ally-text-secondary);
}

.workspace-tabs :deep(.workspace-tab.active) {
  /* The active tab used to take --header-strong (var(--ally-text-primary)), which made the tab
     strip the brightest steady text in the chrome. The bottom accent rule is
     already the selected signal, so the label only needs to clear the inactive
     var(--ally-text-muted) — it now sits on the shared secondary level with the rest of the
     persistent chrome. */
  color: var(--ally-text-secondary);
  /* No active fill: the bottom accent rule is the selected signal, and any
     background on top of the chrome read as a bright band. */
  background: transparent;
  border-color: transparent;
  box-shadow: none;
}

.workspace-tabs :deep(.workspace-tab.running) {
  color: var(--ally-text-body);
}

.workspace-tabs :deep(.workspace-tab.running.active) {
  color: var(--ally-text-primary);
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
  color: var(--ally-text-soft);
  cursor: pointer;
  transition: background 0.12s, color 0.12s;
  margin-left: auto;
  flex-shrink: 0;
}

.workspace-tabs :deep(.workspace-tab .tab-close:hover) {
  background: var(--ally-hover-strong);
  color: var(--ally-text-primary);
}

.workspace-tabs :deep(.workspace-tab.running .tab-close) {
  color: var(--ally-success-pale);
}

</style>
