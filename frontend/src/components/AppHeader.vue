<!--
SPDX-License-Identifier: GPL-3.0-only

Copyright (C) 2026 tangssst <tangssst@qq.com>
GitHub: https://github.com/Bronya0/ally-agent

This file is part of ally-agent, licensed under the GNU General
Public License v3. See the LICENSE file for details.
-->
<template>
  <!-- macOS uses AppKit traffic lights over a reserved area of this row. -->
  <n-layout-header bordered class="app-header" :class="{ 'app-header--mac': isMacOS }">
    <div v-if="isMacOS" class="mac-window-controls-space" aria-hidden="true"></div>
    <div class="brand">
      <!-- 起波瞬间垫在字标底下的光晕（只动 opacity，纯合成） -->
      <span class="brand-glow" :class="{ 'brand-glow--struck': brandStruck }" aria-hidden="true"></span>
      <AllyWordmark
        class="brand-wordmark"
        :title="$t('header.brandTip')"
        @click="onBrandClick"
      />
    </div>
    <div class="header-tabs-area">
      <div class="workspace-tabs-host">
        <n-tabs
          class="workspace-tabs"
          type="bar"
          size="small"
          :value="activeWorkspaceId"
          :theme-overrides="tabsThemeOverrides"
          @update:value="onWorkspaceTabUpdate"
        >
          <n-tab
            v-for="tab in workspaceTabs"
            :key="tab.id"
            :name="tab.id"
            :class="['workspace-tab', { active: tab.id === activeWorkspaceId, running: tab.isRunning }]"
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
        <!-- 工作区菜单：加号图标对应菜单首项「添加工作区」，同一菜单下面还跟
             临时工作区与最近工作区列表，所以提示文案跟图标对齐到添加动作，
             不再叫「历史工作区」（加号配历史会自相矛盾）。 -->
        <n-dropdown
          trigger="click"
          scrollable
          :options="historyOptions"
          :menu-props="historyMenuProps"
          @select="onHistorySelect"
        >
          <n-tooltip placement="bottom">
            <template #trigger>
              <n-button class="header-icon-button tabs-action-button history-action-button" size="small" quaternary :aria-label="$t('header.addWorkspace')">
                <!-- Fluent FolderAdd48Regular：文件夹轮廓 + 右下角加号徽标，比裸加号更
                     贴合“添加工作区”，也和旁边 twotone 文件夹图标同族。 -->
                <svg class="header-icon workspace-add-icon" viewBox="0 0 48 48" aria-hidden="true">
                  <path fill="currentColor" d="M17.06 9c.833 0 1.64.277 2.295.784l.175.144l2.586 2.263c.19.166.425.27.673.3l.15.009H40.25a3.75 3.75 0 0 1 3.745 3.55l.005.2v7.806a12.024 12.024 0 0 0-2.5-1.724V16.25a1.25 1.25 0 0 0-1.122-1.243L40.25 15l-17.403-.001l-2.127 2.616a3.75 3.75 0 0 1-2.685 1.378L17.81 19L6.5 18.999V35.25c0 .647.492 1.18 1.122 1.243l.128.007h16.769c.268.881.635 1.719 1.087 2.501L7.75 39a3.75 3.75 0 0 1-3.745-3.55L4 35.25v-22.5a3.75 3.75 0 0 1 3.55-3.745L7.75 9h9.31zm0 2.5H7.75a1.25 1.25 0 0 0-1.244 1.122l-.006.128v3.749l11.31.001c.33 0 .643-.13.876-.358l.094-.104l1.635-2.013l-2.531-2.216a1.25 1.25 0 0 0-.673-.3l-.15-.009zM36 23c5.523 0 10 4.477 10 10s-4.477 10-10 10s-10-4.477-10-10s4.477-10 10-10zm0 4a1 1 0 0 0-.993.883L35 28v4h-4a1 1 0 0 0-.993.883L30 33a1 1 0 0 0 .883.993L31 34h4v4a1 1 0 0 0 .883.993L36 39a1 1 0 0 0 .993-.883L37 38v-4h4a1 1 0 0 0 .993-.883L42 33a1 1 0 0 0-.883-.993L41 32h-4v-4a1 1 0 0 0-.883-.993L36 27z"/>
                </svg>
              </n-button>
            </template>
            {{ $t('header.addWorkspace') }}
          </n-tooltip>
        </n-dropdown>
        <!-- 工作区文件浏览器开关：原先停在输入栏最右（界面右下角），按用户要求挪进
             顶栏、紧邻历史工作区。只有「工作区页」（Agent / 有 Tab 的知识库）才有树
             可开，其余整页页面按禁用处理而不是一起隐藏：位置稳定，也说明这里有开关。 -->
        <n-tooltip placement="bottom">
          <template #trigger>
            <n-button
              :class="['header-icon-button', 'tabs-action-button', 'explorer-action-button', { active: explorerVisible }]"
              :theme-overrides="{ opacityDisabled: '1' }"
              size="small"
              quaternary
              :disabled="!explorerAvailable"
              :aria-label="$t('app.workspaceExplorer.open')"
              @click="$emit('toggleExplorer')"
            >
              <FolderOpenTwotone class="header-icon" />
            </n-button>
          </template>
          {{ $t('app.workspaceExplorer.open') }}
        </n-tooltip>
      </div>
    </div>
    <div class="header-actions">
      <n-tooltip placement="bottom">
        <template #trigger>
          <n-button
            :class="['header-icon-button', 'repository-button', { 'update-available': updateAvailable }]"
            size="small"
            quaternary
            :aria-label="updateAvailable ? $t('header.updateAria') : $t('header.githubAria')"
            @click="onRepositoryClick"
          >
            <DownloadOutlined v-if="updateAvailable" class="header-icon" />
            <GithubOutlined v-else class="header-icon github-icon" />
          </n-button>
        </template>
        {{ updateAvailable ? (updateAutoSupported ? $t('header.updateAuto', { version: latestVersion }) : $t('header.update', { version: latestVersion })) : $t('header.github') }}
      </n-tooltip>
    </div>
    <div v-if="!isMacOS" class="window-controls">
      <button class="window-control-btn" @click="$emit('minimise')" :title="$t('header.minimize')" :aria-label="$t('header.minimize')">
        <MinusOutlined class="window-icon" />
      </button>
      <button class="window-control-btn" @click="toggleMaximise" :title="isMaximised ? $t('header.restore') : $t('header.maximize')" :aria-label="isMaximised ? $t('header.restore') : $t('header.maximize')">
        <!-- 手画的窗口图标：@vicons/antd 里没有系统那套字形——SwitcherOutlined 带一条
             莫名其妙的横条，BorderOutlined 的线宽又比它细。两个状态共用同一线宽，
             切换时字形不会突然变粗变细。 -->
        <svg v-if="isMaximised" class="window-icon" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1">
          <path d="M6.6 1.9H14v7.4" />
          <rect x="2" y="4.9" width="9.2" height="9.2" />
        </svg>
        <svg v-else class="window-icon" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1">
          <rect x="2.2" y="2.2" width="11.6" height="11.6" />
        </svg>
      </button>
      <button class="window-control-btn close" @click="$emit('closeWindow')" :title="$t('header.close')" :aria-label="$t('header.close')">
        <CloseOutlined class="window-icon window-close-icon" />
      </button>
    </div>
  </n-layout-header>
