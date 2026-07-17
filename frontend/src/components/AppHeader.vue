<template>
  <!-- Unified header: brand + tabs + actions + meta -->
  <n-layout-header bordered class="app-header">
    <div class="brand">
      <AllyWordmark class="brand-wordmark" />
    </div>
    <div class="header-tabs-area">
      <div class="workspace-tabs">
        <div
          v-for="tab in workspaceTabs"
          :key="tab.id"
          :class="['workspace-tab', { active: tab.id === activeWorkspaceId, running: tab.isRunning }]"
          @click="$emit('switchWorkspace', tab.id)"
        >
          <span v-if="tab.isRunning" class="tab-running-dot" :aria-label="$t('header.running')"></span>
          <span class="tab-label">{{ tab.label }}</span>
          <span class="tab-close" @click.stop="$emit('closeWorkspace', tab.id)">&times;</span>
        </div>
      </div>
      <div class="header-tabs-actions">
        <n-button size="tiny" quaternary @click="$emit('addWorkspace')" :title="$t('header.addWorkspace')">+</n-button>
        <n-dropdown
          trigger="click"
          scrollable
          :options="historyOptions"
          :menu-props="historyMenuProps"
          @select="onHistorySelect"
        >
          <n-button size="tiny" quaternary :title="$t('header.workspaceHistory')">
            <svg class="header-dropdown-icon" width="12" height="12" viewBox="0 0 12 12" fill="none" stroke="currentColor" stroke-width="1.25" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
              <polyline points="3 4.5 6 7.5 9 4.5" />
            </svg>
          </n-button>
        </n-dropdown>
      </div>
    </div>
    <n-space align="center" :size="6">
      <n-tag v-if="grillModeActive" size="small" round type="error" bordered>GRILL</n-tag>
      <n-button
        :class="['repository-button', { 'update-available': updateAvailable }]"
        size="small"
        quaternary
        :title="updateAvailable ? $t('header.update', { version: latestVersion }) : $t('header.github')"
        :aria-label="updateAvailable ? $t('header.updateAria') : $t('header.githubAria')"
        @click="$emit('openRepository')"
      >
        <svg v-if="updateAvailable" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
          <circle cx="12" cy="12" r="9" />
          <path d="M12 16V8" />
          <path d="m8.5 11.5 3.5-3.5 3.5 3.5" />
        </svg>
        <svg v-else width="16" height="16" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
          <path d="M12 .7a11.5 11.5 0 0 0-3.64 22.41c.58.1.79-.25.79-.56v-2.23c-3.22.7-3.9-1.37-3.9-1.37-.52-1.34-1.28-1.69-1.28-1.69-1.05-.72.08-.7.08-.7 1.16.08 1.77 1.19 1.77 1.19 1.03 1.77 2.71 1.26 3.37.96.1-.75.4-1.26.73-1.55-2.57-.29-5.27-1.29-5.27-5.68 0-1.26.45-2.28 1.19-3.09-.12-.29-.52-1.47.11-3.05 0 0 .97-.31 3.16 1.18a10.94 10.94 0 0 1 5.76 0c2.2-1.49 3.16-1.18 3.16-1.18.63 1.58.23 2.76.11 3.05.74.81 1.19 1.83 1.19 3.09 0 4.4-2.71 5.38-5.29 5.67.42.36.79 1.07.79 2.16v3.21c0 .31.21.67.8.56A11.5 11.5 0 0 0 12 .7Z" />
        </svg>
      </n-button>
      <n-button size="small" quaternary @click="$emit('openSettings')" :title="$t('header.settings')">
        <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
          <circle cx="12" cy="12" r="3"/>
          <path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 0 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-4 0v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 0 1-2.83-2.83l.06-.06A1.65 1.65 0 0 0 4.68 15a1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1 0-4h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 0 1 2.83-2.83l.06.06A1.65 1.65 0 0 0 9 4.68a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 0 1 2.83 2.83l-.06.06A1.65 1.65 0 0 0 19.4 9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1z"/>
        </svg>
      </n-button>
    </n-space>
    <div class="window-controls">
      <button class="window-control-btn" @click="$emit('minimise')" :title="$t('header.minimize')">─</button>
      <button class="window-control-btn" @click="toggleMaximise" :title="isMaximised ? $t('header.restore') : $t('header.maximize')" :aria-label="isMaximised ? $t('header.restore') : $t('header.maximize')">
        <svg v-if="isMaximised" width="14" height="14" viewBox="0 0 14 14" fill="none" stroke="currentColor" stroke-width="1.4">
          <rect x="4" y="1" width="9" height="9" rx="1"/>
          <rect x="1" y="4" width="9" height="9" rx="1"/>
        </svg>
        <svg v-else width="14" height="14" viewBox="0 0 14 14" fill="none" stroke="currentColor" stroke-width="1.4">
          <rect x="1.5" y="1.5" width="11" height="11" rx="1.5"/>
        </svg>
      </button>
      <button class="window-control-btn close" @click="$emit('closeWindow')" :title="$t('header.close')" :aria-label="$t('header.close')">
        <svg class="window-close-icon" width="16" height="16" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="2.1" stroke-linecap="round">
          <path d="M4.5 4.5l7 7M11.5 4.5l-7 7"/>
        </svg>
      </button>
    </div>
  </n-layout-header>
