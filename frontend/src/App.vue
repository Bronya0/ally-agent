<template>
  <n-config-provider :theme="darkTheme" :theme-overrides="themeOverrides" :locale="naiveLocale" :date-locale="naiveDateLocale" inline-theme-disabled>
    <n-dialog-provider>
      <n-notification-provider>
        <n-message-provider>
          <n-layout class="app-shell" content-style="display: flex; flex-direction: column;">
            <AppHeader
              :workspace-tabs="workspaceTabsWithStatus"
              :active-workspace-id="activeWorkspaceId"
              :update-available="updateAvailable"
              :update-auto-supported="updateAutoSupported"
              :latest-version="latestReleaseVersion"
              :is-maximised="isMaximised"
              :history-options="historyOptions"
              @switch-workspace="switchWorkspaceTab"
              @close-workspace="closeWorkspaceTab"
              @reorder-workspace="reorderWorkspaceTabs"
              @add-workspace="addWorkspaceTab"
              @history-select="onHistorySelect"
              @open-repository="openRepositoryPage"
              @start-update="startUpdate"
              @open-settings="configVisible = true"
              @open-token-stats="tokenStatsVisible = true"
              @minimise="minimiseWindow"
              @toggle-maximise="toggleMaximiseWindow"
              @close-window="closeWindow"
            />

            <!-- Main chat area -->
            <div class="main-area">
              <n-layout class="chat-layout" :content-style="chatLayoutContentStyle">
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
                    <ChatMessages
                      :ref="(instance) => setConversationMessagesRef(tab.id, instance)"
                      :messages="displayMessagesForTab(tab)"
                      :focused-id="focusedToolIdForSession(tab.sessionId)"
                      :render-fn="renderMarkdown"
                      :fmt-k="fmtK"
                      :tools="availableTools"
                      :mcp-servers="mcpServers"
                      @toggle-archive="toggleArchiveMessages"
                      @toggle-tool="toggleToolExpand"
                      @focus-tool="(eventId) => focusTool(tab.sessionId, eventId)"
                      @clear-focus="clearFocus(tab.sessionId)"
                      @export-one-msg="exportOneMessage"
                      @export-all-msgs="exportAllMessages(tab.sessionId)"
                      @submit-ask="(msg, answers) => submitAskResponse(tab.sessionId, msg, answers)"
                    />
                  </n-tab-pane>
                </n-tabs>

              <!-- Fixed todo panel; kept above the transient composer status row. -->
              <Transition name="todo-panel">
                <div v-if="showTodoPanel" :class="['todo-panel', { collapsed: todoPanelCollapsed }]">
                  <button class="todo-panel-header" :title="todoPanelCollapsed ? $t('app.todo.expand') : $t('app.todo.collapse')" @click="toggleTodoPanel">
                    <span>Todo</span>
                    <span class="todo-panel-count">{{ activeTodoCount }}/{{ todos.length }}</span>
                    <span :class="['todo-panel-toggle', { expanded: !todoPanelCollapsed }]"></span>
                  </button>
                  <div v-show="!todoPanelCollapsed" ref="todoPanelListRef" class="todo-panel-list">
                    <div v-for="item in orderedTodoEntries" :key="item.key" :class="['todo-item', item.status]">
                      <span class="todo-status">{{ item.status === 'done' ? '✓' : item.status === 'in_progress' ? '●' : '○' }}</span>
                      <span class="todo-title">{{ item.title }}</span>
                    </div>
                  </div>
                </div>
              </Transition>

              <div class="composer">
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
                  <div class="command-title">{{ $t('app.sessions.title', { count: sessions.length }) }}</div>
                  <div ref="sessionsScrollRef" class="command-scroll">
                    <div
                      v-for="(s, index) in sessions"
                      :key="s.id"
                      :class="['command-item', 'session-item', { active: index === sessionsSelectedIndex, current: s.id === activeSessionId }]"
                      role="button"
                      @mousedown.prevent="selectSession(index)"
                    >
                      <span class="session-index">{{ index + 1 }}</span>
                      <div class="session-body">
                        <span class="session-label">{{ sessionDisplayTitle(s) }}</span>
                        <span v-if="sessionWorkspaceSummary(s) && sessionDisplayTitle(s) !== sessionWorkspaceSummary(s)" class="session-prompt-summary" :title="sessionWorkspacePath(s)">
                          {{ $t('common.workspace') }}: {{ sessionWorkspaceSummary(s) }}
                        </span>
                        <span class="session-time">
                          {{ fmtTime(s.createdAt) }}
                          <template v-if="s.id === activeSessionId && s.isRunning"> ~ {{ $t('app.sessions.inProgress') }}</template>
                          <template v-else-if="s.updatedAt && s.updatedAt !== s.createdAt"> ~ {{ fmtTime(s.updatedAt) }}</template>
                        </span>
                      </div>
                      <span class="session-meta">{{ $t('app.sessions.messages', { count: msgCount(s) }) }} · {{ ctxSize(s) }}t</span>
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
                        ×
                      </button>
                    </div>
                  </div>
                </div>
                <div v-if="retryBanner" class="composer-retry-banner" :title="retryBanner.error">
                  <span class="composer-retry-icon" aria-hidden="true">↻</span>
                  <span class="composer-retry-text">{{ $t('app.run.retryBanner', { attempt: retryBanner.attempt, max: retryBanner.maxAttempts, error: retryBanner.error }) }}</span>
                  <span v-if="retryBanner.totalKeys > 1" class="composer-retry-key">{{ $t('app.run.retryKey', { key: retryBanner.keyIndex + 1, total: retryBanner.totalKeys }) }}</span>
                </div>
                <div v-if="activeSessionRunning || activeGoal || compactLoadingActive" class="composer-run-status">
                  <template v-if="compactLoadingActive">
                    <span class="composer-run-status-dots" aria-hidden="true">
                      <span class="composer-run-status-dot"></span>
                      <span class="composer-run-status-dot"></span>
                      <span class="composer-run-status-dot"></span>
                    </span>
                    <span class="composer-run-prompt">{{ $t('app.compact.compacting') }}</span>
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
                    <span v-if="activeGoal" class="composer-goal-status" :title="activeGoal.objective || ''">
                      <span class="composer-goal-label">{{ $t('tools.kind.goal') }}</span>
                      <span class="composer-goal-objective">{{ activeGoal.objective }}</span>
                      <span v-if="activeGoal.maxTurns" class="composer-goal-progress">{{ activeGoal.turnsUsed || 0 }}/{{ activeGoal.maxTurns }}</span>
                    </span>
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
                  :placeholder="$t('app.composer.placeholder')"
                  @update:value="(v) => { if (tab.sessionId) sessionPromptTexts[tab.sessionId] = v; }"
                  @keydown="handlePromptKeydown"
                  @input="handlePromptInput"
                />
                <div v-if="pendingAttachments.length" class="pending-attachments">
                  <div v-for="att in pendingAttachments" :key="att.id" class="pending-attachment">
                    <span class="pending-attachment-icon">{{ attachmentIcon(att) }}</span>
                    <span class="pending-attachment-name" :title="att.name">{{ att.name }}</span>
                    <span class="pending-attachment-size">{{ fmtBytes(att.size) }}</span>
                    <button class="pending-attachment-remove" @click="removeAttachment(att.id)" :title="$t('app.attachment.remove')">×</button>
                  </div>
                </div>
                <input ref="attachmentInputRef" type="file" multiple class="hidden-file-input" @change="handleAttachmentSelected" />
                <ComposerInfoBar
                  :running="activeSessionRunning"
                  :config="config"
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
                  @add-extra-root="addExtraRoot"
                  @remove-extra-root="removeExtraRoot"
                  @switch-model="switchToModel"
                  @open-config="configVisible = true"
                  @open-git-diff="openGitDiff"
                  @open-workspace="openWorkspaceInFileManager"
                  @change-reasoning-effort="changeReasoningEffort"
                  @open-task-center="openTaskCenter"
                  @new-session="createNewSession"
                  @show-sessions="showSessionList"
                  @compact-context="handleCompactCommand"
                  :get-session-messages="() => activeMessages"
                  :session-title="activeSession?.title || ''"
                />
              </div>
            </n-layout>

            </div>
          </n-layout>
          <SettingsModal
            :visible="configVisible"
            :config-draft="configDraft"
            :check-update-result="checkUpdateResult"
            @close="configVisible = false"
            @closed="focusPromptInput"
            @save="onSettingsSave"
            @skills-changed="onSkillsChanged"
            @mcp-saved="onMcpSaved"
            @background-changed="onBackgroundChanged"
            @check-update="onCheckUpdate"
          />
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
          <TokenStatsModal
            v-if="tokenStatsVisible"
            :show="tokenStatsVisible"
            @close="tokenStatsVisible = false"
          />
          <RenderBoundary :label="$t('app.gitChanges')"><GitDiffModal v-model:show="gitDiffVisible" :git-status="gitStatus" :workspace="config.workspace" /></RenderBoundary>

          <n-modal v-model:show="updateModalVisible" preset="card" :title="$t('app.update.title')" class="update-modal" :mask-closable="false" :close-on-esc="false" :show-close="!isUpdateBusy">
            <div class="update-modal-body">
              <template v-if="updateState === 'downloading' || updateState === 'extracting'">
                <div class="update-status-text">{{ updateState === 'downloading' ? $t('app.update.downloading') : $t('app.update.extracting') }}</div>
                <div class="update-progress-bar">
                  <div class="update-progress-fill" :style="{ width: `${updateProgress.percent}%` }"></div>
                </div>
                <div class="update-progress-meta">
                  <span>{{ updateProgress.percent }}%</span>
                  <span v-if="updateState === 'downloading' && updateProgress.bytesTotal > 0">{{ formatBytes(updateProgress.bytesDownloaded) }} / {{ formatBytes(updateProgress.bytesTotal) }}</span>
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
            </div>
          </n-modal>

          <SplashScreen v-if="splashVisible" @done="splashVisible = false" />
        </n-message-provider>
      </n-notification-provider>
    </n-dialog-provider>
  </n-config-provider>
</template>

<script setup>
import { computed, defineAsyncComponent, h, nextTick, onErrorCaptured, onMounted, onUnmounted, reactive, ref, watch } from 'vue';
import { createDiscreteApi, darkTheme } from 'naive-ui';
import MarkdownIt from 'markdown-it';
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
import {
  clearSessionSnapshotStore,
  loadSessionSnapshots,
} from './utils/sessionStore.mjs';
import {
  CancelRun,
  CheckForUpdates,
  GetConfig,
  GetContextBreakdown,
  GetSessionContextTokens,
  GetWorkspaceTokenUsage,
  ResetWorkspaceTokenUsage,
  GetSubagents,
  GetGitStatus,
  SaveConfig,
  SelectWorkspace,
  StartChat,
  CompactSession,
  ListSkills,
  ListTools,
  ListSessions,
  LoadSession,
  SaveSession,
  SaveSessionIndex,
  OpenWorkspaceInFileManager,
  ActivateSkill,
  DeactivateSkill,
  GetActiveSkills,
  SwitchModel,
  GetTodos,
  GetGoal,
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
  SubmitAskResponse,
  SearchWorkspacePaths,
  DownloadUpdate,
  ApplyUpdate,
  QuitForUpdate,
  SkipUpdate,
  GetBackgroundImageURL,
} from '../bindings/ally-dev/internal/app/app';
import { Application, Browser, Events, Window } from '@wailsio/runtime';
import AllyWordmark from './components/AllyWordmark.vue';
import ComposerInfoBar from './components/ComposerInfoBar.vue';
import MessageAttachments from './components/MessageAttachments.vue';
import ReadGroupCard from './components/ReadGroupCard.vue';
import RenderBoundary from './components/RenderBoundary.vue';
import SplashScreen from './components/SplashScreen.vue';
import SubagentInlineCard from './components/SubagentInlineCard.vue';
import WelcomeMessage from './components/WelcomeMessage.vue';
import ToolCallCard from './components/ToolCallCard.vue';
import AppHeader from './components/AppHeader.vue';
import CommandMenu from './components/CommandMenu.vue';
import FileMentionMenu from './components/FileMentionMenu.vue';
import SettingsModal from './components/SettingsModal.vue';
import ChatMessages from './components/ChatMessages.vue';
import TaskCenterPanel from './components/TaskCenterPanel.vue';
import TokenStatsModal from './components/TokenStatsModal.vue';
import { assignConfig, defaultConfig } from './utils/config.mjs';
import { modelConfigIdentity, normalizeApiKeysArray, normalizeReasoningEffort } from './utils/modelConfigIO.mjs';
import { buildVersion } from './utils/buildVersion.js';
import { computeEditStats, formatEditStats } from './utils/diff.js';
import { isNewerReleaseVersion } from './utils/versionCheck.mjs';
import { findSessionWorkspaceTab, isEditableNavigationTarget, shouldAcceptRunTerminal } from './utils/sessionState.mjs';
import { orderTodoPanelEntries } from './utils/todoPanel.mjs';
import { formatDateTime, naiveDateLocale, naiveLocale, reasoningEffortLabel, t, welcomeGreeting as localizedWelcomeGreeting } from './i18n.mjs';
import {
  displaySourceMessages as buildDisplaySourceMessages,
  formatHttpToolTitle,
  isRenderableMessage,
} from './utils/toolPreview.mjs';
import {
  commitToolEventMessage as commitToolEventById,
  findToolEventMessage as findToolEventById,
} from './utils/toolEventState.mjs';
import { unwrapWailsEvent } from './utils/wailsEvent.mjs';