</template>

<script setup>
import { onBeforeUnmount, ref } from 'vue';
import { NDropdown } from 'naive-ui';
import AllyWordmark from './AllyWordmark.vue';
import { burstDigitalWave } from '../composables/digitalWave.mjs';
import { detectPlatform } from '../utils/platform.mjs';
import FolderOpenTwotone from '@vicons/antd/FolderOpenTwotone';
import DownloadOutlined from '@vicons/antd/DownloadOutlined';
import GithubOutlined from '@vicons/antd/GithubOutlined';
import CloseOutlined from '@vicons/antd/CloseOutlined';
import MinusOutlined from '@vicons/antd/MinusOutlined';

const isMacOS = detectPlatform() === 'darwin';

const props = defineProps({
  workspaceTabs: { type: Array, required: true },
  tabsThemeOverrides: { type: Object, default: () => ({}) },
  activeWorkspaceId: { type: String, required: true },
  updateAvailable: { type: Boolean, default: false },
  updateAutoSupported: { type: Boolean, default: false },
  latestVersion: { type: String, default: '' },
  isMaximised: { type: Boolean, default: false },
  historyOptions: { type: Array, default: () => [] },
  // 当前 Tab 的资源树是否展开（按钮选中态）。
  explorerVisible: { type: Boolean, default: false },
  // 当前页面有没有资源树可开（仅工作区页有）；不适用时按钮禁用。
  explorerAvailable: { type: Boolean, default: false },
});