</template>

<script setup>
import { h } from 'vue';
import { NDropdown } from 'naive-ui';
import AllyWordmark from './AllyWordmark.vue';

defineProps({
  workspaceTabs: { type: Array, required: true },
  activeWorkspaceId: { type: String, required: true },
  grillModeActive: { type: Boolean, default: false },
  updateAvailable: { type: Boolean, default: false },
  latestVersion: { type: String, default: '' },
  isMaximised: { type: Boolean, default: false },
  historyOptions: { type: Array, default: () => [] },
});

const emit = defineEmits([
  'switchWorkspace',
  'closeWorkspace',
  'addWorkspace',
  'historySelect',
  'openRepository',
  'openSettings',
  'minimise',
  'toggleMaximise',
  'closeWindow',
]);

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
  padding: 0 10px;
  display: flex;
  align-items: stretch;
  background: #242424 !important;
  border-bottom: none;
  box-shadow: inset 0 -1px rgba(255, 255, 255, 0.08);
  --wails-draggable: drag;
  gap: 4px;
}

.app-header button,
.app-header input,
.app-header textarea,
.brand,
.n-button,
.n-input,
.n-modal {
  --wails-draggable: no-drag;
}

/* Remove Naive UI button white focus border */
.n-button:focus,
.n-button:focus-visible,
.n-button:hover {
  box-shadow: none !important;
}

.repository-button {
  color: #8a8a8a !important;
  --wails-draggable: no-drag;
}

.repository-button:hover {
  color: #d4d4d4 !important;
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
  padding: 0 10px 0 4px;
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
  align-items: stretch;
  overflow: hidden;
}

.workspace-tabs {
  display: flex;
  align-items: stretch;
  overflow-x: auto;
  gap: 1px;
  height: 100%;
  flex: 1;
  min-width: 0;
}

.workspace-tabs::-webkit-scrollbar {
  height: 0;
}

.header-tabs-actions {
  display: flex;
  align-items: center;
  gap: 2px;
  margin-left: 4px;
  flex-shrink: 0;
}

.header-tabs-actions .n-button {
  display: inline-flex !important;
  align-items: center !important;
  justify-content: center !important;
  width: 30px !important;
  height: 30px !important;
  padding: 0 !important;
  line-height: 1 !important;
  font-size: 16px;
}

.header-tabs-actions .n-button .n-button__content {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 100%;
  height: 100%;
  line-height: 1;
}

.header-tabs-actions .n-button svg {
  display: block;
  width: 14px;
  height: 14px;
}

/* ── Window control buttons (frameless) ── */

.window-controls {
  display: flex;
  align-items: stretch;
  height: 100%;
  margin-left: auto;
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
  color: #8a8a8a;
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
  stroke-width: 2.1;
  flex: none;
}

body.platform-darwin .window-close-icon {
  width: 18px;
  height: 18px;
  stroke-width: 2.3;
}

.window-control-btn:hover {
  background: rgba(255, 255, 255, 0.08);
  color: #f5f5f5;
}

.window-control-btn.close:hover {
  background: #e81123;
  color: #fff;
}

.workspace-tab {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 0 8px 0 12px;
  height: 100%;
  border-radius: 0;
  cursor: pointer;
  color: #8a8a8a;
  font-size: 12px;
  line-height: 1;
  white-space: nowrap;
  user-select: none;
  transition: background 0.12s, color 0.12s;
  flex-shrink: 0;
  min-width: 112px;
  max-width: 180px;
  border: 1px solid transparent;
  border-bottom: none;
  --wails-draggable: no-drag;
}

.workspace-tab:hover {
  background: rgba(255, 255, 255, 0.05);
  color: #d4d4d4;
}

.workspace-tab.active {
  color: #f5f5f5;
  background: #1a1a1a;
  border-color: rgba(255, 255, 255, 0.08);
}

.workspace-tab.running {
  color: #d4d4d4;
}

.workspace-tab.running.active {
  color: #f5f5f5;
}

.workspace-tab.running .tab-label {
  font-weight: 500;
}

.workspace-tab .tab-label {
  display: inline-flex;
  align-items: center;
  flex: 1;
  min-width: 0;
  height: 18px;
  overflow: hidden;
  text-overflow: ellipsis;
}

.tab-running-dot {
  width: 5px;
  height: 5px;
  border-radius: 999px;
  background: #8fd4b4;
  opacity: 0.85;
  flex: none;
}

.workspace-tab .tab-close {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 18px;
  height: 18px;
  border-radius: 4px;
  font-size: 15px;
  line-height: 18px;
  color: #737373;
  transition: background 0.12s, color 0.12s;
  margin-left: auto;
  flex-shrink: 0;
}

.workspace-tab .tab-close:hover {
  background: rgba(255, 255, 255, 0.12);
  color: #f5f5f5;
}

.workspace-tab.running .tab-close {
  color: rgba(216, 248, 231, 0.7);
}

.header-dropdown-icon {
  display: block;
}

</style>