const GitDiffModal = defineAsyncComponent(() => import('./components/GitDiffModal.vue'));

onErrorCaptured((err, _instance, info) => {
  console.error('[ui:error]', info, err);
  return false;
});

const conversationMessagesRefs = new Map();
const promptInputRefs = reactive({});
function setPromptInputRef(tabId, el) {
  if (el) promptInputRefs[tabId] = el;
  else delete promptInputRefs[tabId];
}
const promptComposing = ref(false);
let promptCompositionEndedAt = 0;
let fileMentionTimer = 0;
let fileMentionRequestId = 0;

const themeOverrides = {
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

const { message } = createDiscreteApi(['message'], {
  configProviderProps: {
    theme: darkTheme,
    themeOverrides,
    locale: naiveLocale,
    dateLocale: naiveDateLocale,
  },
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

const MERMAID_FENCE_DIRECTIVES = new Map([
  ['mermaid', ''],
  ['mmd', ''],
  ['flowchart', 'flowchart TD'],
  ['graph', 'graph TD'],
  ['sequencediagram', 'sequenceDiagram'],
  ['sequence', 'sequenceDiagram'],
  ['classdiagram', 'classDiagram'],
  ['statediagram', 'stateDiagram'],
  ['statediagram-v2', 'stateDiagram-v2'],
  ['erdiagram', 'erDiagram'],
  ['journey', 'journey'],
  ['gantt', 'gantt'],
  ['pie', 'pie'],
  ['gitgraph', 'gitGraph'],
  ['mindmap', 'mindmap'],
  ['timeline', 'timeline'],
  ['quadrantchart', 'quadrantChart'],
  ['requirementdiagram', 'requirementDiagram'],
  ['c4diagram', 'c4Diagram'],
  ['sankey-beta', 'sankey-beta'],
  ['xychart-beta', 'xychart-beta'],
  ['block-beta', 'block-beta'],
  ['architecture-beta', 'architecture-beta'],
  ['packet-beta', 'packet-beta'],
]);

const MERMAID_SOURCE_START_RE = /^(?:---[\s\S]*?---\s*)?(?:flowchart|graph|sequenceDiagram|classDiagram|stateDiagram(?:-v2)?|erDiagram|journey|gantt|pie|gitGraph|mindmap|timeline|quadrantChart|requirementDiagram|c4Diagram|sankey-beta|xychart-beta|block-beta|architecture-beta|packet-beta)\b/i;

let markdownRenderStreaming = false;
let mermaidModulePromise = null;
let mermaidInitialized = false;
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

function mermaidFenceSpec(lang) {
  const raw = String(lang || '').trim();
  if (!raw) return null;
  const first = raw.split(/\s+/)[0].toLowerCase();
  if (!MERMAID_FENCE_DIRECTIVES.has(first)) return null;
  return {
    raw,
    first,
    directive: MERMAID_FENCE_DIRECTIVES.get(first),
  };
}

function normalizeMermaidSource(code, spec) {
  const source = String(code || '').trim();
  if (!source || !spec?.directive) return source;
  if (MERMAID_SOURCE_START_RE.test(source)) return source;
  if (spec.first === 'flowchart' || spec.first === 'graph') {
    const directive = /\s/.test(spec.raw) ? spec.raw : spec.directive;
    return `${directive}\n${source}`;
  }
  return `${spec.directive}\n${source}`;
}

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
});

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
    const highlightLang = isShellLanguage(lang) ? 'bash' : lang;
    const highlighted = highlightLang && hljs.getLanguage(highlightLang)
      ? hljs.highlight(token.content, { language: highlightLang }).value
      : hljs.highlightAuto(token.content).value;
    return `${renderHighlightedCodeBlock(token.content, highlighted, isShellLanguage(lang) ? 'shell-code' : '')}\n`;
  } catch (_) {
    return `${renderHighlightedCodeBlock(token.content, markdown.utils.escapeHtml(token.content), '')}\n`;
  }
};

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