const emit = defineEmits([
  'switchWorkspace',
  'closeWorkspace',
  'addWorkspace',
  'addTempWorkspace',
  'toggleExplorer',
  'historySelect',
  'openRepository',
  'startUpdate',
  'minimise',
  'toggleMaximise',
  'closeWindow',
]);

const brandStruck = ref(false);
let brandStrikeTimer = null;
onBeforeUnmount(() => {
  clearTimeout(brandStrikeTimer);
});

function onWorkspaceTabUpdate(id) {
  if (id && id !== props.activeWorkspaceId) emit('switchWorkspace', id);
}

function onRepositoryClick() {
  if (props.updateAvailable && props.updateAutoSupported) {
    emit('startUpdate');
  } else {
    emit('openRepository');
  }
}

/* 点击左上角 logo：从落点放一道全屏数码波浪，同时字标自己闪一下
   （有因果交代，而不是“背景莫名动了一帧”）。纯装饰彩蛋，失败也不影响任何功能。 */
function onBrandClick(event) {
  burstDigitalWave(event.clientX, event.clientY);
  brandStruck.value = true;
  clearTimeout(brandStrikeTimer);
  brandStrikeTimer = setTimeout(() => { brandStruck.value = false; }, 420);
}

function toggleMaximise() {
  emit('toggleMaximise');
}

