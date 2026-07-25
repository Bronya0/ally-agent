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
          <button type="button" class="tab-close" :title="$t('header.close')" :aria-label="$t('header.close')" @click.stop="$emit('closeWorkspace', tab.id)">
            <svg class="tab-close-icon" width="14" height="14" viewBox="0 0 14 14" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" aria-hidden="true">
              <path d="M4.25 4.25l5.5 5.5M9.75 4.25l-5.5 5.5" />
            </svg>
          </button>
        </div>
      </div>
      <div class="header-tabs-actions">
        <n-button class="header-icon-button tabs-action-button" size="small" quaternary @click="$emit('addWorkspace')" :title="$t('header.addWorkspace')" :aria-label="$t('header.addWorkspace')">
          <svg class="header-icon" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" aria-hidden="true">
            <path d="M12 5v14M5 12h14" />
          </svg>
        </n-button>
        <n-dropdown
          trigger="click"
          scrollable
          :options="historyOptions"
          :menu-props="historyMenuProps"
          @select="onHistorySelect"
        >
          <n-button class="header-icon-button tabs-action-button" size="small" quaternary :title="$t('header.workspaceHistory')" :aria-label="$t('header.workspaceHistory')">
            <svg class="header-icon" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.9" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
              <path d="M3 12a9 9 0 1 0 3-6.7" />
              <path d="M3 4v5h5" />
              <path d="M12 7v5l3 2" />
            </svg>
          </n-button>
        </n-dropdown>
      </div>
    </div>
    <div class="header-actions">
      <n-tag v-if="grillModeActive" class="header-grill-tag" size="small" round type="error" bordered>GRILL</n-tag>
      <n-button
        :class="['header-icon-button', 'repository-button', { 'update-available': updateAvailable }]"
        size="small"
        quaternary
        :title="updateAvailable ? $t('header.update', { version: latestVersion }) : $t('header.github')"
        :aria-label="updateAvailable ? $t('header.updateAria') : $t('header.githubAria')"
        @click="$emit('openRepository')"
      >
        <svg v-if="updateAvailable" class="header-icon" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
          <circle cx="12" cy="12" r="9" />
          <path d="M12 16V8" />
          <path d="m8.5 11.5 3.5-3.5 3.5 3.5" />
        </svg>
        <svg v-else class="header-icon github-icon" width="16" height="16" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
          <path d="M12 .7a11.5 11.5 0 0 0-3.64 22.41c.58.1.79-.25.79-.56v-2.23c-3.22.7-3.9-1.37-3.9-1.37-.52-1.34-1.28-1.69-1.28-1.69-1.05-.72.08-.7.08-.7 1.16.08 1.77 1.19 1.77 1.19 1.03 1.77 2.71 1.26 3.37.96.1-.75.4-1.26.73-1.55-2.57-.29-5.27-1.29-5.27-5.68 0-1.26.45-2.28 1.19-3.09-.12-.29-.52-1.47.11-3.05 0 0 .97-.31 3.16 1.18a10.94 10.94 0 0 1 5.76 0c2.2-1.49 3.16-1.18 3.16-1.18.63 1.58.23 2.76.11 3.05.74.81 1.19 1.83 1.19 3.09 0 4.4-2.71 5.38-5.29 5.67.42.36.79 1.07.79 2.16v3.21c0 .31.21.67.8.56A11.5 11.5 0 0 0 12 .7Z" />
        </svg>
      </n-button>
      <n-button class="header-icon-button" size="small" quaternary @click="$emit('openSettings')" :title="$t('header.settings')" :aria-label="$t('header.settings')">
        <svg class="header-icon" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
          <circle cx="12" cy="12" r="3"/>
          <path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 0 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-4 0v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 0 1-2.83-2.83l.06-.06A1.65 1.65 0 0 0 4.68 15a1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1 0-4h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 0 1 2.83-2.83l.06.06A1.65 1.65 0 0 0 9 4.68a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 0 1 2.83 2.83l-.06.06A1.65 1.65 0 0 0 19.4 9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1z"/>
        </svg>
      </n-button>
    </div>
    <div class="window-controls">
      <button class="window-control-btn" @click="$emit('minimise')" :title="$t('header.minimize')" :aria-label="$t('header.minimize')">
        <svg class="window-icon" width="16" height="16" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" aria-hidden="true">
          <path d="M4 8h8" />
        </svg>
      </button>
      <button class="window-control-btn" @click="toggleMaximise" :title="isMaximised ? $t('header.restore') : $t('header.maximize')" :aria-label="isMaximised ? $t('header.restore') : $t('header.maximize')">
        <svg v-if="isMaximised" class="window-icon" width="16" height="16" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.35" aria-hidden="true">
          <rect x="5" y="2" width="9" height="9" rx="1"/>
          <rect x="2" y="5" width="9" height="9" rx="1"/>
        </svg>
        <svg v-else class="window-icon" width="16" height="16" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.35" aria-hidden="true">
          <rect x="3" y="3" width="10" height="10" rx="1.5"/>
        </svg>
      </button>
      <button class="window-control-btn close" @click="$emit('closeWindow')" :title="$t('header.close')" :aria-label="$t('header.close')">
        <svg class="window-icon window-close-icon" width="16" height="16" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" aria-hidden="true">
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
.brand,
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

.workspace-tabs {
  display: flex;
  align-items: center;
  overflow-x: auto;
  gap: 4px;
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
  stroke-width: 1.8;
  flex: none;
}

body.platform-darwin .window-close-icon {
  width: 18px;
  height: 18px;
  stroke-width: 2.3;
}

.window-control-btn:hover {
  background: var(--header-hover-bg);
  color: var(--header-strong);
}

.window-control-btn.close:hover {
  background: #e81123;
  color: #fff;
}

.workspace-tab {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 0 7px 0 11px;
  height: 34px;
  border-radius: 8px;
  cursor: pointer;
  color: var(--header-muted);
  font-size: 12px;
  line-height: 1;
  white-space: nowrap;
  user-select: none;
  transition: background 0.12s, color 0.12s;
  flex-shrink: 0;
  min-width: 108px;
  max-width: 180px;
  border: 1px solid transparent;
  --wails-draggable: no-drag;
}

.workspace-tab:hover {
  background: var(--header-hover-bg);
  color: var(--header-text);
}

.workspace-tab.active {
  color: var(--header-strong);
  background: var(--header-active-bg);
  border-color: transparent;
  box-shadow: inset 0 -2px 0 color-mix(in srgb, var(--ally-accent) 38%, transparent);
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
  width: 6px;
  height: 6px;
  border-radius: 999px;
  background: var(--ally-accent);
  opacity: 0.9;
  flex: none;
}

.workspace-tab .tab-close {
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

.workspace-tab .tab-close:hover {
  background: rgba(255, 255, 255, 0.12);
  color: #f5f5f5;
}

.workspace-tab.running .tab-close {
  color: rgba(216, 248, 231, 0.7);
}

.header-grill-tag {
  height: 22px;
}

</style>