async function loadMermaidModule() {
  if (!mermaidModulePromise) {
    mermaidModulePromise = import('mermaid').then((mod) => {
      const mermaid = mod.default || mod;
      if (!mermaidInitialized) {
        mermaid.initialize({
          startOnLoad: false,
          securityLevel: 'strict',
          theme: 'base',
          darkMode: true,
          htmlLabels: false,
          flowchart: {
            curve: 'linear',
            useMaxWidth: true,
            nodeSpacing: 36,
            rankSpacing: 48,
          },
          sequence: {
            useMaxWidth: true,
            wrap: true,
            diagramMarginX: 24,
            diagramMarginY: 18,
            actorMargin: 48,
          },
          themeVariables: {
            darkMode: true,
            background: '#2b2b2b',
            mainBkg: '#323232',
            secondBkg: '#383838',
            primaryColor: '#323232',
            primaryTextColor: '#a9b7c6',
            primaryBorderColor: '#cc7832',
            secondaryColor: '#353535',
            secondaryTextColor: '#a9b7c6',
            secondaryBorderColor: '#6897bb',
            tertiaryColor: '#303330',
            tertiaryTextColor: '#a9b7c6',
            tertiaryBorderColor: '#6a8759',
            lineColor: '#808080',
            textColor: '#a9b7c6',
            nodeTextColor: '#a9b7c6',
            noteBkgColor: '#3b352b',
            noteTextColor: '#d7ba7d',
            noteBorderColor: '#bbb529',
            actorBkg: '#323232',
            actorBorder: '#6897bb',
            actorTextColor: '#a9b7c6',
            actorLineColor: '#666666',
            signalColor: '#a9b7c6',
            signalTextColor: '#a9b7c6',
            labelBoxBkgColor: '#323232',
            labelBoxBorderColor: '#6a8759',
            labelTextColor: '#a9b7c6',
            loopTextColor: '#d7ba7d',
            activationBorderColor: '#cc7832',
            activationBkgColor: '#3b332b',
            sequenceNumberColor: '#2b2b2b',
            fontFamily: 'Inter, "Microsoft YaHei", sans-serif',
            fontSize: '14px',
          },
          themeCSS: '.node rect,.node polygon,.node circle,.node ellipse{stroke-width:1.4px}.edgePath .path,.flowchart-link{stroke-width:1.5px}.nodeLabel,.label text{font-weight:500}.cluster rect{fill:#2f2f2f!important;stroke:#5d5d5d!important}',
        });
        mermaidInitialized = true;
      }
      return mermaid;
    });
  }
  return mermaidModulePromise;
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

  if (navigator.clipboard?.writeText) {
    navigator.clipboard.writeText(code).then(showCopied).catch(() => {
      fallbackCopy(code, showCopied);
    });
  } else {
    fallbackCopy(code, showCopied);
  }
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

function fallbackCopy(text, onSuccess) {
  try {
    const ta = document.createElement('textarea');
    ta.value = text;
    ta.style.position = 'fixed';
    ta.style.opacity = '0';
    document.body.appendChild(ta);
    ta.select();
    document.execCommand('copy');
    document.body.removeChild(ta);
    onSuccess();
  } catch (_) {
    message.error(t('app.copy.failed'));
  }
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
  background.setAttribute('fill', '#2b2b2b');
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

// Per-tab model selection. Maps a runtime workspace Tab id to the selected
// model identity. Stored in localStorage (not backend config.json) so the
// backend stays workspace-agnostic; StartChat's overlay already carries the
// active model fields from the frontend config. The Tab id is intentional:
// two Tabs pointing at the same workspace must not share a model selection.
const modelByTab = reactive({});
const MODEL_BY_TAB_KEY = 'ally_model_by_tab';

function loadModelByTab() {
  try {
    const raw = localStorage.getItem(MODEL_BY_TAB_KEY);
    const parsed = raw ? JSON.parse(raw) : {};
    for (const [k, v] of Object.entries(parsed)) {
      modelByTab[k] = v;
    }
  } catch (_) { /* ignore corrupt entries */ }
}

function saveModelByTab() {
  try {
    localStorage.setItem(MODEL_BY_TAB_KEY, JSON.stringify(modelByTab));
  } catch (_) { /* ignore quota errors */ }
}

// Apply a model preset (by index in config.models) to the local config /
// configDraft without calling the backend SwitchModel. Used on workspace tab
// switch so each tab restores its remembered model. Mirrors the field set
// that backend SwitchModel copies from ModelConfig to the top-level config.
function applyModelToConfig(index) {
  const model = (config.models || [])[index];
  if (!model) return;
  for (const target of [config, configDraft]) {
    target.providerName = model.providerName || target.providerName;
    target.apiFormat = model.apiFormat || target.apiFormat;
    target.baseUrl = model.baseUrl || target.baseUrl;
    target.model = model.model || target.model;
    target.temperature = model.temperature ?? target.temperature;
    target.maxTokens = model.maxTokens ?? target.maxTokens;
    if (model.contextWindow > 0) target.contextWindow = model.contextWindow;
    target.tokenParam = model.tokenParam || target.tokenParam;
    target.reasoningTag = String(model.reasoningTag || '').trim() || 'reasoning_content';
    target.reasoningEffort = normalizeReasoningEffort(model.reasoningEffort);
    const keys = normalizeApiKeysArray(
      Array.isArray(model.apiKeys) && model.apiKeys.length
        ? model.apiKeys
        : (model.apiKey ? [model.apiKey] : [])
    );
    target.apiKeys = keys;
    target.apiKey = keys[0] || '';
  }
}

// Find the index in config.models matching the current top-level config.
// Returns -1 when no match is found (e.g. model not in the saved list).
function findCurrentModelIndex() {
  const identity = modelConfigIdentity(config);
  if (!identity) return -1;
  return (config.models || []).findIndex((m) => modelConfigIdentity(m) === identity);
}

function findModelIndexForTab(tab) {
  const remembered = modelByTab[tab?.id];
  if (typeof remembered === 'string' && remembered) {
    return (config.models || []).findIndex((model) => modelConfigIdentity(model) === remembered);
  }
  // Accept numeric values written by an intermediate development build, then
  // normalize them to model identities the next time this Tab is changed.
  if (Number.isInteger(remembered) && remembered >= 0 && remembered < (config.models || []).length) {
    return remembered;
  }
  return -1;
}

function rememberModelForTab(tab, index = findCurrentModelIndex()) {
  if (!tab || !Number.isInteger(index) || index < 0 || index >= (config.models || []).length) return;
  const model = config.models[index];
  const identity = modelConfigIdentity(model);
  if (!identity || modelByTab[tab.id] === identity) return;
  modelByTab[tab.id] = identity;
  saveModelByTab();
}

function rememberActiveTabModel() {
  const tab = workspaceTabs.value.find((item) => item.id === activeWorkspaceId.value);
  rememberModelForTab(tab);
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
const configVisible = ref(false);
const focusedToolIdsBySession = reactive({});
const workspaceTabs = ref([]);
const activeWorkspaceId = ref('');
const extraRoots = ref([]);
const workspaceHistory = ref(loadWorkspaceHistory());
const settingsPage = ref('general');
const showSkillsPanel = ref(false);
const todos = ref([]);
const todosBySession = reactive({});
const goalsBySession = reactive({});
const todoRevisionsBySession = reactive({});
const todoPanelCollapsed = ref(false);
const todoPanelListRef = ref(null);
const isMaximised = ref(false);
const availableSkills = ref([]);
const activeSkillNames = ref([]);
const skillsLoading = ref(false);
const skillToggleInFlight = ref('');
const availableTools = ref([]);
const scheduledTasks = ref([]);
const services = ref([]);
const taskCenterVisible = ref(false);
const tokenStatsVisible = ref(false);
const scheduledTasksLoading = ref(false);
const servicesLoading = ref(false);
const scheduledTaskDeletingIds = ref([]);
const serviceStoppingIds = ref([]);
const subRuns = ref([]);
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
// During large payload streams (e.g. create_file with thousands of lines)
// processing each event synchronously blocks the main thread, freezes the
// tool card, and balloons webview memory. Buffer the latest event per tool
// call and flush on a timer so the UI repaints between updates.
const toolUpdateBuffers = new Map();
let toolUpdateFlushScheduled = false;
let toolUpdateFlushTimer = 0;
let completionAudioContext = null;
let lastCompletionSoundAt = 0;
const pendingAttachments = ref([]);
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
// Result object reported to SettingsModal's About page so the manual
// "Check for updates" button gets explicit latest/found/failed feedback.
// State: 'idle' | 'busy' | 'latest' | 'found' | 'failed'.
const checkUpdateResult = ref({ state: 'idle' });
const isUpdateBusy = computed(() => {
  return updateState.value === 'downloading' || updateState.value === 'extracting' || updateState.value === 'applying' || updateState.value === 'restarting';
});

const ALLY_REPOSITORY_URL = 'https://github.com/Bronya0/ally-agent';


const builtinCommands = [
  { key: 'new', label: '/new', description: t('commands.new'), text: '', special: 'new' },
  { key: 'goal', label: '/goal', description: t('commands.goal'), text: '', special: 'goal' },
  { key: 'skills', label: '/skills', description: t('commands.skills'), text: '', special: 'skills' },
  { key: 'sessions', label: '/sessions', description: t('commands.sessions'), text: '', special: 'sessions' },
  { key: 'init', label: '/init', description: t('commands.init'), text: '', special: 'init' },
  { key: 'note', label: '/note', description: t('commands.note'), text: '', special: 'remember' },
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

First, explore the codebase, then create or update AGENTS.md. If AGENTS.md exists, read it and update it with edit. If it does not exist, create it with create_file.`;

const REMEMBER_PROMPT = `Save durable project knowledge from this conversation.

1. Extract only high-confidence, reusable facts: architecture decisions, conventions, hidden dependencies, gotchas, important file locations, and data flows.
2. Each saved bullet must be concise and cite concrete file paths when relevant.
3. Use memory_read first if an existing memory from the global memory index may already cover this project.
4. Save or update the knowledge with memory_write. Use an explicit stable path such as project-knowledge/<workspace-or-project-name>.md.
5. Do not edit AGENTS.md for this command. Do not save speculation, one-off task status, transient bug-fix notes, or generic advice.`;

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
  if (!title || title === workspace || title === sessionWorkspacePath(session) || title === 'Session' || title === '会话' || title === t('app.sessions.history')) {
    return workspace || t('app.sessions.new');
  }
  return promptSummaryText(title, SESSION_PROMPT_SUMMARY_MAX_CHARS);
}

const latestUserPromptSummary = computed(() => latestPromptSummaryForSession(activeSession.value));
const activeSessionRunning = computed(() => !!activeSession.value?.isRunning);
const scheduledTaskRunningCount = computed(() => scheduledTasks.value.filter((task) => task?.running).length);
const serviceRunningCount = computed(() => services.value.filter((service) => ['starting', 'running'].includes(service?.status)).length);
const activeTodoCount = computed(() => todos.value.filter((item) => item?.status !== 'done').length);
const activeGoal = computed(() => goalsBySession[activeSessionId.value] || null);
const showTodoPanel = computed(() => todos.value.length > 0 && activeTodoCount.value > 0);
const orderedTodoEntries = computed(() => orderTodoPanelEntries(todos.value));

function scrollTodoPanelToFocus() {
  if (todoPanelCollapsed.value) return;
  nextTick(() => {
    const list = todoPanelListRef.value;
    if (list) list.scrollTop = 0;
  });
}

function toggleTodoPanel() {
  todoPanelCollapsed.value = !todoPanelCollapsed.value;
  scrollTodoPanelToFocus();
}

watch(orderedTodoEntries, () => {
  scrollTodoPanelToFocus();
});

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
    // Only merge 2+ consecutive read tool-call cards
    if (m.role === 'tool_call' && m.kind === 'read' && m.status !== 'error') {
      // Count consecutive reads
      let j = i + 1;
      while (j < src.length && src[j].role === 'tool_call' && src[j].kind === 'read' && src[j].status !== 'error') j++;
      const count = j - i;
      if (count < 2) {
        // Single read card — handle read (and legacy batch_read) specially
        if ((m.name === 'read' || m.name === 'batch_read') && m.batchEntries && m.batchEntries.length > 0) {
          out.push({
            role: 'tool_call',
            kind: 'read-group',
            status: m.batchEntries.some((entry) => entry.status === 'error') ? 'error' : m.status,
            time: m.time,
            eventId: m.eventId,
            expanded: true,
            readEntries: m.batchEntries,
            readTotalLines: m.batchEntries.reduce((s, e) => s + (e.lineCount || 0), 0),
            durationMs: m.durationMs || 0,
            durationText: m.durationText || '',
          });
          i++;
          continue;
        }
        // Regular single read: pass through unchanged
        out.push(m);
        i++;
        continue;
      }
      const group = {
        role: 'tool_call',
        kind: 'read-group',
        status: 'success',
        time: m.time,
        eventId: m.eventId,
        expanded: true,
        readEntries: [],
        durationMs: 0,
        durationText: '',
      };
      let totalLines = 0;
      let allDone = true;
      let hasError = false;
      while (i < j) {
        const entry = src[i];
        // If this is a read (or legacy batch_read) with parsed entries, expand inline
        if ((entry.name === 'read' || entry.name === 'batch_read') && entry.batchEntries && entry.batchEntries.length > 0) {
          for (const be of entry.batchEntries) {
            const entryStatus = be.status || entry.status;
            if (entryStatus === 'error') hasError = true;
group.readEntries.push({ title: be.title, chip: be.chip, lineCount: be.lineCount || 0, totalLines: be.totalLines || be.lineCount || 0, startLine: be.startLine || 1, endLine: be.endLine || be.totalLines || 0, truncated: !!be.truncated, body: '', status: entryStatus, expanded: false });
            totalLines += be.lineCount || 0;
          }
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
      out.push(group);
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
  return (promptText.value.trim().length > 0 || pendingAttachments.value.length > 0) && !(s && (s.runId || s.isRunning));
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
    session.contextTokens = contextValue;
    sessionTokensCache[requestedSessionId] = contextValue;
    if (!session.isRunning && (session.hasSnapshot || session.messageCount > 0)) {
      enqueueSessionWrite(() => SaveSessionIndex(sessionIndexEntry(session))).catch(() => {});
    }
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
    await OpenWorkspaceInFileManager();
  } catch (err) {
    message.warning(t('app.workspace.openFailed', { error: err }));
  }
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
      session.contextTokens = value;
      sessionTokensCache[sid] = value;
      if (!session.isRunning && (session.hasSnapshot || session.messageCount > 0)) {
        enqueueSessionWrite(() => SaveSessionIndex(sessionIndexEntry(session))).catch(() => {});
      }
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
  loadGoal(activeSessionId.value);
  nextTick(() => {
    cleanupDisconnectedMermaidNodes();
    observePendingMermaidDiagrams();
  });
});

watch(() => config.workspace, () => {
  closeFileMentionMenu();
});


const contextUsed = computed(() => {
  const n = contextTokens.value;
  if (n >= 1000) return (n / 1000).toFixed(1) + 'k';
  return String(n);
});
const contextWindow = computed(() => config.contextWindow || 1000000);
const contextMax = computed(() => {
  const m = contextWindow.value;
  if (m >= 1000000) return (m / 1000000).toFixed(1) + 'M';
  if (m >= 1000) return (m / 1000).toFixed(1) + 'K';
  return String(m);
});
const contextPercent = computed(() => {
  const used = contextTokens.value;
  const max = contextWindow.value;
  const pct = max > 0 ? (used / max * 100) : 0;
  return pct.toFixed(1) + '%';
});

const contextPct = computed(() => {
  const used = contextTokens.value;
  const max = contextWindow.value;
  return max > 0 ? (used / max * 100) : 0;
});
const contextUsageStyle = computed(() => {
  const pct = Math.max(0, Math.min(100, contextPct.value));
  let hue;
  let saturation = 58;
  let lightness = 66;
  if (pct <= 20) {
    hue = 145 - (pct / 20) * 45;
  } else if (pct <= 40) {
    hue = 100 - ((pct - 20) / 20) * 52;
    saturation = 62;
    lightness = 65;
  } else {
    hue = 48 - ((pct - 40) / 60) * 42;
    saturation = 66;
    lightness = 64 - ((pct - 40) / 60) * 6;
  }
  return { color: `hsl(${Math.round(hue)} ${Math.round(saturation)}% ${Math.round(lightness)}%)` };
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

function dedupeWorkspaceHistory(paths, limit = 30) {
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
  const recent = [...workspaceHistory.value].reverse().slice(0, 30);
  if (recent.length === 0) return [{ label: t('app.history.empty'), disabled: true, key: '__empty__' }];
  return recent.map((path) => {
    const label = path.split(/[/\\]/).filter(Boolean).pop() || path;
    return {
      label: () => h('span', { class: 'hist-label' }, [
        h('span', { class: 'hist-name' }, label),
        h('span', { class: 'hist-path' }, `  —  ${path}`),
        h('span', {
          class: 'hist-del',
          title: t('app.history.remove'),
          onClick: (e) => { e.stopPropagation(); removeFromHistory(path); },
        }, '×'),
      ]),
      key: path,
    };
  });
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
  const skillCount = availableSkills.value.length;
  const rows = [];
  if (workspacePath !== null) {
    rows.push({ kind: 'workspace', label: t('common.workspace'), value: workspacePath || t('common.notSelected') });
  }
  const gitBashPath = String(config.gitBashPath || '').trim();
  if (gitBashPath) {
    rows.push({ kind: 'gitbash', label: t('welcome.gitBash'), value: gitBashPath });
  }
  rows.push({ kind: 'model', label: t('common.model'), value: `${config.providerName || '-'} · ${config.model || '-'}` });
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
      msg.welcome.rows = rows;
      msg.content = buildWelcomeContent(msg.welcome);
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
  if (session.workspace) return session.workspace;
  for (const msg of session.messages || []) {
    const rows = msg?.welcome?.rows;
    if (!Array.isArray(rows)) continue;
    const row = rows.find((item) => item?.kind === 'workspace' || item?.label === '工作区' || item?.label === 'Workspace');
    const value = String(row?.value || '').trim();
    if (value && value !== '未选择' && value !== 'Not selected') return value;
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
  if (tab) {
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
  config.workspace = workspace;
  configDraft.workspace = workspace;
  addToHistory(workspace);
  loadPromptHistory(workspace);
  try {
    await saveWorkspaceConfig({ ...config });
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
      session.contextTokens = Number(snapshot.contextTokens || session.contextTokens || 0);
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

function closeWorkspaceTab(id) {
  if (workspaceTabs.value.length <= 1) return;
  const idx = workspaceTabs.value.findIndex((t) => t.id === id);
  if (idx === -1) return;
  const tab = workspaceTabs.value[idx];
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
      delete todosBySession[tab.sessionId];
      delete todoRevisionsBySession[tab.sessionId];
      delete focusedToolIdsBySession[tab.sessionId];
      displayMessagesCacheBySession.delete(tab.sessionId);
      ReleaseSession(tab.sessionId).catch(() => {});
    }
  }
  saveSessions();
  if (activeWorkspaceId.value === id) {
    const newIdx = Math.min(idx, workspaceTabs.value.length - 1);
    switchWorkspaceTab(workspaceTabs.value[newIdx].id);
  }
}



async function switchWorkspaceTab(id) {
  const tab = workspaceTabs.value.find((t) => t.id === id);
  if (!tab) return;
  const switchVersion = ++workspaceSwitchVersion;
  const linkedSession = ensureWorkspaceTabSession(tab);
  saveSessions();
  activeWorkspaceId.value = id;
  config.workspace = tab.path;
  configDraft.workspace = tab.path;
  prepareFooterStatsForTarget(id, tab.path);
  subRuns.value = [];
  // 切换 Tab 时恢复该 Tab 关联 session 的 extraRoots
  if (linkedSession && Array.isArray(linkedSession.extraRoots)) {
    extraRoots.value = [...linkedSession.extraRoots];
  } else {
    extraRoots.value = [];
  }
  loadPromptHistory(tab.path);
  // Restore the model remembered for this Tab before SaveConfig so the
  // backend sync matches the Tab's model. This is a local-only switch —
  // SwitchModel is not called to avoid touching another Tab's selection.
  const rememberedIndex = findModelIndexForTab(tab);
  if (rememberedIndex >= 0) {
    const currentIndex = findCurrentModelIndex();
    if (currentIndex !== rememberedIndex) {
      applyModelToConfig(rememberedIndex);
    }
  } else {
    // First visit to this Tab: inherit the current model as its independent
    // baseline. Later changes are stored under this Tab's id only.
    rememberModelForTab(tab);
  }
  try {
    await saveWorkspaceConfig({ ...config });
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
  }
  await refreshFooterStats({
    tabId: id,
    sessionId: linkedSession?.id || activeSessionId.value,
    workspace: tab.path,
  });
  // 切换 Tab 后自动聚焦新 Tab 的输入框，避免手动点击才能输入
  nextTick(() => focusPromptInput());
}

function syncConfigToActiveTab() {
  const tab = workspaceTabs.value.find((t) => t.id === activeWorkspaceId.value);
  if (tab && config.workspace !== tab.path) {
    tab.path = config.workspace || '';
    tab.label = workspaceLabel(tab.path);
  }
  const session = activeSession.value;
  if (session && config.workspace) session.workspace = config.workspace;
}

function newSession(title) {
  saveSessions();
  const id = crypto.randomUUID ? crypto.randomUUID() : `s-${Date.now()}-${Math.random()}`;
  const now = Date.now();
  const workspace = config.workspace || '';
  const sessionTitle = title || (workspace ? workspaceLabel(workspace) : t('app.sessions.new'));
  const session = { id, title: sessionTitle, workspace, extraRoots: [], messages: [], messagesLoaded: true, runId: '', isRunning: false, createdAt: now, updatedAt: now };
  sessions.value.unshift(session);
  activeSessionId.value = id;
  bindSessionToActiveWorkspaceTab(session);
  // 新会话默认无附加工作区
  extraRoots.value = [];
  promptText.value = '';
  // 新会话默认无 todo，避免上一会话 todo 残留
  todos.value = [];
  loadTodos(id);
  addWelcome(workspace);
  // Reset workspace token usage for new session
  const ws = workspace;
  if (ws) {
    ResetWorkspaceTokenUsage(ws);
    workspaceTokenUsage.value = emptyWorkspaceTokenUsage();
  }
}

function isDefaultSessionTitle(title) {
  const value = String(title || '');
  return value === '默认会话'
    || value === 'Default session'
    || value.startsWith('会话')
    || value.startsWith('Session ');
}

async function selectSession(index) {
  if (index < 0 || index >= sessions.value.length) return;
  const target = sessions.value[index];
  saveSessions();
  sessionsVisible.value = false;
  await activateSelectedSession(target);
  sessionsVisible.value = false;
  subRuns.value = [];
  scrollMessagesToBottom();
}

function createReplacementSession(title = t('app.sessions.new'), workspacePath = '') {
  const id = crypto.randomUUID ? crypto.randomUUID() : `s-${Date.now()}-${Math.random()}`;
  const now = Date.now();
  const session = { id, title, workspace: workspacePath || '', extraRoots: [], messages: [], messagesLoaded: true, runId: '', isRunning: false, createdAt: now, updatedAt: now };
  session.messages.push(buildWelcomeMessage(workspacePath || t('common.notSelected')));
  return session;
}

function deleteSession(index) {
  if (index < 0 || index >= sessions.value.length) return;
  const target = sessions.value[index];
  if (!target) return;
  if (target.runId || target.isRunning) {
    message.warning(t('app.sessions.runningDeleteBlocked'));
    return;
  }

  const deletedId = target.id;
  const wasActive = deletedId === activeSessionId.value;
  const fallback = sessions.value[index + 1] || sessions.value[index - 1] || null;
  const linkedTabs = workspaceTabs.value.filter((tab) => tab.sessionId === deletedId);
  releaseSessionAttachments(target);
  sessions.value.splice(index, 1);

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
    subRuns.value = [];
    loadTodos(replacementId);
    scrollMessagesToBottom();
  }

  delete todosBySession[deletedId];
  delete todoRevisionsBySession[deletedId];
  delete sessionPromptTexts[deletedId];
  delete focusedToolIdsBySession[deletedId];
  displayMessagesCacheBySession.delete(deletedId);
  enqueueSessionWrite(() => DeleteSession(deletedId)).catch(() => {});
  const expanded = new Set(expandedArchiveSessions.value);
  expanded.delete(deletedId);
  expandedArchiveSessions.value = expanded;

  sessionsSelectedIndex.value = Math.max(0, Math.min(index, sessions.value.length - 1));
  saveSessions();
}

function addWelcome(workspacePath = config.workspace || '') {
  const session = activeSession.value;
  if (!session) return;
  if (workspacePath) session.workspace = workspacePath;
  session.messages.push(buildWelcomeMessage(workspacePath || t('common.notSelected')));
}

async function init() {
  try {
    const loaded = await GetConfig();
    assignConfig(config, loaded);
    assignConfig(configDraft, loaded);
  } catch (err) {
    message.error(t('app.config.readFailed', { error: err }));
  }
  loadModelByTab();

  // Init workspace tabs from config. Model state belongs to this Tab, not to
  // its workspace path, so a second Tab can point to the same path safely.
  const ws = config.workspace || '';
  const tab = createWorkspaceTab(ws);
  workspaceTabs.value.push(tab);
  activeWorkspaceId.value = tab.id;
  if (tab.sessionId) activeSessionId.value = tab.sessionId;
  rememberModelForTab(tab);
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
  let last = session.messages[session.messages.length - 1];
  if (!last || last.role !== 'assistant' || last.error || last.system || last.done || last.runId !== runId) {
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
// argument payloads (e.g. create_file content) on every delta.
function bufferToolUpdate(data) {
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
  // viewport upward while the HTML arguments stream.
  if (lastActiveSessionId) {
    scrollMessagesToBottom({ alignToLastToolCard: lastActiveToolName !== 'render_html' });
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

function getCompletionAudioContext() {
  if (typeof window === 'undefined') return null;
  const AudioCtor = window.AudioContext || window.webkitAudioContext;
  if (!AudioCtor) return null;
  if (!completionAudioContext) {
    completionAudioContext = new AudioCtor();
  }
  return completionAudioContext;
}

function primeCompletionAudio() {
  const ctx = getCompletionAudioContext();
  if (!ctx || ctx.state !== 'suspended') return;
  ctx.resume().catch(() => {});
}

function scheduleCompletionTone(ctx, frequency, start, duration, peakGain = 0.045) {
  const oscillator = ctx.createOscillator();
  const gain = ctx.createGain();
  oscillator.type = 'sine';
  oscillator.frequency.setValueAtTime(frequency, start);
  gain.gain.setValueAtTime(0.0001, start);
  gain.gain.exponentialRampToValueAtTime(peakGain, start + 0.018);
  gain.gain.exponentialRampToValueAtTime(0.0001, start + duration);
  oscillator.connect(gain);
  gain.connect(ctx.destination);
  oscillator.start(start);
  oscillator.stop(start + duration + 0.02);
  oscillator.onended = () => {
    oscillator.disconnect();
    gain.disconnect();
  };
}

function playCompletionSound(kind = 'done') {
  const nowMs = Date.now();
  if (nowMs - lastCompletionSoundAt < 700) return;
  lastCompletionSoundAt = nowMs;

  const ctx = getCompletionAudioContext();
  if (!ctx) return;
  const play = () => {
    const start = ctx.currentTime + 0.02;
    if (kind === 'done') {
      scheduleCompletionTone(ctx, 523.25, start, 0.16, 0.038);
      scheduleCompletionTone(ctx, 659.25, start + 0.105, 0.18, 0.034);
    } else {
      scheduleCompletionTone(ctx, 392.0, start, 0.14, 0.032);
      scheduleCompletionTone(ctx, 329.63, start + 0.095, 0.16, 0.028);
    }
  };

  if (ctx.state === 'suspended') {
    ctx.resume().then(play).catch(() => {});
    return;
  }
  play();
}

function closeCompletionAudio() {
  if (!completionAudioContext) return;
  const ctx = completionAudioContext;
  completionAudioContext = null;
  if (ctx.state !== 'closed') {
    ctx.close().catch(() => {});
  }
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
    if (!session || session.id !== activeSessionId.value) return;
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
      const title = makeToolTitle(data.name, data.args, data);
    updateToolEvent(toolEventId(data), data.name, title, data.args || '', 'running', data, session);
  };
  // tool:start must create the card immediately so the user sees the tool
  // call appear. tool:update carries the growing accumulated arguments and is
  // throttled via bufferToolUpdate to avoid blocking the main thread when
  // streaming large payloads (e.g. create_file with thousands of lines).
  onRuntimeEvent('tool:start', applyToolProgressEvent);
  onRuntimeEvent('tool:update', bufferToolUpdate);
  onRuntimeEvent('ask:ready', (data) => {
    const session = sessionByEvent(data);
    if (!session) return;
    let existing = findToolEventMessage(session, { ...data, name: 'ask' });
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
      existing.status = 'error';
      existing.body = t('app.ask.cancelled');
    }
    scheduleSaveSessions();
  });
  onRuntimeEvent('tool:result', (data) => {
    flushStreamBuffer(data.runId);
    flushToolUpdateBuffer();
    const session = sessionByEvent(data);
    if (!session) return;
    // Tool results grow the live context (tool result messages get appended to
    // the next model request). Refresh the footer counter so it tracks the
    // agent loop while it works, not only after the run ends.
    if (session.id === activeSessionId.value) refreshContextTokens(session.id);
    const eventId = toolEventId(data);
    let existing = findToolEventMessage(session, data);
    if (!existing) existing = appendToolEventFallback(session, data, 'running');
    if (existing) {
      existing.eventId = eventId;
      existing.runId = data.runId || existing.runId || '';
      existing.toolBatchId = data.toolBatchId || existing.toolBatchId || '';
      existing.toolCallId = data.toolCallId || existing.toolCallId || '';
      if (data.toolCallIndex !== undefined && data.toolCallIndex !== null) existing.toolCallIndex = data.toolCallIndex;
      const resultData = parseToolResultData(data.result);
      // The backend silently filters directory and stale/missing paths from a
      // batch read. If every requested path was filtered, remove the transient
      // running card as well so the UI shows neither an error nor an empty read.
      if ((data.name === 'read' || data.name === 'batch_read') && Array.isArray(resultData.files) && resultData.files.length === 0) {
        const messageIndex = session.messages.indexOf(existing);
        if (messageIndex >= 0) session.messages.splice(messageIndex, 1);
        scheduleSaveSessions();
        return;
      }
      if ((data.name === 'subagent' || data.name === 'agent_delegate') && existing.kind === 'subagent') {
        existing.subagentId = resultData.agentId || existing.subagentId || '';
        existing.status = resultData.status || 'completed';
        existing.description = resultData.description || existing.description || '';
        existing.summary = resultData.summary || existing.summary || '';
        existing.filesRead = resultData.filesRead || existing.filesRead || [];
        existing.filesEdited = resultData.filesEdited || existing.filesEdited || [];
        existing.steps = resultData.steps || existing.steps || 0;
        existing.error = resultData.error || '';
        existing.durationMs = Number(data.durationMs || existing.durationMs || 0);
        existing.durationText = formatDurationShort(existing.durationMs);
        existing.time = new Date().toLocaleTimeString();
        scheduleSaveSessions();
        return;
      }
      existing.status = 'success';
      // ESC 终止的命令不应显示绿色 √
      if (data.name === 'run_command' || data.name === 'remote_run_command') {
        try {
          const parsed = JSON.parse(data.result);
          if (parsed?.data?.cancelled) existing.status = 'error';
        } catch (_) { /* ignore */ }
      }
      existing.body = formatToolBody(data.name, data.result);
      existing.chip = formatToolChip(data.name, data.result);
      existing.durationMs = Number(data.durationMs || 0);
      existing.durationText = formatDurationShort(existing.durationMs);
      if (data.mcpServer) existing.mcpServer = data.mcpServer;
      if (data.mcpTool) existing.mcpTool = data.mcpTool;
      existing.time = new Date().toLocaleTimeString();
      if (data.name === 'ask') {
        existing.askReady = false;
        existing.askSubmitting = false;
        existing.askSubmitted = true;
        existing.askAnswers = Array.isArray(resultData.answers) ? resultData.answers : existing.askAnswers || [];
      }
      if (data.name === 'todo_write' && Array.isArray(resultData.todos)) {
        existing.title = formatTodoNextStep(resultData.todos);
      } else if (!existing.title) {
        existing.title = makeToolResultTitle(data.name, data.result, data);
      }
      if ((data.name === 'create_file' || data.name === 'remote_create_file') && resultData.path) {
        existing.editFilePath = resultData.path;
        if (!existing.title) existing.title = resultData.target ? `${resultData.target} · ${resultData.path}` : resultData.path;
      }
      // Store line count for read tools (used in aggregation)
      if (data.name === 'read_file' || data.name === 'remote_read_file') {
        try {
          const rp = JSON.parse(data.result);
          if (rp.data) {
            const d = rp.data;
            const s = d.startLine || 1;
            const e = d.endLine || d.totalLines || 0;
            existing.readLineCount = e >= s ? e - s + 1 : 0;
            existing.readTotalLines = d.totalLines || e;
          }
        } catch (_) { /* ignore */ }
      }
      // Store file entries for read (and legacy batch_read) (used in tree display)
      if (data.name === 'read' || data.name === 'batch_read') {
        try {
          const rp = JSON.parse(data.result);
          if (rp.data && rp.data.files) {
            const entries = [];
            for (const f of rp.data.files) {
              entries.push({
                title: f.path || '',
                startLine: f.startLine || 1,
                endLine: f.endLine || f.totalLines || 0,
                totalLines: f.totalLines || 0,
                truncated: !!f.truncated,
                lineCount: (f.endLine && f.startLine) ? (f.endLine - f.startLine + 1) : (f.totalLines || 0),
                chip: f.error ? `failed: ${f.error}` : formatReadRangeChip(f.startLine || 1, f.endLine || f.totalLines || 0, f.totalLines || 0, !!f.truncated),
                status: f.error ? 'error' : 'success',
              });
            }
            existing.batchEntries = entries;
          }
        } catch (e) { /* ignore */ }
      }
    }
    if (['edit', 'replace_exact', 'replace_lines', 'remote_edit'].includes(data.name)) {
      try {
        const resultParsed = JSON.parse(data.result);
        const resultData = resultParsed.data || resultParsed;
        const msg = session.messages.find(m => m.role === 'tool_call' && m.eventId === eventId);
        if (msg) {
		  const editedFiles = Array.isArray(resultData.files) ? resultData.files : [];
		  if (editedFiles.length) {
			msg.editEntries = editedFiles.map((file, index) => ({
			  ...(msg.editEntries?.[index] || {}), path: file.path || msg.editEntries?.[index]?.path || '',
			  changes: file.diff ? [] : (msg.editEntries?.[index]?.changes || []), diff: file.diff || '',
			  added: file.addedLines || 0, removed: file.removedLines || 0,
			}));
		  }
		  const combinedDiff = editedFiles.map((file) => file?.diff || '').filter(Boolean).join('\n');
		  if (editedFiles.length) {
			msg.editDiff = '';
			msg.editOldString = '';
			msg.editNewString = '';
			msg.body = '';
		  } else if (resultData.diff || combinedDiff) {
			msg.editDiff = resultData.diff || combinedDiff;
		  }
          if (resultData.addedLines !== undefined || resultData.removedLines !== undefined) {
            msg.editAdded = resultData.addedLines || 0;
            msg.editRemoved = resultData.removedLines || 0;
            const parts = [];
            if (msg.editAdded > 0) parts.push('+' + msg.editAdded);
            if (msg.editRemoved > 0) parts.push('-' + msg.editRemoved);
            msg.editStats = parts.join(' ');
          }
		  if (resultData.path) msg.editFilePath = resultData.path;
		  else if (editedFiles.length === 1) msg.editFilePath = editedFiles[0]?.path || '';
		  else if (editedFiles.length > 1) msg.editFilePath = `${editedFiles.length} files`;
          if (resultData.warnings) msg.editWarnings = resultData.warnings;
          if (resultData.changedLinesBlock) msg.editChangedLinesBlock = resultData.changedLinesBlock;
        }
      } catch (_) { /* ignore */ }
    }
    // 详情已写入卡片（status/body/diff 等）。flushToolUpdateBuffer 的
    // alignToLastToolCard 只保证卡片头部可见，详情可能仍在折叠线下，
    // 这里滚动到容器真实底部让最新结果完整露出。
    if (session.id === activeSessionId.value) scrollMessagesToBottom();
  });
  onRuntimeEvent('tool:error', (data) => {
    flushStreamBuffer(data.runId);
    flushToolUpdateBuffer();
    const session = sessionByEvent(data);
    if (!session) return;
    // Failed tool calls also become part of the context; keep the footer fresh.
    if (session.id === activeSessionId.value) refreshContextTokens(session.id);
    const eventId = toolEventId(data);
    let existing = findToolEventMessage(session, data);
    if (!existing) existing = appendToolEventFallback(session, data, 'error');
    if (existing) {
      existing.eventId = eventId;
      existing.runId = data.runId || existing.runId || '';
      existing.toolBatchId = data.toolBatchId || existing.toolBatchId || '';
      existing.toolCallId = data.toolCallId || existing.toolCallId || '';
      if (data.toolCallIndex !== undefined && data.toolCallIndex !== null) existing.toolCallIndex = data.toolCallIndex;
      if ((data.name === 'subagent' || data.name === 'agent_delegate') && existing.kind === 'subagent') {
        existing.status = 'failed';
        existing.error = data.error || '';
        existing.body = '';
        existing.errorCode = data.errorCode || '';
        existing.durationMs = Number(data.durationMs || existing.durationMs || 0);
        existing.durationText = formatDurationShort(existing.durationMs);
        existing.time = new Date().toLocaleTimeString();
        scheduleSaveSessions();
        return;
      }
      existing.status = 'error';
      existing.body = data.error || '';
      existing.errorCode = data.errorCode || '';
      if (data.name === 'ask') {
        existing.askReady = false;
        existing.askSubmitting = false;
        if (existing.errorCode === 'E_ASK_CANCELLED') existing.body = t('app.ask.cancelled');
      }
      existing.durationMs = Number(data.durationMs || 0);
      existing.durationText = formatDurationShort(existing.durationMs);
      if (data.mcpServer) existing.mcpServer = data.mcpServer;
      if (data.mcpTool) existing.mcpTool = data.mcpTool;
      existing.time = new Date().toLocaleTimeString();
    }
    // 错误详情已写入卡片，滚动到可见区域。
    if (session.id === activeSessionId.value) scrollMessagesToBottom();
  });
  onRuntimeEvent('run:done', (data) => {
    flushStreamBuffer(data.runId);
    flushToolUpdateBuffer();
    closeCompactLoading();
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
    setAssistantRoundDuration(session, data.runId, data.durationMs);
    setAssistantCacheRate(session, data.runId, data.cacheHit, data.cacheMiss, data.inputTokens, data.outputTokens);
    finishPersistableTurn(session, data.runId);
    session.runId = '';
    session.isRunning = false;
    if (session.id === activeSessionId.value) {
      playCompletionSound('done');
    }
    persistCompletedSession(session);
    if (session.id === activeSessionId.value) refreshContextTokens(session.id);
  });
  onRuntimeEvent('run:error', (data) => {
    flushStreamBuffer(data.runId);
    flushToolUpdateBuffer();
    closeCompactLoading();
    if (retryBanner.value) {
      const session = sessionByTerminalEvent(data);
      if (session && session.id === activeSessionId.value) retryBanner.value = null;
    }
    const session = sessionByTerminalEvent(data);
    if (!session) return;
    let i = session.messages.length - 1;
    while (i >= 0) {
      const msg = session.messages[i];
      if (msg.role === 'assistant' && msg.streaming && msg.runId === data.runId) {
        msg.streaming = false;
        msg.done = true;
        finalizeReasoningTiming(msg);
        break;
      }
      i--;
    }
    markTransientTurn(session, data.runId);
    session.runId = '';
    session.isRunning = false;
    if (session.id === activeSessionId.value) {
      const err = data.error || 'unknown error';
      const cancelled = err === '已取消' || err === 'Cancelled' || String(err).toLowerCase().includes('context canceled');
      session.messages.push({ role: 'assistant', content: cancelled ? t('app.run.cancelled') : t('app.run.failed', { error: err }), error: !cancelled, system: cancelled, runId: data.runId, transientTurn: true });
      playCompletionSound(cancelled ? 'cancelled' : 'error');
    }
    setAssistantRoundDuration(session, data.runId, data.durationMs);
    setAssistantCacheRate(session, data.runId, data.cacheHit, data.cacheMiss, data.inputTokens, data.outputTokens);
    saveSessions();
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
    if (retryBanner.value) {
      const session = sessionByTerminalEvent(data);
      if (session && session.id === activeSessionId.value) retryBanner.value = null;
    }
    const session = sessionByTerminalEvent(data);
    if (!session) return;
    let i = session.messages.length - 1;
    while (i >= 0) {
      const msg = session.messages[i];
      if (msg.role === 'assistant' && msg.streaming && msg.runId === data.runId) {
        msg.streaming = false;
        msg.done = true;
        finalizeReasoningTiming(msg);
        break;
      }
      i--;
    }
    setAssistantRoundDuration(session, data.runId, data.durationMs);
    setAssistantCacheRate(session, data.runId, data.cacheHit, data.cacheMiss, data.inputTokens, data.outputTokens);
    markTransientTurn(session, data.runId);
    session.runId = '';
    session.isRunning = false;
    if (session.id === activeSessionId.value) {
      playCompletionSound('cancelled');
    }
    saveSessions();
    // Refresh token count after cancellation: streaming deltas and any tool
    // results added before cancellation are now part of the history and the
    // context popover should reflect the actual remaining budget.
    if (session.id === activeSessionId.value) refreshContextTokens(session.id);
    // 后台 Tab 的会话被取消前可能已修改工作区文件，Git 统计同样要刷新。
    if (String(session.workspace || '') === String(config.workspace || '')) refreshGitStatus();
  });
  // Auto-compaction: backend emits run:compact before the blocking summary
  // request and run:compacted after it. Show a loading spinner during the
  // compaction, then surface the token delta so the sudden drop in the
  // footer token counter is no longer mysterious.
  onRuntimeEvent('run:compact', (data) => {
    if (data?.sessionId && data.sessionId === activeSessionId.value && !compactLoadingActive.value) {
      compactLoadingActive.value = true;
    }
  });
  onRuntimeEvent('run:compacted', (data) => {
    closeCompactLoading();
    const sid = data?.sessionId || '';
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
  onRuntimeEvent('todo:update', (data) => {
    const sid = data.sessionId || '';
    if (!sid) return;
    const revision = Number(data.revision || 0);
    const currentRevision = Number(todoRevisionsBySession[sid] || 0);
    if (revision && currentRevision && revision < currentRevision) return;
    const nextTodos = Array.isArray(data.todos) ? data.todos : [];
    todosBySession[sid] = nextTodos;
    if (revision) todoRevisionsBySession[sid] = revision;
    if (sid === activeSessionId.value) {
      todos.value = nextTodos;
    }
  });
  onRuntimeEvent('goal:update', (data) => {
    const sid = data.sessionId || '';
    if (!sid) return;
    const goal = data.goal;
    if (goal?.status === 'active') goalsBySession[sid] = goal;
    else delete goalsBySession[sid];
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
    const isActiveSession = session && session.id === activeSessionId.value;
    // Sidebar tracking
    if (isActiveSession && !subRuns.value.some((item) => item.id === data.id)) {
      subRuns.value.push({
        id: data.id,
        description: data.description || '',
        profile: data.profile || 'coder',
        status: 'running',
        steps: 0,
        summary: '',
        filesRead: [],
        filesEdited: [],
        error: '',
        toolCalls: [],
        startTime: Number(data.startTime || Date.now()),
        durationMs: 0,
        durationText: '',
        inputTokens: 0,
        outputTokens: 0,
        totalTokens: 0,
      });
    }
    // Upgrade the original subagent card in place. Keeping the parent
    // tool identity lets the eventual tool:result/tool:error update this same
    // card instead of appending a second raw JSON result card.
    if (session) {
      const existing = findToolEventMessage(session, { ...data, name: 'subagent' }) ||
        findToolEventMessage(session, { ...data, name: 'agent_delegate' });
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
    const r = subRuns.value.find(s => s.id === data.id);
    if (r) {
      r.steps = data.step || 0;
      r.inputTokens = data.inputTokens || 0;
      r.outputTokens = data.outputTokens || 0;
      r.totalTokens = data.totalTokens || 0;
    }
    const msg = findSubagentMsg(data.id, data.sessionId || '');
    if (msg) {
      msg.steps = data.step || 0;
      msg.inputTokens = data.inputTokens || 0;
      msg.outputTokens = data.outputTokens || 0;
      msg.totalTokens = data.totalTokens || 0;
    }
  });
  onRuntimeEvent('sub:tool:start', (data) => {
    const r = subRuns.value.find(s => s.id === data.id);
    if (r) {
      r.toolCalls.push({ toolCallId: data.toolCallId, name: data.name, args: data.args, status: 'running', summary: '', durationMs: 0, durationText: '' });
    }
    const msg = findSubagentMsg(data.id, data.sessionId || '');
    if (msg) {
      msg.toolCalls.push({ toolCallId: data.toolCallId, name: data.name, args: data.args, status: 'running', summary: '', durationMs: 0, durationText: '' });
    }
  });
  onRuntimeEvent('sub:tool:result', (data) => {
    const r = subRuns.value.find(s => s.id === data.id);
    if (r) {
      const tc = r.toolCalls.find(t => t.toolCallId === data.toolCallId);
      if (tc) { tc.status = 'success'; tc.summary = data.summary || ''; tc.durationMs = Number(data.durationMs || 0); tc.durationText = formatDurationShort(tc.durationMs); }
    }
    const msg = findSubagentMsg(data.id, data.sessionId || '');
    if (msg) {
      const tc = msg.toolCalls.find(t => t.toolCallId === data.toolCallId);
      if (tc) { tc.status = 'success'; tc.summary = data.summary || ''; tc.durationMs = Number(data.durationMs || 0); tc.durationText = formatDurationShort(tc.durationMs); }
    }
  });
  onRuntimeEvent('sub:tool:error', (data) => {
    const r = subRuns.value.find(s => s.id === data.id);
    if (r) {
      const tc = r.toolCalls.find(t => t.toolCallId === data.toolCallId);
      if (tc) { tc.status = 'error'; tc.summary = data.error || ''; tc.durationMs = Number(data.durationMs || 0); tc.durationText = formatDurationShort(tc.durationMs); }
    }
    const msg = findSubagentMsg(data.id, data.sessionId || '');
    if (msg) {
      const tc = msg.toolCalls.find(t => t.toolCallId === data.toolCallId);
      if (tc) { tc.status = 'error'; tc.summary = data.error || ''; tc.durationMs = Number(data.durationMs || 0); tc.durationText = formatDurationShort(tc.durationMs); }
    }
  });
  onRuntimeEvent('sub:done', (data) => {
    const r = subRuns.value.find(s => s.id === data.id);
    if (r) {
      r.status = data.status || 'completed';
      r.summary = data.summary || '';
      r.filesRead = data.filesRead || [];
      r.filesEdited = data.filesEdited || [];
      r.steps = data.steps || r.steps;
      r.durationMs = Number(data.durationMs || 0);
      r.durationText = formatDurationShort(r.durationMs);
      r.inputTokens = data.inputTokens || 0;
      r.outputTokens = data.outputTokens || 0;
      r.totalTokens = data.totalTokens || 0;
    }
    const msg = findSubagentMsg(data.id, data.sessionId || '');
    if (msg) {
      msg.status = data.status || 'completed';
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
    const r = subRuns.value.find(s => s.id === data.id);
    if (r) {
      r.status = 'failed';
      r.error = data.error || '';
      r.durationMs = Number(data.durationMs || 0);
      r.durationText = formatDurationShort(r.durationMs);
    }
    const msg = findSubagentMsg(data.id, data.sessionId || '');
    if (msg) {
      msg.status = 'failed';
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

function focusedToolIdForSession(sessionId) {
  return focusedToolIdsBySession[sessionId] || '';
}

function focusTool(sessionId, eventId) {
  if (sessionId) focusedToolIdsBySession[sessionId] = eventId;
}

function clearFocus(sessionId) {
  if (sessionId) focusedToolIdsBySession[sessionId] = '';
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
  for (const file of files) {
    if (pendingAttachments.value.length >= MAX_ATTACHMENTS_PER_MESSAGE) {
      message.warning(t('app.attachment.limit', { count: MAX_ATTACHMENTS_PER_MESSAGE }));
      break;
    }
    const att = await fileToAttachment(file);
    pendingAttachments.value.push(att);
  }
}

function extractClipboardFiles(event) {
  const data = event?.clipboardData;
  if (!data) return [];
  const files = [];
  const seen = new Set();
  const pushFile = (file) => {
    if (!file) return;
    const key = `${file.name || ''}:${file.type || ''}:${file.size || 0}:${file.lastModified || 0}`;
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
  };

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
  const removed = pendingAttachments.value.find(att => att.id === id);
  releaseAttachmentPreview(removed);
  pendingAttachments.value = pendingAttachments.value.filter(att => att.id !== id);
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

async function switchToModel(index) {
  try {
    await SwitchModel(index);
    const loaded = await GetConfig();
    // Keep both frontend config copies aligned. Settings and workspace changes
    // save configDraft later; leaving it on the previous model would silently
    // switch the backend back before the next message is sent.
    assignConfig(config, loaded);
    assignConfig(configDraft, loaded);
    // Remember this model for the active Tab so another Tab pointing at the
    // same workspace keeps its own selection.
    const tab = workspaceTabs.value.find((item) => item.id === activeWorkspaceId.value);
    rememberModelForTab(tab);
    message.success(t('app.model.switched', { model: loaded.model }));
  } catch (err) {
    message.error(t('app.model.switchFailed', { error: err }));
  }
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

function formatReadRangeChip(startLine, endLine, totalLines, truncated) {
  const start = Number(startLine) || 1;
  const end = Number(endLine) || Number(totalLines) || 0;
  const total = Number(totalLines) || 0;
  const actualLines = total > 0 ? end - start + 1 : 0;
  if (actualLines <= 0) return '';
  const parts = [];
  if (start > 1 || end < total) {
    parts.push(`lines ${start}-${end}`);
    if (total > 0) parts.push(`(of ${total})`);
    if (truncated) parts.push('(truncated)');
  } else {
    parts.push(`${actualLines} line${actualLines !== 1 ? 's' : ''}`);
  }
  return parts.length ? `· ${parts.join(' · ')}` : '';
}

async function sendPrompt() {
  const session = activeSession.value;
  if (!session) return;
  const text = promptText.value.trim();
  const attachments = pendingAttachments.value.map(att => ({ ...att }));
  if ((!text && attachments.length === 0) || session.runId) return;
  primeCompletionAudio();

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
      const builtinPrefixes = ['new','plan','goal','skills','clear','switch','sessions','reload','init','note','remember','compact','push'];
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

  if (!config.workspace) {
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
  const hasApiKey = !!(config.apiKey || (Array.isArray(config.apiKeys) && config.apiKeys.length));
  if (!hasApiKey) {
    settingsPage.value = 'models';
    configVisible.value = true;
    message.warning(t('app.config.apiKeyRequired'));
    return;
  }
  if (!session) return;
  if (config.workspace) session.workspace = config.workspace;
  const userMessage = { role: 'user', content: displayText, attachments, done: true };
  session.messages.push(userMessage);
  session.updatedAt = Date.now();
  session.messageCount = session.messages.filter((message) => message?.role === 'user' || message?.role === 'assistant').length;
  if (isDefaultSessionTitle(session.title)) {
    session.title = displayText.length > 20 ? `${displayText.slice(0, 20)}…` : displayText;
  }
  // Save to workspace-scoped prompt history
  addPromptHistory(displayText);
  commandHistoryIndex.value = -1;
  promptText.value = '';
  pendingAttachments.value = [];
  commandMenuVisible.value = false;
  scrollMessagesToBottom({ force: true });

  try {
    const history = buildSessionMessagesForModel(session, {
      message: userMessage,
      content: sendText,
      attachments: attachmentsForModel(attachments),
    });
    markSessionRunning(session);
    await StartChat({ sessionId: session.id, message: sendText, messages: history, config: { ...config, extraRoots: session.extraRoots || [] } });
  } catch (err) {
    markTransientTurn(session);
    session.runId = '';
    session.isRunning = false;
    pushMessage('assistant', t('app.run.startFailed', { error: err }), { error: true, transientTurn: true });
  }
}

async function stopRun() {
  const session = activeSession.value;
  if (!session || !session.runId) return;
  try {
    await CancelRun(session.runId);
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
  const previousWorkspace = String(config.workspace || '');
  assignConfig(config, draftData);
  assignConfig(configDraft, draftData);
  const workspaceChanged = previousWorkspace !== String(config.workspace || '');
  if (workspaceChanged) clearFooterStats();
  try {
    await saveWorkspaceConfig({ ...configDraft });
    syncConfigToActiveTab();
    // Settings can change the active model without going through the composer
    // dropdown, so keep the current Tab's selection in sync as well.
    rememberActiveTabModel();
    if (workspaceChanged) {
      await refreshFooterStats({
        tabId: activeWorkspaceId.value,
        sessionId: activeSessionId.value,
        workspace: config.workspace || '',
      });
    } else {
      refreshContextTokens(activeSessionId.value);
    }
    if (!silent) message.success(t('app.config.saved'));
  } catch (err) {
    if (workspaceChanged) footerStatsLoading.value = false;
    message.error(t('app.config.saveFailed', { error: err }));
  }
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
  return {
    ...base,
    backgroundImage: `linear-gradient(rgba(26,26,26,${overlay}), rgba(26,26,26,${overlay})), url("${url}")`,
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

function getPromptTextarea() {
  const root = promptInputRefs[activeWorkspaceId]?.$el || promptInputRefs[activeWorkspaceId];
  return root?.querySelector?.('textarea[data-ally-prompt-input="true"], textarea') || null;
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
    const result = await SearchWorkspacePaths({ query, limit: 30, force });
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
  const justCommittedComposition = event.key === 'Enter' && performance.now() - promptCompositionEndedAt < 100;
  if (promptComposing.value || event.isComposing || event.keyCode === 229 || justCommittedComposition) return;

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
    const total = sessions.value.length;
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
  if (command.special === 'goal') {
    promptText.value = t('app.goal.prompt');
    commandMenuVisible.value = false;
    nextTick(() => focusPromptInput());
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

function toolEventId(data = {}) {
  if (data.runId && data.toolBatchId && data.toolCallIndex !== undefined && data.toolCallIndex !== null) {
    return `${data.runId}:tool:${data.toolBatchId}:${data.toolCallIndex}`;
  }
  if (data.runId && data.toolCallIndex !== undefined && data.toolCallIndex !== null) {
    return `${data.runId}:tool:${data.toolCallIndex}`;
  }
  return data.toolCallId || `${data.name || 'tool'}-${Date.now()}`;
}

function findToolEventMessage(session, data = {}) {
  if (!session) return null;
  const eventId = toolEventId(data);
  const toolCallId = data.toolCallId || '';
  const toolBatchId = data.toolBatchId || '';
  const hasIndex = data.runId && data.toolCallIndex !== undefined && data.toolCallIndex !== null;
  return session.messages.find((item) => {
    if (item.role !== 'tool_call') return false;
    if (item.eventId === eventId) return true;
    if (toolCallId && (item.eventId === toolCallId || item.toolCallId === toolCallId)) return true;
    if (!hasIndex || item.runId !== data.runId || Number(item.toolCallIndex) !== Number(data.toolCallIndex)) return false;
    if (toolBatchId || item.toolBatchId) return item.toolBatchId === toolBatchId;
    return true;
  }) || null;
}

function appendToolEventFallback(session, data = {}, status = 'running') {
  if (!session) return null;
  const eventId = toolEventId(data);
  const title = makeToolResultTitle(data.name, data.result, data) || makeToolTitle(data.name, data.args || '', data);
  const scheduledAction = data.name === 'scheduled_task'
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
  return todos.length > 0 ? t('tools.todo.status.done') : t('tools.todo.cleared');
}

function makeToolResultTitle(name, result, meta = {}) {
  const d = parseToolResultData(result);
  if (name === 'todo_write' && Array.isArray(d.todos)) return formatTodoNextStep(d.todos);
	if ((name === 'edit' || name === 'remote_edit') && Array.isArray(d.files)) return d.files.length === 1 ? (d.files[0]?.path || '') : `${d.files.length} files`;
  const path = d.path || d.deleted || '';
  if (path && (name === 'create_file' || name === 'edit' || name === 'remote_edit' || name === 'remote_create_file' || name === 'delete_path' || name === 'remote_delete_path')) {
    return d.target ? `${d.target} · ${path}` : path;
  }
  if (path && (name === 'memory_read' || name === 'memory_write')) {
    return path;
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
  } else if (name === 'create_file' || name === 'remote_create_file') {
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
    body: (name === 'run_command' || name === 'remote_run_command') && meta.output !== undefined
      ? stripAnsi(String(meta.output || ''))
      : formatToolBody(name, displayToolBodyForStatus(name, body, status)),
    time: new Date().toLocaleTimeString(),
    durationMs: existing?.durationMs || Number(meta.durationMs || 0),
    durationText: existing?.durationText || formatDurationShort(meta.durationMs),
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
    // scheduled_task's card verb depends on the action; capture it (args may be
    // absent on an early tool:start, so fall back to the existing value).
    scheduledAction: name === 'scheduled_task' ? (parsed.action || existing?.scheduledAction || '') : (existing?.scheduledAction || ''),
    ...((name === 'subagent' || name === 'agent_delegate') ? {
      subagentId: existing?.subagentId || '',
      description: parsed.description || existing?.description || parsed.task || '',
      profile: existing?.profile || 'coder',
      steps: existing?.steps || 0,
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
  if (!sid) { todos.value = []; return; }
  if (Array.isArray(todosBySession[sid])) {
    todos.value = todosBySession[sid];
  }
  try {
    const list = await GetTodos(sid);
    const nextTodos = list || [];
    todosBySession[sid] = nextTodos;
    if (sid === activeSessionId.value) todos.value = nextTodos;
  } catch (_) { todos.value = []; }
}

async function loadGoal(sid) {
  if (!sid) return;
  try {
    const result = await GetGoal(sid);
    if (result?.hasGoal && result?.status === 'active') goalsBySession[sid] = result;
    else delete goalsBySession[sid];
  } catch (_) {
    delete goalsBySession[sid];
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
      : messages.filter((message) => message?.role === 'user' || message?.role === 'assistant').length,
    contextTokens: Number(session?.contextTokens || sessionTokensCache[session?.id] || 0),
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
    session.contextTokens = Number(snapshot.contextTokens || 0);
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
    delete focusedToolIdsBySession[session.id];
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
    delete sessionPromptTexts[session.id];
    delete focusedToolIdsBySession[session.id];
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
  if (next.role === 'tool_call' && (next.name === 'read' || next.name === 'batch_read')) {
    next.body = '';
    next.codeContent = '';
  } else {
    next.body = truncateStoredText(next.body, MAX_STORED_TOOL_BODY_CHARS, t('app.cache.toolTrimmed'));
    next.codeContent = truncateStoredText(next.codeContent, MAX_STORED_TOOL_BODY_CHARS, t('app.cache.previewTrimmed'));
  }
  next.editDiff = truncateStoredText(next.editDiff, MAX_STORED_TOOL_BODY_CHARS, t('app.cache.diffTrimmed'));
  next.editOldString = '';
  next.editNewString = '';
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
    contextTokens: Number(entry.contextTokens || 0),
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
  session.contextTokens = Number(entry.contextTokens || 0);
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
      messageCount: Number(meta.messageCount || legacyMessages.filter((item) => item?.role === 'user' || item?.role === 'assistant').length),
      contextTokens: Number(meta.contextTokens || 0),
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
  await refreshSessionTokensList();
}

function showSessionList() {
  saveSessions();
  sessionsVisible.value = true;
  sessionsSelectedIndex.value = 0;
  commandMenuVisible.value = false;
  void refreshSessionListData();
  nextTick(() => focusPromptInput());
}

async function switchToSession(index) {
  const idx = parseInt(index);
  if (isNaN(idx) || idx < 1 || idx > sessions.value.length) {
    message.error('\u65e0\u6548\u7684\u4f1a\u8bdd\u7f16\u53f7');
    return;
  }
  const target = sessions.value[idx - 1];
  if (!target) return;
  saveSessions();
  await activateSelectedSession(target);
  scrollMessagesToBottom();
}



function handleInitCommand() {
  const session = activeSession.value;
  if (!session) return;
  // Send the init exploration prompt to the LLM
  session.messages.push({ role: 'user', content: INIT_PROMPT, done: true });
  if (isDefaultSessionTitle(session.title)) {
    session.title = t('app.init.title');
  }
  scrollMessagesToBottom();
  saveSessions();

  const history = session.messages
    .filter(isModelHistoryMessage)
    .map((msg) => ({ role: msg.role, content: msg.content }));

  const text = INIT_PROMPT;
  markSessionRunning(session);
  StartChat({ sessionId: session.id, message: text, messages: history, config: { ...config, extraRoots: session.extraRoots || [] } })
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
  if (isDefaultSessionTitle(session.title)) {
    session.title = t('app.note.title');
  }
  scrollMessagesToBottom();
  saveSessions();

  markSessionRunning(session);
  StartChat({ sessionId: session.id, message: '', messages: history, config: { ...config, extraRoots: session.extraRoots || [] } })
    .catch((err) => {
      markTransientTurn(session);
      session.runId = '';
      session.isRunning = false;
      pushMessage('assistant', t('app.note.failed', { error: err }), { error: true, transientTurn: true });
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
  if (isDefaultSessionTitle(session.title)) {
    session.title = t('app.push.title');
  }
  scrollMessagesToBottom();
  saveSessions();

  markSessionRunning(session);
  StartChat({ sessionId: session.id, message: '', messages: history, config: { ...config, extraRoots: session.extraRoots || [] } })
    .catch((err) => {
      markTransientTurn(session);
      session.runId = '';
      session.isRunning = false;
      pushMessage('assistant', t('app.push.failed', { error: err }), { error: true, transientTurn: true });
    });
}

// Compaction loading uses the same dotted indicator as a running chat
// (composer-run-status-dots) so manual /compact and auto-compaction feel
// like a normal chat run instead of a Naive toast.
const compactLoadingActive = ref(false);
function closeCompactLoading() {
  compactLoadingActive.value = false;
}

async function handleCompactCommand() {
  const session = activeSession.value;
  if (!session) return;
  if (session.runId) { message.warning(t('app.compact.wait')); return; }

  closeCompactLoading();
  compactLoadingActive.value = true;
  saveSessions();

  try {
    const result = await CompactSession(session.id, '');
    closeCompactLoading();
    const tBefore = result.tokensBefore || 0;
    const tAfter = result.tokensAfter || 0;
    const saved = tBefore - tAfter > 0 ? t('app.compact.saved', { tokens: fmtK(tBefore - tAfter) }) : '';

    // Replace messages with the compacted summary
    session.messages = [
      {
        role: 'assistant',
        content: t('app.compact.done', { saved, before: fmtK(tBefore), after: fmtK(tAfter) }),
        system: true,
      },
    ];

    persistCompletedSession(session);
    // Refresh context
    refreshContextTokens(session.id);
    scrollMessagesToBottom();
    message.success(t('app.compact.success', { before: fmtK(tBefore), after: fmtK(tAfter) }));
  } catch (err) {
    closeCompactLoading();
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
        const status = isSkillActive(s.name) ? t('app.skills.enabled') : t('app.skills.disabled');
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

function normalizeSkillName(name) {
  return String(name || '').trim().toLowerCase();
}

function isSkillActive(name) {
  const target = normalizeSkillName(name);
  return activeSkillNames.value.some((item) => normalizeSkillName(item) === target);
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
    const alreadyActive = isSkillActive(skillName);
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
        if (isDefaultSessionTitle(session.title)) {
          const titleBase = userText || `/${skillName}`;
          session.title = titleBase.length > 20 ? `${titleBase.slice(0, 20)}…` : titleBase;
        }
        scrollMessagesToBottom();
        if (config.apiKey || (Array.isArray(config.apiKeys) && config.apiKeys.length)) {
          markSessionRunning(session);
          await StartChat({ sessionId: session.id, message: '', messages: [{ role: 'user', content: modelContent }], config: { ...config, extraRoots: session.extraRoots || [] } }).catch(() => {
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
  config.reasoningEffort = next;
  configDraft.reasoningEffort = next;
  // Keep the active model preset in sync so the choice survives model
  // switching (SwitchModel applies the preset's value to the top level).
  const activeIdentity = modelConfigIdentity(config);
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

// Goal mode helpers
function trackGoal(objective) {
  pushMessage('user', `## Goal\n\n${objective}\n\n${t('app.goal.confirm')}`, { system: false });
  scrollMessagesToBottom();
}

function normalizeToolStatus(status) {
  if (status === 'success' || status === 'error' || status === 'running') return status;
  if (status === 'info') return 'running';
  return 'default';
}

function toolKind(name) {
  if (isMcpToolName(name)) return 'mcp';
  if (name === 'edit' || name === 'replace_exact' || name === 'replace_lines' || name === 'remote_edit') return 'edit';
  if (name === 'create_file' || name === 'remote_create_file') return 'create';
  if (name === 'delete_path' || name === 'remote_delete_path') return 'delete';
  if (name === 'run_command' || name === 'remote_run_command' || name === 'Bash') return 'command';
  if (name === 'background_process' || name === 'start_service' || name === 'stop_service' || name === 'list_services') return 'service';
  if (name === 'wait') return 'wait';
  if (name === 'ask') return 'ask';
  if (name === 'calculate') return 'calculate';
  if (name === 'list_files' || name === 'remote_list_files') return 'list';
  if (name === 'read' || name === 'read_file' || name === 'remote_read_file' || name === 'batch_read' || name === 'document_read') return 'read';
  if (name === 'Glob') return 'glob';
  if (name === 'grep_files') return 'grep';
  if (name === 'run') return 'run';
  if (name === 'todo_write') return 'todo';
  if (name === 'scheduled_task') return 'scheduled';
  if (name === 'memory_read' || name === 'memory_write') return 'memory';
  if (name === 'create_goal' || name === 'update_goal' || name === 'get_goal') return 'goal';
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

function makeToolTitle(name, args, meta = {}) {
  if (isMcpToolName(name)) {
    // MCP 卡片名称位已显示 server/tool；括号里只放参数摘要，
    // 无参数时返回空串，避免重复显示工具名。
    return formatMcpArgsSummary(parseToolArgsForMeta(args, meta));
  }
  const parsed = parseToolArgsForMeta(args, meta);
  if (name === 'todo_write') {
    return Array.isArray(parsed.todos) ? formatTodoNextStep(parsed.todos) : '';
  }
  if (name === 'run_command' || name === 'remote_run_command' || name === 'Bash') {
    const command = parsed.command || parsed.cmd || '';
    if (name === 'remote_run_command' && parsed.target) return `${parsed.target} · ${command}`;
    return command;
  }
  if (name === 'background_process') {
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
    return 'tracked services';
  }
  if (name === 'edit' || name === 'remote_edit') {
	if ((name === 'edit' || name === 'remote_edit') && Array.isArray(parsed.files)) return parsed.files.length === 1 ? (parsed.files[0]?.path || '') : `${parsed.files.length} files`;
    return parsed.target ? `${parsed.target} · ${parsed.path || ''}` : (parsed.path || '');
  }
  if (name === 'create_file' || name === 'delete_path' || name === 'remote_create_file' || name === 'remote_delete_path') {
    return parsed.target ? `${parsed.target} · ${parsed.path || ''}` : (parsed.path || '');
  }
  if (name === 'read_file' || name === 'remote_read_file') {
    return parsed.target ? `${parsed.target} · ${parsed.path || ''}` : (parsed.path || '');
  }
  if (name === 'grep_files') {
    const pattern = parsed.pattern || '';
    const path = parsed.path || '';
    if (pattern && path) return `${pattern}, ${path}`;
    return pattern || path;
  }
  if (name === 'Glob') {
    return parsed.pattern || '';
  }
  if (name === 'list_files' || name === 'remote_list_files') {
    if (parsed.target) return `${parsed.target}${parsed.path ? ' · ' + parsed.path : ''}`;
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
  if (name === 'memory_read' || name === 'memory_write') {
    return parsed.path || parsed.description || '';
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
    // read_file: · N lines 或 · lines M-N
    if ((name === 'read_file' || name === 'remote_read_file') && parsed.data) {
      const d = parsed.data;
      if (d.kind === 'document' || d.contentFormat === 'plain') {
        const chars = String(d.text || d.content || '').length;
        if (chars > 0) return '\u00B7 ' + chars + ' chars' + (d.truncated ? ' truncated' : '');
      }
      const startLine = d.startLine || 1;
      const endLine = d.endLine || d.totalLines || 0;
      const linesReturned = endLine - startLine + 1;
      if (linesReturned > 0) {
        const isPartial = startLine > 1 || endLine < (d.totalLines || 0);
        if (isPartial && d.totalLines > linesReturned) {
          return '\u00B7 lines ' + startLine + '-' + endLine + ' (of ' + d.totalLines + ')';
        }
        return formatReadChip(linesReturned);
      }
    }
    // read (and legacy batch_read): list each file as separate line
    if ((name === 'read' || name === 'batch_read') && parsed.data) {
      if (!parsed.data.files || !Array.isArray(parsed.data.files)) return '';
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
    // grep_files: · N hits / lines
    if (name === 'grep_files' && parsed.data) {
      const count = parsed.data.count || parsed.data.matches?.length || 0;
      const occurrences = parsed.data.occurrences || 0;
      const ms = parsed.data.durationMs || 0;
      let chip = '';
      if (occurrences > 0) {
        chip = '\u00B7 ' + occurrences + ' hit' + (occurrences > 1 ? 's' : '');
        if (count > 0 && count !== occurrences) chip += ' in ' + count + ' line' + (count > 1 ? 's' : '');
      } else if (count > 0) {
        chip = '\u00B7 ' + count + ' match' + (count > 1 ? 'es' : '');
      }
      if (ms > 0) chip += ' \u00B7 ' + formatDuration(ms);
      return chip;
    }
    // Glob: · N files
    if (name === 'Glob' && parsed.data) {
      const files = String(parsed.data.output || '').split('\n').filter(l => l.length > 0).length;
      if (files > 0) return '\u00B7 ' + files + ' file' + (files > 1 ? 's' : '');
    }
    // list_files: · N items
    if ((name === 'list_files' || name === 'remote_list_files') && parsed.data) {
      const items = Array.isArray(parsed.data.entries) ? parsed.data.entries.length : (parsed.data.count || 0);
      if (items > 0) return '\u00B7 ' + items + ' item' + (items > 1 ? 's' : '');
    }
    if (name === 'document_read' && parsed.data) {
      const chars = String(parsed.data.text || '').length;
      if (chars > 0) return '\u00B7 ' + chars + ' chars' + (parsed.data.truncated ? ' truncated' : '');
    }
    if (name === 'memory_read' && parsed.data) {
      const chars = String(parsed.data.content || '').length;
      if (chars > 0) return '\u00B7 ' + formatCharCount(chars);
    }
    if (name === 'memory_write' && parsed.data) {
      return parsed.data.created ? '\u00B7 created' : '\u00B7 updated';
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
    if ((name === 'background_process' || name === 'start_service' || name === 'stop_service') && parsed.data) {
      // background_process now returns distinct result shapes per action.
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
    // create_file: · N lines
    if ((name === 'create_file' || name === 'remote_create_file') && parsed.data) {
      const lines = Number(parsed.data.addedLines ?? 0);
      const bytes = parsed.data.afterBytes || 0;
      const parts = [lines + ' line' + (lines !== 1 ? 's' : '')];
      if (bytes > 0) parts.push(formatBytes(bytes));
      return '\u00B7 ' + parts.join(' \u00B7 ');
    }
    if ((name === 'delete_path' || name === 'remote_delete_path') && parsed.data) {
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
  if (isMcpToolName(name) || name === 'todo_write') return '';
  try {
    const parsed = JSON.parse(text);
    if (name === 'wait' && parsed.data) return '';
    if (name === 'ask' && parsed.data) return '';
    // command result: show output + exit code (command shown in card title)
    if ((name === 'run_command' || name === 'remote_run_command' || name === 'Bash') && parsed.data) {
      const d = parsed.data;
      let out = '';
      if (d.output) out += stripAnsi(d.output);
      if (d.output && !d.output.endsWith('\n')) out += '\n';
      if (d.exitCode === 0) {
        out += 'exit code: 0 [' + formatDuration(d.durationMs) + ']';
      } else {
        out += 'exit code: ' + d.exitCode + ' [' + formatDuration(d.durationMs) + ']';
      }
      if (d.timedOut) out += '  TIMED OUT';
      if (d.cancelled) out += '  CANCELLED';
      if (d.truncated) out += '  [truncated]';
      return out;
    }
    // read_file result: show content with line numbers
    if ((name === 'read_file' || name === 'remote_read_file') && parsed.data) {
      const d = parsed.data;
      if (d.kind === 'document' || d.contentFormat === 'plain') {
        const sheetInfo = d.sheets && d.sheets.length ? '\nsheets: ' + d.sheets.join(', ') : '';
        return `${d.path || ''} (${d.type || 'document'})${sheetInfo}\n\n${d.text || d.content || ''}${d.truncated ? '\n\n[truncated]' : ''}`;
      }
      if (d.content) return d.content;
      if (d.output) return d.output;
      return '';
    }
    // read (and legacy batch_read) result: show each file's content
    if ((name === 'read' || name === 'batch_read') && parsed.data) {
      const d = parsed.data;
      if (d.files && Array.isArray(d.files)) {
        return d.files.map(f => {
          if (f.kind === 'document' || f.contentFormat === 'plain') {
            const sheetInfo = f.sheets && f.sheets.length ? ' sheets: ' + f.sheets.join(', ') : '';
            const header = '### ' + (f.path || '') + '  (' + (f.type || 'document') + sheetInfo + (f.truncated ? ' [truncated]' : '') + ')';
            return header + '\n' + (f.text || f.content || '');
          }
          const range = f.endLine ? (' lines ' + (f.startLine || 1) + '-' + f.endLine + ' of ' + (f.totalLines || '?')) : ((f.totalLines || '?') + ' lines');
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
    if (name === 'memory_read' && parsed.data) {
      const d = parsed.data;
      const header = `${d.path || ''}${d.description ? '\n' + d.description : ''}`;
      return `${header}\n\n${d.content || ''}`;
    }
    if (name === 'memory_write' && parsed.data) {
      const d = parsed.data;
      return `${d.created ? 'Created' : 'Updated'} ${d.path || ''}\n${d.description || ''}`;
    }
    if (name === 'calculate' && parsed.data) {
      return `${parsed.data.expression} = ${parsed.data.text || parsed.data.value}`;
    }
    if (name === 'scheduled_task' && parsed.data) {
      if (parsed.data.task) return formatScheduledTaskToolDetail(parsed.data.task);
      if (parsed.data.deleted) return `Deleted scheduled task ${parsed.data.deleted}`;
      const tasks = Array.isArray(parsed.data.tasks) ? parsed.data.tasks : [];
      if (!tasks.length) return 'No scheduled tasks.';
      return tasks.map(formatScheduledTaskToolDetail).join('\n\n---\n\n');
    }
    if ((name === 'background_process' || name === 'start_service' || name === 'stop_service') && parsed.data) {
      const d = parsed.data;
      // Discriminate by structural fields so list/read results render as
      // human-readable summaries instead of JSON dumps.
      if (Array.isArray(d.services)) return formatServiceListResult(d);
      if (typeof d.returnedBytes === 'number' && typeof d.bufferBytes === 'number') return formatServiceReadResult(d);
      return formatServiceInfo(d);
    }
    if (name === 'list_services' && parsed.data) {
      const services = Array.isArray(parsed.data.services) ? parsed.data.services : [];
      if (services.length === 0) return 'No tracked services.';
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
    if ((name === 'create_file' || name === 'remote_create_file') && parsed.data) {
      const d = parsed.data;
      const lines = Number(d.addedLines ?? 0);
      const parts = [];
      if (d.path) parts.push(d.path);
      parts.push(lines + ' line' + (lines !== 1 ? 's' : '') + ' created');
      if (d.afterBytes > 0) parts.push(formatBytes(d.afterBytes));
      return parts.join('  ');
    }
    if ((name === 'delete_path' || name === 'remote_delete_path') && parsed.data) {
      return '';
    }
    if ((name === 'http_request' || name === 'web_fetch') && parsed.data) return '';
    // grep results stay as a single non-expandable status line. The compact
    // hit count is rendered by formatToolChip(); do not retain matching lines
    // in the message body or build a hidden detail preview.
    if (name === 'grep_files' && parsed.data) return '';
    // list_files result: show entries
    if ((name === 'list_files' || name === 'remote_list_files') && parsed.data && Array.isArray(parsed.data.entries)) {
      let out = parsed.data.count + ' items';
      if (parsed.data.truncated) out += ' (truncated)';
      out += '\n';
      for (const entry of parsed.data.entries.slice(0, 200)) {
        out += entry.path + (entry.dir ? '/' : '') + '\n';
      }
      if (parsed.data.entries.length > 200) out += '... and ' + (parsed.data.entries.length - 200) + ' more';
      return out.slice(0, 12000);
    }
    // Skill result: show loaded skill name, not the full JSON wrapper.
    if ((name === 'skill' || name === 'Skill') && parsed.data) {
      if (parsed.data.message) return parsed.data.message;
      if (parsed.data.name) return 'Skill loaded: ' + parsed.data.name;
    }
    if ((name === 'create_goal' || name === 'update_goal' || name === 'get_goal') && parsed.data) {
      return formatGoalToolResult(parsed.data);
    }
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

function formatGoalToolResult(goal = {}) {
  if (goal.hasGoal === false) return t('tools.goal.noActive');
  const lines = [];
  if (goal.objective) lines.push(`${t('tools.goal.objective')}：${goal.objective}`);
  if (goal.status) lines.push(`${t('tools.goal.status')}：${t(`tools.goal.status.${goal.status}`)}`);
  if (goal.completionCriterion) lines.push(`${t('tools.goal.criterion')}：${goal.completionCriterion}`);
  const reason = goal.reason || goal.statusReason || '';
  if (reason) lines.push(`${t('tools.goal.reason')}：${reason}`);
  const turnsUsed = Number(goal.turnsUsed ?? 0);
  const maxTurns = Number(goal.maxTurns ?? goal.turnBudget ?? 0);
  if (turnsUsed > 0 || maxTurns > 0) {
    lines.push(`${t('tools.goal.progress')}：${turnsUsed}${maxTurns > 0 ? ` / ${maxTurns}` : ''}`);
  }
  return lines.join('\n');
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
  if (task.name) lines.push(`Task: ${task.name}`);
  if (task.id) lines.push(`ID: ${task.id}`);
  lines.push(`Schedule: ${formatScheduledToolSchedule(task.schedule || {})}`);
  if (task.workspace) lines.push(`Workspace: ${task.workspace}`);
  lines.push('Mode: YOLO');
  if (task.nextRunAt) lines.push(`Next run: ${formatDateTime(Number(task.nextRunAt))}`);
  if (task.lastRunAt) lines.push(`Last run: ${formatDateTime(Number(task.lastRunAt))}`);
  if (task.lastStatus) lines.push(`Status: ${task.running ? 'running' : task.lastStatus}`);
  if (task.runCount !== undefined) lines.push(`Runs: ${task.runCount}`);
  if (task.maxSteps || task.timeoutSeconds) {
    lines.push(`Per run: ${task.maxSteps || '-'} steps · ${task.timeoutSeconds || '-'}s timeout`);
  }
  return lines.join('\n');
}

function formatScheduledToolSchedule(schedule = {}) {
  if (schedule.type === 'once') return `once at ${schedule.at || '-'}`;
  if (schedule.type === 'interval') return `every ${schedule.every || '-'}`;
  if (schedule.type === 'cron') return `cron ${schedule.cron || '-'}`;
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
  if (name) lines.push(`name: ${name}`);
  if (service?.id) lines.push(`id: ${service.id}`);
  if (service?.status) lines.push(`status: ${service.status}`);
  if (service?.pid) lines.push(`pid: ${service.pid}`);
  if (service?.command) lines.push(`command: ${service.command}`);
  if (service?.cwd) lines.push(`cwd: ${service.cwd}`);
  if (service?.startedAt) lines.push(`started: ${formatUnixTimestamp(service.startedAt)}`);
  if (service?.stoppedAt) lines.push(`stopped: ${formatUnixTimestamp(service.stoppedAt)}`);
  if (service?.exitCode) lines.push(`exit code: ${service.exitCode}`);
  if (service?.error) lines.push(`error: ${service.error}`);
  const output = stripAnsi(service?.outputTail || '');
  if (output) {
    lines.push('');
    lines.push('output tail:');
    lines.push(output);
  }
  return lines.join('\n');
}

// formatServiceReadResult renders a background_process.read result. The
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

// formatServiceListResult renders a background_process.list result. The
// backend omits output tails; we render one compact line per service so the
// model/user can scan all services without context bloat.
function formatServiceListResult(data) {
  const services = Array.isArray(data?.services) ? data.services : [];
  if (services.length === 0) return 'No tracked services.';
  const header = [];
  if (typeof data?.activeCount === 'number' && typeof data?.maxActive === 'number') {
    header.push(`${data.activeCount}/${data.maxActive} active`);
  }
  header.push(`${services.length} service${services.length === 1 ? '' : 's'}`);
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
  if (chars < 1000) return chars + ' chars';
  if (chars < 1000000) return (chars / 1000).toFixed(chars < 10000 ? 1 : 0) + 'k chars';
  return (chars / 1000000).toFixed(1) + 'm chars';
}

function formatDuration(ms) {
  if (!ms) return '0s';
  const secs = Math.floor(ms / 1000);
  if (secs < 60) return secs + 's';
  return Math.floor(secs / 60) + 'm ' + (secs % 60) + 's';
}

function formatDurationShort(ms) {
  const value = Number(ms);
  if (!Number.isFinite(value) || value <= 0) return '';
  if (value < 1000) return '<1s';
  const secs = Math.max(1, Math.round(value / 1000));
  const hours = Math.floor(secs / 3600);
  const mins = Math.floor((secs % 3600) / 60);
  const rest = secs % 60;
  if (hours > 0) return `${hours}h${mins > 0 ? `${mins}m` : ''}`;
  if (mins > 0) return `${mins}m${rest > 0 ? `${rest}s` : ''}`;
  return `${rest}s`;
}

function stripAnsi(text) {
  // Strip ANSI escape sequences (color codes, cursor moves, etc.)
  return String(text || '').replace(/\x1b\[[0-9;]*[A-Za-z]/g, '');
}

function downloadMD(content, filename) {
  const blob = new Blob([content], { type: 'text/markdown;charset=utf-8' });
  const url = URL.createObjectURL(blob);
  const a = document.createElement('a');
  a.href = url;
  a.download = filename;
  document.body.appendChild(a);
  a.click();
  document.body.removeChild(a);
  URL.revokeObjectURL(url);
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
  if (!n) return '0';
  if (n >= 1000) return (n / 1000).toFixed(1) + 'k';
  return String(n);
}

function fmtTokenUnit(n) {
  const value = Number(n) || 0;
  if (value >= 1000000) return Math.round(value / 1000000) + 'M';
  if (value >= 1000) return Math.round(value / 1000) + 'k';
  return String(value);
}

function fmtTime(ts) {
  if (!ts) return '';
  return formatDateTime(ts, { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' });
}

function msgCount(s) {
  if (Number.isFinite(Number(s?.messageCount))) return Number(s.messageCount);
  if (!s?.messages) return 0;
  return s.messages.filter((message) => message?.role === 'user' || message?.role === 'assistant').length;
}

// Real per-session token counts come from the backend (GetSessionContextTokens),
// which accounts for tool calls, tool results, reasoning — the bulk of the
// actual context payload. The previous chars/4 estimate only counted
// message.content text and missed everything else, so it was always severely
// understated. We cache the numbers per session id and refresh them when the
// list is opened.
const sessionTokensCache = reactive({});
let sessionTokensRefreshing = false;

async function refreshSessionTokensList() {
  if (sessionTokensRefreshing) return;
  sessionTokensRefreshing = true;
  const targets = sessions.value.filter((session) => session?.id && !Number(session.contextTokens || sessionTokensCache[session.id] || 0));
  try {
    await Promise.allSettled(targets.map(async (session) => {
      try {
        const value = Number(await GetSessionContextTokens(session.id)) || 0;
        if (value <= 0) return;
        session.contextTokens = value;
        sessionTokensCache[session.id] = value;
        if (session.hasSnapshot && !session.isRunning) {
          enqueueSessionWrite(() => SaveSessionIndex(sessionIndexEntry(session))).catch(() => {});
        }
      } catch (_) {
        // Keep the persisted value when one session cannot be recalculated.
      }
    }));
  } finally {
    sessionTokensRefreshing = false;
  }
}

function ctxSize(s) {
  const tokens = Number(s?.contextTokens || sessionTokensCache[s?.id] || 0);
  if (!Number.isFinite(tokens) || tokens < 0) return '—';
  if (tokens >= 1000) return (tokens / 1000).toFixed(1) + 'k';
  return String(tokens);
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

function formatBytes(bytes) {
  if (!bytes) return '0 B';
  const units = ['B', 'KB', 'MB', 'GB'];
  let i = 0;
  let n = bytes;
  while (n >= 1024 && i < units.length - 1) {
    n /= 1024;
    i++;
  }
  return `${n.toFixed(i ? 1 : 0)} ${units[i]}`;
}

function focusPromptInput() {
  promptInputRefs[activeWorkspaceId]?.focus?.();
}

watch(configVisible, (visible) => {
  if (visible) {
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
  const tabs = workspaceTabs.value;
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
  if (target.closest('.command-menu, .file-mention-menu, .sessions-menu')) return;
  if (sessionsVisible.value) sessionsVisible.value = false;
  if (fileMentionVisible.value) closeFileMentionMenu();
  if (commandMenuVisible.value) commandMenuVisible.value = false;
}

function handleGlobalKeydown(event) {
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
  if (event.key === 'Escape' && (configVisible.value || taskCenterVisible.value || gitDiffVisible.value)) {
    // Let Naive UI modals handle their own ESC (nested stack); do not stop the run.
    return;
  }
  if (event.key === 'Escape' && activeSession.value?.runId) {
    event.preventDefault();
    event.stopPropagation();
    stopRun();
    return;
  }
  if (!(event.ctrlKey || event.metaKey) || event.altKey) return;
  if (event.key.toLowerCase() === 'w') {
    event.preventDefault();
    closeWorkspaceTab(activeWorkspaceId.value);
    return;
  }
  if (event.key.toLowerCase() === 't') {
    event.preventDefault();
    // New workspace tab reusing the current tab's workspace path
    const currentTab = workspaceTabs.value.find((tab) => tab.id === activeWorkspaceId.value);
    const tab = createWorkspaceTab(currentTab?.path || '');
    workspaceTabs.value.push(tab);
    switchWorkspaceTab(tab.id);
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

function handleAudioUnlock() {
  primeCompletionAudio();
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
    if (!result?.ok) {
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
  window.addEventListener('pointerdown', handleAudioUnlock, { once: true, passive: true });
  window.addEventListener('keydown', handleAudioUnlock, { once: true });
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
  window.removeEventListener('pointerdown', handleAudioUnlock);
  window.removeEventListener('keydown', handleAudioUnlock);
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
  for (const att of pendingAttachments.value) releaseAttachmentPreview(att);
  for (const session of sessions.value) releaseSessionAttachments(session);
  closeCompletionAudio();
});
</script>