function onHistorySelect(key) {
  if (key === '__add__') {
    emit('addWorkspace');
    return;
  }
  if (key === '__temp__') {
    emit('addTempWorkspace');
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

/* Naive 的 quaternary 按钮在 :focus 上铺一层底色（--n-color-focus），而鼠标点击
   不匹配 :focus-visible，所以上面的 :hover/:focus-visible 规则盖不住它：点完把
   鼠标移开，底还一直留到失焦。Tabs 区这两个图标按钮按下/悬停的反馈由 hover
   与 :active 承担，不需要驻留底色；键盘聚焦（:focus-visible）仍保留可见反馈。 */
.tabs-action-button:focus:not(:focus-visible):not(:hover) {
  background: transparent !important;
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

/* 顶栏添加工作区：Fluent FolderAdd48 的图形只占 48 网格的 42x34，且右下角让给
   加号徽标，16px 下比填满整格的 twotone 文件夹小一号；单独抬到 18px 补平视觉
   重量（文件夹本体约 15px 宽，与旁边 16px 的文件夹持平）。 */
.workspace-add-icon {
  width: 18px;
  height: 18px;
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

/* History, active tabs and the mode rail share the active theme's accent.
   Idle uses --ally-accent-bright rather than --ally-accent-text: this glyph is
   hollow line art while the twotone folder beside it fills a whole silhouette,
   so at the same paint the thin strokes read a shade dimmer. One step up on the
   accent ramp evens out the optical weight (light mode maps both to the same
   ink, where the contrast is already high). */
.history-action-button {
  color: var(--ally-accent-bright) !important;
}

.history-action-button:hover,
.history-action-button:focus-visible {
  color: var(--ally-accent-strong) !important;
}

/* 资源树开关：展开时只点亮为强调色，不铺选中背景。
   顶栏给“选中”的信号是颜色（tab 用底部线），再叠一块面会让按钮看起来像
   多了一个 tab、也压过旁边同样处于强调色的图标。 */
.explorer-action-button.active {
  color: var(--ally-accent-text) !important;
}

.brand {
  display: flex;
  align-items: center;
  flex-shrink: 0;
  /* The header gap already separates brand from tabs; this adds room so the
     mark does not crowd the first tab. Everything here is on a 4px grid. */
  padding: 0 8px 0 0;
  /* 起波光晕（.brand-glow）的定位基准 */
  position: relative;
  --wails-draggable: drag;
}

/* Sized against the 16px header icons: at 25px the mark carried more visual
   weight than every control next to it and read as the header's subject. */
.brand-wordmark {
  font-size: 20px;
  line-height: 1;
  white-space: nowrap;
  /* 字标现在可点击（数码波浪）：整个 header 是窗口拖拽区，可交互元素必须显式
     声明 no-drag，否则点击会被 WebView2 的窗口拖动吃掉（AGENTS.md §4.3）。
     这里是静态规则、pointerdown 时已生效。 */
  --wails-draggable: no-drag;
  cursor: pointer;
  user-select: none;
  /* 定位 + 显式层级：字标要压在上面那层光晕之上。两者同为定位兄弟节点，
     画序由 z-index 决定，不去赌哪个祖先恰好建了层叠上下文。 */
  position: relative;
  z-index: 1;
}

/* 起波瞬间字标自己亮一下（点下去 80ms 冲亮、之后 360ms 回落）。
   光晕只动 opacity——纯合成属性；不再用 filter: brightness/drop-shadow 过渡：
   那两个属性每帧都要重绘，正是 AGENTS.md §4.7 排除的写法。 */
.brand-glow {
  position: absolute;
  top: 50%;
  left: -6px;
  right: 2px;
  height: 30px;
  transform: translateY(-50%);
  border-radius: 10px;
  background: radial-gradient(closest-side, color-mix(in srgb, var(--ally-accent) 60%, transparent), transparent 100%);
  opacity: 0;
  transition: opacity 360ms ease;
  pointer-events: none;
  z-index: 0;
}

.brand-glow--struck {
  opacity: 1;
  transition-duration: 80ms;
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

.workspace-tabs {
  width: 100%;
  min-width: 0;
  height: 100%;
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
  transition: none;
  padding: 0;
  line-height: 1;
  --wails-draggable: no-drag;
}

.window-close-icon {
  width: 16px;
  height: 16px;
  flex: none;
}

.window-control-btn:hover {
  background: var(--header-hover-bg);
  color: var(--header-strong);
}

.window-control-btn.close:hover {
  background: #e81123;
  color: #fff;
}

/* macOS: native traffic lights → wordmark → tabs → workspace actions.
   AppKit owns the controls, hover symbols, inactive state and fullscreen.
   Keep a stable leading inset even in fullscreen so tabs do not jump
   when macOS reveals its titlebar. The shared row stays 44px tall.

   Both sides of the wordmark get 16px of air, and both numbers come from
   measured geometry instead of taste:
   - Left: under MacTitleBarHiddenInset + MacToolbarStyleUnifiedCompact AppKit
     places the cluster at x = 12 / 35 / 58 (14px wide, 23px apart), so the
     buttons end at 72px. An 80px reserve plus the header's 8px flex gap puts
     the wordmark ink at 88px — 16px of air. Re-measure if the Wails title bar
     style ever changes.
   - Right: brand padding-right 0 + the same 8px gap + the first tab's 8px
     padding-left = 16px.
   The first tab's padding-left is 8px on macOS only (12px elsewhere), so its
   active underline starts 4px further left than the other tabs'. */
.app-header--mac {
  padding: 0 12px 0 0;
}

.mac-window-controls-space {
  flex: 0 0 80px;
  height: 100%;
  --wails-draggable: drag;
}

.app-header--mac .brand {
  padding-right: 0;
}

/* Only the first tab: every tab after it carries a leading .n-tabs-tab-pad,
   which makes its .workspace-tab the second child of the wrapper. */
.app-header--mac .workspace-tabs :deep(.n-tabs-tab-wrapper > .workspace-tab:first-child) {
  padding-left: 8px !important;
}

.workspace-tabs :deep(.workspace-tab) {
  position: relative;
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 0 8px 0 12px !important;
  height: 100%;
  border-radius: 0;
  cursor: pointer;
  color: var(--header-muted);
  font-size: 12px;
  line-height: 1;
  white-space: nowrap;
  user-select: none;
  transition: none;
  flex-shrink: 0;
  min-width: 112px;
  max-width: 180px;
  border: 0;
  --wails-draggable: no-drag;
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
  transition: none;
  margin-left: auto;
  flex-shrink: 0;
}

.workspace-tabs :deep(.workspace-tab .tab-close:hover) {
  background: var(--ally-hover-strong);
  color: var(--ally-text-primary);
}

</style>
