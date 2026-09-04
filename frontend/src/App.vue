<!--
SPDX-License-Identifier: GPL-3.0-only

Copyright (C) 2026 tangssst <tangssst@qq.com>
GitHub: https://github.com/Bronya0/ally-agent

This file is part of ally-agent, licensed under the GNU General
Public License v3. See the LICENSE file for details.
-->
<template>
  <n-config-provider :theme="naiveTheme" :theme-overrides="naiveThemeOverrides" :locale="naiveLocale" :date-locale="naiveDateLocale" inline-theme-disabled>
    <n-dialog-provider>
      <n-notification-provider>
        <n-message-provider>
          <n-layout class="app-shell" content-style="display: flex; flex-direction: column;">
            <AppHeader
              :workspace-tabs="chatTabsWithStatus"
              :active-workspace-id="activeWorkspaceId"
              :update-available="updateAvailable"
              :update-auto-supported="updateAutoSupported"
              :latest-version="latestReleaseVersion"
              :is-maximised="isMaximised"
              :history-options="historyOptions"
              @switch-workspace="onHeaderSwitchWorkspace"
              @close-workspace="closeWorkspaceTab"
              @reorder-workspace="reorderWorkspaceTabs"
              @add-workspace="onHeaderAddWorkspace"
              @history-select="onHeaderHistorySelect"
              @open-repository="openRepositoryPage"
              @start-update="startUpdate"
              @minimise="minimiseWindow"
              @toggle-maximise="toggleMaximiseWindow"
              @close-window="closeWindow"
            />

            <!-- Main area: mode rail + (chat workbench | KB guidance card) -->
            <div class="main-area" @pointerdown.capture="clearActiveExplorerTreeSelection">
              <ModeSider :mode="mode" :kb-running="kbSessionRunning" @switch="switchMode" />
              <n-layout v-show="!kbEmptyActive && !settingsActive && !statsActive && !gamesActive" class="chat-layout" :content-style="chatLayoutContentStyle">
                <n-tabs
                  class="workspace-content-tabs"
                  :value="activeWorkspaceId"
                  type="bar"
                  pane-class="workspace-chat-pane"
                >
                  <n-tab-pane
                    v-for="tab in workspaceTabs"
                    :key="tab.id"
                    :name="tab.id"
                    :tab="tab.label"
                    display-directive="show"
                  >
                    <!-- Knowledge-base identity header: plain title row, only
                         the hidden KB tab carries it. -->
                    <div v-if="isKbTab(tab)" class="kb-hero">
                      <div class="kb-hero-text">
                        <h1 class="kb-hero-title">{{ $t('kb.hero.title') }}</h1>
                        <p class="kb-hero-subtitle">{{ $t('kb.hero.subtitle') }}</p>
                      </div>
                      <div class="kb-hero-path" :title="tab.path">
                        <FolderOpenOutlined class="kb-hero-path-icon" />
                        <span class="kb-hero-path-text">{{ tab.path }}</span>
                      </div>
                    </div>
                    <!-- No banner: KB maintenance actions live in the action
                         bar above the composer. -->
                    <ChatMessages
                      :ref="(instance) => setConversationMessagesRef(tab.id, instance)"
                      :messages="displayMessagesForTab(tab)"
                      :render-fn="renderMarkdown"
                      :fmt-k="fmtK"
                      :tools="availableTools"
                      :mcp-servers="mcpServers"
                      @toggle-archive="toggleArchiveMessages"
                      @toggle-tool="toggleToolExpand"
                      @export="(key, msg) => handleExportOption(tab.sessionId, key, msg)"
                      @quick-message="(key) => sendQuickMessage(tab.sessionId, key)"
                      @submit-ask="(msg, answers) => submitAskResponse(tab.sessionId, msg, answers)"
                      @send-suggest="(label) => sendSuggest(tab.sessionId, label)"
                      @delete-user-message="(msg) => handleDeleteUserMessage(tab.sessionId, msg)"
                    />

                    <!-- Plan state belongs to the session behind this workspace Tab. -->
                    <Transition name="plan-panel">
                      <div v-if="showPlanPanelFor(tab)" :class="['plan-panel', { collapsed: isPlanPanelCollapsed(tab) }]">
                        <button
                          class="plan-panel-header"
                          :title="isPlanPanelCollapsed(tab) ? $t('app.plan.expand') : $t('app.plan.collapse')"
                          @click="togglePlanPanel(tab)"
                        >
                          <span>{{ $t('app.plan.title') }}</span>
                          <span class="plan-panel-count">{{ currentPlanNumberFor(tab) }}/{{ planEntriesForTab(tab).length }}</span>
                          <span :class="['plan-panel-toggle', { expanded: !isPlanPanelCollapsed(tab) }]"></span>
                        </button>
                        <div
                          v-show="!isPlanPanelCollapsed(tab)"
                          :ref="(el) => setPlanPanelListRef(tab.id, el)"
                          class="plan-panel-list"
                        >
                          <div
                            v-for="item in orderedPlanEntriesFor(tab)"
                            :key="item.key"
                            :class="['plan-item', item.status]"
                          >
                            <span class="plan-status">{{ item.status === 'done' ? '✓' : item.status === 'in_progress' ? '●' : '○' }}</span>
                            <span class="plan-number">{{ item.number }}.</span>
                            <span class="plan-title">{{ item.title }}</span>
                          </div>
                        </div>
                      </div>
                    </Transition>

                  </n-tab-pane>
                </n-tabs>


              <div class="composer">
                <!-- KB maintenance actions: a row of one-click prompts above
                     the composer, ordered by workflow (build → feed → tidy).
                     Before initialization the KB is forced through init:
                     input is disabled and only the init button shows. -->
                <div v-if="activeTabIsKb" class="kb-action-bar">
                  <n-button
                    v-if="kbIndexMissing"
                    size="tiny"
                    type="primary"
                    secondary
                    :disabled="kbSessionRunning"
                    :title="$t('kb.action.initTip')"
                    @click="runKbAction('kb.init.prompt')"
                  >{{ $t('kb.action.init') }}</n-button>
                  <template v-if="!kbIndexMissing">
                    <n-button
                      size="tiny"
                      secondary
                      :disabled="kbSessionRunning"
                      :title="$t('kb.action.ingestTip')"
                      @click="runKbAction('kb.prompt.ingest')"
                    >{{ $t('kb.action.ingest') }}</n-button>
                    <n-button
                      size="tiny"
                      secondary
                      :disabled="kbSessionRunning"
                      :title="$t('kb.action.auditTip')"
                      @click="runKbAction('kb.prompt.audit')"
                    >{{ $t('kb.action.audit') }}</n-button>
                  </template>
                  <span v-if="kbIndexMissing" class="kb-action-bar-hint">{{ $t('kb.action.needInit') }}</span>
                  <span v-else-if="kbSessionRunning" class="kb-action-bar-hint">{{ $t('kb.action.running') }}</span>
                </div>
                <CommandMenu
                  :visible="commandMenuVisible"
                  :commands="filteredCommands"
                  :builtin-count="filteredBuiltin.length"
                  :skills-count="filteredSkills.length"
                  :selected-index="selectedCommandIndex"
                  @select="applyCommand"
                />
                <FileMentionMenu
                  :visible="fileMentionVisible"
                  :entries="fileMentionEntries"
                  :selected-index="fileMentionSelectedIndex"
                  :loading="fileMentionLoading"
                  :meta="fileMentionMeta"
                  @select="applyFileMention"
                />
                <div v-if="sessionsVisible" class="sessions-menu">
                  <div class="command-title">{{ $t('app.sessions.title', { count: currentWorkspaceSessions.length }) }}</div>
                  <div ref="sessionsScrollRef" class="command-scroll">
                    <div
                      v-for="(s, index) in currentWorkspaceSessions"
                      :key="s.id"
                      :class="['command-item', 'session-item', { active: index === sessionsSelectedIndex, current: s.id === activeSessionId }]"
                      role="button"
                      @mousedown.prevent="selectSession(index)"
                    >
                      <span class="session-index">{{ index + 1 }}</span>
                      <div class="session-body">
                        <div class="session-details">
                          <span class="session-time">{{ fmtTime(s.updatedAt || s.createdAt) }}</span>
                          <span class="session-meta">{{ $t('app.sessions.messages', { count: msgCount(s) }) }}</span>
                        </div>
                        <span class="session-label">{{ sessionDisplayTitle(s) }}</span>
                      </div>
                      <span v-if="s.id === activeSessionId" class="session-current">{{ $t('app.sessions.current') }}</span>
                      <span v-if="s.isRunning" class="session-running">●</span>
                      <button
                        type="button"
                        class="session-delete"
                        :title="$t('app.sessions.delete')"
                        :disabled="s.isRunning || !!s.runId"
                        @mousedown.stop.prevent
                        @click.stop="deleteSession(index)"
                      >
                        <CloseOutlined />
                      </button>
                    </div>
                  </div>
                </div>
                <div v-if="retryBanner" class="composer-retry-banner" :title="retryBanner.error">
                  <ReloadOutlined class="composer-retry-icon" aria-hidden="true" />
                  <span class="composer-retry-text">{{ $t('app.run.retryBanner', { attempt: retryBanner.attempt, max: retryBanner.maxAttempts, error: retryBanner.error }) }}</span>
                  <span v-if="retryBanner.totalKeys > 1" class="composer-retry-key">{{ $t('app.run.retryKey', { key: retryBanner.keyIndex + 1, total: retryBanner.totalKeys }) }}</span>
                </div>
                <div v-if="activeSessionRunning || compactLoadingActive" class="composer-run-status">
                  <template v-if="compactLoadingActive">
                    <span class="composer-run-status-dots" aria-hidden="true">
                      <span class="composer-run-status-dot"></span>
                      <span class="composer-run-status-dot"></span>
                      <span class="composer-run-status-dot"></span>
                    </span>
                    <span class="composer-run-prompt">{{ compactStatusText }}</span>
                  </template>
                  <template v-else>
                    <span v-if="activeSessionRunning" class="composer-run-status-dots" aria-hidden="true">
                      <span class="composer-run-status-dot"></span>
                      <span class="composer-run-status-dot"></span>
                      <span class="composer-run-status-dot"></span>
                    </span>
                    <span
                      v-if="activeSessionRunning && latestUserPromptSummary"
                      class="composer-run-prompt"
                      :title="latestUserPromptSummary"
                    >{{ latestUserPromptSummary }}</span>

                  </template>
                </div>
                <!-- One input instance per workspace tab (keyed by session:tab) so each
                     keeps its own Naive UI autosize mirror, cursor and scroll state. A
                     single shared instance would collapse to minRows after tab switches:
                     restoring a draft equal to the last typed value is skipped by the
                     library's syncSource guard, leaving the mirror empty. -->
                <n-input
                  v-for="tab in workspaceTabs"
                  v-show="tab.id === activeWorkspaceId"
                  :key="`${tab.sessionId}:${tab.id}`"
                  :ref="(el) => setPromptInputRef(tab.id, el)"
                  class="prompt-input"
                  :value="tab.sessionId ? (sessionPromptTexts[tab.sessionId] || '') : ''"
                  type="textarea"
                  :input-props="{
                    onPaste: handlePromptPaste,
                    onCompositionstart: handlePromptCompositionStart,
                    onCompositionend: handlePromptCompositionEnd,
                    onClick: handlePromptCursorActivity,
                    onKeyup: handlePromptKeyup,
                    'data-ally-prompt-input': 'true',
                  }"
                  :autosize="{ minRows: 2, maxRows: 10 }"
                  :disabled="isKbTab(tab) && kbIndexMissing"
                  :placeholder="isKbTab(tab)
                    ? (kbIndexMissing ? $t('kb.composer.uninitPlaceholder') : $t('kb.composer.placeholder'))
                    : $t('app.composer.placeholder')"
                  @update:value="(v) => { if (tab.sessionId) sessionPromptTexts[tab.sessionId] = v; }"
                  @keydown="handlePromptKeydown"
                  @input="handlePromptInput"
                />
                <div v-if="activePendingAttachments.length" class="pending-attachments">
                  <div v-for="att in activePendingAttachments" :key="att.id" class="pending-attachment">
                    <span class="pending-attachment-icon">{{ attachmentIcon(att) }}</span>
                    <span class="pending-attachment-name" :title="att.name">{{ att.name }}</span>
                    <span class="pending-attachment-size">{{ fmtBytes(att.size) }}</span>
                    <button class="pending-attachment-remove" @click="removeAttachment(att.id)" :title="$t('app.attachment.remove')"><CloseOutlined /></button>
                  </div>
                </div>
                <input ref="attachmentInputRef" type="file" multiple class="hidden-file-input" @change="handleAttachmentSelected" />
                <ComposerInfoBar
                  :running="activeSessionRunning"
                  :config="chatConfig"
                  :workspace="activeComposerWorkspace"
                  :git-status="gitStatus"
                    :context-breakdown="contextBreakdown"
                    :footer-stats-loading="footerStatsLoading"
                  :context-percent="contextPercent"
                  :context-used="contextUsed"
                  :context-max="contextMax"
                  :context-usage-style="contextUsageStyle"
                  :workspace-input-tokens="workspaceInputTokens"
                  :workspace-output-tokens="workspaceOutputTokens"
                  :task-center-count="scheduledTasks.length + services.length"
                  :task-center-running-count="scheduledTaskRunningCount + serviceRunningCount"
                  :fmt-k="fmtK"
                  :extra-roots="extraRoots"
                  :explorer-visible="explorerVisibleFor(activeWorkspaceId)"
                  @add-extra-root="addExtraRoot"
                  @remove-extra-root="removeExtraRoot"
                  @switch-model="switchToModel"
                  @open-config="openSettings('models')"
                  @open-git-diff="openGitDiff"
                  @open-workspace="openWorkspaceInFileManager"
                  @change-reasoning-effort="changeReasoningEffort"
                  @open-task-center="openTaskCenter"
                  @toggle-explorer="toggleWorkspaceExplorer"
                  @new-session="createNewSession"
                  @show-sessions="showSessionList"
                  @compact-context="handleCompactCommand"
                  :get-session-messages="() => activeMessages"
                  :session-title="activeSession?.title || ''"
                />
              </div>
            </n-layout>

            <!-- Keep explorers outside Naive UI's tab pane wrapper so the active
                 tree is a normal right-hand flex column, not an overflow escape. -->
            <template v-for="tab in workspaceTabs" :key="`explorer-${tab.id}`">
              <div
                v-if="explorerVisibleFor(tab.id) && !kbEmptyActive && !settingsActive && !statsActive && !gamesActive"
                v-show="tab.id === activeWorkspaceId"
                class="workspace-explorer-slot"
              >
                <WorkspaceExplorer
                  :ref="(el) => setExplorerRef(tab.id, el)"
                  :workspace="explorerWorkspaceFor(tab.id)"
                  :active="tab.id === activeWorkspaceId"
                  :initial-width="explorerTreeWidthFor(tab.id)"
                  :hide-hidden="isKbTab(tab)"
                  :title-text="isKbTab(tab) ? $t('kb.tabLabel') : ''"
                  :empty-hint="isKbTab(tab) ? $t('kb.explorer.emptyHint') : ''"
                  @close="closeExplorerForTab(tab.id)"
                  @tree-width-change="(w) => onExplorerTreeWidthChange(tab.id, w)"
                />
              </div>
            </template>

            <!-- KB mode with no configured root: guidance card replaces the
                 chat workbench (which is v-show hidden for this state). -->
            <div v-if="kbEmptyActive" class="kb-empty-state">
              <div class="kb-empty-card">
                <div class="kb-empty-title">{{ $t('kb.empty.title') }}</div>
                <p class="kb-empty-desc">{{ $t('kb.empty.desc') }}</p>
                <n-button type="primary" :loading="kbPicking" @click="pickKbRootFromEmptyState">
                  {{ $t('kb.empty.pick') }}
                </n-button>
              </div>
            </div>

            <!-- Settings page: the modal was replaced by this inline container
                 on the right of the mode sider. v-show keeps in-page state
                 (tab, scroll, unsaved draft edits) across mode switches. -->
            <div v-show="settingsActive" class="settings-page-container">
              <SettingsModal
                :visible="settingsActive"
                :initial-page="settingsPage"
                :config-draft="configDraft"
                :check-update-result="checkUpdateResult"
                :color-mode="colorMode"
                @set-mode="setColorMode"
                @close="closeSettings"
                @save="onSettingsSave"
                @skills-changed="onSkillsChanged"
                @mcp-saved="onMcpSaved"
                @background-changed="onBackgroundChanged"
                @check-update="onCheckUpdate"
              />
            </div>

            <!-- Token stats page: v-show keeps loaded stats across switches. -->
            <div v-show="statsActive" class="settings-page-container">
              <TokenStatsModal :show="statsActive" @close="closeStats" />
            </div>

            <!-- Games page (协作休息区): v-show keeps the component mounted so
                 an active room/connection survives switching to other modes
                 (unmounting would stop the server and close the room). -->
            <div v-show="gamesActive" class="settings-page-container">
              <GamePanel :show="gamesActive" @close="closeGames" />
            </div>

            </div>
          </n-layout>
          <TaskCenterPanel
            :show="taskCenterVisible"
            :tasks="scheduledTasks"
            :services="services"
            :scheduled-loading="scheduledTasksLoading"
            :services-loading="servicesLoading"
            :deleting-ids="scheduledTaskDeletingIds"
            :stopping-ids="serviceStoppingIds"
            @close="taskCenterVisible = false"
            @refresh="refreshTaskCenter"
            @delete-task="deleteScheduledTask"
            @stop-service="stopManagedService"
          />
          <RenderBoundary :label="$t('app.gitChanges')"><GitDiffModal v-model:show="gitDiffVisible" :git-status="gitStatus" :workspace="config.workspace" /></RenderBoundary>

          <n-modal v-model:show="updateModalVisible" preset="card" :title="$t('app.update.title')" class="update-modal" :mask-closable="false" :close-on-esc="false" :show-close="!isUpdateBusy">
            <div class="update-modal-body">
              <template v-if="updateState === 'downloading' || updateState === 'extracting'">
                <div class="update-status-text">{{ updateState === 'downloading' ? $t('app.update.downloading') : $t('app.update.extracting') }}</div>
                <div class="update-version">{{ $t('app.update.version', { version: latestReleaseVersion }) }}</div>
                <div class="update-progress-bar">
                  <div class="update-progress-fill" :style="{ width: `${updateProgress.percent}%` }"></div>
                </div>
                <div class="update-progress-meta">
                  <span>{{ updateProgress.percent }}%</span>
                  <span v-if="updateState === 'downloading' && updateProgress.bytesTotal > 0">{{ formatBytes(updateProgress.bytesDownloaded) }} / {{ formatBytes(updateProgress.bytesTotal) }}</span>
                </div>
                <div class="update-actions">
                  <n-button size="small" tertiary @click="cancelUpdate">{{ $t('app.update.cancel') }}</n-button>
                </div>
              </template>
              <template v-else-if="updateState === 'ready'">
                <div class="update-status-text">{{ $t('app.update.ready', { version: latestReleaseVersion }) }}</div>
                <div class="update-ready-hint">{{ $t('app.update.readyHint') }}</div>
                <div class="update-actions">
                  <n-button size="small" tertiary @click="skipCurrentVersion">{{ $t('app.update.skip') }}</n-button>
                  <n-button size="small" secondary @click="closeUpdateModal">{{ $t('app.update.later') }}</n-button>
                  <n-button size="small" type="primary" @click="applyAndRestart">{{ $t('app.update.restartNow') }}</n-button>
                </div>
              </template>
              <template v-else-if="updateState === 'applying'">
                <div class="update-status-text">{{ $t('app.update.applying') }}</div>
                <div class="update-spinner"></div>
                <div class="update-ready-hint">{{ $t('app.update.applyingHint') }}</div>
              </template>
              <template v-else-if="updateState === 'restarting'">
                <div class="update-status-text">{{ $t('app.update.restarting') }}</div>
                <div class="update-spinner"></div>
              </template>
              <template v-else-if="updateState === 'error'">
                <div class="update-status-text error">{{ $t('app.update.failed') }}</div>
                <div class="update-error-detail">{{ updateError }}</div>
                <div class="update-actions">
                  <n-button size="small" secondary @click="closeUpdateModal">{{ $t('common.close') }}</n-button>
                  <n-button size="small" type="primary" @click="startUpdate">{{ $t('app.update.retry') }}</n-button>
                </div>
              </template>
              <div v-if="updateState === 'downloading' || updateState === 'extracting' || updateState === 'ready' || updateState === 'error'" class="update-github-row">
                <span class="update-github-label">{{ $t('app.update.githubLabel') }}</span>
                <button class="update-github-link" @click="openUpdateReleasesPage">{{ ALLY_RELEASES_URL }}</button>
              </div>
            </div>
          </n-modal>

          <!-- 樱花青草风特效层：全局唯一实例，body 顶层播放，切 Tab 不中断 -->
          <SakuraBreeze :open="sakuraOn" />

          <SplashScreen v-if="splashVisible" @done="splashVisible = false" />
        </n-message-provider>
      </n-notification-provider>
    </n-dialog-provider>
  </n-config-provider>
</template>

<script setup>
import { computed, defineAsyncComponent, h, nextTick, onErrorCaptured, onMounted, onUnmounted, reactive, ref, watch } from 'vue';
import { NButton, createDiscreteApi, darkTheme } from 'naive-ui';
import { getStoredMode, setMode as persistMode } from './utils/theme.mjs';
import MarkdownIt from 'markdown-it';
// @traptitech/markdown-it-katex 把 $...$ / $$...$$ 交给 katex 渲染。
// katex 本体已作为 mermaid 的间接依赖存在于依赖树中，这里显式声明以避免
// mermaid 升级时断链；插件本身极轻（~10KB）。
import katexPlugin from '@traptitech/markdown-it-katex';
import 'katex/dist/katex.min.css';
import hljs from 'highlight.js/lib/core';
import javascript from 'highlight.js/lib/languages/javascript';
import typescript from 'highlight.js/lib/languages/typescript';
import json from 'highlight.js/lib/languages/json';
import bash from 'highlight.js/lib/languages/bash';
import go from 'highlight.js/lib/languages/go';
import xml from 'highlight.js/lib/languages/xml';
import cssLang from 'highlight.js/lib/languages/css';
import markdownLang from 'highlight.js/lib/languages/markdown';
import 'highlight.js/styles/base16/darcula.css';
import { highlightShellCommand, isShellLanguage, looksLikeShellCommand } from './utils/shellHighlight.mjs';
import { formatReadRangeChip } from './utils/toolFormat.mjs';
import {
  clearSessionSnapshotStore,
  loadSessionSnapshots,
} from './utils/sessionStore.mjs';
import { saveTextFile } from './utils/download.mjs';
import { mermaidFenceSpec, normalizeMermaidSource, loadMermaid } from './utils/mermaidShared.mjs';
import { copyText } from './utils/clipboard.mjs';
import {
  CancelRun,
  CancelCompaction,
  CheckForUpdates,
  GetConfig,
  GetContextBreakdown,
  GetWorkspaceTokenUsage,
  ListFiles,
  ResetWorkspaceTokenUsage,
  GetGitStatus,
  SaveConfig,
  SelectWorkspace,
  SelectKnowledgeBaseRoot,
  StartChat,
  InjectRunMessage,
  CompactSession,
  ListSkills,
  ListTools,
  ListSessions,
  LoadSession,
  SaveSession,
  SaveSessionIndex,
  OpenWorkspaceInFileManager,
  OpenWorkspacePathInFileManagerAt,
  ActivateSkill,
  DeactivateSkill,
  GetActiveSkills,
  GetTodos,
  GetMcpServers,
  GetMcpConfig,
  SaveMcpConfig,
  RestartMcpServers,
  ListScheduledTasks,
  DeleteScheduledTask,
  ListServices,
  StopService,
  DeleteSession,
  ReleaseSession,
  TruncateSessionHistory,
  SubmitAskResponse,
  SearchWorkspacePaths,
  DownloadUpdate,
  CancelUpdate,
  ApplyUpdate,
  QuitForUpdate,
  SkipUpdate,
  GetBackgroundImageURL,
} from '../bindings/ally-dev/internal/app/app';
import { Application, Browser, Events, Window } from '@wailsio/runtime';
import CloseOutlined from '@vicons/antd/CloseOutlined';
import PlusOutlined from '@vicons/antd/PlusOutlined';
import ReloadOutlined from '@vicons/antd/ReloadOutlined';
import FolderOpenOutlined from '@vicons/antd/FolderOpenOutlined';
import AllyWordmark from './components/AllyWordmark.vue';
import ComposerInfoBar from './components/ComposerInfoBar.vue';
import MessageAttachments from './components/MessageAttachments.vue';
import ReadGroupCard from './components/ReadGroupCard.vue';
import RenderBoundary from './components/RenderBoundary.vue';
import SplashScreen from './components/SplashScreen.vue';
import SakuraBreeze from './components/SakuraBreeze.vue';
import SubagentInlineCard from './components/SubagentInlineCard.vue';
import WelcomeMessage from './components/WelcomeMessage.vue';
import ToolCallCard from './components/ToolCallCard.vue';
import AppHeader from './components/AppHeader.vue';
import ModeSider from './components/ModeSider.vue';
import CommandMenu from './components/CommandMenu.vue';
import FileMentionMenu from './components/FileMentionMenu.vue';
import SettingsModal from './components/SettingsModal.vue';
import ChatMessages from './components/ChatMessages.vue';
import TaskCenterPanel from './components/TaskCenterPanel.vue';
import TokenStatsModal from './components/TokenStatsModal.vue';
import GamePanel from './games/GamePanel.vue';
import { assignConfig, defaultConfig } from './utils/config.mjs';
import { useSakuraBreeze } from './composables/sakuraBreeze.mjs';

// 樱花青草风特效的全局开关；唯一特效层实例见模板根部
const { sakuraOn } = useSakuraBreeze();
import { modelConfigIdentity, normalizeApiKeysArray, normalizeReasoningEffort } from './utils/modelConfigIO.mjs';
import { getSpecificModelUsage } from './utils/modelUsage.mjs';
import { buildVersion } from './utils/buildVersion.js';
import { computeEditStats, formatEditStats } from './utils/diff.js';
import { isNewerReleaseVersion } from './utils/versionCheck.mjs';
import { findSessionWorkspaceTab, isEditableNavigationTarget, shouldAcceptRunTerminal } from './utils/sessionState.mjs';
import { orderPlanPanelEntries, planFocusScrollDelta } from './utils/planPanel.mjs';
import { formatDateTime, naiveDateLocale, naiveLocale, reasoningEffortLabel, t, welcomeGreeting as localizedWelcomeGreeting } from './i18n.mjs';
import { fmtCompact, fmtDuration } from './utils/format.mjs';
import { isSkillActive } from './utils/skills.mjs';
import {
  displaySourceMessages as buildDisplaySourceMessages,
  formatBytes as formatBytesShared,
  formatHttpToolTitle,
  isRenderableMessage,
} from './utils/toolPreview.mjs';
import {
  commitToolEventMessage as commitToolEventById,
  findToolEventMessage as findToolEventById,
  findToolEventByData,
  normalizeToolStatus,
  setToolStatus,
  toolEventId,
} from './utils/toolEventState.mjs';
import { useToolEvents } from './composables/useToolEvents.mjs';
import { unwrapWailsEvent } from './utils/wailsEvent.mjs';

const GitDiffModal = defineAsyncComponent(() => import('./components/GitDiffModal.vue'));
const WorkspaceExplorer = defineAsyncComponent(() => import('./components/WorkspaceExplorer.vue'));
onErrorCaptured((err, _instance, info) => {
  console.error('[ui:error]', info, err);
  return false;
});

const conversationMessagesRefs = new Map();
const promptInputRefs = reactive({});
function setPromptInputRef(tabId, el) {
  if (el) {
    promptInputRefs[tabId] = el;
  } else {
    delete promptInputRefs[tabId];
    // 输入框卸载（切换/新建会话时 v-for 重建）会丢失进行中的输入法组合事件，
    // 不重置会让 promptComposing 残留为 true，导致之后按 Enter 一直只换行不发送。
    promptComposing.value = false;
    promptCompositionEndedAt = 0;
  }
}
const promptComposing = ref(false);
let promptCompositionEndedAt = 0;
let fileMentionTimer = 0;
let fileMentionRequestId = 0;

// ── Color mode (dark / light) ──
// The mode is a pure front-end preference (localStorage, see utils/theme.mjs);
// main.js already applied it to <html data-mode> before mount. This ref is the
// reactive source for every Naive UI theme decision below.
const colorMode = ref(getStoredMode());
const isLightMode = computed(() => colorMode.value === 'light');
const naiveTheme = computed(() => (isLightMode.value ? null : darkTheme));

function setColorMode(mode) {
  colorMode.value = persistMode(mode);
}

// Mode switch re-themes already-rendered Mermaid diagrams: mermaidShared reads
// the new data-mode on the next loadMermaid() call, so every observed diagram
// just needs its rendered/suspended state reset and a re-queue.
watch(colorMode, () => {
  for (const node of Array.from(mermaidObservedNodes)) {
    if (!node.isConnected) continue;
    node._mermaidCleanup?.();
    node._mermaidCleanup = null;
    node._mermaidStopTransforming = null;
    node.classList.remove('rendered', 'mermaid-suspended', 'interaction-active', 'mermaid-transforming');
    delete node.dataset.mermaidRendered;
    delete node.dataset.mermaidQueued;
    delete node.dataset.mermaidSuspended;
    removeMermaidCache(node._mermaidCacheKey);
    const output = node.querySelector('.markdown-mermaid-output');
    if (output) output.replaceChildren();
    queueMermaidDiagram(node);
  }
});

const darkThemeOverrides = {
  common: {
    bodyColor: '#1a1a1a',
    baseColor: '#1a1a1a',
    cardColor: '#1a1a1a',
    modalColor: '#1a1a1a',
    popoverColor: '#1a1a1a',
    tableColor: '#1a1a1a',
    primaryColor: '#d4d4d4',
    primaryColorHover: '#ffffff',
    primaryColorPressed: '#b8b8b8',
    primaryColorSuppl: '#ffffff',
    borderColor: 'rgba(255, 255, 255, 0.08)',
    dividerColor: 'rgba(255, 255, 255, 0.08)',
    textColorBase: '#f5f5f5',
    textColor1: '#f5f5f5',
    textColor2: '#d4d4d4',
    textColor3: '#8a8a8a',
    borderRadius: '10px',
    fontFamily: 'Inter',
  },
  Layout: {
    color: '#1a1a1a',
    siderColor: '#1a1a1a',
    headerColor: '#1a1a1a',
  },
  Card: {
    color: '#1a1a1a',
    colorEmbedded: '#1a1a1a',
  },
  Input: {
    color: '#1a1a1a',
    colorFocus: '#1a1a1a',
    border: '1px solid rgba(255,255,255,0.12)',
    borderFocus: '1px solid rgba(255,255,255,0.32)',
  },
  // Switch 开启态默认沿用 primaryColor（#d4d4d4 灰白），与关闭态（灰）
  // 区分度太低，看着像没开。这里单独覆盖为项目"激活/已连接"语义的绿色，
  // 与 session-running / mcp-status-dot.connected 一致。不影响其它依赖
  // primaryColor 的组件（n-button / n-checkbox / n-tag 等）。
  Switch: {
    railColorActive: '#4ade80',
    loadingColor: '#4ade80',
  },
};

// Light mode: clean acrylic glass (DeepSeek-style modal language). Overlay
// surfaces are translucent white so the blur added in style.css reads as
// frosted glass over the soft gradient canvas.
const lightThemeOverrides = {
  common: {
    bodyColor: '#f6f7f9',
    baseColor: '#ffffff',
    cardColor: 'rgba(255, 255, 255, 0.88)',
    modalColor: 'rgba(255, 255, 255, 0.92)',
    popoverColor: 'rgba(255, 255, 255, 0.94)',
    tableColor: 'rgba(255, 255, 255, 0.88)',
    primaryColor: '#3a3f45',
    primaryColorHover: '#1f2328',
    primaryColorPressed: '#2e3338',
    primaryColorSuppl: '#1f2328',
    borderColor: 'rgba(15, 23, 42, 0.08)',
    dividerColor: 'rgba(15, 23, 42, 0.08)',
    textColorBase: '#1f2328',
    textColor1: '#1f2328',
    textColor2: '#3a3f45',
    textColor3: '#6b7280',
    borderRadius: '10px',
    fontFamily: 'Inter',
  },
  Layout: {
    color: 'transparent',
    siderColor: 'rgba(255, 255, 255, 0.55)',
    headerColor: 'rgba(255, 255, 255, 0.55)',
  },
  Card: {
    color: 'rgba(255, 255, 255, 0.88)',
    colorEmbedded: 'rgba(255, 255, 255, 0.6)',
  },
  Input: {
    color: 'rgba(255, 255, 255, 0.72)',
    colorFocus: 'rgba(255, 255, 255, 0.88)',
    border: '1px solid rgba(15, 23, 42, 0.12)',
    borderFocus: '1px solid rgba(15, 23, 42, 0.32)',
  },
  Switch: {
    railColorActive: '#16a34a',
    loadingColor: '#16a34a',
  },
};

const naiveThemeOverrides = computed(() => (isLightMode.value ? lightThemeOverrides : darkThemeOverrides));

const { message, dialog } = createDiscreteApi(['message', 'dialog'], {
  configProviderProps: computed(() => ({
    theme: naiveTheme.value,
    themeOverrides: naiveThemeOverrides.value,
    locale: naiveLocale,
    dateLocale: naiveDateLocale,
  })),
});

hljs.registerLanguage('javascript', javascript);
hljs.registerLanguage('js', javascript);
hljs.registerLanguage('typescript', typescript);
hljs.registerLanguage('ts', typescript);
hljs.registerLanguage('json', json);
hljs.registerLanguage('bash', bash);
hljs.registerLanguage('shell', bash);
hljs.registerLanguage('sh', bash);
hljs.registerLanguage('go', go);
hljs.registerLanguage('html', xml);
hljs.registerLanguage('xml', xml);
hljs.registerLanguage('css', cssLang);
hljs.registerLanguage('markdown', markdownLang);
hljs.registerLanguage('md', markdownLang);


let markdownRenderStreaming = false;
let mermaidRenderScheduled = false;
let mermaidRenderSequence = 0;
let mermaidObserver = null;
let mermaidRenderQueue = Promise.resolve();
const mermaidObservedNodes = new Set();
const mermaidSvgCache = new Map();
const MERMAID_CACHE_MAX_ENTRIES = 16;
const MERMAID_CACHE_MAX_CHARS = 2_000_000;
let mermaidSvgCacheChars = 0;
let mermaidCacheSequence = 0;

function renderMermaidFence(code, spec) {
  const source = normalizeMermaidSource(code, spec);
  if (!source) return '';
  const encodedSource = encodeURIComponent(source);
  scheduleMermaidRender();
  return [
    `<div class="markdown-mermaid" data-mermaid-source="${markdown.utils.escapeHtml(encodedSource)}" tabindex="0" title="${markdown.utils.escapeHtml(t('app.mermaid.interactionHint'))}">`,
    `<div class="markdown-mermaid-toolbar" aria-label="${t('app.mermaid.actions')}">`,
    `<button type="button" class="markdown-mermaid-action" data-mermaid-action="download" title="${t('app.mermaid.download')}" aria-label="${t('app.mermaid.download')}"><svg viewBox="0 0 24 24" aria-hidden="true"><path d="M12 3v12M7 10l5 5 5-5M5 21h14"/></svg></button>`,
    '</div>',
    '<div class="markdown-mermaid-output"></div>',
    `<pre class="hljs code-block markdown-mermaid-fallback"><code>${markdown.utils.escapeHtml(source)}</code></pre>`,
    '</div>',
  ].join('');
}

function renderMarkdownWithMode(source, streaming) {
  markdownRenderStreaming = streaming;
  try {
    return markdown.render(source);
  } finally {
    markdownRenderStreaming = false;
  }
}

const markdown = new MarkdownIt({
  html: false,
  linkify: true,
  breaks: true,
}).use(katexPlugin);

function renderHighlightedCodeBlock(code, highlighted, extraClass = '') {
  const rawLines = String(code || '').replace(/\r\n/g, '\n').split('\n');
  if (rawLines.length > 1 && rawLines[rawLines.length - 1] === '') rawLines.pop();
  const lineCount = Math.max(1, rawLines.length);
  const copyLabel = markdown.utils.escapeHtml(t('code.copy'));
  const countLabel = markdown.utils.escapeHtml(t('code.lines', { count: lineCount }));
  return `<div class="code-block-wrapper"><span class="code-line-count">${countLabel}</span><button class="code-copy-btn" title="${copyLabel}" aria-label="${copyLabel}"><svg viewBox="0 0 24 24" aria-hidden="true"><rect x="9" y="9" width="13" height="13" rx="2" fill="none" stroke="currentColor" stroke-width="2"/><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1" fill="none" stroke="currentColor" stroke-width="2"/></svg></button><pre class="hljs code-block ${extraClass}"><code>${highlighted}</code></pre></div>`;
}

const defaultCodeInlineRenderer = markdown.renderer.rules.code_inline || ((tokens, idx, options, _env, self) => self.renderToken(tokens, idx, options));
markdown.renderer.rules.code_inline = (tokens, idx, options, env, self) => {
  const content = tokens[idx]?.content || '';
  if (looksLikeShellCommand(content)) {
    return `<code class="shell-inline">${highlightShellCommand(content)}</code>`;
  }
  return defaultCodeInlineRenderer(tokens, idx, options, env, self);
};

markdown.renderer.rules.fence = (tokens, idx, options, env, self) => {
  const token = tokens[idx];
  const diagramSpec = mermaidFenceSpec(token.info);
  if (diagramSpec && !markdownRenderStreaming) {
    const renderedDiagram = renderMermaidFence(token.content, diagramSpec);
    if (renderedDiagram) return `${renderedDiagram}\n`;
  }
  const lang = String(token.info || '').trim().split(/\s+/)[0].toLowerCase();
  if (lang === 'ascii-art') {
    return `<pre class="ascii-banner"><code>${markdown.utils.escapeHtml(token.content)}</code></pre>\n`;
  }
  try {
    const highlighted = highlightFence(lang, token.content);
    if (highlighted === null) {
      return `${renderHighlightedCodeBlock(token.content, markdown.utils.escapeHtml(token.content), '')}\n`;
    }
    return `${renderHighlightedCodeBlock(token.content, highlighted, isShellLanguage(lang) ? 'shell-code' : '')}\n`;
  } catch (_) {
    return `${renderHighlightedCodeBlock(token.content, markdown.utils.escapeHtml(token.content), '')}\n`;
  }
};

// fence 高亮缓存：流式渲染每次 flush 都会对累积全文重新 Markdown 解析，
// 已闭合的历史 fence 若每帧重新 tokenize，高亮成本随输出长度平方增长。
// 流式与非流式渲染都读写缓存：已闭合 fence 的内容稳定，第二帧起命中；
// 流式中活跃增长块的 key 每帧不同，只会多占一条位置，由 LRU 上限挤出。
// 另外流式渲染中未标注语言的 fence 跳过 highlightAuto——它要尝试数十种
// 语法、是单帧最贵的一步，流结束后最终渲染会自动恢复高亮。
const fenceHighlightCache = new Map();
const FENCE_HIGHLIGHT_CACHE_LIMIT = 64;
// 超长代码块的高亮 HTML 可达数百 KB，设源码长度上限防止缓存长期占用过多内存
const FENCE_HIGHLIGHT_CACHE_MAX_CHARS = 8000;

function highlightFence(lang, content) {
  const highlightLang = isShellLanguage(lang) ? 'bash' : lang;
  const knownLang = Boolean(highlightLang && hljs.getLanguage(highlightLang));
  if (!knownLang && markdownRenderStreaming) return null;
  // 流式 flush 与最终渲染都读写缓存：流式期间已闭合 fence 的内容稳定，
  // 第二帧起命中的正是上一帧的结果；活跃增长块每帧 key 不同，只会多占
  // 一条缓存位置，不会污染其他条目的命中。
  const cacheable = content.length <= FENCE_HIGHLIGHT_CACHE_MAX_CHARS;
  const cacheKey = cacheable ? `${highlightLang || ''}\u0000${content}` : '';
  if (cacheable) {
    const cached = fenceHighlightCache.get(cacheKey);
    if (cached !== undefined) {
      fenceHighlightCache.delete(cacheKey);
      fenceHighlightCache.set(cacheKey, cached);
      return cached;
    }
  }
  let highlighted;
  try {
    highlighted = knownLang
      ? hljs.highlight(content, { language: highlightLang }).value
      : hljs.highlightAuto(content).value;
  } catch (_) {
    return null;
  }
  if (cacheable) {
    fenceHighlightCache.set(cacheKey, highlighted);
    if (fenceHighlightCache.size > FENCE_HIGHLIGHT_CACHE_LIMIT) {
      fenceHighlightCache.delete(fenceHighlightCache.keys().next().value);
    }
  }
  return highlighted;
}

function scheduleMermaidRender() {
  if (typeof window === 'undefined' || typeof document === 'undefined') return;
  if (mermaidRenderScheduled) return;
  mermaidRenderScheduled = true;
  nextTick(() => {
    window.requestAnimationFrame(() => {
      mermaidRenderScheduled = false;
      observePendingMermaidDiagrams();
    });
  });
}

function ensureMermaidObserver() {
  if (mermaidObserver || typeof IntersectionObserver === 'undefined') return mermaidObserver;
  mermaidObserver = new IntersectionObserver((entries) => {
    for (const entry of entries) {
      const node = entry.target;
      node._mermaidNearViewport = entry.isIntersecting;
      if (entry.isIntersecting) {
        if (node.dataset.mermaidSuspended === 'true') restoreMermaidDiagram(node);
        else queueMermaidDiagram(node);
      } else {
        suspendMermaidDiagram(node);
      }
    }
  }, { rootMargin: '900px 0px' });
  return mermaidObserver;
}

function cleanupDisconnectedMermaidNodes() {
  let removedAny = false;
  for (const node of mermaidObservedNodes) {
    if (node.isConnected) continue;
    mermaidObserver?.unobserve(node);
    node._mermaidCleanup?.();
    removeMermaidCache(node._mermaidCacheKey);
    mermaidObservedNodes.delete(node);
    removedAny = true;
  }
  // If we removed anything, schedule a self-cleanup pass to drop more
  // disconnected nodes that may surface after this batch. Without this,
  // a session that only swaps messages (no archive toggle) would never
  // call cleanupDisconnectedMermaidNodes again, and orphan nodes with
  // their 7 event listeners + closures would leak until the next switch.
  if (removedAny) {
    requestAnimationFrame(() => {
      for (const node of mermaidObservedNodes) {
        if (node.isConnected) continue;
        mermaidObserver?.unobserve(node);
        node._mermaidCleanup?.();
        removeMermaidCache(node._mermaidCacheKey);
        mermaidObservedNodes.delete(node);
      }
    });
  }
}

function observePendingMermaidDiagrams() {
  cleanupDisconnectedMermaidNodes();
  const nodes = Array.from(document.querySelectorAll('.markdown-mermaid[data-mermaid-source]'));
  if (!nodes.length) return;
  const observer = ensureMermaidObserver();
  if (!observer) {
    for (const node of nodes) queueMermaidDiagram(node);
    return;
  }
  for (const node of nodes) {
    if (mermaidObservedNodes.has(node)) continue;
    mermaidObservedNodes.add(node);
    observer.observe(node);
  }
}

function queueMermaidDiagram(node) {
  if (!node || node.dataset.mermaidQueued || node.dataset.mermaidRendered || node._mermaidNearViewport === false) return;
  node.dataset.mermaidQueued = 'true';
  mermaidRenderQueue = mermaidRenderQueue
    .then(waitForMermaidRenderSlot)
    .then(() => renderMermaidDiagram(node))
    .catch(() => {});
}

function waitForMermaidRenderSlot() {
  return new Promise((resolve) => {
    if (typeof window.requestIdleCallback === 'function') {
      window.requestIdleCallback(() => resolve(), { timeout: 300 });
      return;
    }
    window.requestAnimationFrame(() => window.setTimeout(resolve, 0));
  });
}

function loadMermaidModule() {
  // 初始化配置已抽到 utils/mermaidShared.mjs，与编辑器 Markdown 预览共享。
  return loadMermaid();
}

async function renderMermaidDiagram(node) {
  if (!node?.isConnected || node.dataset.mermaidRendered || node._mermaidNearViewport === false) {
    if (node) delete node.dataset.mermaidQueued;
    return;
  }
  node.dataset.mermaidRendering = 'true';
  let mermaid;
  try {
    mermaid = await loadMermaidModule();
  } catch (err) {
    markMermaidError(node, t('app.mermaid.loadFailed', { error: err?.message || err || 'unknown error' }));
    delete node.dataset.mermaidQueued;
    delete node.dataset.mermaidRendering;
    return;
  }

  try {
    const source = decodeURIComponent(node.dataset.mermaidSource || '');
    const output = node.querySelector('.markdown-mermaid-output');
    if (!source || !output) throw new Error('empty diagram');
    const id = `ally-mermaid-${Date.now()}-${++mermaidRenderSequence}`;
    const result = await mermaid.render(id, source);
    if (!node.isConnected) return;
    output.innerHTML = result.svg || '';
    setupMermaidInteraction(node, output, null, true);
    if (typeof result.bindFunctions === 'function') {
      result.bindFunctions(output);
    }
    node.dataset.mermaidRendered = 'true';
    node.classList.add('rendered');
    if (node._mermaidNearViewport === false) suspendMermaidDiagram(node);
  } catch (err) {
    markMermaidError(node, err?.message || String(err || t('app.mermaid.renderFailed')));
  } finally {
    delete node.dataset.mermaidQueued;
    delete node.dataset.mermaidRendering;
  }
}

function setupMermaidInteraction(node, output, initialState = null, measureHeight = true) {
  const svg = output?.querySelector?.('svg');
  if (!node || !output || !svg) return;

  node._mermaidCleanup?.();

  const stage = document.createElement('div');
  stage.className = 'markdown-mermaid-stage';
  stage.appendChild(svg);
  output.replaceChildren(stage);

  const state = {
    scale: Number(initialState?.scale) || 1,
    x: Number(initialState?.x) || 0,
    y: Number(initialState?.y) || 0,
    dragging: false,
    pointerId: null,
    startX: 0,
    startY: 0,
    originX: 0,
    originY: 0,
  };
  let transformFrame = 0;
  let transformReleaseTimer = 0;

  const persistViewState = () => {
    node._mermaidViewState = { scale: state.scale, x: state.x, y: state.y };
  };
  const flushTransform = () => {
    transformFrame = 0;
    if (!stage.isConnected) return;
    stage.style.transform = `translate(${state.x}px, ${state.y}px) scale(${state.scale})`;
    persistViewState();
  };
  const scheduleTransform = () => {
    node.classList.add('mermaid-transforming');
    if (!transformFrame) transformFrame = window.requestAnimationFrame(flushTransform);
    if (transformReleaseTimer) window.clearTimeout(transformReleaseTimer);
    transformReleaseTimer = window.setTimeout(() => {
      transformReleaseTimer = 0;
      node.classList.remove('mermaid-transforming');
    }, 160);
  };
  const activate = () => {
    for (const active of document.querySelectorAll('.markdown-mermaid.interaction-active')) {
      if (active !== node) {
        active.classList.remove('interaction-active', 'mermaid-transforming');
        active._mermaidStopTransforming?.();
      }
    }
    node.classList.add('interaction-active');
    node.focus({ preventScroll: true });
  };
  const reset = () => {
    state.scale = 1;
    state.x = 0;
    state.y = 0;
    scheduleTransform();
  };

  flushTransform();

  node._mermaidStopTransforming = () => {
    if (transformReleaseTimer) window.clearTimeout(transformReleaseTimer);
    transformReleaseTimer = 0;
    node.classList.remove('mermaid-transforming');
  };
  node._mermaidCleanup = () => {
    if (transformFrame) window.cancelAnimationFrame(transformFrame);
    transformFrame = 0;
    node._mermaidStopTransforming?.();
  };

  output.addEventListener('pointerdown', (event) => {
    if (event.button !== 0 || event.target?.closest?.('a, button')) return;
    activate();
    state.dragging = true;
    state.pointerId = event.pointerId;
    state.startX = event.clientX;
    state.startY = event.clientY;
    state.originX = state.x;
    state.originY = state.y;
    output.classList.add('dragging');
    output.setPointerCapture?.(event.pointerId);
    event.preventDefault();
  });

  output.addEventListener('pointermove', (event) => {
    if (!state.dragging || event.pointerId !== state.pointerId) return;
    state.x = state.originX + event.clientX - state.startX;
    state.y = state.originY + event.clientY - state.startY;
    scheduleTransform();
  });

  const stopDragging = (event) => {
    if (!state.dragging || (event.pointerId !== undefined && event.pointerId !== state.pointerId)) return;
    state.dragging = false;
    output.classList.remove('dragging');
    if (state.pointerId !== null && output.hasPointerCapture?.(state.pointerId)) {
      output.releasePointerCapture(state.pointerId);
    }
    state.pointerId = null;
  };
  output.addEventListener('pointerup', stopDragging);
  output.addEventListener('pointercancel', stopDragging);
  output.addEventListener('lostpointercapture', stopDragging);

  output.addEventListener('wheel', (event) => {
    if (!node.classList.contains('interaction-active')) return;
    event.preventDefault();
    const rect = output.getBoundingClientRect();
    const pointerX = event.clientX - rect.left;
    const pointerY = event.clientY - rect.top;
    const deltaUnit = event.deltaMode === 1 ? 16 : event.deltaMode === 2 ? Math.max(output.clientHeight, 1) : 1;
    const factor = Math.exp(-event.deltaY * deltaUnit * 0.0015);
    const nextScale = Math.min(5, Math.max(0.25, state.scale * factor));
    if (Math.abs(nextScale - state.scale) < 0.0001) return;
    const contentX = (pointerX - state.x) / state.scale;
    const contentY = (pointerY - state.y) / state.scale;
    state.x = pointerX - contentX * nextScale;
    state.y = pointerY - contentY * nextScale;
    state.scale = nextScale;
    scheduleTransform();
  }, { passive: false });

  output.addEventListener('dblclick', (event) => {
    if (event.target?.closest?.('a, button')) return;
    activate();
    reset();
    event.preventDefault();
  });

  if (!measureHeight) return;
  window.requestAnimationFrame(() => {
    if (!output.isConnected) return;
    const renderedHeight = Math.ceil(svg.getBoundingClientRect().height || 0);
    output.style.height = `${Math.min(640, Math.max(180, renderedHeight + 8))}px`;
  });
}

function removeMermaidCache(cacheKey) {
  if (!cacheKey || !mermaidSvgCache.has(cacheKey)) return;
  const cached = mermaidSvgCache.get(cacheKey);
  mermaidSvgCacheChars = Math.max(0, mermaidSvgCacheChars - Number(cached?.size || 0));
  mermaidSvgCache.delete(cacheKey);
}

function cacheMermaidSvg(node, entry) {
  const svgText = String(entry?.svg || '');
  if (!node._mermaidCacheKey) node._mermaidCacheKey = `mermaid-cache-${++mermaidCacheSequence}`;
  removeMermaidCache(node._mermaidCacheKey);
  if (!svgText || svgText.length > MERMAID_CACHE_MAX_CHARS) return false;
  const cached = { ...entry, size: svgText.length };
  mermaidSvgCache.set(node._mermaidCacheKey, cached);
  mermaidSvgCacheChars += cached.size;
  while (mermaidSvgCache.size > MERMAID_CACHE_MAX_ENTRIES || mermaidSvgCacheChars > MERMAID_CACHE_MAX_CHARS) {
    const oldestKey = mermaidSvgCache.keys().next().value;
    if (!oldestKey) break;
    removeMermaidCache(oldestKey);
  }
  return mermaidSvgCache.has(node._mermaidCacheKey);
}

function suspendMermaidDiagram(node) {
  if (!node?.isConnected || node.dataset.mermaidRendered !== 'true' || node.dataset.mermaidSuspended === 'true') return;
  const output = node.querySelector('.markdown-mermaid-output');
  const svg = output?.querySelector?.('svg');
  if (!output || !svg) return;
  node.classList.remove('interaction-active', 'mermaid-transforming');
  if (document.activeElement === node) node.blur();
  const height = output.style.height || `${Math.max(180, Math.ceil(output.getBoundingClientRect().height || 0))}px`;
  cacheMermaidSvg(node, {
    svg: svg.outerHTML,
    height,
    state: node._mermaidViewState || { scale: 1, x: 0, y: 0 },
  });
  node._mermaidCleanup?.();
  node._mermaidCleanup = null;
  node._mermaidStopTransforming = null;
  const placeholder = output.cloneNode(false);
  placeholder.style.height = height;
  output.replaceWith(placeholder);
  node.dataset.mermaidSuspended = 'true';
  node.classList.add('mermaid-suspended');
}

function restoreMermaidDiagram(node) {
  if (!node?.isConnected || node.dataset.mermaidSuspended !== 'true') return;
  const cacheKey = node._mermaidCacheKey;
  const cached = cacheKey ? mermaidSvgCache.get(cacheKey) : null;
  if (!cached) {
    delete node.dataset.mermaidRendered;
    delete node.dataset.mermaidSuspended;
    node.classList.remove('rendered', 'mermaid-suspended');
    queueMermaidDiagram(node);
    return;
  }
  mermaidSvgCache.delete(cacheKey);
  mermaidSvgCache.set(cacheKey, cached);
  const output = node.querySelector('.markdown-mermaid-output');
  if (!output) return;
  output.innerHTML = cached.svg;
  output.style.height = cached.height || '180px';
  node._mermaidViewState = cached.state || { scale: 1, x: 0, y: 0 };
  setupMermaidInteraction(node, output, node._mermaidViewState, false);
  delete node.dataset.mermaidSuspended;
  node.classList.remove('mermaid-suspended');
}

function deactivateMermaidInteraction() {
  const active = document.querySelector('.markdown-mermaid.interaction-active');
  if (!active) return false;
  active.classList.remove('interaction-active', 'mermaid-transforming');
  active._mermaidStopTransforming?.();
  if (document.activeElement === active) active.blur();
  return true;
}

function handleMermaidOutsidePointerDown(event) {
  if (event.target?.closest?.('.markdown-mermaid')) return;
  deactivateMermaidInteraction();
}

function markMermaidError(node, messageText) {
  if (!node || !node.isConnected) return;
  node.dataset.mermaidRendered = 'true';
  node.classList.add('error');
  let error = node.querySelector('.markdown-mermaid-error');
  if (!error) {
    error = document.createElement('div');
    error.className = 'markdown-mermaid-error';
    node.prepend(error);
  }
  error.textContent = messageText || t('app.mermaid.renderFailed');
}

function handleMermaidToolbarClick(event) {
  const button = event.target?.closest?.('.markdown-mermaid-action');
  if (!button) return;
  const node = button.closest('.markdown-mermaid');
  if (!node) return;
  event.preventDefault();
  event.stopPropagation();
  const action = button.dataset.mermaidAction || '';
  if (action === 'download') {
    downloadMermaidSvg(node);
  }
}

function handleCodeCopyClick(event) {
  const btn = event.target?.closest?.('.code-copy-btn');
  if (!btn) return;
  event.preventDefault();
  event.stopPropagation();
  const wrapper = btn.closest('.code-block-wrapper');
  const codeEl = wrapper?.querySelector('code');
  const code = codeEl?.textContent || '';
  if (!code) return;

  const showCopied = () => {
    if (!document.body.contains(btn)) return;
    // Clear any previous restore timer so a rapid second click doesn't
    // restore the original innerHTML early — multiple stacked timers each
    // captured different "original" states and the last-to-fire would
    // overwrite an in-flight copy icon. Clearing before starting keeps
    // the cycle (original → checkmark → original) well-ordered.
    if (btn._copyTimer) {
      clearTimeout(btn._copyTimer);
      btn._copyTimer = null;
    }
    const original = btn.innerHTML;
    btn.innerHTML = '<svg viewBox="0 0 24 24" aria-hidden="true"><polyline points="20 6 9 17 4 12" fill="none" stroke="currentColor" stroke-width="2"/></svg>';
    btn._copyTimer = setTimeout(() => {
      if (document.body.contains(btn)) btn.innerHTML = original;
      btn._copyTimer = null;
    }, 1500);
  };

  copyText(code).then((ok) => {
    if (ok) showCopied();
    else message.error(t('app.copy.failed'));
  });
}

function handleMarkdownLinkClick(event) {
  const anchor = event.target?.closest?.('.markdown-body a[href]');
  if (!anchor) return;
  const rawHref = String(anchor.getAttribute('href') || '').trim();
  if (!rawHref || rawHref.startsWith('#')) return;
  event.preventDefault();
  event.stopPropagation();
  const href = rawHref.startsWith('//') ? `https:${rawHref}` : rawHref;
  if (/^(?:https?:|mailto:)/i.test(href)) Browser.OpenURL(href);
}

function downloadMermaidSvg(node) {
  const svg = node.querySelector('.markdown-mermaid-output svg');
  if (!svg) return;
  const copy = svg.cloneNode(true);
  if (!copy.getAttribute('xmlns')) {
    copy.setAttribute('xmlns', 'http://www.w3.org/2000/svg');
  }
  const background = document.createElementNS('http://www.w3.org/2000/svg', 'rect');
  const viewBox = String(copy.getAttribute('viewBox') || '').trim().split(/[\s,]+/).map(Number);
  if (viewBox.length === 4 && viewBox.every(Number.isFinite)) {
    background.setAttribute('x', String(viewBox[0]));
    background.setAttribute('y', String(viewBox[1]));
    background.setAttribute('width', String(viewBox[2]));
    background.setAttribute('height', String(viewBox[3]));
  } else {
    background.setAttribute('x', '0');
    background.setAttribute('y', '0');
    background.setAttribute('width', '100%');
    background.setAttribute('height', '100%');
  }
  background.setAttribute('fill', isLightMode.value ? '#ffffff' : '#2b2b2b');
  background.setAttribute('aria-hidden', 'true');
  copy.insertBefore(background, copy.firstChild);
  const source = decodeURIComponent(node.dataset.mermaidSource || '');
  const svgText = new XMLSerializer().serializeToString(copy);
  const blob = new Blob([svgText], { type: 'image/svg+xml;charset=utf-8' });
  const url = URL.createObjectURL(blob);
  const link = document.createElement('a');
  link.href = url;
  link.download = mermaidDownloadName(source);
  document.body.appendChild(link);
  link.click();
  link.remove();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}

function mermaidDownloadName(source) {
  const firstLine = String(source || '').split('\n').find((line) => line.trim()) || 'diagram';
  const slug = firstLine
    .trim()
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-+|-+$/g, '')
    .slice(0, 48) || 'diagram';
  return `${slug}.svg`;
}

const config = reactive(defaultConfig());
const configDraft = reactive(defaultConfig());
// Custom chat background data URL (base64). Loaded once after config init and
// refreshed whenever the user picks or clears an image in Settings. Kept here
// rather than in config so the multi-MB data URL never round-trips through
// SaveConfig — only the filename and opacity are persisted.
const backgroundImageUrl = ref('');

// Per-tab model selection. Maps a runtime workspace Tab id to a full snapshot
// of that Tab's model fields. Stored in localStorage (not backend config.json)
// so the backend stays workspace-agnostic; chat requests overlay the active
// Tab's snapshot on top of the persisted config. The Tab id is intentional:
// two Tabs pointing at the same workspace must not share a model selection.
//
// 单一职责：config 顶层的模型字段（providerName/model/baseUrl/...）只表示
// "默认模型"——设置页"使用模型"保存的就是它，新 Tab 从它初始化。当前 Tab
// 实际使用的模型只存在这里，切换 Tab / 切换模型 / 保存设置都不会改写
// config 顶层的模型字段，因此设置保存从结构上就不可能重置当前 Tab。
const modelByTab = reactive({});
const MODEL_BY_TAB_KEY = 'ally_model_by_tab';
const LAST_USED_MODEL_KEY = 'ally_last_used_model';

function getLastUsedModelIdentity() {
  try {
    return localStorage.getItem(LAST_USED_MODEL_KEY) || '';
  } catch {
    return '';
  }
}

function setLastUsedModelIdentity(identity) {
  try {
    if (identity) localStorage.setItem(LAST_USED_MODEL_KEY, String(identity));
  } catch {}
}

// Extract a normalized model snapshot from a config-shaped source (a preset
// from config.models, or the top-level default fields).
function modelSnapshotFrom(source) {
  const keys = normalizeApiKeysArray(
    Array.isArray(source?.apiKeys) && source.apiKeys.length
      ? source.apiKeys
      : (source?.apiKey ? [source.apiKey] : [])
  );
  return {
    providerName: source?.providerName || 'OpenAI Compatible',
    apiFormat: source?.apiFormat || 'openai_chat',
    baseUrl: source?.baseUrl || '',
    model: source?.model || '',
    temperature: source?.temperature ?? 0.2,
    maxTokens: source?.maxTokens || 131072,
    contextWindow: source?.contextWindow || 1000000,
    tokenParam: source?.tokenParam || 'auto',
    reasoningTag: String(source?.reasoningTag || '').trim() || 'reasoning_content',
    reasoningEffort: normalizeReasoningEffort(source?.reasoningEffort),
    apiKeys: keys,
    apiKey: keys[0] || '',
  };
}

// 查找按照使用频率累计倒排的最常用有效模型，若频率都相同或无记录则返回第一个
function getFallbackModelPreset(models) {
  if (!Array.isArray(models) || !models.length) return null;
  if (models.length === 1) return models[0];
  const usage = getSpecificModelUsage();
  const sorted = [...models].sort((a, b) => {
    const countA = Number(usage[modelConfigIdentity(a)]) || 0;
    const countB = Number(usage[modelConfigIdentity(b)]) || 0;
    return countB - countA;
  });
  return sorted[0] || models[0];
}

// The default model every newly opened Tab starts from:
// 1. 用户上次对话/切换选了什么模型，新开的 tab 默认就继承这个模型；
// 2. 如果上次选的模型已经被删除了（或不存在）：按每个模型的使用频率累计倒排回退；
// 3. 如果首次只有一个模型那默认就这个；
// 4. 若模型列表为空则回退到 config。
function defaultModelSnapshot() {
  const models = config.models || [];
  const lastIdentity = getLastUsedModelIdentity();

  if (lastIdentity) {
    const matched = models.find((m) => modelConfigIdentity(m) === lastIdentity);
    if (matched) return modelSnapshotFrom(matched);
  }

  // 尝试当前活跃 Tab 上的模型
  const activeTabModel = modelByTab[activeWorkspaceId.value];
  if (activeTabModel) {
    const matched = models.find((m) => modelConfigIdentity(m) === modelConfigIdentity(activeTabModel));
    if (matched) {
      setLastUsedModelIdentity(modelConfigIdentity(matched));
      return modelSnapshotFrom(matched);
    }
  }

  // 上次选的模型不存在或已被删除，按使用频率倒排回退
  const fallbackPreset = getFallbackModelPreset(models);
  if (fallbackPreset) {
    setLastUsedModelIdentity(modelConfigIdentity(fallbackPreset));
    return modelSnapshotFrom(fallbackPreset);
  }

  return modelSnapshotFrom(config);
}

// Initialize a Tab's model from the default the first time it is visited.
function ensureTabModel(tab) {
  if (!tab || modelByTab[tab.id]) return;
  modelByTab[tab.id] = defaultModelSnapshot();
  saveModelByTab();
}

// The config every chat request sends: the persisted config with the active
// Tab's model snapshot overlaid. Replaces the old pattern of mutating config's
// top-level model fields on every Tab/model switch.
const chatConfig = computed(() => ({ ...config, ...modelByTab[activeWorkspaceId.value] }));

function loadModelByTab() {
  try {
    const raw = localStorage.getItem(MODEL_BY_TAB_KEY);
    const parsed = raw ? JSON.parse(raw) : {};
    for (const [k, v] of Object.entries(parsed)) {
      // Tab ids are regenerated on launch, so stale string entries from older
      // builds never match anyway — only load full snapshots.
      if (v && typeof v === 'object' && typeof v.model === 'string' && v.model) {
        modelByTab[k] = modelSnapshotFrom(v);
      }
    }
  } catch (_) { /* ignore corrupt entries */ }
}

function saveModelByTab() {
  try {
    localStorage.setItem(MODEL_BY_TAB_KEY, JSON.stringify(modelByTab));
  } catch (_) { /* ignore quota errors */ }
}

// After Settings saves an updated model list, re-sync every Tab's snapshot
// from its preset so edits (API key, base URL, ...) propagate to Tabs already
// using that model. Tabs whose preset was deleted keep their last snapshot.
function resyncTabModelsFromPresets() {
  const models = config.models || [];
  for (const tabId of Object.keys(modelByTab)) {
    const snapshot = modelByTab[tabId];
    const preset = models.find((m) => modelConfigIdentity(m) === modelConfigIdentity(snapshot));
    if (preset) modelByTab[tabId] = modelSnapshotFrom(preset);
  }
  saveModelByTab();
}

const sessions = ref([]);
const activeSessionId = ref('');
// 当前会话的 LLM 请求重试状态(null 表示无重试)。非持久化,切换会话或新一轮 delta 时清除。
const retryBanner = ref(null);
const sessionPromptTexts = reactive({});
const promptText = computed({
  get: () => sessionPromptTexts[activeSessionId.value] || '',
  set: (val) => { sessionPromptTexts[activeSessionId.value] = val; },
});
const commandMenuVisible = ref(false);
const selectedCommandIndex = ref(0);
const fileMentionVisible = ref(false);
const fileMentionEntries = ref([]);
const fileMentionSelectedIndex = ref(0);
const fileMentionLoading = ref(false);
const fileMentionMeta = ref('');
const fileMentionRange = ref(null);
const sessionsVisible = ref(false);
const sessionsSelectedIndex = ref(0);
const sessionsScrollRef = ref(null);
const commandHistory = ref([]);
const commandHistoryIndex = ref(-1);
const settingsPage = ref('general');
// Settings & token stats render inline in the main area (mode === 'settings'
// / 'stats'); configVisible is derived so existing watchers/guards keep
// working. preOverlayMode is the mode to return to when either page closes.
const preOverlayMode = ref('chat');
const configVisible = computed(() => mode.value === 'settings');

function openSettings(page = 'general') {
  settingsPage.value = page;
  if (mode.value !== 'settings') preOverlayMode.value = mode.value;
  mode.value = 'settings';
}
const workspaceTabs = ref([]);
const activeWorkspaceId = ref('');
const extraRoots = ref([]);
const workspaceHistory = ref(loadWorkspaceHistory());
const showSkillsPanel = ref(false);
const todosBySession = reactive({});
const todoRevisionsBySession = reactive({});
// Plan UI state is per session/Tab so switching tabs never reuses another
// session's collapsed state or list scroll position.
const planPanelCollapsedBySession = reactive({});
const planPanelListRefsByTab = reactive(new Map());
const isMaximised = ref(false);
const availableSkills = ref([]);
const activeSkillNames = ref([]);
const skillsLoading = ref(false);
const skillToggleInFlight = ref('');
const availableTools = ref([]);
const scheduledTasks = ref([]);
const services = ref([]);
const taskCenterVisible = ref(false);
// Workspace explorer 状态按 Tab 独立保存：切换 Tab 时不会重置或取消任何
// 每个 Tab 看到自己工作区的目录树。已经打开过的 Tab
// 会保留一个常驻组件实例（v-show 切换），编辑草稿不因切 Tab 丢失。
// 注意：必须用 reactive() 包装 Map，computed/v-for 才能追踪变化。
// 默认开启：新 Tab 首次访问时自动打开资源树（未手动关闭过的工作区视为默认 true）。
const workspaceExplorerByTab = reactive(new Map());
const explorerWorkspaceByTab = reactive(new Map());
const explorerRefsByTab = reactive(new Map());
// 资源树开关持久化：按工作区路径（归一化后）记录“用户手动关闭”的工作区，
// 无记录 = 默认开启。存 localStorage 与 modelByTab 同模式；路径比 Tab id 稳定，
// 重启后同一工作区能恢复关闭状态。两个 Tab 指向同一工作区时共享默认值。
const explorerClosedWorkspaces = reactive({});
const EXPLORER_CLOSED_KEY = 'ally_explorer_closed_workspaces';

function loadExplorerClosedWorkspaces() {
  try {
    const raw = localStorage.getItem(EXPLORER_CLOSED_KEY);
    const parsed = raw ? JSON.parse(raw) : {};
    for (const [k, v] of Object.entries(parsed)) {
      if (v) explorerClosedWorkspaces[k] = true;
    }
  } catch (_) { /* ignore corrupt entries */ }
}

function persistExplorerClosedWorkspaces() {
  try {
    localStorage.setItem(EXPLORER_CLOSED_KEY, JSON.stringify(explorerClosedWorkspaces));
  } catch (_) { /* ignore quota errors */ }
}

function explorerDefaultForPath(path) {
  // 未选工作区时不展示资源树（与 toggle 入口的工作区检查一致）。
  const key = workspaceHistoryDedupeKey(path || '');
  if (!key) return false;
  return !explorerClosedWorkspaces[key];
}

function setExplorerClosedForPath(path, closed) {
  const key = workspaceHistoryDedupeKey(path || '');
  if (!key) return;
  if (closed) explorerClosedWorkspaces[key] = true;
  else if (explorerClosedWorkspaces[key]) delete explorerClosedWorkspaces[key];
  persistExplorerClosedWorkspaces();
}

function explorerTabPath(tabId) {
  const tab = workspaceTabs.value.find((t) => t.id === tabId);
  return (tab && tab.path) || config.workspace || '';
}

function explorerVisibleFor(tabId) {
  const recorded = workspaceExplorerByTab.get(tabId);
  if (recorded !== undefined) return recorded;
  return explorerDefaultForPath(explorerTabPath(tabId));
}
function explorerWorkspaceFor(tabId) {
  const tab = workspaceTabs.value.find((t) => t.id === tabId);
  return explorerWorkspaceByTab.get(tabId) ?? (tab ? tab.path : config.workspace);
}
function setExplorerRef(tabId, el) {
  if (el) explorerRefsByTab.set(tabId, el);
  else explorerRefsByTab.delete(tabId);
}

function clearActiveExplorerTreeSelection(event) {
  if (event?.target?.closest?.('.workspace-explorer')) return;
  explorerRefsByTab.get(activeWorkspaceId.value)?.clearTreeSelection?.();
}

function closeExplorerForTab(tabId) {
  workspaceExplorerByTab.set(tabId, false);
  setExplorerClosedForPath(explorerTabPath(tabId), true);
}
const explorerTreeWidthByTab = reactive(new Map());
// 文件树默认宽度 = 窗口宽度的 20%，clamp 到 240–360：下限保证文件名可读，
// 上限防止大屏出现空走廊；首访懒计算一次并缓存，窗口 resize 不回跳已展示的宽度。
// 手动拖拽（150–600）后按 tab 记忆，不再走此默认值。
let explorerTreeWidthDefault = 0;
function defaultExplorerTreeWidth() {
  if (!explorerTreeWidthDefault) {
    explorerTreeWidthDefault = Math.max(240, Math.min(360, Math.round(window.innerWidth * 0.2)));
  }
  return explorerTreeWidthDefault;
}
function explorerTreeWidthFor(tabId) {
  return explorerTreeWidthByTab.get(tabId) ?? defaultExplorerTreeWidth();
}
function onExplorerTreeWidthChange(tabId, width) {
  explorerTreeWidthByTab.set(tabId, width);
}
const scheduledTasksLoading = ref(false);
const servicesLoading = ref(false);
const scheduledTaskDeletingIds = ref([]);
const serviceStoppingIds = ref([]);
const mcpConfigText = ref('');
const mcpServers = ref([]);
const mcpLoading = ref(false);
const streamBuffers = new Map();
const runtimeEventOffs = [];
const missingDependencyWarningsShown = new Set();
let streamFlushScheduled = false;
let streamFlushRaf = 0;
let runtimeEventsBound = false;
// Periodic update check timer. Ally performs one best-effort check at
// startup; this timer ensures a long-running session (e.g. always-on tray)
// still discovers new releases every 2 hours without requiring a restart.
// The Atom feed used for the check is not rate-limited, so a shorter interval
// is safe.
let updateCheckTimer = 0;
const UPDATE_CHECK_INTERVAL_MS = 2 * 60 * 60 * 1000;
// tool:update events carry the full accumulated arguments on every emit.
// During large payload streams (e.g. create with thousands of lines)
// processing each event synchronously blocks the main thread, freezes the
// tool card, and balloons webview memory. Buffer the latest event per tool
// call and flush on a timer so the UI repaints between updates.
const toolUpdateBuffers = new Map();
// Tools that never render as visible tool cards. Their tool:start / tool:update
// / tool:error events are silently dropped; tool:result may still carry custom
// UI-injection logic (e.g. suggest injects chips into the assistant message).
const HIDDEN_TOOL_NAMES = new Set(['suggest']);
const isHiddenTool = (name) => HIDDEN_TOOL_NAMES.has(name);
let toolUpdateFlushScheduled = false;
let toolUpdateFlushTimer = 0;
// Pending attachments are scoped per session (mirrors sessionPromptTexts) so a
// file pasted or picked into one workspace's composer does not leak into another
// workspace when you switch tabs without sending.
const pendingAttachmentsBySession = reactive({});

function pendingAttachmentsOf(sessionId) {
  if (!sessionId) return [];
  if (!pendingAttachmentsBySession[sessionId]) pendingAttachmentsBySession[sessionId] = [];
  return pendingAttachmentsBySession[sessionId];
}

function clearPendingAttachments(sessionId, { revoke = true } = {}) {
  const arr = pendingAttachmentsBySession[sessionId];
  if (!arr) return;
  if (revoke) for (const att of arr) releaseAttachmentPreview(att);
  pendingAttachmentsBySession[sessionId] = [];
}

function deletePendingAttachments(sessionId) {
  clearPendingAttachments(sessionId);
  delete pendingAttachmentsBySession[sessionId];
}

const activePendingAttachments = computed(() => pendingAttachmentsOf(activeSessionId.value));
const attachmentInputRef = ref(null);
const MAX_ATTACHMENTS_PER_MESSAGE = 8;
const MAX_ATTACHMENT_PREVIEW_BYTES = 8 * 1024 * 1024;
const MAX_IMAGE_INPUT_BYTES = 5 * 1024 * 1024;
const MAX_TEXT_ATTACHMENT_BYTES = 200 * 1024;
const MAX_STORED_ATTACHMENT_TEXT_CHARS = 20000;
const splashVisible = ref(true);
const expandedArchiveSessions = ref(new Set());
const updateAvailable = ref(false);
const latestReleaseVersion = ref('');
// Self-update state (Windows x64 ZIP and macOS DMG; other platforms keep the open-browser behavior).
const updateAutoSupported = ref(false);
const updateState = ref('idle'); // idle | downloading | extracting | ready | applying | restarting | error
const updateProgress = ref({ stage: '', percent: 0, bytesDownloaded: 0, bytesTotal: 0, version: '' });
const updateError = ref('');
const updateModalVisible = ref(false);
// When true, download runs in background without a progress modal. Set by
// automatic startup download; flipped to false once download succeeds so the
// "ready to restart" modal can pop.
const updateSilent = ref(false);
// Set when the user clicks "cancel update" so late update:ready / download
// results are ignored instead of re-opening the modal (the backend may not
// have registered its cancel handle yet when the click arrives).
let updateCancelRequested = false;
// Result object reported to SettingsModal's About page so the manual
// "Check for updates" button gets explicit latest/found/failed feedback.
// State: 'idle' | 'busy' | 'latest' | 'found' | 'failed'.
const checkUpdateResult = ref({ state: 'idle' });
const isUpdateBusy = computed(() => {
  return updateState.value === 'downloading' || updateState.value === 'extracting' || updateState.value === 'applying' || updateState.value === 'restarting';
});

const ALLY_REPOSITORY_URL = 'https://github.com/Bronya0/ally-agent';
const ALLY_RELEASES_URL = 'https://github.com/Bronya0/ally-agent/releases';


const builtinCommands = [
  { key: 'new', label: '/new', description: t('commands.new'), text: '', special: 'new' },
  { key: 'skills', label: '/skills', description: t('commands.skills'), text: '', special: 'skills' },
  { key: 'sessions', label: '/sessions', description: t('commands.sessions'), text: '', special: 'sessions' },
  { key: 'init', label: '/init', description: t('commands.init'), text: '', special: 'init' },
  { key: 'note', label: '/note', description: t('commands.note'), text: '', special: 'remember' },
  { key: 'lesson', label: '/lesson', description: t('commands.lesson'), text: '', special: 'lesson' },
  { key: 'review', label: '/review', description: '', text: '', special: 'review' },
  { key: 'compact', label: '/compact', description: t('commands.compact'), text: '', special: 'compact' },
  { key: 'push', label: '/push', description: t('commands.push'), text: '', special: 'push' },
];

// Dynamically includes skill commands
const commands = computed(() => {
  const cmds = [...builtinCommands];
  for (const sk of availableSkills.value) {
    // Don't add duplicates if a builtin has the same label
    const label = `/${sk.name}`;
    if (!cmds.some(c => c.label === label)) {
      cmds.push({
        key: `skill:${sk.name}`,
        label,
        description: sk.description || sk.name,
        text: '',
        special: 'skill',
        skillName: sk.name,
      });
    }
  }
  return cmds;
});

const INIT_PROMPT = `Explore the current project directory to understand the project architecture and main details.

Task requirements:
1. Analyze the project structure and identify key configuration files (such as package.json, go.mod, Cargo.toml, pyproject.toml, etc.).
2. Understand the project technology stack, build process and runtime architecture.
3. Identify how the code is organized and main module divisions.
4. Discover project-specific development conventions, testing strategies, and deployment processes.

After the exploration, write a thorough summary into AGENTS.md file in the project root. AGENTS.md is a file intended to be read by AI coding agents. Assume the reader knows nothing about the project.

Compose the file according to the actual project content. Do not make assumptions or generalizations. Ensure the information is accurate and useful.

Popular sections to include:
- Project overview
- Build and test commands
- Code style guidelines
- Testing instructions
- Security considerations

First, explore the codebase, then create or update AGENTS.md. If AGENTS.md exists, read it and update it with edit. If it does not exist, create it with create.`;

const REMEMBER_PROMPT = `Save durable project knowledge from this conversation.

1. Extract only high-confidence, reusable facts: architecture decisions, conventions, hidden dependencies, gotchas, important file locations, and data flows.
2. Each saved bullet must be concise and cite concrete file paths when relevant.
3. Use read first if an existing memory from the global memory index may already cover this project (paths are absolute under ~/.ally_agent/memories).
4. Save or update the knowledge with create (new) or edit (existing, using the version from read). Use an explicit stable path such as ~/.ally_agent/memories/project-knowledge/<workspace-or-project-name>.md.
5. Do not edit AGENTS.md for this command. Do not save speculation, one-off task status, transient bug-fix notes, or generic advice.`;

const LESSON_PROMPT = `Review this conversation for reusable pitfalls: hidden framework behavior, project-specific conventions, or environment traps that would likely trip again in another file or task.

Update .ally/lessons.md in the workspace root:
1. Read the file first; if it does not exist, create it.
2. Add one line per lesson in this format: - [tag] symptom → root cause → fix @file-or-area
3. Update the matching line when the same lesson is already recorded; otherwise append a new line.
4. Only record pitfalls that would recur elsewhere. Never record one-off compile errors, failed tests, plain coding mistakes, or tool errors.
5. If nothing qualifies, say there is nothing to save and do not create the file.`;

// REVIEW_PROMPT instructs the model when the user runs /review. Sub-agents
// are used only when the change is complex enough to justify them; the main
// agent verifies evidence and consolidates.
const REVIEW_PROMPT = `Review the current uncommitted code changes in this workspace.

Step 1 — Scope the change:
Run git status and git diff --stat. Read the diff plus enough surrounding context to understand the intent.

Step 2 — Review directly or delegate, based on the change's complexity:
Judge the complexity from Step 1: number of files, total diff size, and whether it touches sensitive areas (auth/security, data handling, concurrency, network, destructive operations).

- Simple change (1-2 small files, a few lines, cosmetic/config/docs/test-only): review it yourself in the main context — do NOT spawn sub-agents. Check the same things a correctness sub-agent would: trace the changed paths, verify error handling and edge cases.
- Moderate change (one medium-sized file, or a few files with real logic): spawn ONE sub-agent with the correctness lens; handle security and architecture yourself. Use two only when the change touches sensitive areas.
- Large or cross-module change (many files, or touching auth/security/data/concurrency/network): spawn 2-3 parallel sub-agents, one per lens:
  1. correctness — logic errors, edge cases, error handling, resource leaks, concurrency races
  2. security — injection, authn/authz, input validation, secrets & sensitive data exposure, plus the failure modes security-related changes introduce: e.g. a hardcoded secret moved into a database or vault must also work on a fresh install where that secret does not exist yet, must fail closed (never log the secret, never fall back to an insecure default), and must not silently break startup or existing deployments; also check missing env/config dependencies and whether the change weakens existing checks (removed validation, loosened permissions, disabled security gates)
  3. architecture & maintainability — layering violations, duplication, dead code, missing or weakened tests (skip this lens for anything but large or cross-module changes)

Each sub-agent costs time and tokens: use the fewest that cover the risk, and never spawn one for a change you can fully review in the main context.
Every sub-agent task must include all of the following:
  (a) the changed file paths only — pass relative paths and, when known, the specific functions/symbols to review; never paste file contents or the full diff into the task; let each sub-agent run git status / git diff / git diff --stat itself to inspect the actual changes;
  (b) the full requirement — extract the user's complete requirement from the conversation history: the original request, acceptance criteria, business rules, and constraints (what inputs are invalid, what side effects are forbidden); pass that whole requirement to every sub-agent so it can judge whether the code meets it; if the history has no explicit requirement, state plainly that the requirement is unknown and have the sub-agent review against the code's apparent intent — never silently guess;
  (c) that sub-agent's single lens (only the lens it owns, nothing else);
  (d) output requirements — output is for the main agent to verify, not for the user: be complete, terse, professional, zero filler — just findings, each with file:line, a concrete trigger path, and a P0/P1/P2 grade; no greetings, disclaimers, explanatory prose, or markdown decoration;
  (e) constraints — read-only, review only this change and its direct impact (not the whole project), never modify files; read only the scoped files plus, when needed, the interfaces/dependencies they call — do NOT grep, search, or read files outside the scope unless you must understand an interface or dependency the scoped code uses; stay pragmatic: do not over-investigate or chase rabbit holes — once your lens is covered, stop and report (deep dives waste time and tokens);
  (f) deployment reality — for any changed code that now depends on something new (database records, environment variables, external services, config), check whether that dependency exists in a fresh install and in an existing deployment; if it may be missing, the code must fail safely and clearly, not silently misbehave or fall back to insecure defaults
Keep the sub-agents fully independent: never share one sub-agent's conclusions with another. They may read files and run read-only commands (git diff, grep, tests), but must NOT modify any files.

Step 3 — Verify and consolidate:
Check the evidence for every reported issue yourself (file, line, trigger path) before accepting it. Drop findings you cannot confirm from the actual code, deduplicate across sub-agents keeping the strongest evidence per issue, and do not rubber-stamp sub-agent output.

Final report — write it for a human, in the user's preferred language (match the language the user is using in the UI), easy to understand:
- Present the issues as a Markdown table, one row per issue, with columns: 等级 (P0/P1/P2), 问题与风险 (what it is and its risk), 修复建议 (how to fix), 工作量 (effort), 推荐修复 (recommendation: 强烈建议 / 建议 / 没必要). Keep each cell to one plain sentence.
- Order rows by severity (P0 first). Above the table, add a two-to-three-line plain-language summary of the biggest risks in everyday words.
- If nothing significant was found, say so plainly in one or two lines — no table needed.
Do not modify any files — this is a review only.`;

const activeSession = computed(() => sessions.value.find((session) => session.id === activeSessionId.value));

function sessionForWorkspaceTab(tab) {
  if (!tab?.sessionId) return null;
  return sessions.value.find((session) => session.id === tab.sessionId) || null;
}
// Return a stable array reference when nothing actually changed so downstream
// watchers (AppHeader) short-circuit. The previous version returned a fresh
// array + fresh per-tab object literals on every recompute, even when every
// tab's isRunning flag was identical to the previous result — that forced
// AppHeader to re-render its tab list on every reactive tick that touched
// sessions or workspaceTabs, even though no tab actually moved.
let workspaceTabsWithStatusCache = null;
let workspaceTabsWithStatusSignature = '';
const workspaceTabsWithStatus = computed(() => {
  const tabs = workspaceTabs.value;
  // Build a cheap signature: tab ids + label + path + running flags. If the
  // signature matches the last computed one, we can reuse the previous array
  // reference. label/path are included because applySessionWorkspace and
  // syncConfigToActiveTab mutate them in place — without them in the
  // signature, AppHeader would keep showing the old tab label after a
  // workspace switch until isRunning or tab list shape also changed.
  const parts = [];
  for (const tab of tabs) {
    const session = tab.sessionId ? sessions.value.find((item) => item.id === tab.sessionId) : null;
    parts.push(`${tab.id}:${tab.sessionId || ''}:${tab.label || ''}:${tab.path || ''}:${!!(session?.isRunning || session?.runId) ? 1 : 0}`);
  }
  const sig = parts.join('|');
  if (sig === workspaceTabsWithStatusSignature && workspaceTabsWithStatusCache) {
    return workspaceTabsWithStatusCache;
  }
  workspaceTabsWithStatusSignature = sig;
  workspaceTabsWithStatusCache = tabs.map((tab) => {
    const session = tab.sessionId ? sessions.value.find((item) => item.id === tab.sessionId) : null;
    return { ...tab, isRunning: !!(session?.isRunning || session?.runId) };
  });
  return workspaceTabsWithStatusCache;
});
const activeMessages = computed(() => activeSession.value?.messages || []);
const SESSION_PROMPT_SUMMARY_MAX_CHARS = 96;

function promptSummaryText(value, maxChars = Infinity) {
  const text = String(value || '').replace(/\s+/g, ' ').trim();
  if (!Number.isFinite(maxChars) || text.length <= maxChars) return text;
  const contentLimit = Math.max(1, maxChars - 1);
  return `${text.slice(0, contentLimit).trimEnd()}…`;
}

function latestPromptSummaryForSession(session, maxChars = Infinity) {
  const messages = Array.isArray(session?.messages) ? session.messages : [];
  for (let index = messages.length - 1; index >= 0; index--) {
    const message = messages[index];
    if (message?.role !== 'user') continue;
    const content = String(message.content || '').replace(/\s+/g, ' ').trim();
    const attachments = Array.isArray(message.attachments) ? message.attachments : [];
    const attachmentLabel = attachmentDisplayLabel(attachments);
    if (!content) return promptSummaryText(attachmentLabel, maxChars);
    const combined = attachmentLabel && content !== attachmentLabel
      ? `${content} · ${attachmentLabel}`
      : content;
    return promptSummaryText(combined, maxChars);
  }
  return promptSummaryText(session?.latestPrompt || '', maxChars);
}

function firstPromptSummaryForSession(session) {
  const messages = Array.isArray(session?.messages) ? session.messages : [];
  for (const message of messages) {
    if (message?.role !== 'user') continue;
    const content = String(message.content || '').replace(/\s+/g, ' ').trim();
    const attachments = Array.isArray(message.attachments) ? message.attachments : [];
    const attachmentLabel = attachmentDisplayLabel(attachments);
    if (!content) return promptSummaryText(attachmentLabel);
    return attachmentLabel && content !== attachmentLabel
      ? `${content} · ${attachmentLabel}`
      : content;
  }
  return promptSummaryText(session?.firstPrompt || '');
}

function firstSessionPromptSummary(session) {
  return promptSummaryText(firstPromptSummaryForSession(session), SESSION_PROMPT_SUMMARY_MAX_CHARS);
}

function sessionWorkspacePath(session) {
  return inferSessionWorkspace(session) || String(session?.workspace || '').trim();
}

function sessionWorkspaceSummary(session) {
  const path = sessionWorkspacePath(session);
  return path ? workspaceLabel(path) : '';
}

function sessionDisplayTitle(session) {
  const prompt = firstSessionPromptSummary(session);
  if (prompt) return prompt;
  const title = String(session?.title || '').trim();
  const workspace = sessionWorkspaceSummary(session);
  // 先读默认标题标志（内部含 legacy 会话的一次性启发式判定与写回），
  // 再走旧的文案/工作区兜底。
  if (!title || isDefaultSessionTitle(session) || title === workspace || title === sessionWorkspacePath(session) || title === 'Session' || title === '会话' || title === t('app.sessions.history')) {
    return t('app.sessions.new');
  }
  return promptSummaryText(title, SESSION_PROMPT_SUMMARY_MAX_CHARS);
}

// recountSessionMessages 是 session.messageCount 的唯一重算口径：user + assistant。
// 所有写入点都必须调用它，禁止再内联同口径的 filter 重算。
function recountSessionMessages(session) {
  if (!Array.isArray(session?.messages)) return 0;
  return session.messages.filter(
    (message) => message?.role === 'user' || message?.role === 'assistant'
  ).length;
}

// 会话标题默认态标志（前端内存态，不落盘）：会话创建时 isDefault=true；任何
// 设置自定义标题的位置（第一条用户消息自动命名、/init 等命令命名）设置标题后
// 必须调用 markCustomSessionTitle 清标志。旧会话（从后端索引加载，无标志）
// 保留文案启发式 fallback，判定结果一次性写回标志，避免以"会话"开头的用户
// 自定义标题被永久误判并反复改写。
function markCustomSessionTitle(session) {
  if (!session) return;
  session.isDefault = false;
}

function isDefaultSessionTitle(session) {
  if (!session || typeof session !== 'object') return false;
  if (session.isDefault === true) return true;
  if (session.isDefault === false) return false;
  const isDefault = isLegacyDefaultSessionTitleText(session.title);
  session.isDefault = isDefault;
  return isDefault;
}

function isLegacyDefaultSessionTitleText(title) {
  const value = String(title || '');
  return value === '默认会话'
    || value === 'Default session'
    || value.startsWith('会话')
    || value.startsWith('Session ');
}

function currentWorkspacePath() {
  const tab = workspaceTabs.value.find((item) => item.id === activeWorkspaceId.value);
  return tab ? String(tab.path || '').trim() : String(config.workspace || '').trim();
}

// The session picker is scoped to the workspace currently shown by the active
// Tab. Sessions from other workspaces remain persisted, but cannot be selected
// or displayed from this list.
const currentWorkspaceSessions = computed(() => {
  const workspaceKey = workspaceHistoryDedupeKey(currentWorkspacePath());
  return sessions.value.filter((session) => (
    workspaceHistoryDedupeKey(sessionWorkspacePath(session)) === workspaceKey
  ));
});

// ── Knowledge base mode ──
// The KB is a hidden workspace tab (kind:'kb') that never appears in the
// header tab list. Mode switching only repoints activeWorkspaceId, so every
// per-tab chat mechanism (ChatMessages instance, composer input, session
// menu, plan panel, explorer, footer stats) is reused as-is, and state
// isolation comes from the same per-session keying that separates chat tabs.
const mode = ref('chat'); // 'chat' | 'kb'
const lastChatWorkspaceId = ref(null);

function isKbTab(tab) {
  return tab?.kind === 'kb';
}

// True when a workspace path IS the configured KB root. Used to shape the
// welcome message and KB-specific behavior at build time.
function isKnowledgeBasePath(path) {
  const value = String(path || '').trim();
  if (!value || !config.kbRoot) return false;
  return workspaceHistoryDedupeKey(value) === workspaceHistoryDedupeKey(config.kbRoot);
}

function kbTab() {
  return workspaceTabs.value.find((tab) => isKbTab(tab)) || null;
}

const chatTabsWithStatus = computed(() => workspaceTabsWithStatus.value.filter((tab) => !isKbTab(tab)));

// Settings page state (rendered inline in the main area, see configVisible).
const settingsActive = computed(() => mode.value === 'settings');
// Token-stats page state (inline sibling of the settings page).
const statsActive = computed(() => mode.value === 'stats');
// Games page state (协作休息区, inline sibling of the settings page).
const gamesActive = computed(() => mode.value === 'games');

function closeSettings() {
  if (!settingsActive.value) return;
  switchMode(['chat', 'kb'].includes(preOverlayMode.value) ? preOverlayMode.value : 'chat');
  nextTick(() => focusPromptInput());
}

function closeStats() {
  if (!statsActive.value) return;
  switchMode(['chat', 'kb', 'settings'].includes(preOverlayMode.value) ? preOverlayMode.value : 'chat');
  nextTick(() => focusPromptInput());
}

function closeGames() {
  if (!gamesActive.value) return;
  switchMode(['chat', 'kb', 'settings', 'stats'].includes(preOverlayMode.value) ? preOverlayMode.value : 'chat');
  nextTick(() => focusPromptInput());
}

// KB mode with nothing to show yet: no configured root (or the tab could not
// be created). The chat workbench is hidden and the guidance card takes over.
const kbEmptyActive = computed(() => mode.value === 'kb' && !kbTab());

const kbSessionRunning = computed(() => {
  const tab = kbTab();
  const session = tab ? sessions.value.find((item) => item.id === tab.sessionId) : null;
  return !!(session?.isRunning || session?.runId);
});

// Index-file presence in the KB root. Checked on KB entry and re-checked when
// a KB run finishes (initialization creates index.md), driving the
// "initialize knowledge base" banner.
const kbIndexMissing = ref(false);

async function refreshKbIndexState() {
  const root = String(config.kbRoot || '');
  if (!root || !kbTab()) {
    kbIndexMissing.value = false;
    return;
  }
  try {
    const listing = await ListFiles({ workspace: root, path: '', maxDepth: 1, limit: 500 });
    const entries = Array.isArray(listing?.entries) ? listing.entries : [];
    const names = entries.map((entry) => String(entry?.name || '').toLowerCase());
    kbIndexMissing.value = !names.includes('index.md');
  } catch (err) {
    // Unreadable root: never nag with the banner; the model surfaces the
    // actual error if it cannot work there either.
    kbIndexMissing.value = false;
  }
}

watch(kbSessionRunning, (running) => {
  if (!running) refreshKbIndexState();
});

// True when the active workspace tab is the hidden KB tab: gates the KB
// action bar above the composer.
const activeTabIsKb = computed(() => (
  isKbTab(workspaceTabs.value.find((tab) => tab.id === activeWorkspaceId.value))
));

// Workspace path shown in the composer info bar: the KB tab shows the
// configured KB root instead of the shared chat workspace.
const activeComposerWorkspace = computed(() => {
  const tab = workspaceTabs.value.find((item) => item.id === activeWorkspaceId.value);
  return (isKbTab(tab) ? tab.path : config.workspace) || '';
});

// Canned KB actions on the toolbar above the composer: drop the prompt into
// the KB composer and send it through the normal sendPrompt path (streaming,
// tool cards, plan all work unchanged). Queuing is intentionally not offered
// while a KB run is live — the buttons disable instead.
function runKbAction(promptKey) {
  const session = activeSession.value;
  if (!session || session.isRunning) return;
  promptText.value = t(promptKey);
  // The init button is the one sanctioned send path while the KB has no index
  // yet (the composer is deliberately disabled in that state), so it must opt
  // out of the uninitialized-KB guard inside sendPrompt().
  sendPrompt({ allowKbUninitialized: promptKey === 'kb.init.prompt' });
}

function ensureKbTab() {
  if (!config.kbRoot) return null;
  let tab = kbTab();
  if (tab) {
    if (workspaceHistoryDedupeKey(tab.path) === workspaceHistoryDedupeKey(config.kbRoot)) {
      return tab;
    }
    // KB root changed in Settings: drop the stale tab and rebuild.
    closeWorkspaceTab(tab.id);
    tab = kbTab();
    if (tab) {
      // Close was refused (the KB tab is the only one left): repoint it to
      // the new root with a fresh linked session instead.
      tab.path = config.kbRoot;
      tab.label = t('kb.tabLabel');
      const session = createReplacementSession(t('kb.tabLabel'), config.kbRoot);
      sessions.value.unshift(session);
      tab.sessionId = session.id;
      if (activeWorkspaceId.value === tab.id) activeSessionId.value = session.id;
      return tab;
    }
  }
  tab = createWorkspaceTab(config.kbRoot);
  tab.kind = 'kb';
  tab.label = t('kb.tabLabel');
  workspaceTabs.value.push(tab);
  return tab;
}

async function switchMode(next) {
  if (next === mode.value) return;
  if (next === 'settings' || next === 'stats' || next === 'games') {
    if (mode.value !== 'settings' && mode.value !== 'stats' && mode.value !== 'games') {
      preOverlayMode.value = mode.value;
    }
    mode.value = next;
    return;
  }
  if (mode.value === 'settings' || mode.value === 'stats' || mode.value === 'games') {
    // Sider jump straight from an overlay page to chat/kb.
    mode.value = next;
  }
  if (next === 'kb') {
    mode.value = 'kb';
    const tab = ensureKbTab();
    if (tab && activeWorkspaceId.value !== tab.id) {
      await switchWorkspaceTab(tab.id);
    }
    refreshKbIndexState();
    return;
  }
  mode.value = 'chat';
  const chatTabs = workspaceTabs.value.filter((tab) => !isKbTab(tab));
  const target = chatTabs.find((tab) => tab.id === lastChatWorkspaceId.value)
    || chatTabs[chatTabs.length - 1];
  if (target) {
    if (activeWorkspaceId.value !== target.id) await switchWorkspaceTab(target.id);
    return;
  }
  // No chat tab remains: rebuild one from the persisted chat workspace
  // without a dialog, so a cancelled dialog cannot strand chat mode on the
  // KB tab.
  const tab = createWorkspaceTab(config.workspace || '');
  workspaceTabs.value.push(tab);
  await switchWorkspaceTab(tab.id);
}

const kbPicking = ref(false);

// Empty-state entry: pick a KB root, persist it, and build the KB tab in
// place so the guidance card hands over to the chat workbench directly.
async function pickKbRootFromEmptyState() {
  kbPicking.value = true;
  try {
    const selected = await SelectKnowledgeBaseRoot();
    if (!selected) return;
    config.kbRoot = selected;
    configDraft.kbRoot = selected;
    await saveWorkspaceConfig({ ...config });
    const tab = ensureKbTab();
    if (tab) await switchWorkspaceTab(tab.id);
    refreshKbIndexState();
  } catch (err) {
    message.error(t('kb.empty.pickFailed', { error: err }));
  } finally {
    kbPicking.value = false;
  }
}

// Header interactions (tab click / new tab / workspace history) are
// chat-only by construction, so they always hand control back to chat mode.
function onHeaderSwitchWorkspace(id) {
  mode.value = 'chat';
  switchWorkspaceTab(id);
}

function onHeaderAddWorkspace() {
  mode.value = 'chat';
  addWorkspaceTab();
}

function onHeaderHistorySelect(key) {
  mode.value = 'chat';
  onHistorySelect(key);
}

const latestUserPromptSummary = computed(() => latestPromptSummaryForSession(activeSession.value));
const activeSessionRunning = computed(() => !!activeSession.value?.isRunning);
const scheduledTaskRunningCount = computed(() => scheduledTasks.value.filter((task) => task?.running).length);
const serviceRunningCount = computed(() => services.value.filter((service) => ['starting', 'running'].includes(service?.status)).length);
function todosForSession(sessionId) {
  const entries = sessionId ? todosBySession[sessionId] : null;
  return Array.isArray(entries) ? entries : [];
}

function planEntriesForTab(tab) {
  return todosForSession(tab?.sessionId);
}

function showPlanPanelFor(tab) {
  const entries = planEntriesForTab(tab);
  return entries.length > 0 && entries.some((item) => item?.status !== 'done');
}

function currentPlanNumberFor(tab) {
  const index = planEntriesForTab(tab).findIndex((item) => item?.status === 'in_progress');
  return index >= 0 ? index + 1 : 0;
}

function orderedPlanEntriesFor(tab) {
  return orderPlanPanelEntries(planEntriesForTab(tab));
}

function isPlanPanelCollapsed(tab) {
  const sessionId = String(tab?.sessionId || '');
  return sessionId ? planPanelCollapsedBySession[sessionId] === true : false;
}

function setPlanPanelListRef(tabId, el) {
  if (el) {
    planPanelListRefsByTab.set(tabId, el);
    scrollPlanPanelToFocus(tabId);
  } else {
    planPanelListRefsByTab.delete(tabId);
  }
}

function scrollPlanPanelToFocus(tabId) {
  const tab = workspaceTabs.value.find((item) => item.id === tabId);
  if (!tab || tab.id !== activeWorkspaceId.value || isPlanPanelCollapsed(tab)) return;
  nextTick(() => {
    const list = planPanelListRefsByTab.get(tabId);
    if (!list) return;
    // 面板按原始顺序显示，固定滚到顶部会把进行中任务挤出可视区。
    // 改为把当前 in_progress 项滚动到列表中部；没有进行中项时才回到顶部。
    const current = list.querySelector('.plan-item.in_progress');
    if (current) {
      const delta = planFocusScrollDelta(list.getBoundingClientRect(), current.getBoundingClientRect());
      list.scrollTop += delta;
      return;
    }
    list.scrollTop = 0;
  });
}

function scrollPlanPanelsForSession(sessionId) {
  for (const tab of workspaceTabs.value) {
    if (tab.sessionId === sessionId) scrollPlanPanelToFocus(tab.id);
  }
}

function togglePlanPanel(tab) {
  const sessionId = String(tab?.sessionId || '');
  if (!sessionId) return;
  planPanelCollapsedBySession[sessionId] = !isPlanPanelCollapsed(tab);
  scrollPlanPanelToFocus(tab.id);
}

const MAX_RENDER_MESSAGES = 180;
const MAX_EXPANDED_RENDER_MESSAGES = 360;

function displaySourceMessages(session) {
  return buildDisplaySourceMessages(session, expandedArchiveSessions.value, {
    maxMessages: MAX_RENDER_MESSAGES,
    expandedMaxMessages: MAX_EXPANDED_RENDER_MESSAGES,
  });
}

function toggleArchiveMessages(sessionId) {
  if (!sessionId) return;
  const next = new Set(expandedArchiveSessions.value);
  if (next.has(sessionId)) next.delete(sessionId);
  else next.add(sessionId);
  expandedArchiveSessions.value = next;
  nextTick(() => {
    conversationMessagesForSession(sessionId)?.scrollbarRef?.scrollTo({ top: 0 });
    cleanupDisconnectedMermaidNodes();
    observePendingMermaidDiagrams();
  });
}

// Merge consecutive read tool cards into a single aggregated card.
//
// perf: the display list and read-card merge depend only on message-list shape
// and archive expansion state. A structural signature lets content-only
// streaming updates reuse the cached array without re-running the O(n) merge
// or re-allocating its output.
const displayMessagesCacheBySession = new Map();
function buildDisplayMessagesSignature(session, expanded) {
  const msgs = session?.messages;
  if (!msgs) return '';
  const parts = [`session:${session?.id || ''}`, `len:${msgs.length}`, `exp:${expanded.has(session?.id) ? 1 : 0}`];
  // Only structural fields — no content/body access, so we don't subscribe
  // to streaming content mutations.
  for (let i = 0; i < msgs.length; i++) {
    const m = msgs[i];
    parts.push(`${m.role}:${m.kind || ''}:${m.status || ''}:${m.name || ''}:${m.eventId || ''}`);
  }
  return parts.join('|');
}
function displayMessagesForSession(session) {
  const sessionId = session?.id || '';
  const sig = buildDisplayMessagesSignature(session, expandedArchiveSessions.value);
  const cached = displayMessagesCacheBySession.get(sessionId);
  if (cached?.signature === sig) {
    return cached.messages;
  }
  const src = displaySourceMessages(session);
  const out = [];
  let i = 0;
  while (i < src.length) {
    const m = src[i];
    // Fold consecutive read + grep tool-call cards (errors included) into a
    // single collapsed "已读取 N 次，搜索 M 次" group. Reasoning-only
    // assistant messages BETWEEN tool batches do not break the group (they
    // render above the fold). A trailing reasoning-only message — the model
    // thinking right before its final answer — stays BELOW the fold: once its
    // text streams in it must not shove the fold upward.
    if (m.role === 'tool_call' && (m.kind === 'read' || m.kind === 'grep' || m.kind === 'list')) {
      const skippedThinks = [];
      let pendingThinks = [];
      let j = i + 1;
      while (j < src.length) {
        const n = src[j];
        if (n.role === 'tool_call' && (n.kind === 'read' || n.kind === 'grep' || n.kind === 'list')) {
          // 思考后面又跟了 read/grep：这批思考确实夹在两批工具之间，上提
          skippedThinks.push(...pendingThinks);
          pendingThinks = [];
          j++;
          continue;
        }
        // 思考消息（无正文的 assistant）：先挂起，看后面是否还有工具
        if (n.role === 'assistant' && !String(n.content || '').trim() && !n.error && !n.system) {
          pendingThinks.push(n);
          j++;
          continue;
        }
        break;
      }
      const group = {
        role: 'tool_call',
        kind: 'read-grep-group',
        status: 'success',
        time: m.time,
        eventId: m.eventId,
        readEntries: [],
        grepItems: [],
        listItems: [],
        readCount: 0,
        grepCount: 0,
        listCount: 0,
        durationMs: 0,
        durationText: '',
      };
      let totalLines = 0;
      let totalHits = 0;
      let totalItems = 0;
      let allDone = true;
      let hasError = false;
      while (i < j) {
        const entry = src[i];
        if (entry.role !== 'tool_call') { i++; continue; }
        if (entry.kind === 'grep') {
          group.grepCount++;
          // 命中数优先取工具结果落下的结构化 stats（B3：{hits, files}），
          // 历史消息没有 stats 字段时回退到 grep chip（"· N hits"）正则。
          const hitsFromStats = entry.stats ? Number(entry.stats.hits || 0) : null;
          if (hitsFromStats !== null) {
            totalHits += hitsFromStats;
          } else {
            const hitsMatch = String(entry.chip || '').match(/(\d+)\s*hits?/i);
            totalHits += hitsMatch ? Number(hitsMatch[1]) : 0;
          }
          group.grepItems.push({
            title: entry.title || '',
            chip: entry.chip || '',
            status: entry.status,
            body: entry.body || '',
          });
        } else if (entry.kind === 'list') {
          group.listCount++;
          // 条目数优先取结构化 stats.items（B3：list_files 结果 entries/
          // count），历史消息没有 stats 时回退到 list chip（"· N items"）正则。
          const itemsFromStats = entry.stats ? Number(entry.stats.items || 0) : null;
          if (itemsFromStats !== null) {
            totalItems += itemsFromStats;
          } else {
            const itemsMatch = String(entry.chip || '').match(/(\d+)\s*items?/i);
            totalItems += itemsMatch ? Number(itemsMatch[1]) : 0;
          }
          group.listItems.push({
            title: entry.title || '',
            chip: entry.chip || '',
            status: entry.status,
            body: entry.body || '',
          });
        } else if ((entry.name === 'read' || entry.name === 'batch_read') && entry.batchEntries && entry.batchEntries.length > 0) {
          for (const be of entry.batchEntries) {
            const entryStatus = be.status || entry.status;
            if (entryStatus === 'error') hasError = true;
            group.readEntries.push({ title: be.title, chip: be.chip, lineCount: be.lineCount || 0, totalLines: be.totalLines || be.lineCount || 0, startLine: be.startLine || 1, endLine: be.endLine || be.totalLines || 0, truncated: !!be.truncated, body: '', status: entryStatus, expanded: false });
            totalLines += be.lineCount || 0;
          }
          group.readCount++;
        } else {
          group.readEntries.push({
            title: entry.title || '',
            chip: entry.chip || '',
            lineCount: entry.readLineCount || 0,
            totalLines: entry.readTotalLines || 0,
            body: entry.body || '',
            status: entry.status,
            expanded: false,
          });
          totalLines += entry.readLineCount || 0;
          group.readCount++;
        }
        if (entry.status === 'running') allDone = false;
        if (entry.status === 'error') hasError = true;
        if ((entry.durationMs || 0) > group.durationMs) {
          group.durationMs = entry.durationMs || 0;
          group.durationText = entry.durationText || formatDurationShort(entry.durationMs);
        }
        i++;
      }
      group.status = hasError ? 'error' : (allDone ? 'success' : 'running');
      group.readTotalLines = totalLines;
      group.grepTotalHits = totalHits;
      group.listTotalItems = totalItems;
      // 夹在工具批次之间的思考上提到折叠组上方（原顺序）；
      // 尾随的思考保持在折叠组下方（它多半是正在流式的最终回答，
      // 一旦有正文就不能把折叠往下顶）。
      out.push(...skippedThinks);
      out.push(group);
      out.push(...pendingThinks);
    } else {
      out.push(m);
      i++;
    }
  }
  displayMessagesCacheBySession.set(sessionId, { signature: sig, messages: out });
  return out;
}

function displayMessagesForTab(tab) {
  return displayMessagesForSession(sessionForWorkspaceTab(tab));
}

const canSend = computed(() => {
  const s = activeSession.value;
  return (promptText.value.trim().length > 0 || activePendingAttachments.value.length > 0) && !(s && (s.runId || s.isRunning));
});

const contextTokens = ref(0);
const contextBreakdown = ref(null);
const workspaceTokenUsage = ref({ inputTokens: 0, outputTokens: 0 });
const gitStatus = ref({ isRepo: false });
const gitDiffVisible = ref(false);
const footerStatsLoading = ref(true);
let footerStatsRequestVersion = 0;
let contextRequestVersion = 0;
let gitStatusRequestVersion = 0;
let workspaceConfigSaveQueue = Promise.resolve();
let workspaceSwitchVersion = 0;

function emptyGitStatus() {
  return { isRepo: false };
}

function emptyWorkspaceTokenUsage() {
  return { inputTokens: 0, outputTokens: 0 };
}

function clearFooterStats() {
  footerStatsRequestVersion += 1;
  contextRequestVersion += 1;
  footerStatsLoading.value = true;
  gitStatus.value = emptyGitStatus();
  workspaceTokenUsage.value = emptyWorkspaceTokenUsage();
  contextTokens.value = 0;
  contextBreakdown.value = null;
}

function isCurrentFooterTarget(tabId, sessionId, workspace) {
  return (
    tabId === activeWorkspaceId.value
    && sessionId === activeSessionId.value
    && String(workspace || '') === String(config.workspace || '')
  );
}

// Per-tab snapshot of the footer stats so switching back to a recently visited
// tab shows the last result instantly while a fresh fetch refreshes it in the
// background. Keyed by tab id; each entry remembers the workspace it belongs to
// so a tab whose workspace changed doesn't display another workspace's numbers.
const footerStatsCache = new Map();
const FOOTER_STATS_CACHE_LIMIT = 16;

function captureFooterSnapshot(tabId, workspace) {
  if (!tabId) return;
  footerStatsCache.set(tabId, {
    workspace: String(workspace || ''),
    gitStatus: gitStatus.value,
    workspaceTokenUsage: { ...workspaceTokenUsage.value },
    contextTokens: contextTokens.value,
    contextBreakdown: contextBreakdown.value,
  });
  if (footerStatsCache.size > FOOTER_STATS_CACHE_LIMIT) {
    footerStatsCache.delete(footerStatsCache.keys().next().value);
  }
}

function applyFooterSnapshot(tabId, workspace) {
  const snap = footerStatsCache.get(tabId);
  if (!snap || String(snap.workspace || '') !== String(workspace || '')) return false;
  gitStatus.value = snap.gitStatus;
  workspaceTokenUsage.value = { ...snap.workspaceTokenUsage };
  contextTokens.value = snap.contextTokens;
  contextBreakdown.value = snap.contextBreakdown;
  return true;
}

// Show the cached snapshot for the target tab instantly when it matches the
// workspace, otherwise fall back to the loading placeholder. The caller still
// kicks off refreshFooterStats afterwards to refresh the numbers in the
// background.
function prepareFooterStatsForTarget(tabId, workspace) {
  if (applyFooterSnapshot(tabId, workspace)) {
    footerStatsLoading.value = false;
  } else {
    clearFooterStats();
  }
}

function saveWorkspaceConfig(snapshot) {
  const save = workspaceConfigSaveQueue
    .catch(() => {})
    .then(() => SaveConfig(snapshot));
  workspaceConfigSaveQueue = save.catch(() => {});
  return save;
}

async function refreshFooterStats({
  tabId = activeWorkspaceId.value,
  sessionId = activeSessionId.value,
  workspace = config.workspace || '',
} = {}) {
  const requestedWorkspace = String(workspace || '');
  const requestedSessionId = String(sessionId || '');
  const requestVersion = ++footerStatsRequestVersion;
  const requestedContextVersion = ++contextRequestVersion;

  if (!requestedWorkspace || !requestedSessionId) {
    if (
      requestVersion === footerStatsRequestVersion
      && requestedContextVersion === contextRequestVersion
      && isCurrentFooterTarget(tabId, requestedSessionId, requestedWorkspace)
    ) {
      footerStatsLoading.value = false;
    }
    return;
  }

  // Only the breakdown is fetched: it already carries `total`, so a separate
  // GetSessionContextTokens call would just recompute the same payload on the
  // backend and double the IPC cost on every footer refresh.
  const [gitResult, usageResult, breakdownResult] = await Promise.allSettled([
    // SaveConfig has completed before this function is called, so this uses
    // the backend's new active workspace.
    GetGitStatus(),
    GetWorkspaceTokenUsage(requestedWorkspace),
    GetContextBreakdown(requestedSessionId),
  ]);

  if (
    requestVersion !== footerStatsRequestVersion
    || requestedContextVersion !== contextRequestVersion
    || !isCurrentFooterTarget(tabId, requestedSessionId, requestedWorkspace)
  ) return;

  gitStatus.value = gitResult.status === 'fulfilled' ? (gitResult.value || emptyGitStatus()) : emptyGitStatus();
  workspaceTokenUsage.value = usageResult.status === 'fulfilled'
    ? (usageResult.value || emptyWorkspaceTokenUsage())
    : emptyWorkspaceTokenUsage();

  const breakdown = breakdownResult.status === 'fulfilled' ? (breakdownResult.value || null) : null;
  const contextValue = Number(breakdown?.total) || 0;
  contextTokens.value = contextValue;
  contextBreakdown.value = breakdown;

  const session = sessions.value.find((item) => item.id === requestedSessionId);
  if (session) {
  }
  footerStatsLoading.value = false;
  captureFooterSnapshot(tabId, requestedWorkspace);
}

async function refreshGitStatus() {
  const tabId = activeWorkspaceId.value;
  const sessionId = activeSessionId.value;
  const workspace = String(config.workspace || '');
  if (!workspace || footerStatsLoading.value) return;
  const requestVersion = ++gitStatusRequestVersion;
  try {
    const status = await GetGitStatus();
    if (
      requestVersion !== gitStatusRequestVersion
      || !isCurrentFooterTarget(tabId, sessionId, workspace)
      || footerStatsLoading.value
    ) return;
    gitStatus.value = status || emptyGitStatus();
    captureFooterSnapshot(tabId, workspace);
  } catch (_) {
    if (
      requestVersion === gitStatusRequestVersion
      && isCurrentFooterTarget(tabId, sessionId, workspace)
      && !footerStatsLoading.value
    ) {
      gitStatus.value = emptyGitStatus();
    }
  }
}

function openGitDiff() {
  gitDiffVisible.value = true;
}

async function openWorkspaceInFileManager() {
  try {
    // On the KB tab the button shows the KB root, so open that directory —
    // not the persisted chat workspace the backend helper defaults to.
    if (activeTabIsKb.value && activeComposerWorkspace.value) {
      await OpenWorkspacePathInFileManagerAt({ workspace: activeComposerWorkspace.value, path: '' });
    } else {
      await OpenWorkspaceInFileManager();
    }
  } catch (err) {
    message.warning(t('app.workspace.openFailed', { error: err }));
  }
}

function toggleWorkspaceExplorer() {
  if (!config.workspace) {
    message.info(t('app.workspace.required'));
    return;
  }
  const tabId = activeWorkspaceId.value;
  const workspace = explorerTabPath(tabId);
  if (explorerVisibleFor(tabId)) {
    const explorer = explorerRefsByTab.get(tabId);
    if (explorer) void explorer.requestClose();
    else closeExplorerForTab(tabId);
    return;
  }
  explorerWorkspaceByTab.set(tabId, workspace);
  workspaceExplorerByTab.set(tabId, true);
  setExplorerClosedForPath(workspace, false);
}

// Context computation — call backend for accurate full-payload token count.
// React to session switches and tool completions; in-run token refresh is
// handled by tool:result / tool:error / run:done / run:error / run:cancelled /
// run:compacted handlers. A previous deep:true watch on activeMessages fired
// on every streaming delta (40+ IPC/s) — wasteful. Only the active session is
// refreshed because the backend calculates context data from its active config.
let refreshContextTimer = null;
let refreshContextInFlight = false;
let refreshContextPendingSid = null;

function refreshContextTokens(sid) {
  if (!sid || sid !== activeSessionId.value) return;
  // Debounce: collapse calls within 120ms into one request. Tool batches
  // (4 tools → 4 tool:result events) arrive in quick succession; without
  // this each event triggers a separate GetContextBreakdown IPC call.
  if (refreshContextTimer) clearTimeout(refreshContextTimer);
  refreshContextTimer = setTimeout(() => {
    refreshContextTimer = null;
    if (refreshContextInFlight || footerStatsLoading.value) {
      // A request is already in flight — remember the sid and trigger a
      // trailing refresh once it completes, so the latest context is shown.
      refreshContextPendingSid = sid;
      return;
    }
    doRefreshContextTokens(sid);
  }, 120);
}

function doRefreshContextTokens(sid) {
  if (!sid || sid !== activeSessionId.value || footerStatsLoading.value) return;
  refreshContextInFlight = true;
  const requestVersion = ++contextRequestVersion;
  // The breakdown already carries `total`, so a separate GetSessionContextTokens
  // call would duplicate the backend computation on every tool:result burst.
  GetContextBreakdown(sid).then((breakdown) => {
    refreshContextInFlight = false;
    // If another refresh was requested while this one was in flight, trigger
    // it now so the footer reflects the latest tool results.
    const pendingSid = refreshContextPendingSid;
    refreshContextPendingSid = null;
    if (pendingSid && pendingSid === activeSessionId.value && !footerStatsLoading.value) {
      refreshContextTokens(pendingSid);
    }
    if (
      requestVersion !== contextRequestVersion
      || sid !== activeSessionId.value
      || footerStatsLoading.value
    ) return;

    const value = Number(breakdown?.total) || 0;
    const session = sessions.value.find((item) => item.id === sid);
    if (session) {
    }
    contextTokens.value = value;
    contextBreakdown.value = breakdown || null;
    captureFooterSnapshot(activeWorkspaceId.value, config.workspace || '');
  }).catch(() => { refreshContextInFlight = false; });
}

watch(activeSessionId, (sid) => {
  if (sid && config.workspace && !footerStatsLoading.value) refreshContextTokens(sid);
});

watch(activeSessionId, () => {
  // 切换会话时清掉重试横幅:重试横幅只反映当前可见会话的瞬时状态。
  retryBanner.value = null;
  nextTick(() => {
    cleanupDisconnectedMermaidNodes();
    observePendingMermaidDiagrams();
  });
});

watch(() => config.workspace, () => {
  closeFileMentionMenu();
});


const contextUsed = computed(() => fmtCompact(contextTokens.value));
const contextWindow = computed(() => (modelByTab[activeWorkspaceId.value] || config).contextWindow || 1000000);
const contextMax = computed(() => fmtCompact(contextWindow.value));
const contextPercent = computed(() => {
  const used = contextTokens.value;
  const max = contextWindow.value;
  const pct = max > 0 ? (used / max * 100) : 0;
  return Math.round(pct) + '%';
});

const contextPct = computed(() => {
  const used = contextTokens.value;
  const max = contextWindow.value;
  return max > 0 ? (used / max * 100) : 0;
});
const contextUsageStyle = computed(() => {
  const pct = Math.max(0, Math.min(100, contextPct.value));
  // Only the hue is semantic (green → amber → red as the window fills). The
  // old constant saturation/lightness meant a session at 0% usage rendered a
  // fully saturated #76daa0 green in the footer — the brightest pixels in the
  // chrome, for information that says "nothing to see yet". Now chroma scales
  // with urgency: the calm end is a muted sage that sits at the footer's own
  // grey level, and the alert end comes out *more* saturated than before, so
  // the one case that should pull the eye pulls harder.
  let hue;
  if (pct <= 20) {
    hue = 145 - (pct / 20) * 45;
  } else if (pct <= 40) {
    hue = 100 - ((pct - 20) / 20) * 52;
  } else {
    hue = 48 - ((pct - 40) / 60) * 42;
  }
  const urgency = pct / 100;
  const saturation = Math.round(16 + urgency * 54);
  const lightness = Math.round(60 - urgency * 4);
  return { color: `hsl(${Math.round(hue)} ${saturation}% ${lightness}%)` };
});

const workspaceInputTokens = computed(() => fmtTokenUnit(workspaceTokenUsage.value?.inputTokens || 0));
const workspaceOutputTokens = computed(() => fmtTokenUnit(workspaceTokenUsage.value?.outputTokens || 0));

// Workspace history
function loadWorkspaceHistory() {
  try {
    const raw = localStorage.getItem('agent_workspace_history');
    return dedupeWorkspaceHistory(raw ? JSON.parse(raw) : []);
  } catch (_) {
    return [];
  }
}

function workspaceHistoryDedupeKey(path) {
  const original = String(path || '').trim();
  let value = original.replace(/\\/g, '/').replace(/\/{2,}/g, '/');
  if (value.length > 1 && !/^[a-z]:\/$/i.test(value)) value = value.replace(/\/+$/, '');
  const isWindowsPath = /^[a-z]:\//i.test(value) || /^(?:\\\\|\/\/)/.test(original);
  return isWindowsPath ? value.toLowerCase() : value;
}

function dedupeWorkspaceHistory(paths, limit = 50) {
  const source = Array.isArray(paths) ? paths : [];
  const seen = new Set();
  const result = [];
  for (let index = source.length - 1; index >= 0; index--) {
    const path = String(source[index] || '').trim();
    const key = workspaceHistoryDedupeKey(path);
    if (!key || seen.has(key)) continue;
    seen.add(key);
    result.push(path);
    if (result.length >= limit) break;
  }
  return result.reverse();
}

function saveWorkspaceHistory() {
  try {
    const list = dedupeWorkspaceHistory(workspaceHistory.value);
    workspaceHistory.value = list;
    localStorage.setItem('agent_workspace_history', JSON.stringify(list));
  } catch (_) { /* ignore */ }
}

function addToHistory(path) {
  if (!path) return;
  workspaceHistory.value = dedupeWorkspaceHistory([...workspaceHistory.value, path]);
  saveWorkspaceHistory();
}

function removeFromHistory(path) {
  if (!path) return;
  const key = workspaceHistoryDedupeKey(path);
  workspaceHistory.value = workspaceHistory.value.filter((p) => workspaceHistoryDedupeKey(p) !== key);
  saveWorkspaceHistory();
}

// ── 会话级附加工作区（extraRoots）管理 ──────────────────────────
// extraRoots 是 session 维度的临时白名单，让安全围栏允许写入主工作区之外的目录。
// 持久化在 session 对象里，切回历史会话时自动还原。
async function addExtraRoot() {
  const path = await SelectWorkspace();
  if (!path) return;
  const session = activeSession.value;
  if (!session) return;
  // 主工作区无需添加
  if (config.workspace && workspaceHistoryDedupeKey(path) === workspaceHistoryDedupeKey(config.workspace)) {
    message.warning(t('extraRoots.duplicatePrimary'));
    return;
  }
  if (!Array.isArray(session.extraRoots)) session.extraRoots = [];
  // 去重
  const key = workspaceHistoryDedupeKey(path);
  if (session.extraRoots.some((p) => workspaceHistoryDedupeKey(p) === key)) {
    message.warning(t('extraRoots.duplicate'));
    return;
  }
  session.extraRoots.push(path);
  extraRoots.value = [...session.extraRoots];
  saveSessions();
}

function removeExtraRoot(path) {
  if (!path) return;
  const session = activeSession.value;
  if (!session || !Array.isArray(session.extraRoots)) return;
  const key = workspaceHistoryDedupeKey(path);
  session.extraRoots = session.extraRoots.filter((p) => workspaceHistoryDedupeKey(p) !== key);
  extraRoots.value = [...session.extraRoots];
  saveSessions();
}

function workspaceHistoryKey(path = config.workspace || '') {
  return `ally_prompt_history:${path || '__none__'}`;
}

function loadPromptHistory(path = config.workspace || '') {
  try {
    const raw = localStorage.getItem(workspaceHistoryKey(path));
    commandHistory.value = raw ? JSON.parse(raw).slice(-50) : [];
  } catch (_) {
    commandHistory.value = [];
  }
  commandHistoryIndex.value = -1;
}

function savePromptHistory(path = config.workspace || '') {
  try {
    localStorage.setItem(workspaceHistoryKey(path), JSON.stringify(commandHistory.value.slice(-50)));
  } catch (_) { /* ignore */ }
}

function addPromptHistory(text) {
  const value = (text || '').trim();
  if (!value) return;
  commandHistory.value = commandHistory.value.filter((item) => item !== value);
  commandHistory.value.push(value);
  commandHistory.value = commandHistory.value.slice(-50);
  savePromptHistory();
}

const historyOptions = computed(() => {
  const recent = [...workspaceHistory.value].reverse().slice(0, 50);
  return [
  {
    key: '__add__',
    props: {
      class: 'add-workspace-option',
    },
    label: () =>
      h(
        NButton,
        {
          size: 'small',
          type: 'default',
          block: true,
          style: { color: 'var(--ally-accent)', textAlign: 'left', justifyContent: 'flex-start', border: 'none', background: 'transparent' },
        },
        {
          default: () => [
            h('span', { class: 'add-label', style: { justifyContent: 'flex-start', width: '100%', display: 'flex', alignItems: 'center', gap: '6px' } }, [
              h('span', '+'),
              h(PlusOutlined, { class: 'add-icon', style: { fontSize: '12px' } }),
              t('header.addWorkspace'),
            ]),
          ],
        },
      ),
  },
  ...(recent.length === 0
    ? [{ label: t('app.history.empty'), disabled: true, key: '__empty__' }]
    : recent.map((path) => {
        const label = path.split(/[/\\]/).filter(Boolean).pop() || path;
        return {
          label: () =>
            h('span', { class: 'hist-label' }, [
              h('span', { class: 'hist-name' }, label),
              h('span', { class: 'hist-path' }, `  —  ${path}`),
              h('span', {
                class: 'hist-del',
                role: 'button',
                tabindex: 0,
                title: t('app.history.remove'),
                'aria-label': t('app.history.remove'),
                onClick: (e) => {
                  e.stopPropagation();
                  removeFromHistory(path);
                },
                onKeydown: (e) => {
                  if (e.key !== 'Enter' && e.key !== ' ') return;
                  e.preventDefault();
                  e.stopPropagation();
                  removeFromHistory(path);
                },
              }, h(CloseOutlined)),
            ]),
          key: path,
        };
      })),
  ];
});

function onHistorySelect(key) {
  if (!key || key === '__empty__') return;
  const tab = createWorkspaceTab(key);
  workspaceTabs.value.push(tab);
  switchWorkspaceTab(tab.id);
}

function welcomeGreeting() {
  return localizedWelcomeGreeting();
}

function buildWelcomeMessage(workspacePath = '') {
  // Knowledge-base sessions skip the avatar / info table / greeting chrome:
  // the KB page has its own identity header (kb-hero), so the welcome stays
  // a single plain line.
  if (isKnowledgeBasePath(workspacePath)) {
    const welcome = {
      title: t('kb.hero.title'),
      rows: [],
      greeting: t('kb.welcome.greeting'),
      kb: true,
    };
    return {
      role: 'assistant',
      content: t('kb.welcome.greeting'),
      system: true,
      welcome,
    };
  }
  const skillCount = availableSkills.value.length;
  const rows = [];
  if (workspacePath !== null) {
    rows.push({ kind: 'workspace', label: t('common.workspace'), value: workspacePath || t('common.notSelected') });
  }
  const gitBashPath = String(config.gitBashPath || '').trim();
  if (gitBashPath) {
    rows.push({ kind: 'gitbash', label: t('welcome.gitBash'), value: gitBashPath });
  }
  // The info table shows the model the session will actually chat with:
  // the active Tab's model (fall back to the default before initialization).
  const activeModel = modelByTab[activeWorkspaceId.value] || config;
  rows.push({ kind: 'model', label: t('common.model'), value: `${activeModel.providerName || '-'} · ${activeModel.model || '-'}` });
  rows.push({ kind: 'mcp', label: 'MCP', value: formatMcpSummary() });
  if (skillCount > 0) {
    rows.push({ kind: 'skills', label: t('common.skills'), value: t('welcome.skillsAvailable', { count: skillCount }) });
  }

  const title = 'Ally';
  const greeting = welcomeGreeting();
  const welcome = { title, rows, greeting };
  return {
    role: 'assistant',
    content: buildWelcomeContent(welcome),
    system: true,
    welcome,
  };
}

function formatMcpSummary() {
  const servers = Array.isArray(mcpServers.value) ? mcpServers.value : [];
  const connected = servers.filter((srv) => srv.status === 'connected').length;
  const tools = servers.reduce((sum, srv) => sum + (Number(srv.toolCount) || 0), 0);
  if (servers.length === 0) return t('app.mcp.noServices');
  if (connected === 0) return t('app.mcp.disconnected', { count: servers.length });
  return t('app.mcp.summary', { connected, count: servers.length, tools });
}

function updateWelcomeMcpRows() {
  const value = formatMcpSummary();
  const gitBashPath = String(config.gitBashPath || '').trim();
  for (const session of sessions.value) {
    for (const msg of session.messages || []) {
      if (!msg.welcome || !Array.isArray(msg.welcome.rows)) continue;
      const rows = msg.welcome.rows.filter((row) => row.kind !== 'commands' && row.label !== '指令' && row.label !== 'Commands' && row.kind !== 'gitbash');
      if (gitBashPath) {
        const workspaceIndex = rows.findIndex((row) => row.kind === 'workspace' || row.label === '工作区' || row.label === 'Workspace');
        rows.splice(workspaceIndex >= 0 ? workspaceIndex + 1 : 0, 0, { kind: 'gitbash', label: t('welcome.gitBash'), value: gitBashPath });
      }
      const existing = rows.find((row) => row.kind === 'mcp' || row.label === 'MCP');
      if (existing) existing.value = value;
      else {
        const modelIndex = rows.findIndex((row) => row.kind === 'model' || row.label === '模型' || row.label === 'Model');
        rows.splice(modelIndex >= 0 ? modelIndex + 1 : rows.length, 0, { kind: 'mcp', label: 'MCP', value });
      }
      // 只更新 rows：欢迎表格由 WelcomeMessage 组件按 rows 实时渲染，
      // msg.content（首次构建时的 Markdown 快照）保持不变——本函数只读。
      msg.welcome.rows = rows;
    }
  }
}

function buildWelcomeContent(welcome) {
  const table = (welcome.rows || []).map((row) => `| ${row.label} | ${row.value} |`).join('\n');
  return `${welcome.title || 'Ally'}\n\n| ${t('common.project')} | ${t('common.info')} |\n|------|------|\n${table}\n\n${welcome.greeting || ''}`;
}

function workspaceLabel(path) {
  return path ? (path.split(/[/\\]/).filter(Boolean).pop() || path) : t('app.workspace.none');
}

function inferSessionWorkspace(session) {
  if (!session) return '';
  // 常规路径：直接读会话字段（创建会话/切 Tab 绑定/加载会话索引时写入）。
  if (session.workspace) return session.workspace;
  // 仅旧会话缺字段时的一次性推断：从欢迎消息表格反解工作区路径并写回
  // session.workspace，之后永远读字段，不再每次扫描消息数组（B1）。
  for (const msg of session.messages || []) {
    const rows = msg?.welcome?.rows;
    if (!Array.isArray(rows)) continue;
    const row = rows.find((item) => item?.kind === 'workspace' || item?.label === '工作区' || item?.label === 'Workspace');
    const value = String(row?.value || '').trim();
    if (value && value !== '未选择' && value !== 'Not selected') {
      session.workspace = value;
      return value;
    }
  }
  return '';
}

function bindSessionToActiveWorkspaceTab(session) {
  if (!session) return null;
  const tab = workspaceTabs.value.find((item) => item.id === activeWorkspaceId.value) || null;
  if (tab) tab.sessionId = session.id;
  return tab;
}

async function applySessionWorkspace(session) {
  if (!session) return false;
  const workspace = inferSessionWorkspace(session);
  if (workspace) session.workspace = workspace;

  // 恢复会话级 extraRoots（历史会话切回时从 session 字段还原）
  if (Array.isArray(session.extraRoots)) {
    extraRoots.value = [...session.extraRoots];
  } else {
    session.extraRoots = [];
    extraRoots.value = [];
  }

  const tab = bindSessionToActiveWorkspaceTab(session);
  const tabIsKb = isKbTab(tab);
  if (tab && !tabIsKb) {
    if (workspace) {
      tab.path = workspace;
      tab.label = workspaceLabel(workspace);
    }
  }

  // Session/Tab linkage is valid even before a workspace is selected. Only
  // workspace-dependent refreshes need to wait for a non-empty path.
  if (!workspace) {
    clearFooterStats();
    footerStatsLoading.value = false;
    return true;
  }

  prepareFooterStatsForTarget(activeWorkspaceId.value, workspace);
  // KB sessions never claim the persisted chat workspace or the workspace
  // history list: they live entirely under the configured KB root.
  if (!tabIsKb) {
    config.workspace = workspace;
    configDraft.workspace = workspace;
    addToHistory(workspace);
  }
  loadPromptHistory(workspace);
  try {
    if (!tabIsKb) await saveWorkspaceConfig({ ...config });
    await refreshFooterStats({
      tabId: activeWorkspaceId.value,
      sessionId: session.id,
      workspace,
    });
  } catch (err) {
    if (isCurrentFooterTarget(activeWorkspaceId.value, session.id, workspace)) {
      footerStatsLoading.value = false;
      message.error(t('app.config.saveFailed', { error: err }));
    }
  }
  return true;
}

async function loadSessionMessages(session) {
  if (!session || session.messagesLoaded) return session;
  if (session.messagesLoading) return session.messagesLoading;
  session.messagesLoading = (async () => {
    try {
      const snapshot = await LoadSession(session.id);
      const messages = Array.isArray(snapshot?.messages) ? snapshot.messages : [];
      session.title = snapshot.title || session.title;
      session.firstPrompt = snapshot.firstPrompt || session.firstPrompt || '';
      session.workspace = snapshot.workspace || session.workspace || '';
      session.extraRoots = Array.isArray(snapshot.extraRoots) ? [...snapshot.extraRoots] : (session.extraRoots || []);
      session.createdAt = snapshot.createdAt || session.createdAt || Date.now();
      session.updatedAt = snapshot.updatedAt || session.updatedAt || session.createdAt;
      displayMessagesCacheBySession.delete(session.id);
      session.messages = messages.map((item) => ({
        ...item,
        streaming: false,
        done: item.role === 'assistant' ? true : !!item.done,
      }));
      session.messagesLoaded = true;
      session.hasSnapshot = snapshot.hasSnapshot !== false;
      if (messages.length === 0 && !session.hasSnapshot) {
        session.messages.push(buildWelcomeMessage(session.workspace || ''));
      }
    } catch (err) {
      message.error(t('app.sessions.loadFailed', { error: err }));
    } finally {
      session.messagesLoading = null;
    }
    return session;
  })();
  await session.messagesLoading;
  return session;
}

function ensureWorkspaceTabSession(tab) {
  if (!tab) return null;
  const existing = tab.sessionId
    ? sessions.value.find((session) => session.id === tab.sessionId) || null
    : null;
  if (existing) return existing;

  // If the active Tab has a stale link but the UI still has a valid active
  // session, preserve what the user is viewing and repair the link in place.
  if (tab.id === activeWorkspaceId.value && activeSession.value) {
    tab.sessionId = activeSession.value.id;
    return activeSession.value;
  }

  const replacement = createReplacementSession(tab.label || t('app.sessions.new'), tab.path || '');
  sessions.value.unshift(replacement);
  tab.sessionId = replacement.id;
  if (tab.id === activeWorkspaceId.value) {
    activeSessionId.value = replacement.id;
  }
  return replacement;
}

async function activateSelectedSession(target) {
  if (!target) return false;
  const currentWorkspaceKey = workspaceHistoryDedupeKey(currentWorkspacePath());
  if (workspaceHistoryDedupeKey(sessionWorkspacePath(target)) !== currentWorkspaceKey) return false;
  const currentTab = workspaceTabs.value.find((tab) => tab.id === activeWorkspaceId.value) || null;
  const linkedTab = currentTab?.sessionId === target.id
    ? currentTab
    : findSessionWorkspaceTab(workspaceTabs.value, target.id);
  if (linkedTab && linkedTab.id !== activeWorkspaceId.value) {
    await switchWorkspaceTab(linkedTab.id);
    return true;
  }

  activeSessionId.value = target.id;
  await applySessionWorkspace(target);
  await loadSessionMessages(target);
  unloadInactiveSessionMessages();
  loadTodos(target.id);
  restoreMessagesToBottom(target.id);
  return true;
}

function createWorkspaceTab(path) {
  const id = crypto.randomUUID ? crypto.randomUUID() : `ws-${Date.now()}-${Math.random()}`;
  const label = workspaceLabel(path);
  // Create a linked session for this tab
  const sessionId = crypto.randomUUID ? crypto.randomUUID() : `s-${Date.now()}-${Math.random()}`;
  const now = Date.now();
  const session = { id: sessionId, title: label, workspace: path || '', extraRoots: [], messages: [], messagesLoaded: true, runId: '', isRunning: false, createdAt: now, updatedAt: now };
  session.messages.push(buildWelcomeMessage(path || t('common.notSelected')));
  sessions.value.unshift(session);
  // Reset cumulative token usage for this workspace (new workspace = fresh counter)
  if (path) {
    ResetWorkspaceTokenUsage(path).catch(() => {});
  }
  return { id, path: path || '', label, sessionId };
}

function addWorkspaceTab() {
  chooseWorkspace().then((workspacePath) => {
    if (!workspacePath) return;
    addToHistory(workspacePath);
    const tab = createWorkspaceTab(workspacePath);
    workspaceTabs.value.push(tab);
    switchWorkspaceTab(tab.id);
  });
}

function reorderWorkspaceTabs({ sourceId, targetId, after = false } = {}) {
  const sourceIndex = workspaceTabs.value.findIndex((tab) => tab.id === sourceId);
  if (sourceIndex === -1 || sourceId === targetId) return;

  const [movedTab] = workspaceTabs.value.splice(sourceIndex, 1);
  const targetIndex = workspaceTabs.value.findIndex((tab) => tab.id === targetId);
  if (targetIndex === -1) {
    workspaceTabs.value.splice(sourceIndex, 0, movedTab);
    return;
  }
  workspaceTabs.value.splice(targetIndex + (after ? 1 : 0), 0, movedTab);
}

async function closeWorkspaceTab(id) {
  if (workspaceTabs.value.length <= 1) return;
  const idx = workspaceTabs.value.findIndex((t) => t.id === id);
  if (idx === -1) return;
  // Explorer 内存状态随 Tab 一起释放（未保存草稿直接丢弃）；持久化按工作区路径
  // 记录，与 Tab 生命周期无关，无需在此清理。
  workspaceExplorerByTab.delete(id);
  explorerWorkspaceByTab.delete(id);
  explorerRefsByTab.delete(id);
  explorerTreeWidthByTab.delete(id);
  // 释放该 Tab 的模型快照，避免 localStorage 条目随关闭的 Tab 无限累积。
  if (modelByTab[id]) {
    delete modelByTab[id];
    saveModelByTab();
  }
  const tab = workspaceTabs.value[idx];
  if (tab?.sessionId) delete planPanelCollapsedBySession[tab.sessionId];
  planPanelListRefsByTab.delete(id);
  workspaceTabs.value.splice(idx, 1);
  conversationMessagesRefs.delete(id);
  // Release the linked session's backend resources but keep it in the session
  // list so it remains accessible via /sessions. The session's workspace is
  // preserved via session.workspace / inferSessionWorkspace.
  if (tab && tab.sessionId) {
    const linkedSession = sessions.value.find(s => s.id === tab.sessionId) || null;
    // A closed Tab does not cancel its background run. Keep all session-local
    // UI/backend state until that run actually finishes.
    if (!sessionMayHaveBackgroundRun(linkedSession)) {
      releaseSessionAttachments(linkedSession);
      delete sessionPromptTexts[tab.sessionId];
      deletePendingAttachments(tab.sessionId);
      delete todosBySession[tab.sessionId];
      delete todoRevisionsBySession[tab.sessionId];
      displayMessagesCacheBySession.delete(tab.sessionId);
      ReleaseSession(tab.sessionId).catch(() => {});
    }
  }
  saveSessions();
  if (activeWorkspaceId.value === id) {
    const remaining = workspaceTabs.value;
    // Prefer handing control back to a chat tab; only fall back to the KB
    // tab (flipping mode with it) when no chat tab remains.
    const fallback = remaining.find((tab) => !isKbTab(tab))
      || remaining[Math.min(idx, remaining.length - 1)];
    if (fallback) {
      if (isKbTab(fallback)) mode.value = 'kb';
      switchWorkspaceTab(fallback.id);
    }
  }
}



async function switchWorkspaceTab(id) {
  const tab = workspaceTabs.value.find((t) => t.id === id);
  if (!tab) return;
  const tabIsKb = isKbTab(tab);
  if (!workspaceExplorerByTab.has(id)) workspaceExplorerByTab.set(id, explorerDefaultForPath(tab.path));
  const switchVersion = ++workspaceSwitchVersion;
  const linkedSession = ensureWorkspaceTabSession(tab);
  saveSessions();
  activeWorkspaceId.value = id;
  if (!tabIsKb) lastChatWorkspaceId.value = id;
  // Reset transient composer state so it does not bleed into the switched-to
  // workspace (a stale command menu or arrow-history cursor from the previous
  // session would otherwise persist across the switch).
  commandMenuVisible.value = false;
  commandHistoryIndex.value = -1;
  // The hidden KB tab never owns the persisted chat workspace: config.json's
  // workspace must keep pointing at the last chat workspace so a restart
  // restores chat mode, and the workspace history stays chat-only.
  if (!tabIsKb) {
    config.workspace = tab.path;
    configDraft.workspace = tab.path;
  }
  prepareFooterStatsForTarget(id, tab.path);
  // 切换 Tab 时恢复该 Tab 关联 session 的 extraRoots
  if (linkedSession && Array.isArray(linkedSession.extraRoots)) {
    extraRoots.value = [...linkedSession.extraRoots];
  } else {
    extraRoots.value = [];
  }
  loadPromptHistory(tab.path);
  // Each Tab owns its model snapshot; first visit initializes it from the
  // configured default. The persisted config's model fields are NOT touched
  // here — the active Tab's model reaches the backend only via the chat
  // request overlay.
  ensureTabModel(tab);
  try {
    if (!tabIsKb) await saveWorkspaceConfig({ ...config });
  } catch (err) {
    if (workspaceSwitchVersion === switchVersion) {
      footerStatsLoading.value = false;
      message.error(t('app.config.saveFailed', { error: err }));
    }
    return;
  }
  if (workspaceSwitchVersion !== switchVersion || activeWorkspaceId.value !== id) return;

  // Switch to linked session
  if (linkedSession) {
    activeSessionId.value = linkedSession.id;
    await loadSessionMessages(linkedSession);
    if (workspaceSwitchVersion !== switchVersion || activeWorkspaceId.value !== id) return;
    unloadInactiveSessionMessages();
    loadTodos(linkedSession.id);
    // Do NOT restore to bottom on tab switch - each ChatMessages instance
    // stays mounted (display-directive="show") so the browser naturally
    // preserves its scroll position when hidden/shown.
    // restoreMessagesToBottom is only for new session loads (activateSelectedSession).
  }
  await refreshFooterStats({
    tabId: id,
    sessionId: linkedSession?.id || activeSessionId.value,
    workspace: tab.path,
  });
  // 切换 Tab 时如果目录树已打开且工作区变化，更新捕获的工作区；
  // 组件内 watch(workspace) 会自动触发 loadRoot 重建
  // 默认开启：切换到从未访问过的 Tab 时同步其工作区，目录树首挂即有正确根目录
  if (workspaceExplorerByTab.get(id) !== false && explorerWorkspaceByTab.get(id) !== tab.path) {
    explorerWorkspaceByTab.set(id, tab.path);
  }
  // 切换 Tab 后自动聚焦新 Tab 的输入框，避免手动点击才能输入
  nextTick(() => {
    focusPromptInput();
    scrollPlanPanelToFocus(id);
  });
}

function syncConfigToActiveTab() {
  const tab = workspaceTabs.value.find((t) => t.id === activeWorkspaceId.value);
  // The hidden KB tab does not mirror the persisted chat workspace.
  if (tab && !isKbTab(tab) && config.workspace !== tab.path) {
    tab.path = config.workspace || '';
    tab.label = workspaceLabel(tab.path);
  }
  const session = activeSession.value;
  if (session && config.workspace && !isKbTab(tab)) session.workspace = config.workspace;
}

function newSession(title) {
  saveSessions();
  const id = crypto.randomUUID ? crypto.randomUUID() : `s-${Date.now()}-${Math.random()}`;
  const now = Date.now();
  const activeTab = workspaceTabs.value.find((item) => item.id === activeWorkspaceId.value) || null;
  const workspace = (isKbTab(activeTab) ? activeTab.path : config.workspace) || '';
  const sessionTitle = title || (workspace ? workspaceLabel(workspace) : t('app.sessions.new'));
  const session = { id, title: sessionTitle, isDefault: true, workspace, extraRoots: [], messages: [], messagesLoaded: true, runId: '', isRunning: false, createdAt: now, updatedAt: now };
  sessions.value.unshift(session);
  activeSessionId.value = id;
  bindSessionToActiveWorkspaceTab(session);
  // 新会话默认无附加工作区
  extraRoots.value = [];
  promptText.value = '';
  loadTodos(id);
  addWelcome(workspace);
  // Reset workspace token usage for new session
  const ws = workspace;
  if (ws) {
    ResetWorkspaceTokenUsage(ws);
    workspaceTokenUsage.value = emptyWorkspaceTokenUsage();
  }
}

async function selectSession(index) {
  const visibleSessions = currentWorkspaceSessions.value;
  if (index < 0 || index >= visibleSessions.length) return;
  const target = visibleSessions[index];
  saveSessions();
  sessionsVisible.value = false;
  await activateSelectedSession(target);
  sessionsVisible.value = false;
}

function createReplacementSession(title = t('app.sessions.new'), workspacePath = '') {
  const id = crypto.randomUUID ? crypto.randomUUID() : `s-${Date.now()}-${Math.random()}`;
  const now = Date.now();
  const session = { id, title, isDefault: true, workspace: workspacePath || '', extraRoots: [], messages: [], messagesLoaded: true, runId: '', isRunning: false, createdAt: now, updatedAt: now };
  session.messages.push(buildWelcomeMessage(workspacePath || t('common.notSelected')));
  return session;
}

function deleteSession(index) {
  const visibleSessions = currentWorkspaceSessions.value;
  if (index < 0 || index >= visibleSessions.length) return;
  const target = visibleSessions[index];
  if (!target) return;
  const actualIndex = sessions.value.findIndex((session) => session.id === target.id);
  if (actualIndex < 0) return;
  if (target.runId || target.isRunning) {
    message.warning(t('app.sessions.runningDeleteBlocked'));
    return;
  }

  const deletedId = target.id;
  const wasActive = deletedId === activeSessionId.value;
  const fallback = visibleSessions[index + 1] || visibleSessions[index - 1] || null;
  const linkedTabs = workspaceTabs.value.filter((tab) => tab.sessionId === deletedId);
  releaseSessionAttachments(target);
  sessions.value.splice(actualIndex, 1);

  let replacement = null;
  if (linkedTabs.length > 0 || sessions.value.length === 0) {
    const tab = linkedTabs.find((item) => item.id === activeWorkspaceId.value) || linkedTabs[0];
    replacement = createReplacementSession(tab?.label || t('app.sessions.new'), tab?.path || '');
    sessions.value.unshift(replacement);
  }

  const replacementId = replacement?.id || fallback?.id || sessions.value[0]?.id || '';
  for (const tab of linkedTabs) {
    tab.sessionId = replacementId;
  }

  if (wasActive) {
    activeSessionId.value = replacementId;
    const nextSession = sessions.value.find((item) => item.id === replacementId);
    applySessionWorkspace(nextSession);
    promptText.value = '';
    loadTodos(replacementId);
    scrollMessagesToBottom();
  }

  delete todosBySession[deletedId];
  delete todoRevisionsBySession[deletedId];
  delete planPanelCollapsedBySession[deletedId];
  delete sessionPromptTexts[deletedId];
  deletePendingAttachments(deletedId);
  displayMessagesCacheBySession.delete(deletedId);
  enqueueSessionWrite(() => DeleteSession(deletedId)).catch(() => {});
  const expanded = new Set(expandedArchiveSessions.value);
  expanded.delete(deletedId);
  expandedArchiveSessions.value = expanded;

  sessionsSelectedIndex.value = Math.max(0, Math.min(index, currentWorkspaceSessions.value.length - 1));
  saveSessions();
}

function addWelcome(workspacePath = config.workspace || '') {
  const session = activeSession.value;
  if (!session) return;
  if (workspacePath) session.workspace = workspacePath;
  session.messages.push(buildWelcomeMessage(workspacePath || t('common.notSelected')));
}

// 用户主动纠错：鼠标 hover 到某条用户提问，点击 ✕ 删除该提问及之后的所有
// 对话（前端 UI 快照 + 后端模型上下文历史），并重算上下文 token 统计。
function hasImageAttachments(msg) {
  return Array.isArray(msg?.attachments) && msg.attachments.some((att) => att?.kind === 'image');
}

async function handleDeleteUserMessage(sessionId, msg) {
  const session = sessions.value.find((item) => item.id === sessionId);
  if (!session || !msg) return;
  if (session.runId || session.isRunning) {
    message.warning(t('app.sessions.runningDeleteBlocked'));
    return;
  }
  const baseIdx = session.messages ? session.messages.indexOf(msg) : -1;
  if (baseIdx < 0) return;

  // 该提问是第几条 user 消息（0-based），后端用它定位历史里的同一条。
  let userIndex = -1;
  for (let i = 0; i <= baseIdx; i++) {
    if (session.messages[i]?.role === 'user') userIndex += 1;
  }
  const userText = String(msg?.skill ? (msg.skill.args || '') : (msg?.content || '')).trim();
  // 纯附件/图片消息没有文本：后端历史里该类消息 content 会被替换为
  // "[N image attachment(s) omitted from saved history]"，用该子串匹配。
  const expectedContent = userText || (hasImageAttachments(msg) ? 'image attachment(s) omitted' : '');

  dialog.warning({
    title: t('chat.userMessage.deleteConfirmTitle'),
    content: t('chat.userMessage.deleteConfirmContent'),
    positiveText: t('common.delete'),
    negativeText: t('common.cancel'),
    onPositiveClick: async () => {
      try {
        // 1. 截断底层消息数组（含该条及之后所有）
        session.messages.splice(baseIdx);
        session.updatedAt = Date.now();
        session.messageCount = recountSessionMessages(session);
        displayMessagesCacheBySession.delete(sessionId);

        // 2. 持久化 UI 快照（截断后的消息）
        await persistCompletedSession(session);

        // 3. 同步截断后端模型上下文历史（内存 + 磁盘），并重算 breakdown
        try {
          await TruncateSessionHistory({
            sessionId,
            userMessageIndex: Math.max(0, userIndex),
            expectedContent,
          });
        } catch (err) {
          // UI 已删，后端未同步；提示用户，避免静默不一致。
          message.error(t('chat.userMessage.deleteBackendFailed', { error: String(err?.message || err || '') }));
        }

        // 4. 重算上下文 token 统计并刷新页脚
        await refreshFooterStats({
          sessionId,
          workspace: session.workspace || config.workspace || '',
        });
        scrollMessagesToBottom({ force: true });
      } catch (err) {
        message.error(t('chat.userMessage.deleteFailed', { error: String(err?.message || err || '') }));
      }
    },
  });
}

async function init() {
  try {
    const loaded = await GetConfig();
    assignConfig(config, loaded);
    assignConfig(configDraft, loaded);
    applyFontSizes(config);
  } catch (err) {
    message.error(t('app.config.readFailed', { error: err }));
  }
  loadModelByTab();
  loadExplorerClosedWorkspaces();

  // Init workspace tabs from config. Model state belongs to this Tab, not to
  // its workspace path, so a second Tab can point to the same path safely.
  const ws = config.workspace || '';
  const tab = createWorkspaceTab(ws);
  workspaceTabs.value.push(tab);
  activeWorkspaceId.value = tab.id;
  if (tab.sessionId) activeSessionId.value = tab.sessionId;
  // The startup Tab starts from the configured default model; it owns its
  // own snapshot from here on.
  ensureTabModel(tab);
  if (ws) addToHistory(ws);
  loadPromptHistory(ws);

  await loadSavedSessions();
  await refreshFooterStats({
    tabId: tab.id,
    sessionId: tab.sessionId,
    workspace: ws,
  });
  loadMcpConfig();
  loadBackgroundImage();
}

function sessionByRunId(runId) {
  return sessions.value.find(s => s.runId === runId) || null;
}

function sessionByEvent(data) {
  const sid = data?.sessionId || '';
  if (sid) return sessions.value.find(s => s.id === sid) || null;
  if (data?.runId) return sessionByRunId(data.runId);
  return activeSession.value;
}

function sessionByTerminalEvent(data) {
  const sid = data?.sessionId || '';
  const session = sid
    ? sessions.value.find((item) => item.id === sid) || null
    : sessionByRunId(data?.runId || '');
  if (!session || !shouldAcceptRunTerminal(session.runId, data?.runId)) return null;
  return session;
}

function markSessionRunning(session) {
  if (!session) return;
  if (!session.workspace && config.workspace) session.workspace = config.workspace;
  for (let index = session.messages.length - 1; index >= 0; index--) {
    const item = session.messages[index];
    if (item?.role !== 'user') continue;
    if (!item.runId && !item.transientTurn) item.pendingTurn = true;
    break;
  }
  session.isRunning = true;
}

function bindPendingTurnToRun(session, runId) {
  if (!session || !runId) return;
  for (const item of session.messages || []) {
    if (!item?.pendingTurn) continue;
    item.runId = runId;
  }
}

function finishPersistableTurn(session, runId) {
  if (!session || !runId) return;
  for (const item of session.messages || []) {
    if (item?.runId !== runId) continue;
    item.pendingTurn = false;
    item.transientTurn = false;
  }
}

function markTransientTurn(session, runId = '') {
  if (!session) return;
  for (const item of session.messages || []) {
    if (!item) continue;
    if ((runId && item.runId === runId) || item.pendingTurn) {
      item.pendingTurn = false;
      item.transientTurn = true;
    }
  }
}

function discardInterruptedResponse(session, runId) {
  if (!session || !runId) return;
  for (let i = session.messages.length - 1; i >= 0; i--) {
    const msg = session.messages[i];
    if (msg?.role === 'assistant' && msg.runId === runId && msg.streaming) {
      session.messages.splice(i, 1);
      break;
    }
  }
  streamBuffers.delete(runId);
  for (const key of [...toolUpdateBuffers.keys()]) {
    if (String(key).startsWith(`${runId}:`)) toolUpdateBuffers.delete(key);
  }
}

function queueStreamDelta(data, field) {
  const runId = data?.runId || '';
  if (!runId) return;
  let buffer = streamBuffers.get(runId);
  if (!buffer) {
    buffer = { runId, content: '', reasoningLen: 0 };
    streamBuffers.set(runId, buffer);
  }
  if (field === 'reasoning') buffer.reasoningLen += Number(data.reasoningLen) || String(data.reasoning || data.content || '').length;
  else if (field === 'content') buffer.content += data.content || '';
  // Merged run:stream payload carries both fields in one event; route each
  // non-empty field into its own buffer slot so a single IPC feeds both the
  // reasoning counter and the answer body.
  else {
    if (data.reasoningLen) buffer.reasoningLen += Number(data.reasoningLen);
    else if (data.reasoning) buffer.reasoningLen += String(data.reasoning).length;
    if (data.content) buffer.content += data.content;
  }
  scheduleStreamFlush();
}

function scheduleStreamFlush() {
  if (streamFlushScheduled) return;
  streamFlushScheduled = true;
  // Wails dispatches events as synchronous JS calls, so the only throttle we
  // need is the browser's frame boundary. Coalescing on rAF keeps latency at
  // ~16ms instead of the previous 48ms setTimeout + rAF chain (~64ms).
  streamFlushRaf = window.requestAnimationFrame(() => {
    streamFlushRaf = 0;
    streamFlushScheduled = false;
    for (const runId of [...streamBuffers.keys()]) {
      flushStreamBuffer(runId);
    }
  });
}

function flushStreamBuffer(runId) {
  const buffer = streamBuffers.get(runId);
  if (!buffer) return;
  streamBuffers.delete(runId);
  const session = sessionByRunId(runId);
  if (!session) return;
  // Reuse the last assistant message that is still streaming for this run.
  // Scanning (not just peeking the tail) keeps an injected user message that
  // sits after the in-flight assistant response from splitting the current
  // response into a new message mid-stream; the split happens only when
  // run:inject finalizes the previous response and the next one starts.
  let last = null;
  for (let i = session.messages.length - 1; i >= 0; i--) {
    const m = session.messages[i];
    if (m && m.role === 'assistant' && m.streaming && !m.error && !m.system && m.runId === runId) {
      last = m;
      break;
    }
  }
  if (!last) {
    last = { role: 'assistant', content: '', reasoningChars: 0, streaming: true, runId };
    session.messages.push(last);
  }
  last.streaming = true;
  const hadContent = !!buffer.content;
  // The first reasoning delta inserts the "Thinking" label into the message
  // list, so it is visible growth too. Later pure reasoning deltas only bump
  // the token counter without changing layout height.
  const reasoningStarts = buffer.reasoningLen > 0 && !last.reasoningStartedAt;
  if (buffer.reasoningLen > 0) {
    // Only track a running char count for the "Thinking · N tokens" indicator.
    // The reasoning body itself is never stored — it is too large to be useful
    // after session restore and bloats the snapshot files.
    if (last.reasoningChars === undefined) last.reasoningChars = 0;
    if (!last.reasoningStartedAt) last.reasoningStartedAt = Date.now();
    last.reasoningChars += buffer.reasoningLen;
  }
  if (buffer.content) {
    // First content delta marks the end of the thinking phase.
    if (last.reasoningStartedAt && !last.reasoningEndedAt) {
      last.reasoningEndedAt = Date.now();
    }
    last.content += buffer.content;
    // 一次性标志：正文已开始输出。统计占位行据此渲染（不直接读 content，
    // 避免父级渲染 effect 订阅流式增量）；纯思考/工具阶段的空消息不占位。
    last.hasBody = true;
  }
  // Auto-scroll on visible growth: the first reasoning delta or a content
  // delta. Pure reasoning deltas do not grow the visible message body, so
  // scrolling on every reasoning flush wastes a layout pass.
  if (session.id === activeSessionId.value && (hadContent || reasoningStarts)) {
    scrollMessagesToBottom();
  }
}

// finalizeReasoningTiming closes out the thinking window when a run ends
// without ever emitting a content delta (e.g. pure-reasoning replies, errors,
// or cancellations), so the "Thought for Xs" label still gets a duration.
function finalizeReasoningTiming(msg) {
  if (!msg || msg.role !== 'assistant') return;
  if (msg.reasoningStartedAt && !msg.reasoningEndedAt) {
    msg.reasoningEndedAt = Date.now();
  }
}
// Buffer the latest tool:update event per tool call and flush on a timer so
// the main thread is not blocked by parsing/re-rendering large streaming
// argument payloads (e.g. create content) on every delta.
function bufferToolUpdate(data) {
  if (isHiddenTool(data.name)) return;
  flushStreamBuffer(data.runId);
  const session = sessionByRunId(data.runId);
  if (!session) return;
  toolUpdateBuffers.set(toolEventId(data), data);
  scheduleToolUpdateFlush();
}

function scheduleToolUpdateFlush() {
  if (toolUpdateFlushScheduled) return;
  toolUpdateFlushScheduled = true;
  toolUpdateFlushTimer = window.setTimeout(() => {
    window.requestAnimationFrame(() => {
      toolUpdateFlushTimer = 0;
      toolUpdateFlushScheduled = false;
      flushToolUpdateBuffer();
    });
  }, 120);
}

function flushToolUpdateBuffer() {
  if (toolUpdateFlushTimer) {
    clearTimeout(toolUpdateFlushTimer);
    toolUpdateFlushTimer = 0;
    toolUpdateFlushScheduled = false;
  }
  if (toolUpdateBuffers.size === 0) return;
  const entries = [...toolUpdateBuffers.values()];
  toolUpdateBuffers.clear();
  let lastActiveSessionId = '';
  let lastActiveToolName = '';
  for (const data of entries) {
    const session = sessionByRunId(data.runId);
    if (!session) continue;
    const progressMeta = { ...data, suppressScroll: true };
    const args = String(progressMeta.args || '');
    const title = makeToolTitle(progressMeta.name, args, progressMeta);
    const liveBody = progressMeta.output !== undefined ? String(progressMeta.output || '') : args;
    updateToolEvent(toolEventId(progressMeta), progressMeta.name, title, liveBody, 'running', progressMeta, session);
    if (session.id === activeSessionId.value) {
      lastActiveSessionId = session.id;
      lastActiveToolName = data.name || '';
    }
  }
  // After the latest tool card has been rendered, scroll so its header stays
  // visible (top + 96px padding) rather than pinning scroll to the card's
  // bottom, which would push the header above the viewport fold. render_html
  // uses its own fixed-height streaming preview instead of a rich tool card;
  // trying to align it would select an older rich card and repeatedly pull the
  // viewport upward while the HTML arguments stream. read/batch_read/grep now
  // render as the one-line folded group: aligning it to viewport-top + 96px
  // leaves a big gap above the fold while running and snaps back when the
  // result lands, so they follow the regular bottom-pinned scroll instead.
  const noAlignTools = ['render_html', 'read', 'batch_read', 'grep'];
  if (lastActiveSessionId) {
    scrollMessagesToBottom({ alignToLastToolCard: !noAlignTools.includes(lastActiveToolName) });
  }
}

function assistantMessageForRun(session, runId) {
  if (!session || !runId) return null;
  for (let i = session.messages.length - 1; i >= 0; i--) {
    const msg = session.messages[i];
    if (msg.role === 'assistant' && msg.runId === runId) return msg;
  }
  return null;
}

function setAssistantRoundDuration(session, runId, durationMs) {
  const msg = assistantMessageForRun(session, runId);
  if (!msg) return;
  const text = formatDurationShort(durationMs);
  if (!text) return;
  msg.roundDurationMs = Number(durationMs || 0);
  msg.roundDurationText = text;
}

function setAssistantCacheRate(session, runId, hit, miss, inputTokens, outputTokens) {
  const msg = assistantMessageForRun(session, runId);
  if (!msg) return;
  const h = Number(hit || 0);
  const m = Number(miss || 0);
  const inp = Number(inputTokens || 0);
  const out = Number(outputTokens || 0);
  const rate = (h + m) > 0 ? Math.floor((h / (h + m)) * 100) : null;
  msg.cacheHit = h;
  msg.cacheMiss = m;
  if (rate === null) delete msg.cacheRate;
  else msg.cacheRate = rate;
  msg.runInputTokens = inp;
  msg.runOutputTokens = out;
}

function onRuntimeEvent(eventName, handler) {
  // Wails v3 delivers a WailsEvent wrapper ({ name, data, sender }), while
  // application handlers consume the backend payload directly. Keep this
  // boundary centralized so every runtime event follows the same contract.
  const off = Events.On(eventName, (event) => {
    handler(unwrapWailsEvent(event, eventName));
  });
  if (typeof off === 'function') runtimeEventOffs.push(off);
}

function cleanupRuntimeEvents() {
  while (runtimeEventOffs.length > 0) {
    const off = runtimeEventOffs.pop();
    try { off(); } catch (_) { /* ignore cleanup errors */ }
  }
  runtimeEventsBound = false;
}

function bindRuntimeEvents() {
  if (runtimeEventsBound) return;
  runtimeEventsBound = true;

  onRuntimeEvent('run:start', (data) => {
    const sid = data?.sessionId || '';
    const session = sid ? sessions.value.find((item) => item.id === sid) || null : null;
    if (session) {
      session.runId = data.runId;
      session.isRunning = true;
      bindPendingTurnToRun(session, data.runId);
    }
  });

  onRuntimeEvent('tokens:update', (data) => {
    if (
      footerStatsLoading.value
      || String(data.workspace || '') !== String(config.workspace || '')
    ) return;
    workspaceTokenUsage.value = {
      inputTokens: data.inputTokens || 0,
      outputTokens: data.outputTokens || 0,
    };
  });

  onRuntimeEvent('mcp:status', (data) => {
    mcpServers.value = data?.servers || [];
    refreshToolList();
    updateWelcomeMcpRows();
    // MCP tool schemas contribute to ToolSchemas in the context breakdown.
    // When a server finishes ListTools (or fails / disconnects), the set of
    // injected MCP tool definitions changes and the breakdown must be
    // recomputed — otherwise the footer context percent stays at the value
    // from when MCP was still initializing and tool list was empty.
    if (activeSessionId.value) refreshContextTokens(activeSessionId.value);
  });

  onRuntimeEvent('dependency:missing', (data) => {
    const tool = data?.tool || data?.name || 'dependency';
    if (missingDependencyWarningsShown.has(tool)) return;
    missingDependencyWarningsShown.add(tool);
    const messageText = data?.messageKey
      ? t(data.messageKey, data?.messageParams || {})
      : (data?.message || t('app.tool.notInstalled', { tool }));
    const stepKeys = Array.isArray(data?.installStepKeys) ? data.installStepKeys : [];
    const steps = stepKeys.length
      ? stepKeys.map((key) => t(key))
      : (Array.isArray(data?.installSteps) ? data.installSteps : []);
    const detail = steps.length ? '\n\n' + steps.join('\n') : '';
    message.warning(`${messageText}${detail}`, { duration: 18000 });
  });

  onRuntimeEvent('config:warning', (data) => {
    if (!data?.message) return;
    message.warning(data.message, { duration: 10000 });
  });

  onRuntimeEvent('run:stream', (data) => {
    // Merged event: payload may carry content, reasoning, or both. Routing
    // both fields in one IPC halves event count vs separate run:delta +
    // run:reasoning emissions.
    if (retryBanner.value) {
      const session = sessionByTerminalEvent(data);
      if (session && session.id === activeSessionId.value) retryBanner.value = null;
    }
    queueStreamDelta(data);
  });
  onRuntimeEvent('run:inject', (data) => {
    // 封口前先 flush 流式缓冲:后端在流式结束后才发 run:inject,但剩余
    // delta 可能还在前端的 rAF 缓冲里。直接封口会让这些内容在下一轮
    // 新建消息,把回答劈开并粘到注入消息后面(与 run:done/run:error
    // 的先 flush 再收尾顺序保持一致)。
    flushStreamBuffer(data.runId);
    const session = sessionByEvent(data);
    if (!session) return;
    finalizeStreamingMessageForRun(session, data.runId);
  });
  // Legacy events kept for compatibility with older backend builds and with
  // session replay paths that still emit them individually.
  onRuntimeEvent('run:delta', (data) => {
    if (retryBanner.value) {
      const session = sessionByTerminalEvent(data);
      if (session && session.id === activeSessionId.value) retryBanner.value = null;
    }
    queueStreamDelta(data, 'content');
  });
  onRuntimeEvent('run:reasoning', (data) => {
    if (retryBanner.value) {
      const session = sessionByTerminalEvent(data);
      if (session && session.id === activeSessionId.value) retryBanner.value = null;
    }
    queueStreamDelta(data, 'reasoning');
  });
  onRuntimeEvent('run:retry', (data) => {
    const session = sessionByTerminalEvent(data);
    if (!session) return;
    // Discard interrupted output for background tabs too. Otherwise the next
    // attempt appends to the failed partial response when the tab is revisited.
    if (data.discardCurrentResponse) discardInterruptedResponse(session, data.runId);
    if (session.id !== activeSessionId.value) return;
    retryBanner.value = {
      attempt: Number(data.attempt || 0),
      maxAttempts: Number(data.maxAttempts || 0),
      error: String(data.error || ''),
      waitMs: Number(data.waitMs || 0),
      keyIndex: Number(data.keyIndex || 0),
      totalKeys: Number(data.totalKeys || 0),
    };
  });
  onRuntimeEvent('run:image', (data) => {
    flushStreamBuffer(data.runId);
    const session = sessionByEvent(data);
    if (!session || !data?.dataUrl) return;
    let target = session.messages[session.messages.length - 1];
    if (!target || target.role !== 'assistant' || target.done || target.error || target.system || target.runId !== data.runId) {
      target = { role: 'assistant', content: '', reasoningChars: 0, streaming: true, attachments: [], runId: data.runId };
      session.messages.push(target);
    }
    target.streaming = true;
    // 图片输出意味着思考已结束
    finalizeReasoningTiming(target);
    if (!Array.isArray(target.attachments)) target.attachments = [];
    const imageId = data.id || `generated-${target.attachments.length + 1}`;
    let attachment = target.attachments.find((item) => item.id === imageId);
    if (!attachment) {
      attachment = {
        id: imageId,
        name: t('app.attachment.generatedImage'),
        type: data.mimeType || 'image/png',
        size: 0,
        kind: 'image',
        previewUrl: '',
        dataUrl: '',
        text: '',
        truncated: false,
        error: '',
        generated: true,
      };
      target.attachments.push(attachment);
    }
    attachment.type = data.mimeType || attachment.type || 'image/png';
    attachment.previewUrl = data.dataUrl;
    attachment.dataUrl = data.dataUrl;
    attachment.partial = !!data.partial;
    attachment.size = Math.max(0, Math.floor((String(data.dataUrl).length * 3) / 4));
    if (!data.partial) {
      createImageThumbnailDataUrl(data.dataUrl).then((thumbnail) => {
        if (thumbnail && attachment.dataUrl === data.dataUrl) {
          attachment.previewUrl = thumbnail;
          scheduleSaveSessions();
        }
      }).catch(() => {});
    }
    if (session.id === activeSessionId.value) scrollMessagesToBottom();
  });
  const applyToolProgressEvent = (data) => {
    flushStreamBuffer(data.runId);
    const session = sessionByRunId(data.runId);
    if (!session) return;
    // 工具调用开始意味着本轮思考已结束，闭合思考时间窗口
    const last = session.messages[session.messages.length - 1];
    finalizeReasoningTiming(last);
    // suggest 不渲染 running card，结果直接注入前一条 assistant 消息
    if (isHiddenTool(data.name)) return;
      const title = makeToolTitle(data.name, data.args, data);
    updateToolEvent(toolEventId(data), data.name, title, data.args || '', 'running', data, session);
  };
  // tool:start must create the card immediately so the user sees the tool
  // call appear. tool:update carries the growing accumulated arguments and is
  // throttled via bufferToolUpdate to avoid blocking the main thread when
  // streaming large payloads (e.g. create with thousands of lines).
  onRuntimeEvent('tool:start', applyToolProgressEvent);
  onRuntimeEvent('tool:update', bufferToolUpdate);
  onRuntimeEvent('ask:ready', (data) => {
    const session = sessionByEvent(data);
    if (!session) return;
    let existing = findToolEventByData(session, { ...data, name: 'ask' });
    if (!existing) {
      existing = appendToolEventFallback(session, {
        ...data,
        name: 'ask',
        args: JSON.stringify({ questions: data.questions || [] }),
      }, 'running');
    }
    if (!existing) return;
    existing.kind = 'ask';
    existing.askId = data.askId || '';
    existing.askQuestions = Array.isArray(data.questions) ? data.questions : [];
    existing.askReady = true;
    existing.askSubmitting = false;
    existing.title = existing.askQuestions.length === 1
      ? (existing.askQuestions[0]?.question || '')
      : t('app.ask.questions', { count: existing.askQuestions.length });
    scheduleSaveSessions();
    if (session.id === activeSessionId.value) scrollMessagesToBottom();
  });
  onRuntimeEvent('ask:closed', (data) => {
    const session = sessionByEvent(data);
    if (!session) return;
    const existing = session.messages.find((item) => item.role === 'tool_call' && item.askId === data.askId);
    if (existing) {
      existing.askReady = false;
      existing.askSubmitting = false;
      setToolStatus(existing, 'error');
      existing.body = t('app.ask.cancelled');
    }
    scheduleSaveSessions();
  });
  // Tool-card subsystem: handlers + per-tool adapters live in useToolEvents.
  useToolEvents({
    onRuntimeEvent,
    sessionByEvent,
    appendToolEventFallback,
    scheduleSaveSessions,
    flushStreamBuffer,
    flushToolUpdateBuffer,
    finalizeStreamingMessageForRun,
    refreshContextTokens,
    isHiddenTool,
    parseToolResultData,
    formatToolBody,
    formatToolChip,
    formatDurationShort,
    makeToolResultTitle,
    formatTodoNextStep,
    scrollMessagesToBottomIfStale,
    scrollMessagesToBottom,
    activeSessionId,
  });
  // Run 终态三胞胎（run:done / run:error / run:cancelled）的公共收尾。
  // 收敛前每个 handler 各自维护约 70 行逐字重复的步骤；这里按三者原本完全
  // 一致的顺序编排，差异点全部通过 opts.variant 注入（见下面的分支）：
  // - done:      setAssistant* 之后走 finishPersistableTurn + persistCompletedSession
  // - error:     封口后先 markTransientTurn，再插入错误/取消提示消息
  // - cancelled: setAssistant* 之后才 markTransientTurn
  // 说明：session.runId/isRunning 的复位对 done/cancelled 变体比原实现提前
  // 了几步（原实现夹在 setAssistant*/finishPersistableTurn 之后），这些步骤
  // 读写互不相交的字段（消息条目 vs 会话字段），行为完全一致；
  // saveSessions/persistCompletedSession 的守卫依赖复位先完成，顺序保持不变。
  function finalizeRunTerminal(session, data, opts) {
    const variant = opts?.variant || 'done';
    // 从末尾向前找本 run 的流式 assistant 消息封口（方向与原实现一致）。
    let i = session.messages.length - 1;
    while (i >= 0) {
      const msg = session.messages[i];
      if (msg.role === 'assistant' && msg.streaming && msg.runId === data.runId) {
        msg.streaming = false;
        msg.done = true;
        finalizeReasoningTiming(msg);
        // 思考内容始终保留在折叠的 thinking 区，不转正为正文：
        // 对话正常结束（即使模型只输出了思考、没有正文）。
        break;
      }
      i--;
    }
    if (variant === 'error') markTransientTurn(session, data.runId);
    session.runId = '';
    session.isRunning = false;
    if (variant === 'error') {
      // 仅当前可见会话插入错误/取消提示消息；插入发生在 setAssistant* 之前，
      // 与原实现一致（插入的消息带 runId，roundDuration/cacheRate 落在它上面）。
      if (session.id === activeSessionId.value) {
        const err = data.error || 'unknown error';
        const cancelled = err === '已取消' || err === 'Cancelled' || String(err).toLowerCase().includes('context canceled');
        session.messages.push({ role: 'assistant', content: cancelled ? t('app.run.cancelled') : t('app.run.failed', { error: err }), error: !cancelled, system: cancelled, runId: data.runId, transientTurn: true });
      }
    }
    setAssistantRoundDuration(session, data.runId, data.durationMs);
    setAssistantCacheRate(session, data.runId, data.cacheHit, data.cacheMiss, data.inputTokens, data.outputTokens);
    if (variant === 'cancelled') markTransientTurn(session, data.runId);
    if (variant === 'done') {
      finishPersistableTurn(session, data.runId);
      persistCompletedSession(session);
    } else {
      // run:error / run:cancelled 是瞬态轮次，仅刷新轻量会话元数据，
      // 不落完整快照（消息会在下一轮被 transientTurn 清理规则淘汰）。
      saveSessions();
    }
  }
  onRuntimeEvent('run:done', (data) => {
    flushStreamBuffer(data.runId);
    flushToolUpdateBuffer();
    closeCompactLoading(data?.sessionId || '');
    if (retryBanner.value) {
      const session = sessionByTerminalEvent(data);
      if (session && session.id === activeSessionId.value) retryBanner.value = null;
    }
    const session = sessionByTerminalEvent(data);
    if (!session) return;
    // 会话结束可能修改了工作区文件。即使这个会话挂在后台 Tab 上，只要它
    // 属于当前工作区，底部 Git 统计也应刷新（GetGitStatus 查询的是当前
    // 工作区，与具体会话无关）。
    if (String(session.workspace || '') === String(config.workspace || '')) refreshGitStatus();
    finalizeRunTerminal(session, data, { variant: 'done' });
    if (session.id === activeSessionId.value) refreshContextTokens(session.id);
  });
  onRuntimeEvent('run:error', (data) => {
    flushStreamBuffer(data.runId);
    flushToolUpdateBuffer();
    closeCompactLoading(data?.sessionId || '');
    if (retryBanner.value) {
      const session = sessionByTerminalEvent(data);
      if (session && session.id === activeSessionId.value) retryBanner.value = null;
    }
    const session = sessionByTerminalEvent(data);
    if (!session) return;
    finalizeRunTerminal(session, data, { variant: 'error' });
    // Refresh token count after error: messages already grew with streamed
    // content, tool args, and the error footer — the context popover should
    // reflect that immediately instead of waiting for the next session switch.
    if (session.id === activeSessionId.value) refreshContextTokens(session.id);
    // 后台 Tab 的会话出错结束前可能已修改工作区文件，Git 统计同样要刷新。
    if (String(session.workspace || '') === String(config.workspace || '')) refreshGitStatus();
  });
  onRuntimeEvent('run:cancelled', (data) => {
    flushStreamBuffer(data.runId);
    flushToolUpdateBuffer();
    closeCompactLoading(data?.sessionId || '');
    if (retryBanner.value) {
      const session = sessionByTerminalEvent(data);
      if (session && session.id === activeSessionId.value) retryBanner.value = null;
    }
    const session = sessionByTerminalEvent(data);
    if (!session) return;
    finalizeRunTerminal(session, data, { variant: 'cancelled' });
    // Refresh token count after cancellation: streaming deltas and any tool
    // results added before cancellation are now part of the history and the
    // context popover should reflect the actual remaining budget.
    if (session.id === activeSessionId.value) refreshContextTokens(session.id);
    // 后台 Tab 的会话被取消前可能已修改工作区文件，Git 统计同样要刷新。
    if (String(session.workspace || '') === String(config.workspace || '')) refreshGitStatus();
  });
  // Compaction progress. Both manual /compact and in-run auto-compaction
  // stream compact:start / compact:progress events; state is keyed per
  // session so a background tab's compaction never surfaces in another
  // tab's composer. tokensBefore/messages arrive up front so the user sees
  // the scale of the summary request immediately.
  onRuntimeEvent('compact:start', (data) => {
    const sid = data?.sessionId || '';
    if (!sid) return;
    const existing = compactingSessions[sid];
    const text = t('app.compact.compactingDetail', {
      messages: Number(data?.messages || 0),
      tokens: fmtK(Number(data?.tokensBefore || 0)),
    });
    if (existing) {
      setCompactProgress(sid, text);
      if (!existing.startedAt) existing.startedAt = Date.now();
    } else {
      startCompactTracking(sid, text);
    }
  });
  onRuntimeEvent('compact:progress', (data) => {
    const sid = data?.sessionId || '';
    if (!sid) return;
    const usage = data?.usage || {};
    const parts = [];
    const inTok = Number(usage.inputTokens || 0);
    const outTok = Number(usage.outputTokens || 0);
    if (inTok > 0 || outTok > 0) {
      parts.push(t('app.compact.usage', { input: fmtK(inTok), output: fmtK(outTok) }));
    }
    if (data?.note) parts.push(String(data.note));
    const text = parts.length > 0
      ? parts.join(' · ')
      : t('app.compact.compacting');
    if (compactingSessions[sid]) setCompactProgress(sid, text);
    else startCompactTracking(sid, text);
  });
  onRuntimeEvent('compact:done', (data) => {
    const sid = data?.sessionId || '';
    if (sid) delete compactingSessions[sid];
  });
  // Auto-compaction: the backend emits run:compact before the blocking summary
  // request and run:compacted after it. Surface the token delta so the sudden
  // drop in the footer token counter is no longer mysterious.
  onRuntimeEvent('run:compact', () => {});
  onRuntimeEvent('run:compacted', (data) => {
    const sid = data?.sessionId || '';
    if (sid) delete compactingSessions[sid];
    if (!sid) return;
    if (data?.error) {
      if (sid === activeSessionId.value) message.warning(t('app.compact.failed', { error: data.error }));
      return;
    }
    const before = Number(data?.tokensBefore || 0);
    const after = Number(data?.tokensAfter || 0);
    if (sid === activeSessionId.value) {
      if (after > 0 && before > after) {
        message.info(t('app.compact.autoToast', { before: fmtK(before), after: fmtK(after) }));
      }
      refreshContextTokens(sid);
    }
  });
  onRuntimeEvent('plan:update', (data) => {
    const sid = data.sessionId || '';
    if (!sid) return;
    const revision = Number(data.revision || 0);
    const currentRevision = Number(todoRevisionsBySession[sid] || 0);
    if (revision && currentRevision && revision < currentRevision) return;
    const nextTodos = Array.isArray(data.todos) ? data.todos : [];
    todosBySession[sid] = nextTodos;
    if (revision) todoRevisionsBySession[sid] = revision;
    scrollPlanPanelsForSession(sid);
  });
  for (const eventName of ['scheduled:update', 'scheduled:run_start', 'scheduled:run_done', 'scheduled:run_error']) {
    onRuntimeEvent(eventName, (data) => applyScheduledTaskEvent(data));
  }
  onRuntimeEvent('service:update', (data) => applyServiceEvent(data));

  // ── Sub-agent events ──

  function findSubagentMsg(id, sessionId = '') {
    const session = sessionId ? sessions.value.find(s => s.id === sessionId) : activeSession.value;
    if (!session) return null;
    return session.messages.find(m => m.kind === 'subagent' && (m.subagentId === id || m.eventId === id)) || null;
  }

  onRuntimeEvent('sub:spawn', (data) => {
    const session = sessionByEvent(data);
    // Upgrade the original subagent card in place. Keeping the parent
    // tool identity lets the eventual tool:result/tool:error update this same
    // card instead of appending a second raw JSON result card.
    if (session) {
      const existing = findToolEventByData(session, { ...data, name: 'subagent' }) ||
        findToolEventByData(session, { ...data, name: 'agent_delegate' });
      const payload = {
        role: 'tool_call',
        kind: 'subagent',
        eventId: existing?.eventId || data.toolCallId || data.id,
        runId: data.runId || existing?.runId || '',
        toolBatchId: data.toolBatchId || existing?.toolBatchId || '',
        toolCallId: data.toolCallId || existing?.toolCallId || '',
        toolCallIndex: data.toolCallIndex ?? existing?.toolCallIndex,
        name: 'subagent',
        subagentId: data.id,
        status: 'running',
        description: data.description || '',
        profile: data.profile || 'coder',
        subagentRole: data.role || '',
        steps: 0,
        summary: '',
        filesEdited: [],
        error: '',
        toolCalls: existing?.toolCalls || [],
        time: new Date().toLocaleTimeString(),
        startTime: Number(data.startTime || Date.now()),
        durationMs: 0,
        durationText: '',
        inputTokens: 0,
        outputTokens: 0,
        totalTokens: 0,
      };
      if (existing) Object.assign(existing, payload);
      else session.messages.push(payload);
    }
  });
  onRuntimeEvent('sub:step', (data) => {
    const msg = findSubagentMsg(data.id, data.sessionId || '');
    if (msg) {
      msg.steps = data.step || 0;
      msg.inputTokens = data.inputTokens || 0;
      msg.outputTokens = data.outputTokens || 0;
      msg.totalTokens = data.totalTokens || 0;
    }
  });
  onRuntimeEvent('sub:tool:start', (data) => {
    const msg = findSubagentMsg(data.id, data.sessionId || '');
    if (msg) {
      msg.toolCalls.push({ toolCallId: data.toolCallId, name: data.name, args: data.args, status: 'running', summary: '', durationMs: 0, durationText: '' });
    }
  });
  onRuntimeEvent('sub:tool:result', (data) => {
    const msg = findSubagentMsg(data.id, data.sessionId || '');
    if (msg) {
      const tc = msg.toolCalls.find(t => t.toolCallId === data.toolCallId);
      if (tc) { setToolStatus(tc, 'success'); tc.summary = data.summary || ''; tc.durationMs = Number(data.durationMs || 0); tc.durationText = formatDurationShort(tc.durationMs); }
    }
  });
  onRuntimeEvent('sub:tool:error', (data) => {
    const msg = findSubagentMsg(data.id, data.sessionId || '');
    if (msg) {
      const tc = msg.toolCalls.find(t => t.toolCallId === data.toolCallId);
      if (tc) { setToolStatus(tc, 'error'); tc.summary = data.error || ''; tc.durationMs = Number(data.durationMs || 0); tc.durationText = formatDurationShort(tc.durationMs); }
    }
  });
  onRuntimeEvent('sub:done', (data) => {
    const msg = findSubagentMsg(data.id, data.sessionId || '');
    if (msg) {
      setToolStatus(msg, data.status || 'success');
      msg.summary = data.summary || '';
      msg.filesEdited = data.filesEdited || [];
      msg.steps = data.steps || msg.steps;
      msg.time = new Date().toLocaleTimeString();
      msg.durationMs = Number(data.durationMs || 0);
      msg.durationText = formatDurationShort(msg.durationMs);
      msg.inputTokens = data.inputTokens || 0;
      msg.outputTokens = data.outputTokens || 0;
      msg.totalTokens = data.totalTokens || 0;
    }
  });
  onRuntimeEvent('sub:error', (data) => {
    const msg = findSubagentMsg(data.id, data.sessionId || '');
    if (msg) {
      setToolStatus(msg, 'failed');
      msg.error = data.error || '';
      msg.time = new Date().toLocaleTimeString();
      msg.durationMs = Number(data.durationMs || 0);
      msg.durationText = formatDurationShort(msg.durationMs);
    }
  });
  // Self-update lifecycle events.
  onRuntimeEvent('update:progress', (data) => {
    if (!data) return;
    updateProgress.value = {
      stage: String(data.stage || ''),
      percent: Number(data.percent || 0),
      bytesDownloaded: Number(data.bytesDownloaded || 0),
      bytesTotal: Number(data.bytesTotal || 0),
      version: String(data.version || latestReleaseVersion.value || ''),
    };
    if (data.stage === 'download' && updateState.value === 'downloading') {
      // keep state
    } else if (data.stage === 'extract' && updateState.value === 'downloading') {
      updateState.value = 'extracting';
    } else if (data.stage === 'apply' && updateState.value === 'ready') {
      updateState.value = 'applying';
    }
  });
  onRuntimeEvent('update:ready', (data) => {
    // Ignore a ready event that races with a user cancel request.
    if (updateCancelRequested) {
      updateCancelRequested = false;
      updateState.value = 'idle';
      updateModalVisible.value = false;
      return;
    }
    if (data?.version) latestReleaseVersion.value = data.version;
    updateState.value = 'ready';
    // Always show the "ready to restart" modal, even for silent downloads.
    updateSilent.value = false;
    updateModalVisible.value = true;
  });
  onRuntimeEvent('update:applied', () => {
    updateState.value = 'restarting';
  });
  onRuntimeEvent('update:error', (data) => {
    updateState.value = 'error';
    updateError.value = String(data?.error || t('app.update.errors.generic'));
    // Silent downloads that fail should not bother the user; they will retry
    // on next startup or when the user clicks the update icon manually.
    if (!updateSilent.value) {
      updateModalVisible.value = true;
    } else {
      updateState.value = 'idle';
      updateSilent.value = false;
    }
  });
  onRuntimeEvent('update:cancelled', () => {
    updateState.value = 'idle';
    updateModalVisible.value = false;
  });
}

function pushMessage(role, content, extra = {}) {
  const session = activeSession.value;
  if (!session) return;
  session.messages.push({ role, content, ...extra });
  scrollMessagesToBottom();
}

function appendAssistantDelta(content) {
  if (!content) return;
  const session = activeSession.value;
  if (!session) return;
  let last = session.messages[session.messages.length - 1];
  if (!last || last.role !== 'assistant' || last.error || last.system || last.done) {
    last = { role: 'assistant', content: '', reasoningChars: 0, streaming: true };
    session.messages.push(last);
  }
  last.streaming = true;
  last.content += content;
  last.hasBody = true; // 统计占位行据此渲染（见 flushStreamBuffer 同名注释）
  scrollMessagesToBottom();
}

function markStreamingDone() {
  const session = activeSession.value;
  if (!session) return;
  for (let i = session.messages.length - 1; i >= 0; i--) {
    const msg = session.messages[i];
    if (msg.role === 'assistant' && msg.streaming) {
      msg.streaming = false;
      msg.done = true;
      return;
    }
  }
}

function appendReasoningDelta(runId, content) {
  if (!content) return;
  const session = activeSession.value;
  if (!session) return;
  let last = session.messages[session.messages.length - 1];
  if (!last || last.role !== 'assistant' || last.error || last.system || last.done) {
    last = { role: 'assistant', content: '', reasoningChars: 0, streaming: true };
    session.messages.push(last);
  }
  last.streaming = true;
  if (last.reasoningChars === undefined) last.reasoningChars = 0;
  if (!last.reasoningStartedAt) last.reasoningStartedAt = Date.now();
  last.reasoningChars += content.length;
}

function setConversationMessagesRef(tabId, instance) {
  if (!tabId) return;
  if (instance) conversationMessagesRefs.set(tabId, instance);
  else conversationMessagesRefs.delete(tabId);
}

function conversationMessagesForSession(sessionId) {
  if (!sessionId) return null;
  const tab = workspaceTabs.value.find((item) => item.sessionId === sessionId);
  return tab ? conversationMessagesRefs.get(tab.id) || null : null;
}

function scrollMessagesToBottom(options = {}, sessionId = activeSessionId.value) {
  nextTick(() => conversationMessagesForSession(sessionId)?.scrollToBottom(options));
}

// 会话恢复：由消息组件在 DOM 更新后执行立即滚底和两帧补滚。
function restoreMessagesToBottom(sessionId = activeSessionId.value) {
  nextTick(() => conversationMessagesForSession(sessionId)?.restoreToBottom());
}

// 大内容（如大 diff）一次性注入后的滚底 + 一帧复查：diff 跨帧展开时
// 首帧可能未贴底，组件内会再补一次（仍尊重自动跟随状态）。
function scrollMessagesToBottomIfStale(sessionId = activeSessionId.value) {
  nextTick(() => conversationMessagesForSession(sessionId)?.scrollToBottomIfStale());
}

function openAttachmentPicker() {
  attachmentInputRef.value?.click();
}

async function handleAttachmentSelected(event) {
  const files = Array.from(event.target.files || []);
  event.target.value = '';
  await addPendingAttachmentFiles(files);
}

async function handlePromptPaste(event) {
  const files = extractClipboardFiles(event);
  if (!files.length) return;
  event.preventDefault();
  await addPendingAttachmentFiles(files);
}

async function addPendingAttachmentFiles(files) {
  const arr = pendingAttachmentsOf(activeSessionId.value);
  for (const file of files) {
    if (arr.length >= MAX_ATTACHMENTS_PER_MESSAGE) {
      message.warning(t('app.attachment.limit', { count: MAX_ATTACHMENTS_PER_MESSAGE }));
      break;
    }
    const att = await fileToAttachment(file);
    // 只有“能真正发给模型”的附件才有意义：图片有 dataUrl，文本有 text。
    // 视频/音频/其他二进制 WebView2 拿不到真实路径，发出去也只有一个名字，
    // 会造成“以为带过去了其实没带”的误会，直接拒绝并提示。
    if (!att.dataUrl && !att.text) {
      message.warning(t('app.attachment.unsupported'));
      releaseAttachmentPreview(att);
      continue;
    }
    arr.push(att);
  }
}

function extractClipboardFiles(event) {
  const data = event?.clipboardData;
  if (!data) return [];
  const files = [];
  const seen = new Set();
  const pushFile = (file) => {
    if (!file) return;
    // A pasted image usually shows up in BOTH data.items (getAsFile) and
    // data.files, and the two representations have different lastModified
    // values. Keying on lastModified let the same image slip past the dedup
    // and appear as two identical attachments. Name+type+size is stable across
    // both representations, so use that instead.
    const key = `${file.name || ''}|${file.type || ''}|${file.size || 0}`;
    if (seen.has(key)) return;
    seen.add(key);
    files.push(withClipboardFallbackName(file));
  };
  for (const item of Array.from(data.items || [])) {
    if (item.kind === 'file') pushFile(item.getAsFile());
  }
  for (const file of Array.from(data.files || [])) {
    pushFile(file);
  }
  return files;
}

function withClipboardFallbackName(file) {
  if (file.name) return file;
  const ext = extensionFromMime(file.type);
  return new File([file], `clipboard-${Date.now()}${ext}`, {
    type: file.type,
    lastModified: file.lastModified || Date.now(),
  });
}

function extensionFromMime(type) {
  const mime = (type || '').toLowerCase();
  if (mime === 'image/png') return '.png';
  if (mime === 'image/jpeg') return '.jpg';
  if (mime === 'image/gif') return '.gif';
  if (mime === 'image/webp') return '.webp';
  if (mime === 'video/mp4') return '.mp4';
  if (mime === 'video/webm') return '.webm';
  if (mime === 'audio/mpeg') return '.mp3';
  if (mime === 'audio/wav') return '.wav';
  if (mime.startsWith('text/')) return '.txt';
  return '';
}

async function fileToAttachment(file) {
  const kind = attachmentKind(file);
  const base = {
    id: `att-${Date.now()}-${Math.random().toString(16).slice(2)}`,
    name: file.name,
    type: file.type,
    size: file.size,
    kind,
    previewUrl: '',
    dataUrl: '',
    text: '',
    truncated: false,
    error: '',
    filePath: '',
  };

  // 浏览器 File API 出于安全限制不暴露文件的真实绝对路径。
  // Wails 的 <input type="file"> 同样不提供 path 属性。
  // 这里记录工作区根 + 文件名作为提示路径，帮助模型用 read 工具定位文件。
  // file.path 在 Electron 中存在，WebView2 中为 undefined，这里取值无副作用。
  const rawPath = String(file.path || '').trim();
  if (rawPath) {
    base.filePath = rawPath;
  } else if (config.workspace) {
    base.filePath = `${String(config.workspace).replace(/[\\/]+$/, '')}/${file.name}`;
  } else {
    base.filePath = file.name;
  }

  try {
    if (kind === 'image' && file.size <= MAX_ATTACHMENT_PREVIEW_BYTES) {
      base.previewUrl = await createImageThumbnailUrl(file);
    } else if ((kind === 'video' || kind === 'audio') && file.size <= MAX_ATTACHMENT_PREVIEW_BYTES) {
      base.previewUrl = URL.createObjectURL(file);
    }
    if (kind === 'image' && file.size <= MAX_IMAGE_INPUT_BYTES) {
      base.dataUrl = await createModelImageDataUrl(file);
      if (!base.dataUrl) base.error = t('app.attachment.imageUnsupported');
    } else if (kind === 'image' && file.size > MAX_IMAGE_INPUT_BYTES) {
      base.truncated = true;
      base.error = t('app.attachment.imageTooLarge');
    }
    if (isTextAttachment(file) && file.size <= MAX_TEXT_ATTACHMENT_BYTES) {
      base.kind = kind === 'file' ? 'text' : kind;
      base.text = await readFileAsText(file);
    } else if (isTextAttachment(file) && file.size > MAX_TEXT_ATTACHMENT_BYTES) {
      base.kind = kind === 'file' ? 'text' : kind;
      base.truncated = true;
      base.error = t('app.attachment.textTooLarge');
    }
  } catch (err) {
    base.error = String(err?.message || err || t('app.attachment.readFailed'));
  }
  return base;
}

async function createModelImageDataUrl(file) {
  const supported = new Set(['image/png', 'image/jpeg', 'image/jpg', 'image/webp', 'image/gif']);
  if (supported.has(String(file.type || '').toLowerCase())) return readFileAsDataUrl(file);
  if (typeof createImageBitmap !== 'function') return '';
  let bitmap;
  try {
    bitmap = await createImageBitmap(file);
    const maxDimension = 2048;
    const scale = Math.min(1, maxDimension / Math.max(bitmap.width, bitmap.height));
    const canvas = document.createElement('canvas');
    canvas.width = Math.max(1, Math.round(bitmap.width * scale));
    canvas.height = Math.max(1, Math.round(bitmap.height * scale));
    const context = canvas.getContext('2d', { alpha: true });
    if (!context) return '';
    context.drawImage(bitmap, 0, 0, canvas.width, canvas.height);
    return canvas.toDataURL('image/webp', 0.9);
  } catch (_) {
    return '';
  } finally {
    bitmap?.close?.();
  }
}

async function createImageThumbnailUrl(file) {
  if (typeof createImageBitmap !== 'function') return '';
  let bitmap;
  try {
    bitmap = await createImageBitmap(file);
  } catch (_) {
    return '';
  }
  try {
    const maxDimension = 1280;
    const scale = Math.min(1, maxDimension / Math.max(bitmap.width, bitmap.height));
    const width = Math.max(1, Math.round(bitmap.width * scale));
    const height = Math.max(1, Math.round(bitmap.height * scale));
    const canvas = document.createElement('canvas');
    canvas.width = width;
    canvas.height = height;
    const context = canvas.getContext('2d', { alpha: true });
    if (!context) return '';
    context.drawImage(bitmap, 0, 0, width, height);
    const blob = await new Promise((resolve) => canvas.toBlob(resolve, 'image/webp', 0.86));
    return blob ? URL.createObjectURL(blob) : '';
  } catch (_) {
    return '';
  } finally {
    bitmap.close?.();
  }
}

async function createImageThumbnailDataUrl(dataUrl) {
  if (typeof createImageBitmap !== 'function' || !String(dataUrl || '').startsWith('data:image/')) return '';
  let bitmap;
  try {
    const blob = await (await fetch(dataUrl)).blob();
    bitmap = await createImageBitmap(blob);
    const maxDimension = 960;
    const scale = Math.min(1, maxDimension / Math.max(bitmap.width, bitmap.height));
    const width = Math.max(1, Math.round(bitmap.width * scale));
    const height = Math.max(1, Math.round(bitmap.height * scale));
    const canvas = document.createElement('canvas');
    canvas.width = width;
    canvas.height = height;
    const context = canvas.getContext('2d', { alpha: true });
    if (!context) return '';
    context.drawImage(bitmap, 0, 0, width, height);
    return canvas.toDataURL('image/webp', 0.78);
  } catch (_) {
    return '';
  } finally {
    bitmap?.close?.();
  }
}

function readFileAsDataUrl(file) {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve(String(reader.result || ''));
    reader.onerror = () => reject(reader.error || new Error(t('app.attachment.readFailed')));
    reader.readAsDataURL(file);
  });
}

function readFileAsText(file) {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve(String(reader.result || ''));
    reader.onerror = () => reject(reader.error || new Error(t('app.attachment.readFailed')));
    reader.readAsText(file);
  });
}

function attachmentKind(file) {
  const type = (file.type || '').toLowerCase();
  if (type.startsWith('image/')) return 'image';
  if (type.startsWith('video/')) return 'video';
  if (type.startsWith('audio/')) return 'audio';
  if (isTextAttachment(file)) return 'text';
  return 'file';
}

function isTextAttachment(file) {
  const type = (file.type || '').toLowerCase();
  if (type.startsWith('text/')) return true;
  const textMimes = new Set([
    'application/json',
    'application/ld+json',
    'application/xml',
    'application/xhtml+xml',
    'application/javascript',
    'application/typescript',
    'application/x-yaml',
    'application/yaml',
    'application/toml',
    'application/sql',
  ]);
  if (textMimes.has(type) || type.endsWith('+json') || type.endsWith('+xml')) return true;
  return /\.(txt|md|markdown|json|jsonl|xml|html?|css|js|mjs|cjs|ts|tsx|jsx|vue|go|py|java|c|cc|cpp|h|hpp|cs|rs|rb|php|sh|bash|zsh|ps1|bat|cmd|sql|yaml|yml|toml|ini|env|log|csv|tsv)$/i.test(file.name || '');
}

function removeAttachment(id) {
  for (const sid of Object.keys(pendingAttachmentsBySession)) {
    const arr = pendingAttachmentsBySession[sid];
    const idx = arr.findIndex(att => att.id === id);
    if (idx >= 0) {
      releaseAttachmentPreview(arr[idx]);
      arr.splice(idx, 1);
      return;
    }
  }
}

function releaseAttachmentPreview(att) {
  const previewUrl = String(att?.previewUrl || '');
  if (previewUrl.startsWith('blob:')) URL.revokeObjectURL(previewUrl);
}

function releaseMessageAttachments(msg) {
  for (const att of Array.isArray(msg?.attachments) ? msg.attachments : []) releaseAttachmentPreview(att);
}

function releaseSessionAttachments(session) {
  for (const msg of Array.isArray(session?.messages) ? session.messages : []) releaseMessageAttachments(msg);
}

function attachmentIcon(att) {
  if (att.kind === 'image') return 'IMG';
  if (att.kind === 'video') return 'VID';
  if (att.kind === 'audio') return 'AUD';
  if (att.kind === 'text') return 'TXT';
  return 'FILE';
}

function fmtBytes(size) {
  const n = Number(size || 0);
  if (n >= 1024 * 1024) return `${(n / 1024 / 1024).toFixed(1)} MB`;
  if (n >= 1024) return `${(n / 1024).toFixed(1)} KB`;
  return `${n} B`;
}

function attachmentDisplayLabel(attachments) {
  if (!attachments.length) return '';
  if (attachments.length === 1) return t('app.attachment.single', { name: attachments[0].name });
  return t('app.attachment.multiple', { name: attachments[0].name, count: attachments.length });
}

function attachmentsForModel(attachments) {
  return attachments.map((att) => ({
    id: att.id,
    name: att.name,
    type: att.type,
    size: att.size,
    kind: att.kind,
    dataUrl: att.kind === 'image' ? att.dataUrl || '' : '',
    text: att.text || '',
    truncated: !!att.truncated,
    error: att.error || '',
    filePath: att.filePath || '',
  }));
}

function isModelHistoryMessage(msg) {
  if (!msg || msg.system || msg.error || msg.welcome || msg.transientTurn || msg.role === 'archive' || msg.role === 'tool_call') return false;
  return msg.role === 'user' || msg.role === 'assistant';
}

function buildSessionMessagesForModel(session, latestOverride = null) {
  const result = [];
  const source = (session?.messages || []).filter(isModelHistoryMessage).slice(-MAX_MODEL_HISTORY_MESSAGES);
  for (const msg of source) {
    const role = msg.role;

    const isLatest = latestOverride?.message === msg;
    const content = isLatest ? latestOverride.content : msg.content;
    const attachments = role === 'user'
      ? (isLatest ? latestOverride.attachments : attachmentsForModel(Array.isArray(msg.attachments) ? msg.attachments : []))
      : [];
    if (!String(content || '').trim() && attachments.length === 0) continue;

    const next = { role, content: String(content || '') };
    if (role === 'user' && attachments.length > 0) next.attachments = attachments;
    result.push(next);
  }
  return result;
}

async function minimiseWindow() {
  try { await Window.Minimise(); } catch (_) {}
}

async function refreshWindowMaximisedState() {
  try {
    isMaximised.value = await Window.IsMaximised();
  } catch (_) { /* ignore runtime state read failures */ }
}

async function toggleMaximiseWindow() {
  try {
    const maximised = await Window.IsMaximised();
    if (maximised) {
      await Window.Restore();
    } else {
      await Window.Maximise();
    }
    await refreshWindowMaximisedState();
  } catch (_) {}
}

async function closeWindow() {
  try {
    await flushSessionWrites();
    await Application.Quit();
  } catch (_) {}
}

// Composer dropdown: switch the ACTIVE Tab's model. Purely frontend state —
// the persisted config (and its top-level default model) is untouched, and
// the backend only ever sees the overlay in each StartChat request.
function switchToModel(index) {
  const model = (config.models || [])[index];
  const tab = workspaceTabs.value.find((item) => item.id === activeWorkspaceId.value);
  if (!model || !tab) return;
  const snapshot = modelSnapshotFrom(model);
  modelByTab[tab.id] = snapshot;
  setLastUsedModelIdentity(modelConfigIdentity(model));
  saveModelByTab();
  message.success(t('app.model.switched', { model: model.model }));
}

const mcpConfigParseResult = computed(() => parseMcpConfigText(mcpConfigText.value));
const mcpConfigValid = computed(() => mcpConfigParseResult.value.valid);
const mcpConfigValidationText = computed(() => {
  const result = mcpConfigParseResult.value;
  if (!result.valid) return t('app.mcp.jsonError', { error: result.error });
  const servers = Object.keys(result.config.mcpServers || {}).length;
  return t('app.mcp.jsonValid', { count: servers });
});

function parseMcpConfigText(text) {
  try {
    const parsed = JSON.parse(text || '{"mcpServers":{}}');
    if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) {
      return { valid: false, error: t('app.mcp.rootObject'), config: { mcpServers: {} } };
    }
    if (parsed.mcpServers === undefined) parsed.mcpServers = {};
    if (!parsed.mcpServers || typeof parsed.mcpServers !== 'object' || Array.isArray(parsed.mcpServers)) {
      return { valid: false, error: t('app.mcp.serversObject'), config: { mcpServers: {} } };
    }
    return { valid: true, error: '', config: parsed };
  } catch (err) {
    return { valid: false, error: err?.message || String(err), config: { mcpServers: {} } };
  }
}

async function loadMcpConfig() {
  mcpLoading.value = true;
  try {
    mcpConfigText.value = await GetMcpConfig();
    mcpServers.value = await GetMcpServers() || [];
    await refreshToolList();
    updateWelcomeMcpRows();
  } catch (err) {
    message.error(t('app.mcp.readFailed', { error: err }));
  } finally {
    mcpLoading.value = false;
  }
}

async function refreshToolList() {
  try {
    availableTools.value = await ListTools() || [];
  } catch (_) {
    availableTools.value = [];
  }
}

async function saveMcpConfigText() {
  mcpLoading.value = true;
  try {
    if (!mcpConfigValid.value) {
      message.warning(mcpConfigValidationText.value);
      return;
    }
    await SaveMcpConfig(mcpConfigText.value);
    await RestartMcpServers();
    mcpServers.value = await GetMcpServers() || [];
    await refreshToolList();
    updateWelcomeMcpRows();
    message.success(t('app.mcp.saved'));
  } catch (err) {
    message.error(t('app.mcp.saveFailed', { error: err }));
  } finally {
    mcpLoading.value = false;
  }
}

function estimateTokens(text) {
  if (!text) return '0';
  const chars = text.length;
  const tokens = Math.round(chars / 3);
  if (tokens > 999) return (tokens / 1000).toFixed(1) + 'k';
  return String(tokens);
}

function formatReadChip(lines) {
  const lineCount = Number(lines) || 0;
  return lineCount > 0 ? '· ' + lineCount + ' line' + (lineCount !== 1 ? 's' : '') : '';
}

// suggest chip 点击：直接发送 label 作为新消息
async function sendSuggest(sessionId, label) {
  const tab = workspaceTabs.value.find(t => t.sessionId === sessionId);
  if (tab && activeWorkspaceId.value !== tab.id) {
    activeWorkspaceId.value = tab.id;
  }
  promptText.value = label;
  await nextTick();
  await sendPrompt();
}

async function sendQuickMessage(sessionId, key) {
  if (key === 'push') {
    const tab = workspaceTabs.value.find(t => t.sessionId === sessionId);
    if (tab && activeWorkspaceId.value !== tab.id) {
      activeWorkspaceId.value = tab.id;
    }
    await handlePushCommand();
    return;
  }
  const message = key === 'continue'
    ? t('chat.quickMessage.continueMessage')
    : key === 'review'
      ? REVIEW_PROMPT
      : key === 'lesson'
        ? LESSON_PROMPT
        : t('chat.plainSpeak.message');
  await sendSuggest(sessionId, message);
}

async function sendPrompt(opts) {
  const session = activeSession.value;
  if (!session) return;
  // An uninitialized knowledge base is forced through initialization: block
  // every send path (the input is disabled; this also covers quick messages
  // and suggestion chips, which bypass the composer input). The KB init
  // action opts out explicitly, otherwise its own button could never send.
  const allowKbUninitialized = opts?.allowKbUninitialized === true;
  const activeTabForSend = workspaceTabs.value.find((item) => item.id === activeWorkspaceId.value) || null;
  if (!allowKbUninitialized && isKbTab(activeTabForSend) && kbIndexMissing.value) {
    message.warning(t('kb.action.needInit'));
    return;
  }
  const text = promptText.value.trim();
  const attachments = (pendingAttachmentsBySession[session.id] || []).map(att => ({ ...att }));
  if (!text && attachments.length === 0) return;

  // The workspace this prompt runs against: the hidden KB tab pins its own
  // root, every other session follows the persisted chat workspace.
  const activeTab = workspaceTabs.value.find((item) => item.id === activeWorkspaceId.value) || null;
  const sessionWorkspace = (isKbTab(activeTab) ? activeTab.path : config.workspace) || '';

  // Resolve command: display label in UI, send expanded text to backend
  const matchedCommand = text.startsWith('/')
    ? commands.value.find(c => c.label === text)
    : null;
  const displayTextBase = matchedCommand ? matchedCommand.label : text;
  const sendText = matchedCommand ? matchedCommand.text || text : text;
  const displayText = displayTextBase || attachmentDisplayLabel(attachments);

  // Handle /switch <N> command
  if (text.startsWith('/switch ')) {
    const idx = text.slice(8).trim();
    if (idx) {
      await switchToSession(idx);
      promptText.value = '';
      commandMenuVisible.value = false;
      return;
    }
  }

  if (matchedCommand?.special && matchedCommand.special !== 'skill') {
    await handleBuiltinCommand(matchedCommand);
    return;
  }

  // Check if this is a skill activation (/skillname or /skill:name)
  if (text.startsWith('/')) {
    const slashContent = text.slice(1).trim();
    // Skip // comments
    if (text.startsWith('//')) {
      // Send as normal message
    } else if (slashContent) {
      // Check if it's a builtin command first
      const builtinPrefixes = ['new','plan','skills','clear','switch','sessions','reload','init','note','remember','compact','push','review'];
      const cmdName = slashContent.split(/\s+/)[0];
      const isBuiltin = builtinPrefixes.includes(cmdName);
      // Also check if any skill name matches
      const matchedSkill = availableSkills.value.find(sk => sk.name === cmdName || `skill:${sk.name}` === cmdName);
      if (!isBuiltin && matchedSkill) {
        await activateSkillByName(matchedSkill.name, slashContent.slice(cmdName.length).trim());
        promptText.value = '';
        commandMenuVisible.value = false;
        return;
      }
    }
  }

  if (!sessionWorkspace) {
    const workspace = await chooseWorkspace();
    if (!workspace) {
      message.warning(t('app.workspace.required'));
      return;
    }
    syncConfigToActiveTab();
    addToHistory(workspace);
    loadPromptHistory(workspace);
    clearFooterStats();
    try {
      await saveWorkspaceConfig({ ...config });
      await refreshFooterStats({
        tabId: activeWorkspaceId.value,
        sessionId: activeSessionId.value,
        workspace,
      });
    } catch (err) {
      footerStatsLoading.value = false;
      message.error(t('app.config.saveFailed', { error: err }));
      return;
    }
  }
  // API Key 可选：本地推理服务（如 Ollama/LM Studio 中转）无需鉴权；后端在 key
  // 为空时不会发送 Authorization 头，空 key 配置直接放行，错误由请求本身回报。
  if (!session) return;
  // 运行中发送:进入注入路径,消息排队等当前工具批次完成后加入模型上下文。
  if (session.runId) {
    await injectMessageToRun(session, sendText, displayText, attachments);
    return;
  }
  // runId 缺失但仍在运行是异常中间态(启动失败/事件未达),禁止重复启动新 run。
  if (session.isRunning) return;
  if (sessionWorkspace) session.workspace = sessionWorkspace;
  const userMessage = { role: 'user', content: displayText, attachments, done: true };
  session.messages.push(userMessage);
  session.updatedAt = Date.now();
  session.messageCount = recountSessionMessages(session);
  if (isDefaultSessionTitle(session)) {
    session.title = displayText.length > 20 ? `${displayText.slice(0, 20)}…` : displayText;
    markCustomSessionTitle(session);
  }
  // Save to workspace-scoped prompt history
  addPromptHistory(displayText);
  commandHistoryIndex.value = -1;
  clearPromptDraft(session.id);
  clearPendingAttachments(session.id, { revoke: false });
  commandMenuVisible.value = false;
  scrollMessagesToBottom({ force: true });

  try {
    const history = buildSessionMessagesForModel(session, {
      message: userMessage,
      content: sendText,
      attachments: attachmentsForModel(attachments),
    });
    markSessionRunning(session);
    const activeModel = modelByTab[activeWorkspaceId.value];
    if (activeModel) setLastUsedModelIdentity(modelConfigIdentity(activeModel));
    await StartChat({ sessionId: session.id, message: sendText, messages: history, config: { ...chatConfig.value, extraRoots: session.extraRoots || [], workspace: sessionWorkspace || config.workspace } });
  } catch (err) {
    markTransientTurn(session);
    session.runId = '';
    session.isRunning = false;
    pushMessage('assistant', t('app.run.startFailed', { error: err }), { error: true, transientTurn: true });
  }
}

// 运行中注入:把新消息排进当前 run 的队列,消息在下一个 agent step(当前工具
// 批次完成后)进入模型上下文。失败时回滚本地插入的消息并提示重发。
async function injectMessageToRun(session, sendText, displayText, attachments) {
  if (attachments.length > 0) {
    message.warning(t('app.run.injectAttachmentsUnsupported'));
    return;
  }
  const runId = session.runId;
  const userMessage = { role: 'user', content: displayText, attachments: [], done: true };
  session.messages.push(userMessage);
  session.updatedAt = Date.now();
  session.messageCount = recountSessionMessages(session);
  if (isDefaultSessionTitle(session)) {
    session.title = displayText.length > 20 ? `${displayText.slice(0, 20)}…` : displayText;
    markCustomSessionTitle(session);
  }
  addPromptHistory(displayText);
  commandHistoryIndex.value = -1;
  clearPromptDraft(session.id);
  clearPendingAttachments(session.id);
  commandMenuVisible.value = false;
  scrollMessagesToBottom({ force: true });
  try {
    await InjectRunMessage(runId, sendText);
  } catch (err) {
    const idx = session.messages.indexOf(userMessage);
    if (idx >= 0) session.messages.splice(idx, 1);
    session.messageCount = recountSessionMessages(session);
    message.error(t('app.run.injectFailed', { error: err }));
  }
}

function finalizeStreamingMessageForRun(session, runId) {
  for (let i = session.messages.length - 1; i >= 0; i--) {
    const msg = session.messages[i];
    if (msg.role === 'assistant' && msg.streaming && msg.runId === runId) {
      msg.streaming = false;
      msg.done = true;
      finalizeReasoningTiming(msg);
      return;
    }
  }
}

async function stopRun() {
  const session = activeSession.value;
  if (!session) return;
  try {
    if (session.runId) await CancelRun(session.runId);
    // Manual compaction is not a run: cancel its in-flight summary LLM call too.
    if (compactStateFor(session.id)) await CancelCompaction(session.id);
  } catch (err) {
    message.error(t('app.run.stopFailed', { error: err }));
  }
}

async function chooseWorkspace() {
  try {
    const workspace = await SelectWorkspace();
    if (!workspace) return null;
    config.workspace = workspace;
    configDraft.workspace = workspace;
    const session = activeSession.value;
    if (session) session.workspace = workspace;
    return workspace;
  } catch (err) {
    message.error(t('app.workspace.selectFailed', { error: err }));
    return null;
  }
}

async function onSettingsSave(draftData, silent = false) {
  // The settings pages never edit the workspace — it belongs to the active
  // workspace Tab and only changes through tab switching / the workspace
  // picker. The settings draft is a snapshot synced once (to preserve unsaved
  // edits while the panel stays mounted), so force the live value in here as
  // the single choke point: a stale draft workspace can otherwise flow through
  // assignConfig + syncConfigToActiveTab and hijack the current Tab.
  const liveWorkspace = String(config.workspace || '');
  if (String(draftData?.workspace ?? '') !== liveWorkspace) {
    draftData = { ...draftData, workspace: liveWorkspace };
  }
  const previousKbRoot = String(config.kbRoot || '');
  // draft 的顶层模型字段就是"默认模型"（设置页"使用模型"），直接写入 config；
  // 各 Tab 已选的模型存在 modelByTab 里，与这里互不干扰。仅需把预设的编辑
  // （API key、Base URL 等）同步到正在使用该模型的 Tab 快照上。
  assignConfig(config, draftData);
  assignConfig(configDraft, draftData);
  resyncTabModelsFromPresets();
  applyFontSizes(config);
  try {
    await saveWorkspaceConfig({ ...configDraft });
    syncConfigToActiveTab();
    // A changed KB root invalidates the hidden KB tab: rebuild it (or drop
    // it when cleared) so the KB mode follows the newly configured root.
    if (previousKbRoot !== String(config.kbRoot || '')) {
      if (kbTab()) {
        closeWorkspaceTab(kbTab().id);
      }
      if (mode.value === 'kb') {
        const fresh = ensureKbTab();
        if (fresh) await switchWorkspaceTab(fresh.id);
      }
    }
    refreshContextTokens(activeSessionId.value);
    if (!silent) message.success(t('app.config.saved'));
  } catch (err) {
    message.error(t('app.config.saveFailed', { error: err }));
  }
}

// Apply the user-configured UI font sizes (px) as CSS variables on the
// document root. The variables are consumed by .message-body, the welcome
// greeting, code content, tool cards and secondary text, so changing them
// takes effect immediately without a rebuild. Zero / invalid values remove
// the variable so the CSS fallback (the default) is used.
function applyFontSizes(cfg) {
  const root = document.documentElement;
  const set = (name, value, min, max, fallback) => {
    const n = Number(value);
    if (!Number.isFinite(n) || n <= 0) {
      root.style.removeProperty(name);
      return;
    }
    root.style.setProperty(name, `${Math.min(max, Math.max(min, n))}px`);
  };
  set('--ally-message-font-size', cfg?.messageFontSize, 12, 24, 15.5);
  set('--ally-code-font-size', cfg?.codeFontSize, 12, 24, 14);
  set('--ally-tool-font-size', cfg?.toolFontSize, 12, 24, 15);
  set('--ally-sub-font-size', cfg?.subFontSize, 11, 18, 13);
  set('--ally-aux-font-size', cfg?.auxFontSize, 10, 20, 12);
}

// Fetch the stored background image as a data URL. Called once after config
// init and again whenever the user picks or clears an image in Settings
// (select/clear persist immediately on the backend, independent of Save).
async function loadBackgroundImage() {
  try {
    const url = await GetBackgroundImageURL();
    backgroundImageUrl.value = url || '';
  } catch (_) {
    // A failed read should not block startup; fall back to no background.
    backgroundImageUrl.value = '';
  }
}

function onBackgroundChanged() {
  loadBackgroundImage();
}

function onSkillsChanged() {
  refreshSkillState();
}

function onMcpSaved() {
  loadMcpConfig();
}

// Inline style for .chat-layout's scroll container (n-layout content-style).
// The background must be applied to the scroll container, not the n-layout
// root, because naive-ui's n-layout gives its scroll container an opaque
// --n-color that would otherwise cover any background-image set on the root.
// Layered as: a flat dark overlay (rgba(26,26,26, 1-opacity)) on top of the
// user image, both with background-attachment:fixed so the browser paints the
// background once to the viewport instead of per-scroll-frame. When no image
// is set, only the flex layout is returned; the base dark --n-color shows.
const chatLayoutContentStyle = computed(() => {
  const base = { display: 'flex', flexDirection: 'column' };
  const url = backgroundImageUrl.value;
  if (!url) return base;
  const overlay = Math.max(0, Math.min(1, 1 - Number(config.backgroundOpacity) || 0));
  // The scrim matches the active mode's canvas so a custom image stays readable
  // (dark ink text needs a light scrim in light mode, and vice versa).
  const scrim = isLightMode.value ? `rgba(246, 247, 249, ${overlay})` : `rgba(26, 26, 26, ${overlay})`;
  return {
    ...base,
    // With a custom background image the opaque reasoning curtain would show
    // as a solid band, so disable it via the variable consumed by
    // .reasoning-block in style.css (falls back to the surface color otherwise).
    '--reasoning-curtain': 'transparent',
    backgroundImage: `linear-gradient(${scrim}, ${scrim}), url("${url}")`,
    backgroundSize: 'cover, cover',
    backgroundPosition: 'center, center',
    backgroundAttachment: 'fixed, fixed',
    backgroundRepeat: 'no-repeat, no-repeat',
  };
});


const filteredCommands = computed(() => {
  const value = promptText.value;
  if (!value.startsWith('/')) return commands.value;
  const filter = value.toLowerCase();
  return commands.value.filter(cmd => cmd.label.toLowerCase().startsWith(filter));
});

const filteredBuiltin = computed(() => filteredCommands.value.filter(cmd => cmd.special !== 'skill'));
const filteredSkills = computed(() => filteredCommands.value.filter(cmd => cmd.special === 'skill'));

function handlePromptInput() {
  const value = promptText.value;
  if (value.startsWith('/')) {
    closeFileMentionMenu();
    commandMenuVisible.value = true;
    selectedCommandIndex.value = 0;
    commandHistoryIndex.value = -1;
    sessionsVisible.value = false;
  } else {
    commandMenuVisible.value = false;
    nextTick(() => updateFileMentionFromCaret());
  }
}

function getPromptTextarea(tabId = activeWorkspaceId.value) {
  const root = promptInputRefs[tabId]?.$el || promptInputRefs[tabId];
  return root?.querySelector?.('textarea[data-ally-prompt-input="true"], textarea') || null;
}

// Keep Naive UI's textarea autosize mirror in sync when a prompt is cleared by
// sending. The value is controlled by Vue, but the component deliberately
// skips some controlled-value watcher updates after a native input event via
// its internal syncSource guard. Replaying the normal input path after Vue has
// rendered the reactive clear updates both the textarea and its hidden mirror,
// so a long draft cannot leave the composer at maxRows.
function clearPromptDraft(sessionId, tabId = activeWorkspaceId.value) {
  if (!sessionId) return;
  sessionPromptTexts[sessionId] = '';
  nextTick(() => {
    // Do not overwrite a new draft typed before this cleanup callback runs.
    if (sessionPromptTexts[sessionId] !== '') return;
    const textarea = getPromptTextarea(tabId);
    if (!textarea) return;
    textarea.value = '';
    textarea.dispatchEvent(new Event('input', { bubbles: true }));
  });
}

function detectFileMention(text, caret) {
  if (!Number.isFinite(caret) || caret < 0) return null;
  const at = text.lastIndexOf('@', Math.max(0, caret - 1));
  if (at < 0) return null;
  if (at > 0 && !/[\s([{,;:]/.test(text[at - 1])) return null;
  const raw = text.slice(at + 1, caret);
  if (raw.includes('\n') || raw.length > 180) return null;
  if (raw.startsWith('"')) {
    const query = raw.slice(1);
    if (query.includes('"')) return null;
    return { start: at, end: caret, query, quoted: true };
  }
  if (raw.startsWith("'")) {
    const query = raw.slice(1);
    if (query.includes("'")) return null;
    return { start: at, end: caret, query, quoted: true };
  }
  if (/\s/.test(raw)) return null;
  return { start: at, end: caret, query: raw, quoted: false };
}

function updateFileMentionFromCaret(force = false) {
  if (promptComposing.value || commandMenuVisible.value || sessionsVisible.value) return;
  const textarea = getPromptTextarea();
  if (!textarea) {
    closeFileMentionMenu();
    return;
  }
  const range = detectFileMention(promptText.value, textarea.selectionStart ?? promptText.value.length);
  if (!range) {
    closeFileMentionMenu();
    return;
  }
  fileMentionRange.value = range;
  fileMentionVisible.value = true;
  fileMentionSelectedIndex.value = 0;
  scheduleFileMentionSearch(range.query, force);
}

function scheduleFileMentionSearch(query, force = false) {
  if (fileMentionTimer) window.clearTimeout(fileMentionTimer);
  if (fileMentionEntries.value.length === 0) fileMentionLoading.value = true;
  fileMentionTimer = window.setTimeout(() => {
    void searchFileMentions(query, force);
  }, 90);
}

async function searchFileMentions(query, force = false) {
  const requestId = ++fileMentionRequestId;
  fileMentionLoading.value = true;
  try {
    const result = await SearchWorkspacePaths({ workspace: currentWorkspacePath(), query, limit: 30, force });
    if (requestId !== fileMentionRequestId) return;
    fileMentionEntries.value = Array.isArray(result?.entries) ? result.entries : [];
    fileMentionMeta.value = result ? `${result.count || 0}/${result.total || 0} · ${result.source || '-'} · ${result.buildDurationMs || 0}ms` : '';
    if (fileMentionSelectedIndex.value >= fileMentionEntries.value.length) fileMentionSelectedIndex.value = 0;
  } catch (err) {
    if (requestId !== fileMentionRequestId) return;
    fileMentionEntries.value = [];
    fileMentionMeta.value = String(err || '');
  } finally {
    if (requestId === fileMentionRequestId) fileMentionLoading.value = false;
  }
}

function closeFileMentionMenu() {
  if (fileMentionTimer) window.clearTimeout(fileMentionTimer);
  fileMentionTimer = 0;
  fileMentionRequestId++;
  fileMentionVisible.value = false;
  fileMentionLoading.value = false;
  fileMentionEntries.value = [];
  fileMentionMeta.value = '';
  fileMentionRange.value = null;
  fileMentionSelectedIndex.value = 0;
}

function formatFileMentionPath(entry) {
  const rawPath = String(entry?.path || '').replace(/\\/g, '/');
  const path = entry?.dir && rawPath && !rawPath.endsWith('/') ? `${rawPath}/` : rawPath;
  if (/[\s"'@]/.test(path)) return `@"${path.replace(/"/g, '\\"')}"`;
  return `@${path}`;
}

function applyFileMention(index = fileMentionSelectedIndex.value) {
  const entry = fileMentionEntries.value[index];
  const range = fileMentionRange.value;
  if (!entry || !range) return;
  const before = promptText.value.slice(0, range.start);
  const after = promptText.value.slice(range.end);
  const replacement = formatFileMentionPath(entry);
  const suffix = after.startsWith(' ') || after.startsWith('\n') ? '' : ' ';
  const nextValue = `${before}${replacement}${suffix}${after}`;
  const caret = before.length + replacement.length + suffix.length;
  promptText.value = nextValue;
  closeFileMentionMenu();
  nextTick(() => {
    const textarea = getPromptTextarea();
    textarea?.focus?.();
    textarea?.setSelectionRange?.(caret, caret);
  });
}

function handlePromptCursorActivity() {
  nextTick(() => updateFileMentionFromCaret());
}

function handlePromptKeyup(event) {
  if (!['ArrowLeft', 'ArrowRight', 'Home', 'End', 'PageUp', 'PageDown'].includes(event.key)) return;
  handlePromptCursorActivity();
}

function handlePromptCompositionStart() {
  promptComposing.value = true;
}

function handlePromptCompositionEnd() {
  promptComposing.value = false;
  promptCompositionEndedAt = performance.now();
}

function handlePromptKeydown(event) {
  // macOS WebKit may emit Enter just after compositionend and report isComposing=false.
  // keyCode 229 and the short post-composition guard cover those event-order variants.
  if (event.key === 'Enter') {
    if (promptComposing.value || event.isComposing) {
      // 输入法组合中：Enter 由输入法消费为确认键，不拦截也不产生换行。
      return;
    }
    if (event.keyCode === 229) {
      // 组合已结束但 keyCode 仍残留 229 的幽灵 Enter：吞掉，防止多出一个换行。
      event.preventDefault();
      return;
    }
    if (performance.now() - promptCompositionEndedAt < 100) {
      // 输入法确认后紧跟的幽灵 Enter（isComposing=false）：吞掉并 preventDefault，
      // 既不误发送也不产生换行；时间戳清零实现只吞一次，紧接着的第二次 Enter
      // 恢复正常发送。
      event.preventDefault();
      promptCompositionEndedAt = 0;
      return;
    }
  }

  if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === 'c') {
    // Only intercept Ctrl+C as interrupt when there is no text selection to copy
    const hasSelection = window.getSelection?.().toString().length > 0;
    const s = activeSession.value;
    if (s && s.runId && !hasSelection) {
      event.preventDefault();
      stopRun();
      return;
    }
  }
  if (event.key === '/' && promptText.value.trim() === '' && !event.ctrlKey && !event.metaKey) {
    closeFileMentionMenu();
    commandMenuVisible.value = true;
    selectedCommandIndex.value = 0;
    return;
  }
  if (fileMentionVisible.value) {
    const total = fileMentionEntries.value.length;
    if (event.key === 'ArrowDown') {
      event.preventDefault();
      if (total > 0) fileMentionSelectedIndex.value = (fileMentionSelectedIndex.value + 1) % total;
      return;
    }
    if (event.key === 'ArrowUp') {
      event.preventDefault();
      if (total > 0) fileMentionSelectedIndex.value = (fileMentionSelectedIndex.value - 1 + total) % total;
      return;
    }
    if (event.key === 'Tab') {
      event.preventDefault();
      if (total > 0) applyFileMention(fileMentionSelectedIndex.value);
      return;
    }
    if (event.key === 'Enter' && !event.shiftKey) {
      if (total > 0 || fileMentionLoading.value) {
        event.preventDefault();
        if (total > 0) applyFileMention(fileMentionSelectedIndex.value);
        return;
      }
      closeFileMentionMenu();
    }
    if (event.key === 'Escape') {
      event.preventDefault();
      closeFileMentionMenu();
      return;
    }
  }
  if (commandMenuVisible.value) {
    const filtered = filteredCommands.value;
    if (filtered.length === 0) {
      if (event.key === 'Enter' && !event.shiftKey) {
        event.preventDefault();
        commandMenuVisible.value = false;
        sendPrompt();
      }
      if (event.key === 'Escape') {
        event.preventDefault();
        commandMenuVisible.value = false;
      }
      return;
    }
    if (event.key === 'ArrowDown') {
      event.preventDefault();
      selectedCommandIndex.value = (selectedCommandIndex.value + 1) % filtered.length;
      return;
    }
    if (event.key === 'ArrowUp') {
      event.preventDefault();
      selectedCommandIndex.value = (selectedCommandIndex.value - 1 + filtered.length) % filtered.length;
      return;
    }
    if (event.key === 'Enter') {
      event.preventDefault();
      applyCommand(selectedCommandIndex.value);
      return;
    }
    if (event.key === 'Tab') {
      event.preventDefault();
      completeCommand(selectedCommandIndex.value);
      return;
    }
    if (event.key === 'Escape') {
      event.preventDefault();
      commandMenuVisible.value = false;
      return;
    }
    if (event.key === 'Backspace' && promptText.value === '/') {
      commandMenuVisible.value = false;
      return;
    }
    return;
  }
  if (sessionsVisible.value) {
    const total = currentWorkspaceSessions.value.length;
    if (total === 0) return;
    if (event.key === 'ArrowDown') {
      event.preventDefault();
      sessionsSelectedIndex.value = (sessionsSelectedIndex.value + 1) % total;
      return;
    }
    if (event.key === 'ArrowUp') {
      event.preventDefault();
      sessionsSelectedIndex.value = (sessionsSelectedIndex.value - 1 + total) % total;
      return;
    }
    if (event.key === 'Enter' || event.key === 'Tab') {
      event.preventDefault();
      selectSession(sessionsSelectedIndex.value);
      return;
    }
    if (event.key === 'Escape') {
      event.preventDefault();
      sessionsVisible.value = false;
      return;
    }
    if (event.key === 'Delete') {
      event.preventDefault();
      deleteSession(sessionsSelectedIndex.value);
      return;
    }
    return;
  }
  // Command history navigation (up/down when menu is closed)
  if (event.key === 'ArrowUp' && !event.shiftKey && (promptText.value.trim() === '' || commandHistoryIndex.value !== -1) && commandHistory.value.length > 0) {
    event.preventDefault();
    const hist = commandHistory.value;
    const newIdx = commandHistoryIndex.value === -1 ? hist.length - 1 : Math.max(0, commandHistoryIndex.value - 1);
    commandHistoryIndex.value = newIdx;
    promptText.value = hist[newIdx];
    return;
  }
  if (event.key === 'ArrowDown' && !event.shiftKey && commandHistoryIndex.value !== -1) {
    event.preventDefault();
    const newIdx = commandHistoryIndex.value + 1;
    if (newIdx >= commandHistory.value.length) {
      commandHistoryIndex.value = -1;
      promptText.value = '';
    } else {
      commandHistoryIndex.value = newIdx;
      promptText.value = commandHistory.value[newIdx];
    }
    return;
  }
  if (event.key === 'Enter' && !event.shiftKey) {
    event.preventDefault();
    sendPrompt();
  }
}

function applyCommand(index) {
  const filtered = filteredCommands.value;
  const command = filtered[index];
  if (!command) return;
  if (command.special === 'skill') {
    // Activate skill immediately
    promptText.value = command.label;
    commandMenuVisible.value = false;
    sendPrompt();
    return;
  }
  if (command.special) {
    handleBuiltinCommand(command);
    return;
  }
  promptText.value = command.label;
  commandMenuVisible.value = false;
  sendPrompt();
}

async function handleBuiltinCommand(command) {
  if (!command) return false;
  if (command.special === 'new') {
    createNewSession();
    promptText.value = '';
    commandMenuVisible.value = false;
    return true;
  }
  if (command.special === 'skills') {
    await loadAndShowSkills();
    promptText.value = '';
    commandMenuVisible.value = false;
    return true;
  }
  if (command.special === 'sessions') {
    showSessionList();
    promptText.value = '';
    commandMenuVisible.value = false;
    return true;
  }
  if (command.special === 'init') {
    handleInitCommand();
    promptText.value = '';
    commandMenuVisible.value = false;
    return true;
  }
  if (command.special === 'remember') {
    handleRememberCommand();
    promptText.value = '';
    commandMenuVisible.value = false;
    return true;
  }
  if (command.special === 'lesson') {
    handleLessonCommand();
    promptText.value = '';
    commandMenuVisible.value = false;
    return true;
  }
  if (command.special === 'review') {
    handleReviewCommand();
    promptText.value = '';
    commandMenuVisible.value = false;
    return true;
  }
  if (command.special === 'compact') {
    await handleCompactCommand();
    promptText.value = '';
    commandMenuVisible.value = false;
    return true;
  }
  if (command.special === 'push') {
    handlePushCommand();
    promptText.value = '';
    commandMenuVisible.value = false;
    return true;
  }
  return false;
}

function completeCommand(index) {
  const filtered = filteredCommands.value;
  const command = filtered[index];
  if (!command) return;
  if (command.special === 'skill') {
    promptText.value = command.label;
  } else {
    promptText.value = command.label;
  }
  commandMenuVisible.value = false;
  nextTick(() => focusPromptInput());
}

function appendToolEventFallback(session, data = {}, status = 'running') {
  if (!session) return null;
  const eventId = toolEventId(data);
  const title = makeToolResultTitle(data.name, data.result, data) || makeToolTitle(data.name, data.args || '', data);
  const scheduledAction = isActionKeyedToolName(data.name)
    ? (parseToolArgsBestEffort(data.args || '').action || '')
    : '';
  const payload = {
    role: 'tool_call',
    eventId,
    runId: data.runId || '',
    toolBatchId: data.toolBatchId || '',
    toolCallId: data.toolCallId || '',
    toolCallIndex: data.toolCallIndex,
    name: data.name || 'tool',
    title,
    body: '',
    time: new Date().toLocaleTimeString(),
    durationMs: Number(data.durationMs || 0),
    durationText: formatDurationShort(data.durationMs),
    status: normalizeToolStatus(status),
    kind: toolKind(data.name),
    mcpServer: data.mcpServer || '',
    mcpTool: data.mcpTool || '',
    errorCode: data.errorCode || '',
    scheduledAction,
    expanded: !isToolCollapsedByDefault(data.name),
    chip: '',
    editOldString: '',
    editNewString: '',
    editFilePath: '',
    editStats: '',
    editAdded: 0,
    editRemoved: 0,
    editDiff: '',
    editWarnings: [],
    editChangedLinesBlock: '',
    codeContent: '',
    waitSeconds: 0,
    waitStartedAt: 0,
    askId: '',
    askQuestions: [],
    askReady: false,
    askSubmitting: false,
    askSubmitted: false,
    askAnswers: [],
    ...((data.name === 'subagent' || data.name === 'agent_delegate') ? {
      subagentId: '',
      description: title || '',
      profile: 'coder',
      subagentRole: data.role || '',
      steps: 0,
      summary: '',
      filesRead: [],
      filesEdited: [],
      error: '',
      toolCalls: [],
      inputTokens: 0,
      outputTokens: 0,
      totalTokens: 0,
    } : {}),
  };
  session.messages.push(payload);
  return payload;
}

function parseToolResultData(result) {
  try {
    const parsed = JSON.parse(String(result || ''));
    return parsed?.data && typeof parsed.data === 'object' ? parsed.data : (parsed && typeof parsed === 'object' ? parsed : {});
  } catch (_) {
    return {};
  }
}

function normalizeTodoEntries(value) {
  if (!Array.isArray(value)) return [];
  return value
    .filter((todo) => todo && typeof todo === 'object' && String(todo.title || '').trim())
    .map((todo) => ({
      title: String(todo.title || '').trim(),
      status: ['pending', 'in_progress', 'done'].includes(todo.status) ? todo.status : 'pending',
    }));
}

function formatTodoNextStep(value) {
  const todos = normalizeTodoEntries(value);
  const next = todos.find((todo) => todo.status === 'in_progress')
    || todos.find((todo) => todo.status === 'pending');
  if (next) return next.title;
  return todos.length > 0 ? t('tools.plan.status.done') : t('tools.plan.cleared');
}

function makeToolResultTitle(name, result, meta = {}) {
  const d = parseToolResultData(result);
  if (name === 'plan' && Array.isArray(d.todos)) return formatTodoNextStep(d.todos);
	if ((name === 'edit' || name === 'remote_edit') && Array.isArray(d.files)) return d.files.length === 1 ? (d.files[0]?.path || '') : `${d.files.length} files`;
  if (name === 'remote_read' && Array.isArray(d.files)) {
    const paths = d.files.map(f => f && f.path).filter(Boolean);
    const summary = paths.length === 1 ? (paths[0] || '') : `${paths.length} files`;
    const target = meta?.target || d.target || '';
    return target ? `${target} · ${summary}` : summary;
  }
  const path = d.path || d.deleted || '';
  if (path && (name === 'create' || name === 'edit' || name === 'remote_edit' || name === 'remote_create_file' || name === 'delete' || name === 'remote_delete_path')) {
    return d.target ? `${d.target} · ${path}` : path;
  }
  if (name === 'web_fetch' || name === 'http_request') {
    return d.url || d.finalUrl || d.URL || d.FinalURL || '';
  }
  // MCP 卡片的标题在 running 阶段已由 makeToolTitle 生成为参数摘要；
  // result 阶段保持原样（返回空串不覆盖），避免重复显示 tool 名。
  return '';
}

const PARTIAL_TOOL_ARG_FIELDS = [
  'target',
  'path',
  'content',
  'command',
  'cmd',
  'pattern',
  'glob',
  'expression',
  'description',
  'url',
  'title',
  'html',
  'changes',
  'oldText',
  'oldString',
  'newString',
  'newText',
];

function parseToolArgsBestEffort(raw) {
  const text = String(raw || '');
  if (!text.trim()) return {};
  try {
    const parsed = JSON.parse(text);
    return parsed && typeof parsed === 'object' ? parsed : {};
  } catch (_) {
    const partial = {};
    for (const field of PARTIAL_TOOL_ARG_FIELDS) {
      const found = readPartialJsonStringField(text, field);
      if (found.found) partial[field] = found.value;
    }
    const lines = readPartialJsonStringArrayField(text, 'lines');
    if (lines.found) partial.lines = lines.value;
    return partial;
  }
}

// Share one best-effort argument parse between the title and rich preview
// builders for the same progress event. Output updates carry `output` plus
// the original `args`; they must never parse the growing command output as
// JSON tool arguments.
function parseToolArgsForMeta(raw, meta = {}) {
  const text = String(raw || '');
  if (meta && meta.__parsedToolArgsRaw === text && meta.__parsedToolArgs) {
    return meta.__parsedToolArgs;
  }
  const parsed = parseToolArgsBestEffort(text);
  if (meta && typeof meta === 'object') {
    meta.__parsedToolArgsRaw = text;
    meta.__parsedToolArgs = parsed;
  }
  return parsed;
}

function readPartialJsonStringField(text, field) {
  const needle = `"${field}"`;
  let keyIndex = text.indexOf(needle);
  while (keyIndex >= 0) {
    let i = keyIndex + needle.length;
    while (/\s/.test(text[i] || '')) i++;
    if (text[i] !== ':') {
      keyIndex = text.indexOf(needle, keyIndex + needle.length);
      continue;
    }
    i++;
    while (/\s/.test(text[i] || '')) i++;
    if (text[i] !== '"') return { found: false, value: '' };
    return readPartialJsonString(text, i);
  }
  return { found: false, value: '' };
}

function readPartialJsonStringArrayField(text, field) {
  const needle = `"${field}"`;
  let keyIndex = text.indexOf(needle);
  while (keyIndex >= 0) {
    let i = keyIndex + needle.length;
    while (/\s/.test(text[i] || '')) i++;
    if (text[i] !== ':') {
      keyIndex = text.indexOf(needle, keyIndex + needle.length);
      continue;
    }
    i++;
    while (/\s/.test(text[i] || '')) i++;
    if (text[i] !== '[') return { found: false, value: [] };
    i++;
    const value = [];
    while (i < text.length) {
      while (/[\s,]/.test(text[i] || '')) i++;
      if (text[i] === ']') return { found: true, value, complete: true };
      if (text[i] !== '"') return value.length ? { found: true, value, complete: false } : { found: false, value: [] };
      const item = readPartialJsonString(text, i);
      if (!item.found) return value.length ? { found: true, value, complete: false } : { found: false, value: [] };
      value.push(item.value);
      i = item.nextIndex || text.length;
      if (!item.complete) return { found: true, value, complete: false };
    }
    return { found: true, value, complete: false };
  }
  return { found: false, value: [] };
}

function readPartialJsonString(text, quoteIndex) {
  let value = '';
  for (let i = quoteIndex + 1; i < text.length; i++) {
    const ch = text[i];
    if (ch === '"') return { found: true, value, complete: true, nextIndex: i + 1 };
    if (ch !== '\\') {
      value += ch;
      continue;
    }
    i++;
    if (i >= text.length) return { found: true, value, complete: false, nextIndex: i };
    const esc = text[i];
    if (esc === 'n') value += '\n';
    else if (esc === 'r') value += '\r';
    else if (esc === 't') value += '\t';
    else if (esc === 'b') value += '\b';
    else if (esc === 'f') value += '\f';
    else if (esc === 'u') {
      const hex = text.slice(i + 1, i + 5);
      if (/^[0-9a-fA-F]{4}$/.test(hex)) {
        value += String.fromCharCode(parseInt(hex, 16));
        i += 4;
      } else {
        return { found: true, value, complete: false, nextIndex: i };
      }
    } else {
      value += esc;
    }
  }
  return { found: true, value, complete: false, nextIndex: text.length };
}

function countPreviewLines(text) {
  if (!text) return 0;
  const lines = String(text).replace(/\r\n/g, '\n').split('\n');
  if (lines.length > 1 && lines[lines.length - 1] === '') lines.pop();
  return lines.length;
}

function formatCreateDraftChip(content) {
  const lines = countPreviewLines(content);
  const bytes = new Blob([String(content || '')]).size;
  const parts = [];
  if (lines > 0) parts.push(lines + ' line' + (lines !== 1 ? 's' : ''));
  if (bytes > 0) parts.push(formatBytes(bytes));
  return parts.length ? '\u00B7 ' + parts.join(' \u00B7 ') : '';
}

function displayToolBodyForStatus(name, body, status) {
  if (normalizeToolStatus(status) === 'running') return '';
  return body;
}

function updateToolEvent(id, name, title, body, status = 'default', meta = {}, targetSession = null) {
  const session = targetSession || activeSession.value;
  if (!session) return;
  const eventId = id || `${name}-${Date.now()}-${Math.random()}`;
  const existing = findToolEventById(session, eventId);

  // Rich display data
  let editOldString = '';
  let editNewString = '';
  let editFilePath = '';
  let editStats = '';
  let editAdded = 0;
  let editRemoved = 0;
	let editEntries = existing?.editEntries || [];
  let codeContent = '';
  let htmlContent = existing?.htmlContent || '';
  let chip = existing?.chip || '';
  let waitSeconds = Number(existing?.waitSeconds || 0);
  let waitStartedAt = Number(existing?.waitStartedAt || 0);
  let askQuestions = existing?.askQuestions || [];
  const expanded = existing ? existing.expanded : !isToolCollapsedByDefault(name);

  const isLiveOutput = meta && meta.output !== undefined;
  const raw = isLiveOutput ? String(meta.args || existing?.toolArgs || '') : String(body || '');
  // edit 耗时基准：UI 收到工具参数（tool:start/update 携带 args）的时刻。
  // 后端 durationMs 只测执行段，不含参数流式到达的时间，显示偏短。
  const uiStartedAt = existing?.uiStartedAt
    || (normalizeToolStatus(status) === 'running' && String(raw || '').trim() ? Date.now() : 0);
  const parsed = parseToolArgsForMeta(raw, meta);
  if (name === 'edit' || name === 'remote_edit') {
	const files = (name === 'edit' || name === 'remote_edit') && Array.isArray(parsed.files) ? parsed.files : [parsed];
	editFilePath = files.length === 1 ? (files[0]?.path || '') : `${files.length} files`;
	const changes = files.flatMap((file) => Array.isArray(file?.changes) ? file.changes : []);
	editEntries = files.map((file, index) => ({
	  path: file?.path || '', changes: Array.isArray(file?.changes) ? file.changes : [],
	  diff: existing?.editEntries?.[index]?.diff || '', added: existing?.editEntries?.[index]?.added || 0,
	  removed: existing?.editEntries?.[index]?.removed || 0,
	}));
    if (changes.length > 0) {
      editOldString = changes.map((change) => String(change?.oldText || '')).join('\n');
      editNewString = changes.map((change) => String(change?.newText || '')).join('\n');
	  chip = files.length > 1 ? `${files.length} files` : '';
    } else if (parsed.startLine && Object.prototype.hasOwnProperty.call(parsed, 'newText')) {
      editNewString = parsed.newText || '';
      const end = parsed.endLine || parsed.startLine;
      chip = `lines ${parsed.startLine}-${end}`;
    } else {
      if (!editOldString && parsed.oldString) editOldString = parsed.oldString;
      if (!editNewString && parsed.newString) editNewString = parsed.newString;
    }
    if (editOldString || editNewString) {
      const stats = computeEditStats(editOldString, editNewString, normalizeToolStatus(status) === 'running');
      editStats = formatEditStats(stats);
      editAdded = stats.added;
      editRemoved = stats.removed;
    }
    if (editEntries.length > 0) {
      editOldString = '';
      editNewString = '';
    }
  } else if (name === 'replace_exact') {
    editOldString = parsed.oldString || '';
    editNewString = parsed.newString || '';
    editFilePath = parsed.path || '';
    if (editOldString || editNewString) {
      const stats = computeEditStats(editOldString, editNewString, normalizeToolStatus(status) === 'running');
      editStats = formatEditStats(stats);
      editAdded = stats.added;
      editRemoved = stats.removed;
    }
  } else if (name === 'replace_lines') {
    codeContent = parsed.newText || '';
    editFilePath = parsed.path || '';
  } else if (name === 'create' || name === 'remote_create_file') {
    codeContent = parsed.content || '';
    editFilePath = parsed.path || '';
    if (codeContent && normalizeToolStatus(status) === 'running') {
      chip = formatCreateDraftChip(codeContent);
    }
  } else if (name === 'wait') {
    waitSeconds = Number(parsed.seconds || waitSeconds || 0);
    if (normalizeToolStatus(status) === 'running' && !waitStartedAt) waitStartedAt = Date.now();
  } else if (name === 'ask' && Array.isArray(parsed.questions)) {
    askQuestions = parsed.questions;
  } else if (name === 'render_html') {
    htmlContent = parsed.html || htmlContent;
    title = parsed.title || title;
  }

  const payload = {
    role: 'tool_call',
    eventId,
    runId: meta.runId || existing?.runId || '',
    toolBatchId: meta.toolBatchId || existing?.toolBatchId || '',
    toolCallId: meta.toolCallId || existing?.toolCallId || '',
    toolCallIndex: meta.toolCallIndex ?? existing?.toolCallIndex,
    name: name || 'tool',
    title: title || existing?.title || '',
    body: (name === 'command' || name === 'remote_run_command') && meta.output !== undefined
      ? stripAnsi(String(meta.output || ''))
      : formatToolBody(name, displayToolBodyForStatus(name, body, status)),
    time: new Date().toLocaleTimeString(),
    durationMs: existing?.durationMs || Number(meta.durationMs || 0),
    durationText: existing?.durationText || formatDurationShort(meta.durationMs),
    uiStartedAt,
    status: normalizeToolStatus(status),
    kind: toolKind(name),
    mcpServer: meta.mcpServer || existing?.mcpServer || '',
    mcpTool: meta.mcpTool || existing?.mcpTool || '',
    // Rich display data
    expanded,
    chip,
    editOldString,
    editNewString,
    editFilePath,
    editStats,
    editAdded,
    editRemoved,
	editEntries,
    editDiff: '',
    editWarnings: [],
    editChangedLinesBlock: '',
    codeContent,
    htmlContent,
    waitSeconds,
    waitStartedAt,
    askId: existing?.askId || '',
    askQuestions,
    askReady: existing?.askReady || false,
    askSubmitting: existing?.askSubmitting || false,
    askSubmitted: existing?.askSubmitted || false,
    askAnswers: existing?.askAnswers || [],
    // scheduled_task / service card verbs depend on the action; capture it (args may be
    // absent on an early tool:start, so fall back to the existing value).
    scheduledAction: isActionKeyedToolName(name) ? (parsed.action || existing?.scheduledAction || '') : (existing?.scheduledAction || ''),
    ...((name === 'subagent' || name === 'agent_delegate') ? {
      subagentId: existing?.subagentId || '',
      description: parsed.description || existing?.description || parsed.task || '',
      profile: existing?.profile || 'coder',
      subagentRole: existing?.subagentRole || parsed.role || '',
      steps: existing?.steps || 0,
      maxSteps: existing?.maxSteps || parsed.maxSteps || 0,
      summary: existing?.summary || '',
      filesRead: existing?.filesRead || [],
      filesEdited: existing?.filesEdited || [],
      error: existing?.error || '',
      toolCalls: existing?.toolCalls || [],
      inputTokens: existing?.inputTokens || 0,
      outputTokens: existing?.outputTokens || 0,
      totalTokens: existing?.totalTokens || 0,
    } : {}),
  };

  if (existing) {
    // Preserve rich display data from initial creation
    if (!payload.editOldString && existing.editOldString) payload.editOldString = existing.editOldString;
    if (!payload.editNewString && existing.editNewString) payload.editNewString = existing.editNewString;
    if (!payload.editFilePath && existing.editFilePath) payload.editFilePath = existing.editFilePath;
    if (!payload.editStats && existing.editStats) payload.editStats = existing.editStats;
    if (!payload.editAdded && existing.editAdded) payload.editAdded = existing.editAdded;
    if (!payload.editRemoved && existing.editRemoved) payload.editRemoved = existing.editRemoved;
    if (!payload.editDiff && existing.editDiff) payload.editDiff = existing.editDiff;
	if ((!payload.editEntries || payload.editEntries.length === 0) && existing.editEntries) payload.editEntries = existing.editEntries;
    if ((!payload.editWarnings || payload.editWarnings.length === 0) && existing.editWarnings) payload.editWarnings = existing.editWarnings;
    if (!payload.editChangedLinesBlock && existing.editChangedLinesBlock) payload.editChangedLinesBlock = existing.editChangedLinesBlock;
    if (!payload.codeContent && existing.codeContent) payload.codeContent = existing.codeContent;
    if (!payload.htmlContent && existing.htmlContent) payload.htmlContent = existing.htmlContent;
    if (!payload.waitSeconds && existing.waitSeconds) payload.waitSeconds = existing.waitSeconds;
    if (!payload.waitStartedAt && existing.waitStartedAt) payload.waitStartedAt = existing.waitStartedAt;
    if ((!payload.askQuestions || payload.askQuestions.length === 0) && existing.askQuestions) payload.askQuestions = existing.askQuestions;
  }
  // Commit also refreshes the hot-path cache after the first insert. Without
  // that write-back, every later tool:update appended another card with the
  // same eventId, leaving Vue with duplicate keys and tool:result updating the
  // earliest (often still argument-less) card.
  commitToolEventById(session, eventId, existing, payload);
  if (session.id === activeSessionId.value && !meta.suppressScroll) scrollMessagesToBottom();
}

function toggleToolExpand(msg) {
  msg.expanded = !msg.expanded;
}

async function submitAskResponse(sessionId, msg, answers) {
  if (!sessionId || !msg?.askId || msg.askSubmitting || msg.askSubmitted) return;
  msg.askSubmitting = true;
  try {
    await SubmitAskResponse({
      askId: msg.askId,
      sessionId,
      answers,
    });
    msg.askSubmitted = true;
    msg.askReady = false;
    saveSessions();
  } catch (err) {
    msg.askSubmitting = false;
    message.error(t('app.ask.submitFailed', { error: err }));
  }
}

async function loadTodos(sid) {
  if (!sid) return;
  if (Array.isArray(todosBySession[sid])) {
    scrollPlanPanelsForSession(sid);
  }
  try {
    const list = await GetTodos(sid);
    const nextTodos = Array.isArray(list) ? list : [];
    todosBySession[sid] = nextTodos;
    scrollPlanPanelsForSession(sid);
  } catch (_) {
    // Keep an already cached plan visible if the refresh fails.
  }
}

function sortScheduledTasks(tasks) {
  return [...tasks].sort((a, b) => {
    if (!!a?.running !== !!b?.running) return a?.running ? -1 : 1;
    const aNext = Number(a?.nextRunAt || 0);
    const bNext = Number(b?.nextRunAt || 0);
    if (!aNext && !bNext) return Number(b?.updatedAt || 0) - Number(a?.updatedAt || 0);
    if (!aNext) return 1;
    if (!bNext) return -1;
    return aNext - bNext;
  });
}

function applyScheduledTaskEvent(data = {}) {
  if (data.deleted) {
    scheduledTasks.value = scheduledTasks.value.filter((task) => task.id !== data.deleted);
    return;
  }
  const task = data.task;
  if (!task?.id) return;
  const next = scheduledTasks.value.filter((item) => item.id !== task.id);
  next.push(task);
  scheduledTasks.value = sortScheduledTasks(next);
}

async function loadScheduledTasks() {
  scheduledTasksLoading.value = true;
  try {
    scheduledTasks.value = sortScheduledTasks(await ListScheduledTasks() || []);
  } catch (err) {
    message.error(t('app.scheduled.loadFailed', { error: err }));
  } finally {
    scheduledTasksLoading.value = false;
  }
}

function applyServiceEvent(data = {}) {
  const service = data.service;
  if (!service?.id) return;
  if (!['starting', 'running'].includes(service.status)) {
    services.value = services.value.filter((item) => item.id !== service.id);
    return;
  }
  const next = services.value.filter((item) => item.id !== service.id);
  next.push(service);
  services.value = sortServices(next);
}

function sortServices(items) {
  return [...items].sort((a, b) => {
    const aActive = ['starting', 'running'].includes(a?.status);
    const bActive = ['starting', 'running'].includes(b?.status);
    if (aActive !== bActive) return aActive ? -1 : 1;
    return Number(b?.startedAt || 0) - Number(a?.startedAt || 0);
  });
}

async function loadServices() {
  servicesLoading.value = true;
  try {
    const result = await ListServices();
    services.value = sortServices(Array.isArray(result?.services) ? result.services : []);
  } catch (err) {
    message.error(t('app.service.loadFailed', { error: err }));
  } finally {
    servicesLoading.value = false;
  }
}

async function refreshTaskCenter() {
  await Promise.all([loadScheduledTasks(), loadServices()]);
}

async function openTaskCenter() {
  taskCenterVisible.value = true;
  await refreshTaskCenter();
}

async function stopManagedService(id) {
  if (!id || serviceStoppingIds.value.includes(id)) return;
  serviceStoppingIds.value = [...serviceStoppingIds.value, id];
  try {
    const service = await StopService({ id });
    applyServiceEvent({ service });
    message.success(t('app.service.stopped'));
  } catch (err) {
    message.error(t('app.service.stopFailed', { error: err }));
  } finally {
    serviceStoppingIds.value = serviceStoppingIds.value.filter((item) => item !== id);
  }
}

async function deleteScheduledTask(id) {
  if (!id || scheduledTaskDeletingIds.value.includes(id)) return;
  scheduledTaskDeletingIds.value = [...scheduledTaskDeletingIds.value, id];
  try {
    await DeleteScheduledTask(id);
    scheduledTasks.value = scheduledTasks.value.filter((task) => task.id !== id);
    message.success(t('app.scheduled.deleted'));
  } catch (err) {
    message.error(t('app.scheduled.deleteFailed', { error: err }));
  } finally {
    scheduledTaskDeletingIds.value = scheduledTaskDeletingIds.value.filter((item) => item !== id);
  }
}

// ── Session persistence ──

const MAX_MODEL_HISTORY_MESSAGES = 400;
const MAX_RUNTIME_SESSIONS = 200;
const MAX_RUNTIME_RENDERABLE_MESSAGES = 260;
const MAX_STORED_MESSAGE_CHARS = 60000;
const MAX_STORED_TOOL_BODY_CHARS = 20000;
let sessionWriteTail = Promise.resolve();

function enqueueSessionWrite(operation) {
  const result = sessionWriteTail.catch(() => {}).then(operation);
  sessionWriteTail = result.catch(() => {});
  return result;
}

function flushSessionWrites() {
  return sessionWriteTail;
}

function sessionIndexEntry(session) {
  const loaded = session?.messagesLoaded !== false;
  const messages = loaded && Array.isArray(session?.messages) ? session.messages : [];
  return {
    id: session?.id || '',
    title: session?.title || t('app.sessions.new'),
    firstPrompt: firstSessionPromptSummary(session),
    workspace: inferSessionWorkspace(session) || (session?.id === activeSessionId.value ? config.workspace || '' : ''),
    extraRoots: Array.isArray(session?.extraRoots) ? [...session.extraRoots] : [],
    createdAt: session?.createdAt || Date.now(),
    updatedAt: session?.updatedAt || session?.createdAt || Date.now(),
    messageCount: Number.isFinite(Number(session?.messageCount))
      ? Number(session.messageCount)
      : recountSessionMessages({ messages }),
    hasSnapshot: !!session?.hasSnapshot,
  };
}

// The session index and conversation snapshots live in the backend's local
// files. This function only writes lightweight metadata; message bodies are
// written by persistCompletedSession after a successful run.
function saveSessions() {
  trimRuntimeSessions();
  const session = activeSession.value;
  if (!session || session.runId || session.isRunning || !session.id || !session.hasSnapshot) return;
  const entry = sessionIndexEntry(session);
  enqueueSessionWrite(() => SaveSessionIndex(entry)).catch(() => {});
}

function buildCompletedSessionSnapshot(session) {
  const snapshot = {
    ...sessionIndexEntry(session),
    messages: sanitizeStoredMessages(session.messages || []),
  };
  return JSON.parse(JSON.stringify(snapshot));
}

function persistCompletedSession(session) {
  if (!session?.id || session.runId || session.isRunning) return Promise.resolve();
  const snapshot = buildCompletedSessionSnapshot(session);
  return enqueueSessionWrite(() => SaveSession(snapshot)).then(() => {
    session.hasSnapshot = true;
    session.messagesLoaded = true;
    session.messageCount = Number(snapshot.messageCount || 0);
  }).catch(() => {
    // Keep the previous completed snapshot when a disk write fails.
  });
}

// Frequent in-run updates only refresh lightweight metadata. Message bodies
// are intentionally not persisted until a complete run succeeds.
const SAVE_SESSIONS_DEBOUNCE_MS = 400;
let saveSessionsTimer = 0;
function scheduleSaveSessions() {
  if (saveSessionsTimer) window.clearTimeout(saveSessionsTimer);
  saveSessionsTimer = window.setTimeout(() => {
    saveSessionsTimer = 0;
    saveSessions();
  }, SAVE_SESSIONS_DEBOUNCE_MS);
}
function unloadInactiveSessionMessages() {
  const openSessionIds = new Set(workspaceTabs.value.map((tab) => tab.sessionId).filter(Boolean));
  for (const session of sessions.value) {
    // Every open workspace Tab owns a mounted ChatMessages tree. Keep those
    // sessions loaded so display-directive="show" can preserve the DOM and the
    // browser's native scroll position while another Tab is active.
    if (!session || openSessionIds.has(session.id) || !session.hasSnapshot || sessionMayHaveBackgroundRun(session) || session.messagesLoaded === false) continue;
    for (const message of session.messages || []) releaseMessageAttachments(message);
    session.messages = [];
    session.messagesLoaded = false;
    displayMessagesCacheBySession.delete(session.id);
  }
}

function trimRuntimeSessions() {
  // A Tab must never point at an evicted/missing session. Repair legacy or
  // partially persisted state before calculating the protected set.
  for (const tab of workspaceTabs.value) ensureWorkspaceTabSession(tab);
  for (const session of sessions.value) trimRuntimeSessionMessages(session);
  unloadInactiveSessionMessages();
  if (sessions.value.length <= MAX_RUNTIME_SESSIONS) return;

  const protectedIds = new Set([
    activeSessionId.value,
    ...workspaceTabs.value.map((tab) => tab.sessionId || ''),
    ...sessions.value.filter(sessionMayHaveBackgroundRun).map((session) => session.id),
  ]);
  for (let index = sessions.value.length - 1; index >= 0 && sessions.value.length > MAX_RUNTIME_SESSIONS; index--) {
    const session = sessions.value[index];
    if (protectedIds.has(session.id)) continue;
    releaseSessionAttachments(session);
    delete todosBySession[session.id];
    delete todoRevisionsBySession[session.id];
    delete planPanelCollapsedBySession[session.id];
    delete sessionPromptTexts[session.id];
    deletePendingAttachments(session.id);
    displayMessagesCacheBySession.delete(session.id);
    ReleaseSession(session.id).catch(() => {});
    const expanded = new Set(expandedArchiveSessions.value);
    expanded.delete(session.id);
    expandedArchiveSessions.value = expanded;
    sessions.value.splice(index, 1);
  }
}

function sessionMayHaveBackgroundRun(session) {
  if (!session) return false;
  // isRunning covers the StartChat -> run:start window; runId covers an
  // established backend run. Both must survive runtime eviction even when
  // their workspace Tab is not active.
  return !!(session.isRunning || session.runId);
}

function trimRuntimeSessionMessages(session) {
  if (!session || session.messagesLoaded === false || session.runId || session.isRunning) return;
  const messages = Array.isArray(session.messages) ? session.messages : [];
  const recentRenderable = messages.filter(isRenderableMessage).slice(-MAX_RUNTIME_RENDERABLE_MESSAGES);
  const recentConversation = messages.filter(isModelHistoryMessage).slice(-MAX_MODEL_HISTORY_MESSAGES);
  const keep = new Set([...recentRenderable, ...recentConversation]);
  if (keep.size === messages.length) return;
  for (const msg of messages) {
    if (!keep.has(msg)) releaseMessageAttachments(msg);
  }
  session.messages = messages.filter((msg) => keep.has(msg));
}

function sanitizeStoredMessages(messages) {
  const completed = messages.filter((msg) => !msg?.transientTurn && !msg?.pendingTurn);
  const recentRenderable = completed.filter(isRenderableMessage).slice(-MAX_RUNTIME_RENDERABLE_MESSAGES);
  const recentConversation = completed.filter(isModelHistoryMessage).slice(-MAX_MODEL_HISTORY_MESSAGES);
  const keep = new Set([...recentRenderable, ...recentConversation]);
  return completed.filter((msg) => keep.has(msg)).map(sanitizeStoredMessage);
}

function sanitizeStoredMessage(msg) {
  const next = { ...msg };
  next.content = truncateStoredText(next.content, MAX_STORED_MESSAGE_CHARS, t('app.cache.contentTrimmed'));
  // Reasoning body is never persisted: it is too large for session snapshots
  // and stale thoughts add no value after restore. Only the char count (used
  // for the "Thinking · N tokens" indicator) is kept.
  next.reasoningBody = '';
  // Read tool results hold file contents that go stale quickly and bloat the
  // snapshot; the model can re-read on demand after restore.
  if (next.role === 'tool_call' && (next.name === 'read' || next.name === 'remote_read' || next.name === 'batch_read')) {
    next.body = '';
    next.codeContent = '';
  } else {
    next.body = truncateStoredText(next.body, MAX_STORED_TOOL_BODY_CHARS, t('app.cache.toolTrimmed'));
    next.codeContent = truncateStoredText(next.codeContent, MAX_STORED_TOOL_BODY_CHARS, t('app.cache.previewTrimmed'));
  }
  next.editDiff = truncateStoredText(next.editDiff, MAX_STORED_TOOL_BODY_CHARS, t('app.cache.diffTrimmed'));
  next.editOldString = '';
  next.editNewString = '';
  // Sub-agent inline tool rows: keep only a bounded tail so long sub-agent
  // sessions do not bloat the persisted snapshot. args are the largest field
  // and go stale fast (they mirror the truncated preview shown live).
  if (Array.isArray(next.toolCalls)) {
    const KEEP = 50;
    next.toolCalls = next.toolCalls.slice(-KEEP).map((tc) => ({
      ...tc,
      args: truncateStoredText(tc?.args, 4096, t('app.cache.toolTrimmed')),
      summary: truncateStoredText(tc?.summary, 2048, t('app.cache.toolTrimmed')),
    }));
  }
  if (Array.isArray(next.editEntries)) {
    next.editEntries = next.editEntries.map((entry) => ({
      ...entry,
      changes: [],
      diff: truncateStoredText(entry?.diff, MAX_STORED_TOOL_BODY_CHARS, t('app.cache.diffTrimmed')),
    }));
  }
  if (Array.isArray(next.attachments)) {
    next.attachments = next.attachments.map((att) => {
      const keepPreview = typeof att.previewUrl === 'string'
        && att.previewUrl.startsWith('data:')
        && att.previewUrl.length <= 500000;
      const text = truncateStoredText(att.text, MAX_STORED_ATTACHMENT_TEXT_CHARS, t('app.cache.attachmentTrimmed'));
      return { ...att, previewUrl: keepPreview ? att.previewUrl : '', dataUrl: '', text };
    });
  }
  return next;
}

function truncateStoredText(value, limit, marker) {
  if (typeof value !== 'string' || value.length <= limit) return value || '';
  return `${value.slice(0, limit)}\n\n${marker}`;
}

function sessionFromIndexEntry(entry) {
  return {
    id: entry.id,
    title: entry.title || t('app.sessions.history'),
    firstPrompt: entry.firstPrompt || '',
    workspace: entry.workspace || '',
    extraRoots: Array.isArray(entry.extraRoots) ? [...entry.extraRoots] : [],
    messages: [],
    messagesLoaded: false,
    runId: '',
    isRunning: false,
    hasSnapshot: !!entry.hasSnapshot,
    messageCount: Number(entry.messageCount || 0),
    createdAt: Number(entry.createdAt || Date.now()),
    updatedAt: Number(entry.updatedAt || entry.createdAt || Date.now()),
  };
}

function applySessionIndexEntry(session, entry) {
  if (!session || !entry) return session;
  session.title = entry.title || session.title || t('app.sessions.history');
  session.firstPrompt = entry.firstPrompt || session.firstPrompt || '';
  session.workspace = entry.workspace || session.workspace || '';
  session.extraRoots = Array.isArray(entry.extraRoots) ? [...entry.extraRoots] : (session.extraRoots || []);
  session.hasSnapshot = !!entry.hasSnapshot;
  session.messageCount = Number(entry.messageCount || 0);
  session.createdAt = Number(entry.createdAt || session.createdAt || Date.now());
  session.updatedAt = Number(entry.updatedAt || session.updatedAt || session.createdAt);
  return session;
}

async function refreshSessionIndex() {
  let entries;
  try {
    entries = await ListSessions();
  } catch (err) {
    return false;
  }
  const currentByID = new Map(sessions.value.map((session) => [session.id, session]));
  const entryIDs = new Set();
  const ordered = [];
  for (const entry of Array.isArray(entries) ? entries : []) {
    if (!entry?.id) continue;
    entryIDs.add(entry.id);
    const current = currentByID.get(entry.id);
    if (current) {
      applySessionIndexEntry(current, entry);
      ordered.push(current);
    } else {
      ordered.push(sessionFromIndexEntry(entry));
    }
  }

  const protectedIDs = new Set([
    activeSessionId.value,
    ...workspaceTabs.value.map((tab) => tab.sessionId || ''),
    ...sessions.value.filter(sessionMayHaveBackgroundRun).map((session) => session.id),
  ]);
  for (const session of sessions.value) {
    if (!entryIDs.has(session.id) && protectedIDs.has(session.id)) ordered.unshift(session);
  }
  sessions.value = ordered;
  trimRuntimeSessions();
  return true;
}

async function migrateLegacySessionStorage() {
  let metadata = [];
  try {
    const raw = localStorage.getItem('ally_sessions');
    const saved = raw ? JSON.parse(raw) : [];
    if (Array.isArray(saved)) metadata = saved;
  } catch (_) { /* ignore invalid legacy data */ }

  let snapshots = [];
  try {
    snapshots = await loadSessionSnapshots();
  } catch (_) {
    snapshots = [];
  }
  if (metadata.length === 0 && snapshots.length === 0) return true;

  let backendEntries;
  try {
    backendEntries = await ListSessions();
  } catch (_) {
    return false;
  }
  const backendByID = new Map((backendEntries || []).filter((entry) => entry?.id).map((entry) => [entry.id, entry]));
  const metadataByID = new Map(metadata.filter((entry) => entry?.id).map((entry) => [entry.id, entry]));
  const snapshotIDs = new Set();
  let migrationFailed = false;

  for (const source of snapshots) {
    if (!source?.id) continue;
    snapshotIDs.add(source.id);
    const existing = backendByID.get(source.id);
    if (existing?.hasSnapshot) continue;
    const meta = metadataByID.get(source.id) || {};
    const payload = {
      ...source,
      id: source.id,
      title: meta.title || source.title || t('app.sessions.history'),
      workspace: meta.workspace || source.workspace || '',
      extraRoots: Array.isArray(meta.extraRoots) ? meta.extraRoots : (source.extraRoots || []),
      createdAt: meta.createdAt || source.createdAt,
      updatedAt: meta.updatedAt || source.updatedAt,
    };
    try {
      await SaveSession(JSON.parse(JSON.stringify(payload)));
    } catch (_) {
      migrationFailed = true;
    }
  }

  for (const meta of metadata) {
    if (!meta?.id || snapshotIDs.has(meta.id) || backendByID.has(meta.id)) continue;
    const legacyMessages = Array.isArray(meta.messages) ? meta.messages : [];
    const entry = {
      id: meta.id,
      title: meta.title || t('app.sessions.history'),
      workspace: meta.workspace || '',
      extraRoots: Array.isArray(meta.extraRoots) ? meta.extraRoots : [],
      createdAt: meta.createdAt || Date.now(),
      updatedAt: meta.updatedAt || meta.createdAt || Date.now(),
      messageCount: Number(meta.messageCount || recountSessionMessages({ messages: legacyMessages })),
      hasSnapshot: false,
    };
    try {
      await SaveSessionIndex(entry);
    } catch (_) {
      migrationFailed = true;
    }
  }

  if (migrationFailed) return false;
  try {
    localStorage.removeItem('ally_sessions');
    await clearSessionSnapshotStore();
  } catch (_) {
    // The backend copy is already authoritative; cleanup is best-effort.
  }
  return true;
}

async function loadSavedSessions() {
  await migrateLegacySessionStorage();
  await refreshSessionIndex();
  saveSessions();
}

async function refreshSessionListData() {
  await refreshSessionIndex();

}

function showSessionList() {
  if (sessionsVisible.value) {
    sessionsVisible.value = false;
    return;
  }
  saveSessions();
  sessionsVisible.value = true;
  sessionsSelectedIndex.value = 0;
  commandMenuVisible.value = false;
  void refreshSessionListData();
  nextTick(() => focusPromptInput());
}

async function switchToSession(index) {
  const idx = parseInt(index);
  const visibleSessions = currentWorkspaceSessions.value;
  if (isNaN(idx) || idx < 1 || idx > visibleSessions.length) {
    message.error(t('app.sessions.invalidNumber'));
    return;
  }
  const target = visibleSessions[idx - 1];
  if (!target) return;
  saveSessions();
  await activateSelectedSession(target);
}



function handleInitCommand() {
  const session = activeSession.value;
  if (!session) return;
  // Send the init exploration prompt to the LLM
  session.messages.push({ role: 'user', content: INIT_PROMPT, done: true });
  if (isDefaultSessionTitle(session)) {
    session.title = t('app.init.title');
    markCustomSessionTitle(session);
  }
  scrollMessagesToBottom();
  saveSessions();

  const history = session.messages
    .filter(isModelHistoryMessage)
    .map((msg) => ({ role: msg.role, content: msg.content }));

  const text = INIT_PROMPT;
  markSessionRunning(session);
  StartChat({ sessionId: session.id, message: text, messages: history, config: { ...chatConfig.value, extraRoots: session.extraRoots || [] } })
    .catch((err) => {
      markTransientTurn(session);
      session.runId = '';
      session.isRunning = false;
      pushMessage('assistant', t('app.init.failed', { error: err }), { error: true, transientTurn: true });
    });
}

function handleRememberCommand() {
  const session = activeSession.value;
  if (!session) return;
  const history = session.messages
    .filter(isModelHistoryMessage)
    .map((msg) => ({ role: msg.role, content: msg.content }));
  history.push({ role: 'user', content: REMEMBER_PROMPT });

  session.messages.push({ role: 'user', content: t('app.note.visibleText'), done: true });
  if (isDefaultSessionTitle(session)) {
    session.title = t('app.note.title');
    markCustomSessionTitle(session);
  }
  scrollMessagesToBottom();
  saveSessions();

  markSessionRunning(session);
  StartChat({ sessionId: session.id, message: '', messages: history, config: { ...chatConfig.value, extraRoots: session.extraRoots || [] } })
    .catch((err) => {
      markTransientTurn(session);
      session.runId = '';
      session.isRunning = false;
      pushMessage('assistant', t('app.note.failed', { error: err }), { error: true, transientTurn: true });
    });
}

function handleLessonCommand() {
  const session = activeSession.value;
  if (!session) return;
  const history = session.messages
    .filter(isModelHistoryMessage)
    .map((msg) => ({ role: msg.role, content: msg.content }));
  history.push({ role: 'user', content: LESSON_PROMPT });

  session.messages.push({ role: 'user', content: t('app.lesson.visibleText'), done: true });
  if (isDefaultSessionTitle(session)) {
    session.title = t('app.lesson.title');
    markCustomSessionTitle(session);
  }
  scrollMessagesToBottom();
  saveSessions();

  markSessionRunning(session);
  StartChat({ sessionId: session.id, message: '', messages: history, config: { ...chatConfig.value, extraRoots: session.extraRoots || [] } })
    .catch((err) => {
      markTransientTurn(session);
      session.runId = '';
      session.isRunning = false;
      pushMessage('assistant', t('app.lesson.failed', { error: err }), { error: true, transientTurn: true });
    });
}

function handleReviewCommand() {
  const session = activeSession.value;
  if (!session) return;
  const history = session.messages
    .filter(isModelHistoryMessage)
    .map((msg) => ({ role: msg.role, content: msg.content }));
  history.push({ role: 'user', content: REVIEW_PROMPT });

  session.messages.push({ role: 'user', content: t('app.review.visibleText'), done: true });
  if (isDefaultSessionTitle(session)) {
    session.title = t('app.review.title');
    markCustomSessionTitle(session);
  }
  scrollMessagesToBottom();
  saveSessions();

  markSessionRunning(session);
  StartChat({ sessionId: session.id, message: '', messages: history, config: { ...chatConfig.value, extraRoots: session.extraRoots || [] } })
    .catch((err) => {
      markTransientTurn(session);
      session.runId = '';
      session.isRunning = false;
      pushMessage('assistant', t('app.review.failed', { error: err }), { error: true, transientTurn: true });
    });
}

function handlePushCommand() {
  const session = activeSession.value;
  if (!session) return;
  const history = session.messages
    .filter(isModelHistoryMessage)
    .map((msg) => ({ role: msg.role, content: msg.content }));
  history.push({ role: 'user', content: t('app.push.prompt') });

  session.messages.push({ role: 'user', content: t('app.push.visibleText'), done: true });
  if (isDefaultSessionTitle(session)) {
    session.title = t('app.push.title');
    markCustomSessionTitle(session);
  }
  scrollMessagesToBottom();
  saveSessions();

  markSessionRunning(session);
  StartChat({ sessionId: session.id, message: '', messages: history, config: { ...chatConfig.value, extraRoots: session.extraRoots || [] } })
    .catch((err) => {
      markTransientTurn(session);
      session.runId = '';
      session.isRunning = false;
      pushMessage('assistant', t('app.push.failed', { error: err }), { error: true, transientTurn: true });
    });
}

// Compaction loading uses the same dotted indicator as a running chat
// (composer-run-status-dots) so manual /compact and auto-compaction feel
// like a normal chat run instead of a Naive toast. State is keyed per
// session: a background tab's compaction must never surface in another
// tab's composer. Progress text streams from the backend via
// compact:start / compact:progress / compact:done events.
const compactingSessions = reactive({});
function compactStateFor(sid) { return compactingSessions[sid] || null; }
const activeCompactState = computed(() => compactStateFor(activeSessionId.value));
const compactLoadingActive = computed(() => !!activeCompactState.value);
function setCompactProgress(sid, text) {
  if (!sid) return;
  compactingSessions[sid] = { ...(compactingSessions[sid] || {}), text };
}
function closeCompactLoading(sid) {
  const target = sid || activeSessionId.value;
  if (target) delete compactingSessions[target];
}
const compactStatusText = computed(() => {
  const state = activeCompactState.value;
  if (!state) return t('app.compact.compacting');
  return [state.text, state.elapsedText].filter(Boolean).join(' · ');
});
// Elapsed timer so a long compaction visibly ticks instead of looking hung.
const compactElapsedNow = ref(Date.now());
let compactElapsedTimer = null;
function ensureCompactElapsedTimer() {
  if (compactElapsedTimer) return;
  compactElapsedTimer = setInterval(() => {
    compactElapsedNow.value = Date.now();
    for (const [sid, state] of Object.entries(compactingSessions)) {
      const startedAt = Number(state?.startedAt || 0);
      if (!startedAt) continue;
      const sec = Math.max(1, Math.floor((compactElapsedNow.value - startedAt) / 1000));
      const elapsedText = t('app.compact.elapsed', { seconds: sec });
      if (state.elapsedText !== elapsedText) state.elapsedText = elapsedText;
    }
  }, 1000);
}
function startCompactTracking(sid, text) {
  if (!sid) return;
  compactingSessions[sid] = { text, startedAt: Date.now(), elapsedText: '' };
  ensureCompactElapsedTimer();
}
watch(compactingSessions, (snapshot) => {
  if (Object.keys(snapshot || {}).length > 0) ensureCompactElapsedTimer();
  if (compactElapsedTimer && Object.keys(snapshot || {}).length === 0) {
    clearInterval(compactElapsedTimer);
    compactElapsedTimer = null;
  }
});
onUnmounted(() => { if (compactElapsedTimer) clearInterval(compactElapsedTimer); });

async function handleCompactCommand() {
  const session = activeSession.value;
  if (!session) return;
  if (session.runId || compactStateFor(session.id)) { message.warning(t('app.compact.wait')); return; }

  startCompactTracking(session.id, t('app.compact.compacting'));
  saveSessions();

  try {
    const result = await CompactSession(session.id, '');
    delete compactingSessions[session.id];

    const summaryText = result?.summary || '';

    // Replace UI messages cleanly with just the LLM summary as an assistant message
    session.messages = [
      {
        role: 'assistant',
        content: summaryText,
      },
    ];

    persistCompletedSession(session);
    // Refresh context
    refreshContextTokens(session.id);
    scrollMessagesToBottom();
  } catch (err) {
    delete compactingSessions[session.id];
    pushMessage('assistant', t('app.compact.failed', { error: err?.message || err }), { error: true });
  }
}

function createNewSession() {
  newSession();
  message.success(t('app.sessions.created'));
  scrollMessagesToBottom();
  nextTick(() => getPromptTextarea()?.focus({ preventScroll: true }));
}

async function loadAndShowSkills() {
  try {
    await refreshSkillState();
    const skills = availableSkills.value;
    if (skills && skills.length > 0) {
      let table = t('app.skills.tableHeader');
      for (const s of skills) {
        const status = isSkillActive(s.name, activeSkillNames.value) ? t('app.skills.enabled') : t('app.skills.disabled');
        table += `| \`/${s.name}\` | ${s.description || '-'} | ${s.source || '-'} · ${status} |\n`;
      }
      pushMessage('assistant', t('app.skills.list', { count: skills.length, table }), { system: true });
    } else {
      pushMessage('assistant', t('app.skills.empty'), { system: true });
    }
  } catch (err) {
    message.error(t('app.skills.listFailed', { error: err }));
  }
  scrollMessagesToBottom();
}

async function refreshSkillState() {
  skillsLoading.value = true;
  try {
    const [skills, active] = await Promise.all([
      ListSkills(),
      GetActiveSkills(),
    ]);
    availableSkills.value = skills || [];
    activeSkillNames.value = active || [];
    const activeSet = new Set((activeSkillNames.value || []).map((name) => normalizeSkillName(name)));
    const disabled = (availableSkills.value || [])
      .filter((skill) => !activeSet.has(normalizeSkillName(skill.name)))
      .map((skill) => skill.name);
    config.disabledSkills = disabled;
    configDraft.disabledSkills = [...disabled];
  } catch (err) {
    message.error(t('app.skills.stateFailed', { error: err }));
  } finally {
    skillsLoading.value = false;
  }
}

async function activateSkillByName(skillName, skillArgs = '', injectIntoChat = true) {
  try {
    const alreadyActive = isSkillActive(skillName, activeSkillNames.value);
    const xmlBlock = await ActivateSkill(skillName);
    if (injectIntoChat && xmlBlock) {
      const session = activeSession.value;
      if (session) {
        // User custom text first, then the skill block below it.
        const userText = (skillArgs || '').trim();
        const modelContent = userText ? `${userText}\n\n${xmlBlock}` : xmlBlock;
        // system:true keeps this message out of buildSessionMessagesForModel so the
        // backend history (source of truth) is never re-sent or deduplicated against
        // a potentially truncated localStorage copy — preserving prefix-cache stability.
        session.messages.push({
          role: 'user',
          content: modelContent,
          done: true,
          system: true,
          skill: { name: skillName, args: userText },
        });
        if (isDefaultSessionTitle(session)) {
          const titleBase = userText || `/${skillName}`;
          session.title = titleBase.length > 20 ? `${titleBase.slice(0, 20)}…` : titleBase;
          markCustomSessionTitle(session);
        }
        scrollMessagesToBottom();
        if (chatConfig.value.apiKey || (Array.isArray(chatConfig.value.apiKeys) && chatConfig.value.apiKeys.length)) {
          markSessionRunning(session);
          await StartChat({ sessionId: session.id, message: '', messages: [{ role: 'user', content: modelContent }], config: { ...chatConfig.value, extraRoots: session.extraRoots || [] } }).catch(() => {
            markTransientTurn(session);
            session.isRunning = false;
          });
        }
      }
    }
    if (!alreadyActive) {
      message.success(t('app.skills.activated', { name: skillName }));
    } else if (alreadyActive) {
      message.info(injectIntoChat ? t('app.skills.loaded', { name: skillName }) : t('app.skills.activated', { name: skillName }));
    }
    await refreshSkillState();
  } catch (err) {
    message.error(t('app.skills.activateFailed', { error: err }));
  }
  scrollMessagesToBottom();
}

async function deactivateSkillByName(skillName) {
  try {
    await DeactivateSkill(skillName);
    await refreshSkillState();
    message.success(t('app.skills.deactivated', { name: skillName }));
  } catch (err) {
    message.error(t('app.skills.deactivateFailed', { error: err }));
  }
}

async function toggleSkillFromSettings(skill, active) {
  const skillName = skill?.name || '';
  if (!skillName) return;
  skillToggleInFlight.value = skillName;
  try {
    if (active) {
      await activateSkillByName(skillName, '', false);
    } else {
      await deactivateSkillByName(skillName);
    }
  } finally {
    skillToggleInFlight.value = '';
  }
}

async function changeReasoningEffort(level) {
  const next = String(level || 'auto').toLowerCase();
  // Effort is a per-Tab runtime choice: it lives on the active Tab's model
  // snapshot, never on the persisted top-level (default) fields.
  const snapshot = modelByTab[activeWorkspaceId.value];
  if (snapshot) snapshot.reasoningEffort = next;
  // Keep the matching preset in sync so the choice survives re-selecting the
  // model in this (or a new) Tab.
  const activeIdentity = modelConfigIdentity(snapshot || config);
  for (const list of [config.models, configDraft.models]) {
    const preset = (list || []).find((m) => modelConfigIdentity(m) === activeIdentity);
    if (preset) preset.reasoningEffort = next;
  }
  const label = reasoningEffortLabel(next);
  try {
    await SaveConfig({ ...config });
    message.success(t('app.model.effortChanged', { level: label }));
  } catch (err) {
    message.error(t('app.model.effortFailed', { error: err }));
  }
}

function toolKind(name) {
  if (isMcpToolName(name)) return 'mcp';
  if (name === 'edit' || name === 'replace_exact' || name === 'replace_lines' || name === 'remote_edit') return 'edit';
  if (name === 'create' || name === 'remote_create_file') return 'create';
  if (name === 'delete' || name === 'remote_delete_path') return 'delete';
  if (name === 'command' || name === 'remote_run_command' || name === 'Bash') return 'command';
  if (name === 'ssh_credential') return 'ssh_credential';
  if (name === 'service' || name === 'start_service' || name === 'stop_service' || name === 'list_services') return 'service';
  if (name === 'wait') return 'wait';
  if (name === 'ask') return 'ask';
  if (name === 'calculate') return 'calculate';
  if (name === 'list_files') return 'list';
  if (name === 'read') return 'read';
  if (name === 'remote_read') return 'remote_read';
  if (name === 'Glob') return 'glob';
  if (name === 'grep') return 'grep';
  if (name === 'run') return 'run';
  if (name === 'plan') return 'plan';
  if (name === 'scheduled_task') return 'scheduled';
  if (name === 'subagent' || name === 'agent_delegate') return 'subagent';
  if (name === 'skill' || name === 'Skill') return 'skill';
  if (name === 'render_html') return 'render_html';

  return 'other';
}

// Kinds and tool names whose cards start collapsed, showing only the fixed
// non-scrollable preview lines until the user expands them manually.
const COLLAPSED_BY_DEFAULT_KINDS = new Set(['grep', 'list', 'create', 'command']);
const COLLAPSED_BY_DEFAULT_NAMES = new Set(['http_request', 'web_fetch']);

function isToolCollapsedByDefault(name) {
  return COLLAPSED_BY_DEFAULT_KINDS.has(toolKind(name)) || COLLAPSED_BY_DEFAULT_NAMES.has(name);
}

function isMcpToolName(name) {
  return typeof name === 'string' && name.startsWith('mcp__');
}

function formatMcpArgsSummary(parsed) {
  if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) return '';
  const parts = [];
  for (const [key, value] of Object.entries(parsed)) {
    if (value === undefined || value === null) continue;
    if (typeof value === 'string') {
      const v = String(value).trim();
      if (!v) continue;
      parts.push(`${key}: ${v.length > 40 ? `${v.slice(0, 40)}…` : v}`);
    } else if (typeof value === 'number' || typeof value === 'boolean') {
      parts.push(`${key}: ${value}`);
    }
  }
  return parts.slice(0, 2).join(' · ');
}

// Tools whose card verb is keyed by the parsed args.action (see toolVerb.mjs).
function isActionKeyedToolName(name) {
  return name === 'scheduled_task' || name === 'service';
}

function makeToolTitle(name, args, meta = {}) {
  if (isMcpToolName(name)) {
    // MCP 卡片名称位已显示 server/tool；括号里只放参数摘要，
    // 无参数时返回空串，避免重复显示工具名。
    return formatMcpArgsSummary(parseToolArgsForMeta(args, meta));
  }
  const parsed = parseToolArgsForMeta(args, meta);
  if (name === 'plan') {
    return Array.isArray(parsed.todos) ? formatTodoNextStep(parsed.todos) : '';
  }
  if (name === 'command' || name === 'remote_run_command' || name === 'Bash') {
    const command = parsed.command || parsed.cmd || '';
    if (name === 'remote_run_command' && parsed.target) return `${parsed.target} · ${command}`;
    return command;
  }
  if (name === 'service') {
    const action = String(parsed.action || '').trim();
    if (action === 'stop') return `stop · ${parsed.id || ''}`;
    if (action === 'list') return 'list';
    if (action === 'read') {
      const parts = ['read'];
      if (parsed.id) parts.push(parsed.id);
      if (parsed.tailBytes) parts.push(`${parsed.tailBytes}B`);
      return parts.join(' · ');
    }
    // action === 'start' (default for backwards compatibility with old cards)
    const parts = ['start'];
    if (parsed.name) parts.push(parsed.name);
    if (parsed.command) parts.push(parsed.command);
    if (parsed.cwd) parts.push(`cwd: ${parsed.cwd}`);
    return parts.join(' · ');
  }
  if (name === 'wait') {
    return parsed.reason || (parsed.seconds ? `${parsed.seconds}s` : '');
  }
  if (name === 'ask') {
    const questions = Array.isArray(parsed.questions) ? parsed.questions : [];
    if (questions.length === 1) return questions[0]?.question || '';
    return questions.length ? t('app.ask.questions', { count: questions.length }) : '';
  }
  if (name === 'start_service') {
    const command = parsed.command || '';
    const parts = [];
    if (parsed.name) parts.push(parsed.name);
    if (command) parts.push(command);
    if (parsed.cwd) parts.push(`cwd: ${parsed.cwd}`);
    if (parsed.port) parts.push(`port: ${parsed.port}`);
    return parts.join(' · ');
  }
  if (name === 'stop_service') {
    return parsed.id || '';
  }
  if (name === 'list_services') {
    return t('app.tools.chip.trackedServices');
  }
  if (name === 'ssh_credential') {
    // Never render the password: show action + host only. The args string is
    // already server-redacted (***), but build the title from target alone so
    // even a redaction miss cannot leak it into the card.
    const action = String(parsed.action || 'set').toLowerCase();
    const host = String(parsed.target || '').split(':')[0] || '';
    if (action === 'list') return 'list';
    if (action === 'clear') return `clear · ${host}`;
    return `set · ${host}`;
  }
  if (name === 'edit' || name === 'remote_edit') {
    if (Array.isArray(parsed.files)) return parsed.files.length === 1 ? (parsed.files[0]?.path || '') : `${parsed.files.length} files`;
    return parsed.target ? `${parsed.target} · ${parsed.path || ''}` : (parsed.path || '');
  }
  if (name === 'create' || name === 'delete' || name === 'remote_create_file' || name === 'remote_delete_path') {
    return parsed.target ? `${parsed.target} · ${parsed.path || ''}` : (parsed.path || '');
  }
  if (name === 'remote_read') {
    if (parsed.target && Array.isArray(parsed.files)) {
      const paths = parsed.files.map(f => f && f.path).filter(Boolean);
      return `${parsed.target} · ${paths.join(', ')}`;
    }
    return parsed.target ? `${parsed.target} · ${parsed.path || ''}` : (parsed.path || '');
  }
  if (name === 'grep') {
    const pattern = parsed.pattern || '';
    const path = parsed.path || '';
    if (pattern && path) return `${pattern}, ${path}`;
    return pattern || path;
  }
  if (name === 'Glob') {
    return parsed.pattern || '';
  }
  if (name === 'list_files') {
    return parsed.path || parsed.pattern || '';
  }
  if (name === 'read' || name === 'batch_read') {
    if (parsed.path) return parsed.path;
    const paths = Array.isArray(parsed.paths) ? parsed.paths : [];
    if (paths.length > 0) return paths.join(', ');
    const files = Array.isArray(parsed.files) ? parsed.files.map(f => f && f.path).filter(Boolean) : [];
    if (files.length > 0) return files.join(', ');
  }
  if (name === 'document_read') {
    return parsed.path || '';
  }
  if (name === 'calculate') {
    return parsed.expression || '';
  }
  if (name === 'scheduled_task') {
    if (parsed.action === 'create') return `create · ${parsed.name || ''}`;
    if (parsed.action === 'delete') return `delete · ${parsed.id || ''}`;
    return parsed.action || 'list';
  }
  if (name === 'http_request' || name === 'web_fetch') {
    return formatHttpToolTitle(parsed);
  }
  if (name === 'render_html') {
    return parsed.title || '';
  }
  if (name === 'skill' || name === 'Skill') {
    const skillName = parsed.skill || '';
    const skillArgs = parsed.args || '';
    if (skillName && skillArgs) return `${skillName} · ${skillArgs}`;
    return skillName || '';
  }
  return '';
}

function formatToolChip(name, result) {
  const text = String(result || '');
  if (isMcpToolName(name)) {
    try {
      const parsed = JSON.parse(text);
      const output = parsed?.data?.output ?? parsed?.output ?? '';
      const chars = String(output || '').length;
      if (chars > 0) return '\u00B7 ' + formatCharCount(chars);
      return parsed?.ok === false ? '\u00B7 failed' : '\u00B7 completed';
    } catch (_) {
      return text ? '\u00B7 ' + formatCharCount(text.length) : '\u00B7 completed';
    }
  }
  try {
    const parsed = JSON.parse(text);
    // read / remote_read: list each file as separate line
    if ((name === 'read' || name === 'remote_read') && parsed.data) {
      if (!parsed.data.files || !Array.isArray(parsed.data.files)) return '';
      if (name === 'remote_read' && parsed.data.files.length === 1) {
        const f = parsed.data.files[0];
        if (f.error) return '· failed';
        return formatReadChip(f.totalLines || f.lineCount || 0);
      }
      const lines = parsed.data.files.map(f => {
        const path = f.path || '';
        const total = f.totalLines || 0;
        if (f.error) return path + ' \u00B7 failed';
        if (f.kind === 'document' || f.contentFormat === 'plain') {
          return path + ' \u00B7 ' + formatCharCount(String(f.text || f.content || '').length);
        }
        return path + ' ' + formatReadChip(total);
      });
      return '\u00B7 ' + lines.join('  ');
    }
    // grep: · N hits in F files
    if (name === 'grep' && parsed.data) {
      const d = parsed.data;
      const hits = Number(d.hits || 0);
      const lines = Number(d.matchedLines || 0);
      const files = Number(d.files || 0);
      let chip = '';
      if (hits > 0) {
        chip = '\u00B7 ' + hits + ' hit' + (hits > 1 ? 's' : '') + ' in ' + files + ' file' + (files > 1 ? 's' : '');
      } else if (lines > 0) {
        chip = '\u00B7 ' + lines + ' match' + (lines > 1 ? 'es' : '');
      }
      if (d.truncated) chip += ' \u00B7 truncated';
      return chip;
    }
    // Glob: · N files
    if (name === 'Glob' && parsed.data) {
      const files = String(parsed.data.output || '').split('\n').filter(l => l.length > 0).length;
      if (files > 0) return '\u00B7 ' + files + ' file' + (files > 1 ? 's' : '');
    }
    // list_files: · N items
    if (name === 'list_files' && parsed.data) {
      const items = Array.isArray(parsed.data.entries) ? parsed.data.entries.length : (parsed.data.count || 0);
      if (items > 0) return '\u00B7 ' + items + ' item' + (items > 1 ? 's' : '');
    }
    if (name === 'document_read' && parsed.data) {
      const chars = String(parsed.data.text || '').length;
      if (chars > 0) return '\u00B7 ' + chars + ' chars' + (parsed.data.truncated ? ' truncated' : '');
    }
    if (name === 'calculate' && parsed.data) {
      return '= ' + (parsed.data.text || parsed.data.value);
    }
    if (name === 'scheduled_task' && parsed.data) {
      if (parsed.data.task) return '\u00B7 created';
      if (parsed.data.deleted) return '\u00B7 deleted';
      const count = Number(parsed.data.count ?? parsed.data.tasks?.length ?? 0);
      return '\u00B7 ' + count + ' task' + (count === 1 ? '' : 's');
    }
    if ((name === 'service' || name === 'start_service' || name === 'stop_service') && parsed.data) {
      // service now returns distinct result shapes per action.
      // Discriminate by structural fields (not action args, which the chip
      // helper does not receive) so list/read render sensible chips instead
      // of falling through to formatServiceChip with missing fields.
      const d = parsed.data;
      if (Array.isArray(d.services)) {
        return '\u00B7 ' + d.services.length + ' service' + (d.services.length !== 1 ? 's' : '');
      }
      if (typeof d.returnedBytes === 'number' && typeof d.bufferBytes === 'number') {
        return '\u00B7 ' + d.returnedBytes + ' B' + (d.status ? ' \u00B7 ' + d.status : '');
      }
      return formatServiceChip(d);
    }
    if (name === 'wait' && parsed.data) return '';
    if (name === 'ask' && parsed.data) return '';
    if (name === 'list_services' && parsed.data) {
      const services = Array.isArray(parsed.data.services) ? parsed.data.services : [];
      return '\u00B7 ' + services.length + ' service' + (services.length !== 1 ? 's' : '');
    }
    // edit / replace_exact/replace_lines: · +N -N
    if ((name === 'edit' || name === 'replace_exact' || name === 'replace_lines' || name === 'remote_edit') && parsed.data) {
      const d = parsed.data;
	  if ((name === 'edit' || name === 'remote_edit') && Array.isArray(d.files)) {
		if (d.diff) return d.diff;
		const diffs = d.files.map((file) => file?.diff || '').filter(Boolean);
		return diffs.length ? diffs.join('\n') : (d.summary || `${d.files.length} files updated`);
	  }
      const parts = [];
      if (d.addedLines > 0) parts.push('+' + d.addedLines);
      if (d.removedLines > 0) parts.push('-' + d.removedLines);
      if (parts.length > 0) return '\u00B7 ' + parts.join(' ');
    }
    // create: · N lines
    if ((name === 'create' || name === 'remote_create_file') && parsed.data) {
      const lines = Number(parsed.data.addedLines ?? 0);
      const bytes = parsed.data.afterBytes || 0;
      const parts = [lines + ' line' + (lines !== 1 ? 's' : '')];
      if (bytes > 0) parts.push(formatBytes(bytes));
      return '\u00B7 ' + parts.join(' \u00B7 ');
    }
    if ((name === 'delete' || name === 'remote_delete_path') && parsed.data) {
      if (parsed.data.deleted) return '\u00B7 deleted';
    }
    if ((name === 'http_request' || name === 'web_fetch') && parsed.data) {
      return formatHTTPToolSummary(parsed.data);
    }
    // FetchURL: · N B/KB
    if (name === 'FetchURL' && parsed.data) {
      const bytes = String(parsed.data.output || '').length;
      if (bytes > 0) {
        const size = bytes < 1024 ? bytes + ' B' : (bytes / 1024).toFixed(1) + ' KB';
        return '\u00B7 ' + size;
      }
    }
  } catch (_) { /* fall through */ }
  return '';
}

function formatToolBody(name, body) {
  const text = String(body || '');
  if (isMcpToolName(name) || name === 'plan') return '';
  try {
    const parsed = JSON.parse(text);
    if (name === 'wait' && parsed.data) return '';
    if (name === 'ask' && parsed.data) return '';
    // command result: show output + exit code (command shown in card title)
    if ((name === 'command' || name === 'remote_run_command' || name === 'Bash') && parsed.data) {
      const d = parsed.data;
      let out = '';
      if (d.output) out += stripAnsi(d.output);
      if (d.output && !d.output.endsWith('\n')) out += '\n';
      if (d.exitCode === 0) {
        out += t('app.tools.exitCode', { code: 0 }) + ' [' + formatDuration(d.durationMs) + ']';
      } else {
        out += t('app.tools.exitCode', { code: d.exitCode }) + ' [' + formatDuration(d.durationMs) + ']';
      }
      if (d.timedOut) out += '  ' + t('app.tools.timedOut');
      if (d.cancelled) out += '  ' + t('app.tools.cancelled');
      if (d.truncated) out += '  [' + t('common.truncated') + ']';
      return out;
    }
    // read_file result: show content with line numbers
    // read (and legacy batch_read) result: show each file's content
    if ((name === 'read' || name === 'remote_read') && parsed.data) {
      const d = parsed.data;
      if (d.files && Array.isArray(d.files)) {
        return d.files.map(f => {
          if (f.kind === 'document' || f.contentFormat === 'plain') {
            const sheetInfo = f.sheets && f.sheets.length ? ' sheets: ' + f.sheets.join(', ') : '';
            const header = '### ' + (f.path || '') + '  (' + (f.type || 'document') + sheetInfo + (f.truncated ? ' [truncated]' : '') + ')';
            return header + '\n' + (f.text || f.content || '');
          }
          const rangeCount = f.endLine ? (f.endLine - (f.startLine || 1) + 1) : 0;
          const range = f.endLine ? (rangeCount + ' line' + (rangeCount !== 1 ? 's' : '') + ' ' + (f.startLine || 1) + '-' + f.endLine) : ((f.totalLines || '?') + ' lines');
          const suffix = f.truncated ? ' [truncated]' : '';
          const header = '### ' + (f.path || '') + '  (' + range + suffix + ')';
          const content = f.content || '';
          return header + '\n' + content;
        }).join('\n\n');
      }
      if (d.output) return d.output;
      return '';
    }
    if (name === 'document_read' && parsed.data) {
      const d = parsed.data;
      const sheetInfo = d.sheets && d.sheets.length ? '\nsheets: ' + d.sheets.join(', ') : '';
      return `${d.path || ''} (${d.type || 'document'})${sheetInfo}\n\n${d.text || ''}${d.truncated ? '\n\n[truncated]' : ''}`;
    }
    if (name === 'calculate' && parsed.data) {
      return `${parsed.data.expression} = ${parsed.data.text || parsed.data.value}`;
    }
    if (name === 'scheduled_task' && parsed.data) {
      if (parsed.data.task) return formatScheduledTaskToolDetail(parsed.data.task);
      if (parsed.data.deleted) return t('app.tools.scheduled.deleted', { id: parsed.data.deleted });
      const tasks = Array.isArray(parsed.data.tasks) ? parsed.data.tasks : [];
      if (!tasks.length) return t('app.tools.scheduled.none');
      return tasks.map(formatScheduledTaskToolDetail).join('\n\n---\n\n');
    }
    if ((name === 'service' || name === 'start_service' || name === 'stop_service') && parsed.data) {
      const d = parsed.data;
      // Discriminate by structural fields so list/read results render as
      // human-readable summaries instead of JSON dumps.
      if (Array.isArray(d.services)) return formatServiceListResult(d);
      if (typeof d.returnedBytes === 'number' && typeof d.bufferBytes === 'number') return formatServiceReadResult(d);
      return formatServiceInfo(d);
    }
    if (name === 'list_services' && parsed.data) {
      const services = Array.isArray(parsed.data.services) ? parsed.data.services : [];
      if (services.length === 0) return t('app.tools.services.none');
      return services.map((service) => formatServiceInfo(service)).join('\n\n---\n\n');
    }
    // edit result: show summary + diff
    if ((name === 'edit' || name === 'replace_exact' || name === 'replace_lines' || name === 'remote_edit') && parsed.data) {
      const d = parsed.data;
      if (d.diff) return d.diff;
      if (d.summary) return d.summary;
      return d.path ? `${d.path} updated` : 'updated';
    }
    // edit result (direct format from tool:result event)
    if ((name === 'edit' || name === 'replace_exact' || name === 'replace_lines' || name === 'remote_edit') && parsed.addedLines !== undefined) {
      const parts = [];
      if (parsed.addedLines > 0) parts.push('+' + parsed.addedLines);
      if (parsed.removedLines > 0) parts.push('-' + parsed.removedLines);
      return (parsed.path || '') + '  ' + parts.join(' ');
    }
    if ((name === 'create' || name === 'remote_create_file') && parsed.data) {
      const d = parsed.data;
      const lines = Number(d.addedLines ?? 0);
      const parts = [];
      if (d.path) parts.push(d.path);
      parts.push(lines + ' line' + (lines !== 1 ? 's' : '') + ' created');
      if (d.afterBytes > 0) parts.push(formatBytes(d.afterBytes));
      return parts.join('  ');
    }
    if ((name === 'delete' || name === 'remote_delete_path') && parsed.data) {
      return '';
    }
    if ((name === 'http_request' || name === 'web_fetch') && parsed.data) return '';
    // grep results stay as a single non-expandable status line. The compact
    // hit count is rendered by formatToolChip(); do not retain matching lines
    // in the message body or build a hidden detail preview.
    if (name === 'grep' && parsed.data) return '';
    // list_files result: show entries
    if (name === 'list_files' && parsed.data && Array.isArray(parsed.data.entries)) {
      let out = parsed.data.count + ' items';
      if (parsed.data.truncated) out += ' (truncated)';
      out += '\n';
      for (const entry of parsed.data.entries.slice(0, 200)) {
        out += entry.path + (entry.dir ? '/' : '') + '\n';
      }
      if (parsed.data.entries.length > 200) out += '... and ' + (parsed.data.entries.length - 200) + ' more';
      return out.slice(0, 12000);
    }
    // Skill result: the card header already reads "Loaded skill (name)" and the
    // user message carries the /skill chip, so the result body only repeats it.
    // Keep the card as a single status line, like delete/http_request.
    if ((name === 'skill' || name === 'Skill') && parsed.data) return '';
    // Never leak a raw tool-result JSON envelope into the UI. Tools without a
    // dedicated renderer still get a bounded, human-readable key/value view.
    if (parsed?.ok === false) return String(parsed.error || t('tools.status.failed'));
    return formatGenericToolData(Object.prototype.hasOwnProperty.call(parsed || {}, 'data') ? parsed.data : parsed);
  } catch (_) {
    // Plain-text tool output is still useful. JSON-looking malformed/partial
    // payloads are hidden instead of exposing implementation details.
    const trimmed = text.trim();
    if (trimmed.startsWith('{') || trimmed.startsWith('[')) return '';
    return trimmed.slice(0, 12000);
  }
}

function formatGenericToolData(value) {
  const lines = [];
  const visit = (current, label = '', depth = 0) => {
    if (lines.length >= 200 || depth > 4 || current === null || current === undefined) return;
    if (Array.isArray(current)) {
      if (current.length === 0) return;
      current.slice(0, 100).forEach((item, index) => visit(item, label ? `${label} ${index + 1}` : String(index + 1), depth + 1));
      if (current.length > 100) lines.push(`… ${current.length - 100} more`);
      return;
    }
    if (typeof current === 'object') {
      for (const [key, item] of Object.entries(current)) {
        if (['ok', 'error'].includes(key) || item === null || item === undefined || item === '') continue;
        visit(item, label ? `${label} · ${humanizeToolResultKey(key)}` : humanizeToolResultKey(key), depth + 1);
      }
      return;
    }
    const display = typeof current === 'boolean'
      ? (current ? t('tools.value.yes') : t('tools.value.no'))
      : String(current);
    lines.push(label ? `${label}: ${display}` : display);
  };
  visit(value);
  return lines.join('\n').slice(0, 12000);
}

function humanizeToolResultKey(key) {
  return String(key || '')
    .replace(/([a-z0-9])([A-Z])/g, '$1 $2')
    .replace(/[_-]+/g, ' ')
    .replace(/^./, (char) => char.toUpperCase());
}

function formatScheduledTaskToolDetail(task = {}) {
  const lines = [];
  if (task.name) lines.push(t('app.tools.scheduled.task', { name: task.name }));
  if (task.id) lines.push(`ID: ${task.id}`);
  lines.push(t('app.tools.scheduled.schedule', { schedule: formatScheduledToolSchedule(task.schedule || {}) }));
  if (task.workspace) lines.push(t('app.tools.scheduled.workspace', { workspace: task.workspace }));
  lines.push(t('app.tools.scheduled.modeYolo'));
  if (task.nextRunAt) lines.push(t('app.tools.scheduled.nextRun', { time: formatDateTime(Number(task.nextRunAt)) }));
  if (task.lastRunAt) lines.push(t('app.tools.scheduled.lastRun', { time: formatDateTime(Number(task.lastRunAt)) }));
  if (task.lastStatus) lines.push(t('app.tools.scheduled.status', { status: task.running ? t('common.running') : task.lastStatus }));
  if (task.runCount !== undefined) lines.push(t('app.tools.scheduled.runs', { count: task.runCount }));
  if (task.maxSteps || task.timeoutSeconds) {
    lines.push(t('app.tools.scheduled.perRun', { steps: task.maxSteps || '-', timeout: task.timeoutSeconds || '-' }));
  }
  return lines.join('\n');
}

function formatScheduledToolSchedule(schedule = {}) {
  if (schedule.type === 'once') return t('app.tools.scheduled.onceAt', { time: schedule.at || '-' });
  if (schedule.type === 'interval') return t('app.tools.scheduled.every', { interval: schedule.every || '-' });
  if (schedule.type === 'cron') return t('app.tools.scheduled.cron', { cron: schedule.cron || '-' });
  return '-';
}

function formatServiceChip(service) {
  const status = String(service?.status || '').trim() || 'unknown';
  const parts = [status];
  if (service?.pid) parts.push('pid ' + service.pid);
  if (service?.exitCode) parts.push('exit ' + service.exitCode);
  return '\u00B7 ' + parts.join(' \u00B7 ');
}

function formatServiceInfo(service) {
  const lines = [];
  const name = String(service?.name || '').trim();
  if (name) lines.push(t('app.tools.service.name', { name }));
  if (service?.id) lines.push(`id: ${service.id}`);
  if (service?.status) lines.push(t('app.tools.service.status', { status: service.status }));
  if (service?.pid) lines.push(`pid: ${service.pid}`);
  if (service?.command) lines.push(t('app.tools.service.command', { command: service.command }));
  if (service?.cwd) lines.push(t('app.tools.service.cwd', { cwd: service.cwd }));
  if (service?.startedAt) lines.push(t('app.tools.service.started', { time: formatUnixTimestamp(service.startedAt) }));
  if (service?.stoppedAt) lines.push(t('app.tools.service.stopped', { time: formatUnixTimestamp(service.stoppedAt) }));
  if (service?.exitCode) lines.push(t('app.tools.exitCode', { code: service.exitCode }));
  if (service?.error) lines.push(t('app.tools.service.error', { error: service.error }));
  const output = stripAnsi(service?.outputTail || '');
  if (output) {
    lines.push('');
    lines.push(t('app.tools.service.outputTail'));
    lines.push(output);
  }
  return lines.join('\n');
}

// formatServiceReadResult renders a service read result. The
// backend already clamps output to 32 KiB; here we further bound the body for
// UI display so a 32 KiB read does not blow up the chat scroll. The full
// output is still available in the Task Center log viewer.
//
// Layout: output content first (so the collapsed tool card preview shows the
// latest output, not just metadata), then a divider, then the metadata block.
// The body is rendered in a <pre> that auto-scrolls to the bottom for service
// read results, so even when expanded the user sees the latest output lines
// without manual scrolling.
function formatServiceReadResult(data) {
  const output = stripAnsi(String(data?.output || ''));
  const lines = [];
  if (output) {
    // UI cap: show the last 8 KiB of the (already bounded) read payload so
    // tool cards stay compact. The full output is in the Task Center.
    const uiCap = 8 * 1024;
    const shown = output.length > uiCap ? output.slice(output.length - uiCap) : output;
    if (shown.length < output.length) {
      lines.push(`[showing last ${uiCap} B of ${output.length} B]`);
    }
    lines.push(shown);
    lines.push('');
    lines.push('---');
  }
  const meta = [];
  if (data?.id) meta.push(`id: ${data.id}`);
  if (data?.status) meta.push(`status: ${data.status}`);
  if (typeof data?.returnedBytes === 'number') meta.push(`returned: ${data.returnedBytes} B`);
  if (typeof data?.bufferBytes === 'number') meta.push(`buffer: ${data.bufferBytes} B`);
  if (typeof data?.totalBytes === 'number') meta.push(`total: ${data.totalBytes} B`);
  if (data?.truncated) meta.push('truncated: true');
  if (typeof data?.fromByte === 'number' && data.fromByte > 0) meta.push(`from byte: ${data.fromByte}`);
  if (meta.length) lines.push(meta.join(' · '));
  return lines.join('\n');
}

// formatServiceListResult renders a service list result. The
// backend omits output tails; we render one compact line per service so the
// model/user can scan all services without context bloat.
function formatServiceListResult(data) {
  const services = Array.isArray(data?.services) ? data.services : [];
  if (services.length === 0) return t('app.tools.services.none');
  const header = [];
  if (typeof data?.activeCount === 'number' && typeof data?.maxActive === 'number') {
    header.push(t('app.tools.services.active', { active: data.activeCount, max: data.maxActive }));
  }
  header.push(t('app.tools.services.count', { count: services.length }));
  const lines = [header.join(' · '), ''];
  for (const svc of services) {
    const parts = [];
    if (svc?.id) parts.push(svc.id);
    if (svc?.name) parts.push(`(${svc.name})`);
    if (svc?.status) parts.push(svc.status);
    if (svc?.pid) parts.push(`pid ${svc.pid}`);
    if (svc?.command) parts.push(`· ${svc.command}`);
    if (typeof svc?.outputBytes === 'number' && svc.outputBytes > 0) parts.push(`${svc.outputBytes} B`);
    lines.push(parts.join(' '));
  }
  return lines.join('\n');
}

function formatUnixTimestamp(value) {
  const seconds = Number(value) || 0;
  if (!seconds) return '';
  try {
    return formatDateTime(seconds * 1000);
  } catch (_) {
    return String(value);
  }
}

function formatHTTPToolSummary(data) {
  const parts = [
    formatHTTPStatus(data),
    formatBytes(Number(data.bytesRead) || 0),
    formatHTTPDuration(data.durationMs),
  ];
  if (data.truncated) parts.push('truncated');
  return '\u00B7 ' + parts.filter(Boolean).join(' \u00B7 ');
}

function formatHTTPToolBody(name, data) {
  return [
    `${name}: ${formatHTTPStatus(data)}`,
    `size: ${formatBytes(Number(data.bytesRead) || 0)}`,
    `duration: ${formatHTTPDuration(data.durationMs)}`,
  ].join('\n');
}

function formatHTTPStatus(data) {
  const status = Number(data?.status) || 0;
  const statusText = String(data?.statusText || '').trim();
  if (!status) return statusText || 'no status';
  if (!statusText) return String(status);
  if (statusText.startsWith(String(status))) return statusText;
  return `${status} ${statusText}`;
}

function formatHTTPDuration(ms) {
  const value = Number(ms) || 0;
  if (value < 1000) return `${Math.max(0, Math.round(value))}ms`;
  return formatDuration(value);
}

function formatCharCount(chars) {
  if (!chars) return '0 chars';
  return fmtCompact(chars) + ' chars';
}

function formatDuration(ms) {
  if (!ms) return '0s';
  const secs = Math.floor(ms / 1000);
  if (secs < 60) return secs + 's';
  return Math.floor(secs / 60) + 'm ' + (secs % 60) + 's';
}

function formatDurationShort(ms) {
  return fmtDuration(ms);
}

function stripAnsi(text) {
  // Strip ANSI escape sequences (color codes, cursor moves, etc.)
  return String(text || '').replace(/\x1b\[[0-9;]*[A-Za-z]/g, '');
}

function downloadMD(content, filename) {
  // Native save dialog via the Wails binding: WKWebView (macOS) ignores
  // <a download>, so the old blob-download approach silently did nothing
  // there. Falls back to blob download when the binding is unavailable.
  saveTextFile({ filename, content, filterName: 'Markdown (*.md)', filterPattern: '*.md' });
}

function exportMsgAsMD(msg) {
  if (!msg || msg.welcome) return '';
  const roleLabel = msg.role === 'user' ? 'User' : 'Assistant';
  if (msg.skill) {
    const chip = `/${msg.skill.name}`;
    const args = (msg.skill.args || '').trim();
    const content = args ? `${chip}\n\n${args}` : chip;
    return `> **${roleLabel}:**

${content}`;
  }
  const content = msg.content || '';
  if (!content) return '';
  return `> **${roleLabel}:**

${content}`;
}

function exportOneMessage(msg) {
  const md = exportMsgAsMD(msg);
  if (!md) return;
  downloadMD(md, `ally-response.md`);
}

function handleExportOption(sessionId, key, msg) {
  if (key === 'response') {
    exportOneMessage(msg);
  } else if (key === 'session') {
    exportAllMessages(sessionId);
  }
}

function exportAllMessages(sessionId = activeSessionId.value) {
  const session = sessions.value.find((item) => item.id === sessionId);
  const msgs = session?.messages || [];
  if (!msgs.length) return;
  const parts = [];
  parts.push(`# ${session?.title || t('app.export.sessionTitle')}\n`);
  parts.push(`> ${t('app.export.time', { time: formatDateTime(new Date()) })}`);
  parts.push('');
  for (const msg of msgs) {
    if (msg.welcome || msg.role === 'tool_call') continue;
    const md = exportMsgAsMD(msg);
    if (md) parts.push(md);
  }
  downloadMD(parts.join('\n---\n\n'), `ally-session.md`);
}

function fmtK(n) {
  return fmtCompact(n);
}

function fmtTime(ts) {
  if (!ts) return '';
  return formatDateTime(ts, { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' });
}

function msgCount(s) {
  if (Number.isFinite(Number(s?.messageCount))) return Number(s.messageCount);
  return recountSessionMessages(s);
}


const markdownRenderCache = new Map();
const MARKDOWN_RENDER_CACHE_LIMIT = 100;
const MARKDOWN_RENDER_CACHE_MAX_CHARS = 30000;

function renderMarkdown(text, streaming = false) {
  const source = normalizeGeneratedImageMarkdown(streaming ? repairStreamingMarkdown(text || '') : (text || ''));
  if (streaming) return renderMarkdownWithMode(source, true);
  if (source.length > MARKDOWN_RENDER_CACHE_MAX_CHARS) return renderMarkdownWithMode(source, false);
  if (markdownRenderCache.has(source)) {
    const cached = markdownRenderCache.get(source);
    markdownRenderCache.delete(source);
    markdownRenderCache.set(source, cached);
    if (cached.includes('markdown-mermaid')) scheduleMermaidRender();
    return cached;
  }
  const rendered = renderMarkdownWithMode(source, false);
  markdownRenderCache.set(source, rendered);
  if (markdownRenderCache.size > MARKDOWN_RENDER_CACHE_LIMIT) {
    const oldest = markdownRenderCache.keys().next().value;
    markdownRenderCache.delete(oldest);
  }
  return rendered;
}

function normalizeGeneratedImageMarkdown(text) {
  if (!text) return '';
  // Most messages contain no standalone image URL. Avoid allocating a line
  // array and joining it again on every streaming render in that common case.
  if (!/(?:https?:\/\/\S+\.(?:png|jpe?g|gif|webp|bmp|svg)(?:[?#]\S*)?|data:image\/(?:png|jpe?g|gif|webp);base64,)/i.test(text)) return text;
  const imageUrlRe = /^(?:https?:\/\/\S+\.(?:png|jpe?g|gif|webp|bmp|svg)(?:[?#]\S*)?|data:image\/(?:png|jpe?g|gif|webp);base64,[A-Za-z0-9+/=]+)$/i;
  let inFence = false;
  return text.split('\n').map((line) => {
    const trimmed = line.trim();
    if (trimmed.startsWith('```')) inFence = !inFence;
    if (inFence || !trimmed || trimmed.startsWith('![') || trimmed.startsWith('[')) return line;
    if (!imageUrlRe.test(trimmed)) return line;
    return `${line.slice(0, line.indexOf(trimmed))}![generated image](${trimmed})`;
  }).join('\n');
}

function repairStreamingMarkdown(text) {
  let repaired = text;
  const fenceCount = (repaired.match(/```/g) || []).length;
  if (fenceCount % 2 === 1) {
    repaired += '\n```';
  }
  const inlineTickCount = countUnescapedBackticks(repaired.replace(/```[\s\S]*?```/g, ''));
  if (inlineTickCount % 2 === 1) {
    repaired += '`';
  }
  return repaired;
}

function countUnescapedBackticks(text) {
  let count = 0;
  for (let i = 0; i < text.length; i++) {
    if (text[i] === '`' && text[i - 1] !== '\\') count++;
  }
  return count;
}

function escapeHTML(value) {
  return String(value || '')
    .replaceAll('&', '&amp;')
    .replaceAll('<', '&lt;')
    .replaceAll('>', '&gt;')
    .replaceAll('"', '&quot;')
    .replaceAll("'", '&#039;');
}


function focusPromptInput() {
  promptInputRefs[activeWorkspaceId.value]?.focus?.();
}

watch(configVisible, (visible) => {
  if (visible) {
    // config 顶层模型字段即默认模型，设置页"使用模型"直接显示/编辑它。
    assignConfig(configDraft, config);
    refreshSkillState();
  }
});


watch(sessionsSelectedIndex, () => {
  nextTick(() => {
    const el = sessionsScrollRef.value?.querySelector('.session-item.active');
    el?.scrollIntoView({ block: 'nearest' });
  });
});

function switchWorkspaceByOffset(offset) {
  // Header tabs are chat tabs only; the hidden KB tab is skipped.
  const tabs = workspaceTabs.value.filter((tab) => !isKbTab(tab));
  if (tabs.length <= 1) return;
  const current = tabs.findIndex((tab) => tab.id === activeWorkspaceId.value);
  if (current === -1) return;
  const next = (current + offset + tabs.length) % tabs.length;
  switchWorkspaceTab(tabs[next].id);
}

// Custom overlays (command menu / file mention / sessions list) have no global
// Escape or outside-click handling; close the topmost one instead of stopping
// the run. Naive UI modals (settings / task center / git diff) own their ESC
// handling in the bubble phase, so we let those events pass through untouched.
function closeTopmostCustomOverlay() {
  if (sessionsVisible.value) { sessionsVisible.value = false; return true; }
  if (fileMentionVisible.value) { closeFileMentionMenu(); return true; }
  if (commandMenuVisible.value) { commandMenuVisible.value = false; return true; }
  return false;
}

// Clicking outside a custom overlay (which has no modal mask) closes it.
function handleOverlayOutsidePointerDown(event) {
  if (!commandMenuVisible.value && !fileMentionVisible.value && !sessionsVisible.value) return;
  const target = event.target;
  if (!(target instanceof Element)) return;
  if (target.closest('.sessions-menu')) return;
  if (target.closest('.composer-sessions-btn')) {
    if (fileMentionVisible.value) closeFileMentionMenu();
    if (commandMenuVisible.value) commandMenuVisible.value = false;
    return;
  }
  if (target.closest('.command-menu, .file-mention-menu')) return;
  if (sessionsVisible.value) sessionsVisible.value = false;
  if (fileMentionVisible.value) closeFileMentionMenu();
  if (commandMenuVisible.value) commandMenuVisible.value = false;
}

function handleGlobalKeydown(event) {
  // 资源树打开时：编辑器（预览面板）打开中则由 WorkspaceExplorer 内部
  // 处理 ESC（关闭编辑器）；编辑器未打开时 ESC 继续冒泡中断运行等全局逻辑。
  // 不再用 ESC 关闭资源树本身——避免中断对话后资源树被误关。
  if (event.key === 'Escape' && deactivateMermaidInteraction()) {
    event.preventDefault();
    event.stopPropagation();
    return;
  }
  if (event.key === 'Escape' && closeTopmostCustomOverlay()) {
    event.preventDefault();
    event.stopPropagation();
    return;
  }
  if (event.key === 'Escape' && (settingsActive.value || statsActive.value || gamesActive.value)) {
    // Settings / token stats / games are inline pages now: ESC navigates back.
    event.preventDefault();
    event.stopPropagation();
    if (gamesActive.value) closeGames();
    else if (statsActive.value) closeStats();
    else closeSettings();
    return;
  }
  if (event.key === 'Escape' && (taskCenterVisible.value || gitDiffVisible.value)) {
    // Let Naive UI modals handle their own ESC (nested stack); do not stop the run.
    return;
  }
  if (event.key === 'Escape' && (activeSession.value?.runId || compactStateFor(activeSession.value?.id))) {
    event.preventDefault();
    event.stopPropagation();
    stopRun();
    return;
  }
  if (!(event.ctrlKey || event.metaKey) || event.altKey) return;
  const activeTabForKeydown = workspaceTabs.value.find((tab) => tab.id === activeWorkspaceId.value) || null;
  if (event.key.toLowerCase() === 'w') {
    event.preventDefault();
    // The KB tab has no header close button; Ctrl+W must not close it either.
    if (!isKbTab(activeTabForKeydown)) closeWorkspaceTab(activeWorkspaceId.value);
    return;
  }
  if (event.key.toLowerCase() === 't') {
    event.preventDefault();
    // New workspace tab reusing the current tab's workspace path (the KB
    // tab never seeds a new chat tab with the KB root).
    const basePath = isKbTab(activeTabForKeydown) ? config.workspace : activeTabForKeydown?.path;
    const tab = createWorkspaceTab(basePath || '');
    workspaceTabs.value.push(tab);
    switchWorkspaceTab(tab.id);
    mode.value = 'chat';
    return;
  }
  if (event.key.toLowerCase() === 'n') {
    event.preventDefault();
    createNewSession();
    return;
  }
  if (event.key !== 'ArrowLeft' && event.key !== 'ArrowRight') return;
  if (isEditableNavigationTarget(event.target)) {
    const isEmptyPrompt = event.target?.closest?.('[data-ally-prompt-input="true"]') && promptText.value.length === 0;
    if (!isEmptyPrompt) return;
  }
  if (event.key === 'ArrowLeft') {
    event.preventDefault();
    switchWorkspaceByOffset(-1);
  } else if (event.key === 'ArrowRight') {
    event.preventDefault();
    switchWorkspaceByOffset(1);
  }
}

// isDevBuild returns true for any build whose version is NOT a正式 release
// tag (e.g. "v1.6.0"). Local dev builds use a timestamp ("v20260805-002211")
// or the literal "dev" when ALLY_BUILD_VERSION is unset; neither should
// trigger update checks, since they would always compare as "older" and
// generate spurious update prompts / unnecessary GitHub requests.
const RELEASE_VERSION_RE = /^v?\d+\.\d+\.\d+$/;
function isDevBuild() {
  return !RELEASE_VERSION_RE.test(String(buildVersion || '').trim());
}

// checkForUpdates queries the backend for the latest release and, when a
// newer version exists, updates the top-right icon + optionally starts a
// silent background download. The optional `manual` flag is set when the
// user clicks "Check for updates" in Settings → About; in that mode the
// function also reports a result object back to the SettingsModal so the
// user sees explicit "latest" / "found" / "failed" feedback.
async function checkForUpdates(manual = false) {
  // Local dev builds (timestamp version) skip update checks entirely.
  if (isDevBuild()) {
    if (manual) checkUpdateResult.value = { state: 'latest' };
    return;
  }
  if (manual) checkUpdateResult.value = { state: 'busy' };
  try {
    const result = await CheckForUpdates();
    if (!result?.ok) {
      if (manual) checkUpdateResult.value = { state: 'failed' };
      return;
    }
    updateAutoSupported.value = !!result.canAutoUpdate;
    const latest = String(result.tag || '').trim();
    if (!isNewerReleaseVersion(latest, buildVersion)) {
      if (manual) checkUpdateResult.value = { state: 'latest' };
      return;
    }
    latestReleaseVersion.value = latest;
    updateAvailable.value = true;
    if (manual) checkUpdateResult.value = { state: 'found', version: latest };

    // Skipped versions never bother the user automatically. The green icon
    // still appears so the user can manually trigger if they change their mind.
    if (result.skipped) return;

    // If this version is already staged, jump straight to "ready" so the
    // user sees the restart prompt without re-downloading.
    if (result.canAutoUpdate && result.stagedVersion === latest) {
      updateState.value = 'ready';
      updateModalVisible.value = true;
      return;
    }

    // Auto-update path: platform supports it, user has not disabled it, and
    // the version is not already downloaded. Run silent download in the
    // background; the update:ready / update:error handlers decide whether
    // to surface a modal.
    if (result.canAutoUpdate && result.autoUpdateEnabled) {
      await startUpdate(true);
    }
  } catch (_) {
    // Update checks are best-effort and must never block startup.
    if (manual) checkUpdateResult.value = { state: 'failed' };
  }
}

function openRepositoryPage() {
  Browser.OpenURL(ALLY_REPOSITORY_URL);
}

// Manual "Check for updates" triggered from Settings → About. Delegates to
// checkForUpdates(manual=true) so the result flows back to the SettingsModal.
function onCheckUpdate() {
  void checkForUpdates(true);
}

// Start the self-update flow: download → extract → ready → apply → restart.
// Supported on Windows x64 (ZIP) and macOS (DMG). On other platforms the
// button falls back to openRepositoryPage, so this is only reached when
// updateAutoSupported is true.
//
// When `silent` is true (automatic background download), no progress modal is
// shown and download failures are swallowed. The update:ready event will
// still pop the "ready to restart" modal so the user is prompted to apply.
async function startUpdate(silent = false) {
  updateCancelRequested = false;
  const version = latestReleaseVersion.value;
  if (!version) return;
  // Avoid double-downloading when the user clicks the icon while a silent
  // background download is already running: just surface the progress modal.
  if (updateState.value === 'downloading' || updateState.value === 'extracting') {
    if (!silent) {
      updateSilent.value = false;
      updateModalVisible.value = true;
    }
    return;
  }
  updateSilent.value = silent;
  updateState.value = 'downloading';
  updateProgress.value = { stage: 'download', percent: 0, bytesDownloaded: 0, bytesTotal: 0, version };
  updateError.value = '';
  if (!silent) {
    updateModalVisible.value = true;
  }
  try {
    const result = await DownloadUpdate(version);
    // A cancel clicked while the backend was still resolving the asset URL
    // leaves no 'cancelled' result; drop it so the ready modal never reopens.
    if (updateCancelRequested) {
      updateCancelRequested = false;
      updateState.value = 'idle';
      updateModalVisible.value = false;
      return;
    }
    if (!result?.ok) {
      // A user-initiated cancel is not an error: cancelUpdate already reset
      // the state and the backend cleaned up the staged files.
      if (result?.error === 'cancelled') {
        updateState.value = 'idle';
        updateModalVisible.value = false;
        return;
      }
      updateState.value = 'error';
      updateError.value = result?.error || t('app.update.errors.download');
      if (!silent) updateModalVisible.value = true;
      return;
    }
    updateState.value = 'ready';
    // Always pop the modal on success, even for silent downloads.
    updateSilent.value = false;
    updateModalVisible.value = true;
  } catch (err) {
    updateState.value = 'error';
    updateError.value = String(err?.message || err || t('app.update.errors.download'));
    if (!silent) updateModalVisible.value = true;
  }
}

// Cancel an in-progress update download and dismiss the modal. The backend
// aborts the active DownloadUpdate and removes its staged files.
async function cancelUpdate() {
  updateCancelRequested = true;
  try {
    await CancelUpdate();
  } catch (_) {
    // Best-effort: even if the backend call fails we dismiss locally.
  }
  updateState.value = 'idle';
  updateModalVisible.value = false;
}

function openUpdateReleasesPage() {
  Browser.OpenURL(ALLY_RELEASES_URL);
}

async function applyAndRestart() {
  const version = latestReleaseVersion.value;
  if (!version) return;
  updateState.value = 'applying';
  updateError.value = '';
  try {
    const result = await ApplyUpdate(version);
    if (!result?.ok) {
      updateState.value = 'error';
      updateError.value = result?.error || t('app.update.errors.apply');
      return;
    }
    updateState.value = 'restarting';
    // Give the user a brief moment to see the "closing" state, then quit.
    // On Windows the user manually relaunches; on macOS the new bundle is
    // opened automatically by the backend after this process exits.
    setTimeout(async () => {
      try {
        await QuitForUpdate();
      } catch (_) {
        // If quit fails, fall back to letting the user close Ally manually.
        updateState.value = 'error';
        updateError.value = t('app.update.errors.restart');
      }
    }, 600);
  } catch (err) {
    updateState.value = 'error';
    updateError.value = String(err?.message || err || t('app.update.errors.apply'));
  }
}

function closeUpdateModal() {
  // Only allow closing when not in the middle of a critical operation.
  const busy = updateState.value === 'downloading' || updateState.value === 'extracting' || updateState.value === 'applying' || updateState.value === 'restarting';
  if (busy) return;
  updateModalVisible.value = false;
  if (updateState.value === 'error' || updateState.value === 'ready') {
    updateState.value = 'idle';
  }
}

// Mark the currently staged / latest version as skipped so it will not be
// auto-downloaded or auto-prompted again. Clears the green update indicator
// until a newer release appears. staged files for the skipped version are
// deleted by the backend.
async function skipCurrentVersion() {
  const version = latestReleaseVersion.value;
  if (!version) return;
  try {
    await SkipUpdate(version);
  } catch (_) {
    // Best-effort: even if persistence fails we still dismiss locally.
  }
  updateModalVisible.value = false;
  updateState.value = 'idle';
  updateAvailable.value = false;
}

function applyPlatformClass() {
  // Wails v3's frontend runtime has no Environment() call; derive the platform
  // from the WebView user agent, which reflects the host OS.
  const ua = navigator.userAgent || '';
  let platform = '';
  if (/macintosh|mac os x/i.test(ua)) platform = 'darwin';
  else if (/windows/i.test(ua)) platform = 'windows';
  else if (/linux|crOS|android/i.test(ua)) platform = 'linux';
  if (!platform) return;
  document.body.classList.add(`platform-${platform}`);
}

onMounted(async () => {
  applyPlatformClass();
  // Do not snapshot on window close. Only wait for writes already queued by a
  // successfully completed turn.
  window.addEventListener('keydown', handleGlobalKeydown, true);
  document.addEventListener('pointerdown', handleMermaidOutsidePointerDown, true);
  document.addEventListener('pointerdown', handleOverlayOutsidePointerDown, true);
  document.addEventListener('click', handleMermaidToolbarClick, true);
  document.addEventListener('click', handleCodeCopyClick, true);
  document.addEventListener('click', handleMarkdownLinkClick, true);
  window.addEventListener('resize', refreshWindowMaximisedState);
  window.addEventListener('focus', refreshWindowMaximisedState);
  bindRuntimeEvents();
  void checkForUpdates();
  // Schedule a periodic re-check so long-running sessions (tray mode) still
  // discover new releases without requiring a restart. The first interval
  // fires 2h after the startup check; manual checks from Settings → About
  // do not reset this timer.
  updateCheckTimer = window.setInterval(() => { void checkForUpdates(); }, UPDATE_CHECK_INTERVAL_MS);
  // Pre-load skills before init so welcome message has the count
  try { await refreshSkillState(); } catch (_) { /* ignore */ }
  await init();
  await Promise.all([loadScheduledTasks(), loadServices()]);
  await refreshWindowMaximisedState();
});

onUnmounted(() => {
  window.removeEventListener('keydown', handleGlobalKeydown, true);
  document.removeEventListener('pointerdown', handleMermaidOutsidePointerDown, true);
  document.removeEventListener('pointerdown', handleOverlayOutsidePointerDown, true);
  document.removeEventListener('click', handleMermaidToolbarClick, true);
  document.removeEventListener('click', handleCodeCopyClick, true);
  document.removeEventListener('click', handleMarkdownLinkClick, true);
  window.removeEventListener('resize', refreshWindowMaximisedState);
  window.removeEventListener('focus', refreshWindowMaximisedState);
  cleanupRuntimeEvents();
  if (streamFlushRaf) window.cancelAnimationFrame(streamFlushRaf);
  streamFlushRaf = 0;
  streamFlushScheduled = false;
  streamBuffers.clear();
  if (updateCheckTimer) window.clearInterval(updateCheckTimer);
  updateCheckTimer = 0;
  if (fileMentionTimer) window.clearTimeout(fileMentionTimer);
  fileMentionTimer = 0;
  if (saveSessionsTimer) window.clearTimeout(saveSessionsTimer);
  saveSessionsTimer = 0;
  if (toolUpdateFlushTimer) window.clearTimeout(toolUpdateFlushTimer);
  toolUpdateFlushTimer = 0;
  toolUpdateFlushScheduled = false;
  toolUpdateBuffers.clear();
  mermaidObserver?.disconnect();
  mermaidObserver = null;
  for (const node of mermaidObservedNodes) node._mermaidCleanup?.();
  mermaidObservedNodes.clear();
  mermaidSvgCache.clear();
  mermaidSvgCacheChars = 0;
  for (const sid of Object.keys(pendingAttachmentsBySession)) {
    for (const att of pendingAttachmentsBySession[sid]) releaseAttachmentPreview(att);
  }
  for (const session of sessions.value) releaseSessionAttachments(session);
});
</script>
