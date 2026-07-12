<template>
  <n-config-provider :theme="darkTheme" :theme-overrides="themeOverrides" inline-theme-disabled>
    <n-dialog-provider>
      <n-notification-provider>
        <n-message-provider>
          <n-layout class="app-shell">
            <AppHeader
              :workspace-tabs="workspaceTabsWithStatus"
              :active-workspace-id="activeWorkspaceId"
              :plan-mode-active="planModeActive"
              :grill-mode-active="!!activeSession?.grillMode"
              :update-available="updateAvailable"
              :latest-version="latestReleaseVersion"
              :is-maximised="isMaximised"
              :history-options="historyOptions"
              @switch-workspace="switchWorkspaceTab"
              @close-workspace="closeWorkspaceTab"
              @add-workspace="addWorkspaceTab"
              @history-select="onHistorySelect"
              @open-update="openUpdatePage"
              @open-settings="configVisible = true"
              @minimise="minimiseWindow"
              @toggle-maximise="toggleMaximiseWindow"
              @close-window="closeWindow"
            />

            <!-- Main chat area -->
            <div class="main-area">
              <n-layout class="chat-layout" content-style="display: flex; flex-direction: column;">
                <ChatMessages
                  ref="chatMessagesRef"
                  :messages="displayMessages"
                  :last-user-msg-index="lastUserMsgIndex"
                  :focused-id="focusedToolId"
                  :render-fn="renderMarkdownWithMode"
                  :fmt-k="fmtK"
                  :tools="visibleAvailableTools"
                  @toggle-archive="toggleArchiveMessages"
                  @toggle-tool="toggleToolExpand"
                  @focus-tool="focusTool"
                  @clear-focus="clearFocus"
                  @export-one-msg="exportOneMessage"
                  @export-all-msgs="exportAllMessages"
                  @submit-ask="submitAskResponse"
                />

              <!-- Fixed todo panel -->
              <Transition name="todo-panel">
                <div v-if="showTodoPanel" :class="['todo-panel', { collapsed: todoPanelCollapsed }]">
                  <button class="todo-panel-header" :title="todoPanelCollapsed ? '展开 Todo' : '折叠 Todo'" @click="todoPanelCollapsed = !todoPanelCollapsed">
                    <span>Todo</span>
                    <span class="todo-panel-count">{{ activeTodoCount }}/{{ todos.length }}</span>
                    <span class="todo-panel-toggle">{{ todoPanelCollapsed ? '▸' : '▾' }}</span>
                  </button>
                  <div v-show="!todoPanelCollapsed" class="todo-panel-list">
                    <div v-for="(item, idx) in todos" :key="idx" :class="['todo-item', item.status]">
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
                <div v-if="sessionsVisible" class="sessions-menu">
                  <div class="command-title">会话 ({{ sessions.length }})</div>
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
                        <span class="session-label">{{ s.title || '新会话' }}</span>
                        <span class="session-time">
                          {{ fmtTime(s.createdAt) }}
                          <template v-if="s.id === activeSessionId && s.isRunning"> ~ 进行中</template>
                          <template v-else-if="s.updatedAt && s.updatedAt !== s.createdAt"> ~ {{ fmtTime(s.updatedAt) }}</template>
                        </span>
                      </div>
                      <span class="session-meta">{{ msgCount(s) }}条 · {{ ctxSize(s) }}t</span>
                      <span v-if="s.id === activeSessionId" class="session-current">当前</span>
                      <span v-if="s.isRunning" class="session-running">●</span>
                      <button
                        type="button"
                        class="session-delete"
                        title="删除会话"
                        :disabled="s.isRunning || !!s.runId"
                        @mousedown.stop.prevent
                        @click.stop="deleteSession(index)"
                      >
                        ×
                      </button>
                    </div>
                  </div>
                </div>
                <div v-if="activeSessionRunning" class="composer-run-status">
                  <span class="composer-run-status-dots">
                    <span class="composer-run-status-dot"></span>
                    <span class="composer-run-status-dot"></span>
                    <span class="composer-run-status-dot"></span>
                  </span>
                </div>
                <n-input
                  ref="promptInputRef"
                  v-model:value="promptText"
                  type="textarea"
                  :input-props="{ onPaste: handlePromptPaste }"
                  :autosize="{ minRows: 2, maxRows: 5 }"
                  placeholder="输入任务，Enter 发送，Shift+Enter 换行，Esc 中断 · 按 / 打开指令菜单"
                  @keydown="handlePromptKeydown"
                  @input="handlePromptInput"
                />
                <div v-if="pendingAttachments.length" class="pending-attachments">
                  <div v-for="att in pendingAttachments" :key="att.id" class="pending-attachment">
                    <span class="pending-attachment-icon">{{ attachmentIcon(att) }}</span>
                    <span class="pending-attachment-name" :title="att.name">{{ att.name }}</span>
                    <span class="pending-attachment-size">{{ fmtBytes(att.size) }}</span>
                    <button class="pending-attachment-remove" @click="removeAttachment(att.id)" title="移除">×</button>
                  </div>
                </div>
                <input ref="attachmentInputRef" type="file" multiple class="hidden-file-input" @change="handleAttachmentSelected" />
                <ComposerInfoBar
                  :running="activeSessionRunning"
                  :config="config"
                  :git-status="gitStatus"
                  :context-breakdown="contextBreakdown"
                  :context-percent="contextPercent"
                  :context-used="contextUsed"
                  :context-max="contextMax"
                  :context-usage-style="contextUsageStyle"
                  :workspace-input-tokens="workspaceInputTokens"
                  :workspace-output-tokens="workspaceOutputTokens"
                  :plan-mode-active="planModeActive"
                  :grill-mode-active="!!activeSession?.grillMode"
                  :scheduled-task-count="scheduledTasks.length"
                  :scheduled-task-running-count="scheduledTaskRunningCount"
                  :fmt-k="fmtK"
                  @switch-model="switchToModel"
                  @open-config="configVisible = true"
                  @open-git-diff="openGitDiff"
                  @open-workspace="openWorkspaceInFileManager"
                  @jump-question="jumpToUserQuestion"
                  @set-run-mode="setRunMode"
                  @open-scheduled-tasks="openScheduledTasks"
                  :session-messages="activeMessages"
                  :session-title="activeSession?.title || ''"
                />
              </div>
            </n-layout>

            </div>
          </n-layout>
          <SettingsModal
            :visible="configVisible"
            :config-draft="configDraft"
            @close="configVisible = false"
            @save="onSettingsSave"
            @skills-changed="onSkillsChanged"
            @mcp-saved="onMcpSaved"
          />
          <ScheduledTasksPanel
            :show="scheduledTasksVisible"
            :tasks="scheduledTasks"
            :loading="scheduledTasksLoading"
            :deleting-ids="scheduledTaskDeletingIds"
            @close="scheduledTasksVisible = false"
            @refresh="loadScheduledTasks"
            @delete="deleteScheduledTask"
          />
          <RenderBoundary label="Git 改动"><GitDiffModal v-model:show="gitDiffVisible" :git-status="gitStatus" :workspace="config.workspace" /></RenderBoundary>

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
  CancelRun,
  GetConfig,
  GetContextBreakdown,
  GetSessionContextTokens,
  GetWorkspaceTokenUsage,
  ResetWorkspaceTokenUsage,
  GetSubagents,
  GetGitStatus,
  ReloadConfig,
  ListFiles,
  ReadFile,
  SaveConfig,
  SelectWorkspace,
  StartChat,
  CompactSession,
  ListSkills,
  ListTools,
  OpenWorkspaceInFileManager,
  ActivateSkill,
  DeactivateSkill,
  ClearSkills,
  GetActiveSkills,
  SetPlanMode,
  GetPlanMode,
  SwitchModel,
  GetTodos,
  GetMcpServers,
  GetMcpConfig,
  SaveMcpConfig,
  RestartMcpServers,
  ListScheduledTasks,
  DeleteScheduledTask,
  DeleteSession,
  ReleaseSession,
  SubmitAskResponse,
} from '../wailsjs/go/main/App';
import { BrowserOpenURL, Environment, EventsOn, WindowMinimise, WindowMaximise, WindowUnmaximise, WindowIsMaximised, Quit } from '../wailsjs/runtime/runtime';
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
import SettingsModal from './components/SettingsModal.vue';
import ChatMessages from './components/ChatMessages.vue';
import ScheduledTasksPanel from './components/ScheduledTasksPanel.vue';
import { assignConfig, defaultConfig } from './utils/config.mjs';
import { buildVersion } from './utils/buildVersion.js';
import { computeEditStats, formatEditStats } from './utils/diff.js';
import { isNewerReleaseVersion } from './utils/versionCheck.mjs';
import {
  DEFAULT_TOOL_PREVIEW_LINES,
  displaySourceMessages as buildDisplaySourceMessages,
  estimateMessageRenderChars as estimateToolMessageRenderChars,
  isRenderableMessage,
} from './utils/toolPreview.mjs';

const GitDiffModal = defineAsyncComponent(() => import('./components/GitDiffModal.vue'));

onErrorCaptured((err, _instance, info) => {
  console.error('[ui:error]', info, err);
  return false;
});

const messageScrollbar = ref(null); // kept for scrollMessagesToBottom to work via chatMessagesRef
const chatMessagesRef = ref(null);
const promptInputRef = ref(null);

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
};

const { message } = createDiscreteApi(['message'], {
  configProviderProps: {
    theme: darkTheme,
    themeOverrides,
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
    `<div class="markdown-mermaid" data-mermaid-source="${markdown.utils.escapeHtml(encodedSource)}">`,
    '<div class="markdown-mermaid-toolbar" aria-label="图表操作">',
    '<button type="button" class="markdown-mermaid-action" data-mermaid-action="download" title="下载 SVG" aria-label="下载 SVG"><svg viewBox="0 0 24 24" aria-hidden="true"><path d="M12 3v12M7 10l5 5 5-5M5 21h14"/></svg></button>',
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
  highlight(code, lang) {
    if (lang === 'ascii-art') {
      return `<pre class="ascii-banner"><code>${markdown.utils.escapeHtml(code)}</code></pre>`;
    }
    if (isShellLanguage(lang)) {
      return `<pre class="hljs code-block shell-code"><code>${highlightShellCommand(code)}</code></pre>`;
    }
    try {
      const highlighted = lang && hljs.getLanguage(lang)
        ? hljs.highlight(code, { language: lang }).value
        : hljs.highlightAuto(code).value;
      return `<pre class="hljs code-block"><code>${highlighted}</code></pre>`;
    } catch (_) {
      return `<pre class="hljs code-block"><code>${markdown.utils.escapeHtml(code)}</code></pre>`;
    }
  },
});

const defaultCodeInlineRenderer = markdown.renderer.rules.code_inline || ((tokens, idx, options, _env, self) => self.renderToken(tokens, idx, options));
markdown.renderer.rules.code_inline = (tokens, idx, options, env, self) => {
  const content = tokens[idx]?.content || '';
  if (looksLikeShellCommand(content)) {
    return `<code class="shell-inline">${highlightShellCommand(content)}</code>`;
  }
  return defaultCodeInlineRenderer(tokens, idx, options, env, self);
};

const defaultFenceRenderer = markdown.renderer.rules.fence || ((tokens, idx, options, _env, self) => self.renderToken(tokens, idx, options));
markdown.renderer.rules.fence = (tokens, idx, options, env, self) => {
  const token = tokens[idx];
  const diagramSpec = mermaidFenceSpec(token.info);
  if (diagramSpec && !markdownRenderStreaming) {
    const renderedDiagram = renderMermaidFence(token.content, diagramSpec);
    if (renderedDiagram) return `${renderedDiagram}\n`;
  }
  return defaultFenceRenderer(tokens, idx, options, env, self);
};

function scheduleMermaidRender() {
  if (typeof window === 'undefined' || typeof document === 'undefined') return;
  if (mermaidRenderScheduled) return;
  mermaidRenderScheduled = true;
  nextTick(() => {
    window.requestAnimationFrame(() => {
      mermaidRenderScheduled = false;
      renderPendingMermaidDiagrams();
    });
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
          theme: 'dark',
          themeVariables: {
            background: '#151515',
            mainBkg: '#202020',
            secondBkg: '#262626',
            primaryColor: '#202020',
            primaryTextColor: '#edf2ef',
            primaryBorderColor: '#8ab4ff',
            lineColor: '#8fd4b4',
            textColor: '#edf2ef',
            fontFamily: 'Inter',
          },
        });
        mermaidInitialized = true;
      }
      return mermaid;
    });
  }
  return mermaidModulePromise;
}

async function renderPendingMermaidDiagrams() {
  const nodes = Array.from(document.querySelectorAll('.markdown-mermaid[data-mermaid-source]:not([data-mermaid-rendered]):not([data-mermaid-rendering])'));
  if (!nodes.length) return;
  let mermaid;
  try {
    mermaid = await loadMermaidModule();
  } catch (err) {
    for (const node of nodes) {
      markMermaidError(node, `Mermaid 加载失败：${err?.message || err || 'unknown error'}`);
    }
    return;
  }

  for (const node of nodes) {
    if (!node.isConnected || node.dataset.mermaidRendered) continue;
    node.dataset.mermaidRendering = 'true';
    try {
      const source = decodeURIComponent(node.dataset.mermaidSource || '');
      const output = node.querySelector('.markdown-mermaid-output');
      if (!source || !output) throw new Error('empty diagram');
      const id = `ally-mermaid-${Date.now()}-${++mermaidRenderSequence}`;
      const result = await mermaid.render(id, source);
      if (!node.isConnected) continue;
      output.innerHTML = result.svg || '';
      if (typeof result.bindFunctions === 'function') {
        result.bindFunctions(output);
      }
      node.dataset.mermaidRendered = 'true';
      node.classList.add('rendered');
    } catch (err) {
      markMermaidError(node, err?.message || String(err || 'Mermaid 渲染失败'));
    } finally {
      delete node.dataset.mermaidRendering;
    }
  }
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
  error.textContent = messageText || 'Mermaid 渲染失败';
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

function downloadMermaidSvg(node) {
  const svg = node.querySelector('.markdown-mermaid-output svg');
  if (!svg) return;
  const copy = svg.cloneNode(true);
  if (!copy.getAttribute('xmlns')) {
    copy.setAttribute('xmlns', 'http://www.w3.org/2000/svg');
  }
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
const sessions = ref([]);
const activeSessionId = ref('');
const activeRunId = ref('');
const files = ref([]);
const filePreview = ref('选择文件查看内容。');
const previewTitle = ref('文件预览');
const currentPreview = ref('');
const currentFileDir = ref('');
const sessionPromptTexts = reactive({});
const promptText = computed({
  get: () => sessionPromptTexts[activeSessionId.value] || '',
  set: (val) => { sessionPromptTexts[activeSessionId.value] = val; },
});
const commandMenuVisible = ref(false);
const selectedCommandIndex = ref(0);
const sessionsVisible = ref(false);
const sessionsSelectedIndex = ref(0);
const sessionsScrollRef = ref(null);
const commandHistory = ref([]);
const commandHistoryIndex = ref(-1);
const configVisible = ref(false);
const focusedToolId = ref('');
const workspaceTabs = ref([]);
const activeWorkspaceId = ref('');
const workspaceHistory = ref(loadWorkspaceHistory());
const settingsPage = ref('general');
const planModeActive = ref(false);
const showSkillsPanel = ref(false);
const todos = ref([]);
const todosBySession = reactive({});
const todoRevisionsBySession = reactive({});
const todoPanelCollapsed = ref(false);
const isMaximised = ref(false);
const availableSkills = ref([]);
const activeSkillNames = ref([]);
const skillsLoading = ref(false);
const skillToggleInFlight = ref('');
const availableTools = ref([]);
const scheduledTasks = ref([]);
const scheduledTasksVisible = ref(false);
const scheduledTasksLoading = ref(false);
const scheduledTaskDeletingIds = ref([]);
const subRuns = ref([]);
const thinking = ref(false);
const modelEditorVisible = ref(false);
const modelEditorIndex = ref(-1);
const modelDraft = reactive(defaultModelDraft());
const activeProviderTab = ref('');
const mcpConfigText = ref('');
const mcpServers = ref([]);
const mcpLoading = ref(false);
const streamBuffers = new Map();
const runtimeEventOffs = [];
const missingDependencyWarningsShown = new Set();
let streamFlushScheduled = false;
let runtimeEventsBound = false;
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

const ALLY_REPOSITORY_URL = 'https://github.com/Bronya0/ally-agent';
const ALLY_LATEST_RELEASE_API = 'https://api.github.com/repos/Bronya0/ally-agent/releases/latest';

const apiFormatOptions = [
  { label: 'OpenAI Chat Completions', value: 'openai_chat' },
  { label: 'OpenAI Responses', value: 'openai_responses' },
  { label: 'Anthropic Messages', value: 'anthropic_messages' },
];

function normalizeApiFormat(value) {
  const v = String(value || '').trim().toLowerCase().replace(/[-\s]+/g, '_');
  if (['openai_responses', 'responses', 'response'].includes(v)) return 'openai_responses';
  if (['anthropic', 'anthropic_messages', 'claude', 'claude_messages', 'messages'].includes(v)) return 'anthropic_messages';
  return 'openai_chat';
}

function apiFormatLabel(value) {
  const format = normalizeApiFormat(value);
  return apiFormatOptions.find((item) => item.value === format)?.label || 'OpenAI Chat Completions';
}

function apiFormatDefaultBaseUrl(value) {
  switch (normalizeApiFormat(value)) {
    case 'openai_responses':
      return 'https://api.openai.com/v1';
    case 'anthropic_messages':
      return 'https://api.anthropic.com';
    default:
      return 'https://api.deepseek.com';
  }
}

function modelPlaceholder(value) {
  switch (normalizeApiFormat(value)) {
    case 'openai_responses':
      return 'gpt-4.1-mini';
    case 'anthropic_messages':
      return 'claude-sonnet-5';
    default:
      return 'deepseek-v4-flash';
  }
}


const builtinCommands = [
  { key: 'new', label: '/new', description: '新建会话，清空上下文', text: '', special: 'new' },
  { key: 'goal', label: '/goal', description: '设置目标模式', text: '', special: 'goal' },
  { key: 'skills', label: '/skills', description: '查看可用技能', text: '', special: 'skills' },
  { key: 'clearskills', label: '/clearskills', description: '停用所有技能', text: '', special: 'clear_skills' },
  { key: 'sessions', label: '/sessions', description: '查看和切换历史会话', text: '', special: 'sessions' },
  { key: 'reload', label: '/reload', description: '重新加载模型配置文件', text: '', special: 'reload' },
  { key: 'init', label: '/init', description: '分析项目并生成 AGENTS.md', text: '', special: 'init' },
  { key: 'note', label: '/note', description: '保存长期记忆', text: '', special: 'remember' },
  { key: 'remember', label: '/remember', description: '同 /note，保存长期记忆', text: '', special: 'remember' },
  { key: 'compact', label: '/compact', description: '压缩对话上下文，用摘要替换历史消息', text: '', special: 'compact' },
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
const GRILL_BLOCKED_TOOL_NAMES = new Set([
  'edit', 'create_file', 'delete_path', 'run_command', 'background_process', 'wait',
  'http_request', 'web_fetch', 'remote_edit', 'remote_create_file',
  'remote_delete_path', 'remote_run_command', 'agent_delegate', 'memory_write',
  'todo_write', 'create_goal', 'update_goal', 'scheduled_task',
]);
const visibleAvailableTools = computed(() => {
  if (!activeSession.value?.grillMode) return availableTools.value;
  return availableTools.value.filter((tool) => {
    const name = String(tool?.name || '');
    return tool?.source !== 'mcp' && !name.startsWith('mcp__') && !GRILL_BLOCKED_TOOL_NAMES.has(name);
  });
});
const workspaceTabsWithStatus = computed(() => workspaceTabs.value.map((tab) => {
  const session = tab.sessionId ? sessions.value.find((item) => item.id === tab.sessionId) : null;
  return { ...tab, isRunning: !!(session?.isRunning || session?.runId) };
}));
const activeMessages = computed(() => activeSession.value?.messages || []);
const activeSessionRunning = computed(() => !!activeSession.value?.isRunning);
const scheduledTaskRunningCount = computed(() => scheduledTasks.value.filter((task) => task?.running).length);
const activeTodoCount = computed(() => todos.value.filter((item) => item?.status !== 'done').length);
const showTodoPanel = computed(() => todos.value.length > 0 && activeTodoCount.value > 0);

const MAX_RENDER_MESSAGES = 180;
const MAX_RENDER_CHARS = 220000;
const MAX_EXPANDED_RENDER_MESSAGES = 360;
const MAX_EXPANDED_RENDER_CHARS = 440000;
const TOOL_PREVIEW_LINES = DEFAULT_TOOL_PREVIEW_LINES;

function estimateMessageRenderChars(msg) {
  return estimateToolMessageRenderChars(msg, { toolPreviewLines: TOOL_PREVIEW_LINES });
}

function displaySourceMessages(session) {
  return buildDisplaySourceMessages(session, expandedArchiveSessions.value, {
    maxMessages: MAX_RENDER_MESSAGES,
    maxChars: MAX_RENDER_CHARS,
    expandedMaxMessages: MAX_EXPANDED_RENDER_MESSAGES,
    expandedMaxChars: MAX_EXPANDED_RENDER_CHARS,
    toolPreviewLines: TOOL_PREVIEW_LINES,
  });
}

function toggleArchiveMessages(sessionId) {
  if (!sessionId) return;
  const next = new Set(expandedArchiveSessions.value);
  if (next.has(sessionId)) next.delete(sessionId);
  else next.add(sessionId);
  expandedArchiveSessions.value = next;
  nextTick(() => chatMessagesRef.value?.scrollbarRef?.scrollTo({ top: 0 }));
}

// Merge consecutive read tool cards into a single aggregated card.
const displayMessages = computed(() => {
  const src = displaySourceMessages(activeSession.value);
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
        // Single read card — handle batch_read specially
        if (m.name === 'batch_read' && m.batchEntries && m.batchEntries.length > 0) {
          out.push({
            role: 'tool_call',
            kind: 'read-group',
            status: m.batchEntries.some((entry) => entry.status === 'error') ? 'error' : m.status,
            time: m.time,
            eventId: m.eventId,
            expanded: true,
            readEntries: m.batchEntries,
            readTotalLines: m.batchEntries.reduce((s, e) => s + (e.lineCount || 0), 0),
            readTotalTokens: m.batchEntries.reduce((s, e) => s + (e.tokenCount || 0), 0),
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
        // If this is a batch_read with parsed entries, expand inline
        if (entry.name === 'batch_read' && entry.batchEntries && entry.batchEntries.length > 0) {
          for (const be of entry.batchEntries) {
            const entryStatus = be.status || entry.status;
            if (entryStatus === 'error') hasError = true;
group.readEntries.push({ title: be.title, chip: be.chip, lineCount: be.lineCount || 0, totalLines: be.totalLines || be.lineCount || 0, startLine: be.startLine || 1, endLine: be.endLine || be.totalLines || 0, truncated: !!be.truncated, tokenCount: be.tokenCount || 0, body: '', status: entryStatus, expanded: false });
            totalLines += be.lineCount || 0;
          }
        } else {
          group.readEntries.push({
            title: entry.title || '',
            chip: entry.chip || '',
            lineCount: entry.readLineCount || 0,
            totalLines: entry.readTotalLines || 0,
            tokenCount: entry.readTokenCount || 0,
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
      group.readTotalTokens = group.readEntries.reduce((s, e) => s + (e.tokenCount || 0), 0);
      out.push(group);
    } else {
      out.push(m);
      i++;
    }
  }
  return out;
});

const lastUserMsgIndex = computed(() => {
  const msgs = displayMessages.value;
  for (let i = msgs.length - 1; i >= 0; i--) {
    if (msgs[i].role === 'user') return i;
  }
  return -1;
});

const canSend = computed(() => {
  const s = activeSession.value;
  return promptText.value.trim().length > 0 && !(s && (s.runId || s.isRunning));
});

const contextTokens = ref(0);
const contextBreakdown = ref(null);
const workspaceTokenUsage = ref({ inputTokens: 0, outputTokens: 0 });
const gitStatus = ref({ isRepo: false });
const gitDiffVisible = ref(false);

async function refreshGitStatus() {
  try { gitStatus.value = await GetGitStatus(); } catch (_) { gitStatus.value = { isRepo: false }; }
}

function openGitDiff() {
  gitDiffVisible.value = true;
}

async function openWorkspaceInFileManager() {
  try {
    await OpenWorkspaceInFileManager();
  } catch (err) {
    message.warning(`无法打开工作区：${err}`);
  }
}

// Context computation — call backend for accurate full-payload token count
watch([activeSessionId, activeMessages], async () => {
  const sid = activeSessionId.value;
  if (!sid) { contextTokens.value = 0; contextBreakdown.value = null; return; }
  try {
    const n = await GetSessionContextTokens(sid);
    contextTokens.value = n || 0;
  } catch (_) { /* ignore */ }
  try {
    const b = await GetContextBreakdown(sid);
    contextBreakdown.value = b;
  } catch (_) { /* ignore */ }
}, { immediate: true, deep: true });

function refreshContextTokens(sid) {
  if (!sid) return;
  GetSessionContextTokens(sid).then(n => { contextTokens.value = n || 0; }).catch(() => {});
  GetContextBreakdown(sid).then(b => { contextBreakdown.value = b; }).catch(() => {});
}

function refreshWorkspaceTokenUsage(workspace = config.workspace || '') {
  GetWorkspaceTokenUsage(workspace)
    .then((usage) => { workspaceTokenUsage.value = usage || { inputTokens: 0, outputTokens: 0 }; })
    .catch(() => { workspaceTokenUsage.value = { inputTokens: 0, outputTokens: 0 }; });
}

watch(() => config.workspace, (workspace) => {
  refreshWorkspaceTokenUsage(workspace || '');
  refreshGitStatus();
}, { immediate: true });


const contextUsed = computed(() => {
  const n = contextTokens.value;
  if (n >= 1000) return (n / 1000).toFixed(1) + 'k';
  return String(n);
});
const contextWindow = computed(() => config.contextWindow || 128000);
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
    return raw ? JSON.parse(raw) : [];
  } catch (_) {
    return [];
  }
}

function saveWorkspaceHistory() {
  try {
    const list = workspaceHistory.value.slice(-20);
    localStorage.setItem('agent_workspace_history', JSON.stringify(list));
  } catch (_) { /* ignore */ }
}

function addToHistory(path) {
  if (!path) return;
  workspaceHistory.value = workspaceHistory.value.filter((p) => p !== path);
  workspaceHistory.value.push(path);
  saveWorkspaceHistory();
}

function removeFromHistory(path) {
  if (!path) return;
  workspaceHistory.value = workspaceHistory.value.filter((p) => p !== path);
  saveWorkspaceHistory();
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
  const recent = [...workspaceHistory.value].reverse().slice(0, 10);
  if (recent.length === 0) return [{ label: '无历史记录', disabled: true, key: '__empty__' }];
  return recent.map((path) => {
    const label = path.split(/[/\\]/).filter(Boolean).pop() || path;
    return {
      label: () => h('span', { class: 'hist-label' }, [
        h('span', { class: 'hist-name' }, label),
        h('span', { class: 'hist-path' }, `  —  ${path}`),
        h('span', {
          class: 'hist-del',
          title: '从历史移除',
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
  const now = new Date();
  const days = ['日', '一', '二', '三', '四', '五', '六'];
  const hh = String(now.getHours()).padStart(2, '0');
  const mm = String(now.getMinutes()).padStart(2, '0');
  const hour = now.getHours();
  const greetingGroups = [
    {
      start: 0,
      end: 4,
      lines: [
        '凌晨还亮着屏幕的人，多半心里装着没放下的事；先把最要紧的一步说出来，我陪你慢慢拆。',
        '夜已经很深了，别让脑子一个人硬扛太久；我们先把问题摊开，能解决的现在解决，不能解决的先放稳。',
        '这个点还在工作，说明事情确实压着你；我会尽量把话说清楚，把弯路省掉一点。',
        '凌晨适合安静地处理难题，也适合提醒自己别透支太狠；先做一小段，做完就该休息。',
        '现在是大多数人睡着的时候，你还在推进事情；我在这里，先从最卡住的地方开始。',
        '深夜容易把问题想得很重，我们把它切小一点，一步一步来，不急着一次全赢。',
        '凌晨的世界很安静，你的问题也会变得清晰一些；我们先挑最简单的一环解开它。',
        '这个点还在和自己较劲，说明你在意这件事；我在，不急着要结果，先把思路理顺。',
        '深夜不是做决定的好时候，却是理清思路的好时候；我们把选项列出来，天亮再选。',
        '别让一个问题在脑子里绕太多次；把它写下来给我，我帮你看看有没有遗漏的角度。',
        '凌晨的工作有一种特别的专注，但也容易钻牛角尖；我们先退一步看看全局。',
        '如果你是因为焦虑睡不着，那就把焦虑拆成具体的问题；能解决的我们今晚就解决，不能解决的先放下。',
      ],
    },
    {
      start: 4,
      end: 6,
      lines: [
        '天快亮了，如果你是早起，那就从一件清楚的小事开始；如果你是熬到现在，也记得给自己留点余地。',
        '清晨前的这段时间很安静，适合整理思路；把目标说具体一点，我帮你把第一步落下来。',
        '这个时间很珍贵，也很容易疲惫；我们把任务处理得干净些，别让它拖到一整天都心烦。',
        '快到早晨了，先别急着冲刺，把今天最重要的事排个顺序，会轻松很多。',
        '如果这一夜不太好过，至少现在可以把问题交给一个稳定的流程；你说任务，我来拆解。',
        '黎明前的脑子有时很清醒，有时很混乱；我们先抓住事实，再决定怎么做。',
        '清晨的光线还很淡，很适合做不需要太多勇气的事——比如先列一个今日清单。',
        '天亮之前是最好的准备时间；把今天可能遇到的难点提前想一遍，心里就有底了。',
        '早起的人已经有了先机，不用贪多；完成一件重要的事，今天就算赢了。',
        '如果你是一夜没睡，这个点该停一停了；把最紧急的事处理完，剩下的交给我。',
        '清晨的脑子像一张白纸，别让杂事先落笔；先做那件最有价值的事。',
        '天色在变亮，节奏也要慢慢跟上；先喝点水，再告诉我今天从哪里开始。',
      ],
    },
    {
      start: 6,
      end: 9,
      lines: [
        '早上好，新的工作日先不用急着满负荷启动；把今天最重要的一件事交出来，我们先稳稳推进。',
        '早上好，先让思路比日程更早醒过来；我可以帮你把目标、风险和下一步都理清楚。',
        '早上好，适合做决定，也适合把昨天遗留的问题收个口；我们从最明确的部分开始。',
        '早上好，今天不必一开始就追求完美，先把方向找准，后面的速度自然会上来。',
        '早上好，愿你今天少一点被打断，多一点完成感；把任务说出来，我们把它变成可执行的步骤。',
        '早上好，喝口水，打开项目，别急；先确认要解决什么，再动手会省很多力气。',
        '早上好，每个高效的一天都从明确目标开始；我们不贪多，但求每一步都踏实。',
        '早上好，别让昨天的情绪带进今天的代码里；新的一天有新的解法。',
        '早上好，一日之计在于晨，但不必把一整天的计划都在十分钟内定完；先确定第一件事就好。',
        '早上好，如果你今天有很多会，那就把最需要脑子的事排在会前做。',
        '早上好，不一定每天都要有进展，但每天至少要有方向；把今天的目标说出来。',
        '早上好，好的开始不一定需要完美的计划，只需要一个清晰的下一个动作。',
      ],
    },
    {
      start: 9,
      end: 12,
      lines: [
        '上午好，现在是适合深度工作的时间；把复杂问题拿出来，我们尽量一次看透关键路径。',
        '上午好，脑子通常还比较清爽，适合处理需要判断力的事；我会帮你把信息压实，不绕圈。',
        '上午好，如果今天事情很多，先别被列表吓住；我们挑最有影响的一项开始推进。',
        '上午好，适合写代码、查问题、做设计，也适合把含糊的需求说清楚。',
        '上午好，别让零碎消息把节奏切碎；把任务放到这里，我们按优先级处理。',
        '上午好，当前精力值得用在真正重要的地方；先说目标，我帮你找最短的可靠路径。',
        '上午好，这段时间的注意力最值钱；别让它浪费在切换上下文上，我们专心搞定一件事。',
        '上午好，如果感觉时间被切得很碎，那就用最小可用方案先跑通再说。',
        '上午好，很多问题只要开始动手，就会发现比想象中简单；从最丑的方案开始也行。',
        '上午好，如果你在犹豫先做哪个，那就选那个卡住别人最久的；解了它，整条路就通了。',
        '上午好，别急着把答案做完美，先把问题定义清楚；好的问题已经解决了大半。',
        '上午好，深度工作的时间窗口有限；关掉消息提醒，我们把最难啃的一块先啃掉。',
      ],
    },
    {
      start: 12,
      end: 14,
      lines: [
        '中午好，忙到现在也该稍微喘口气；如果事情还没停，我们就用更省力的方式把它处理掉。',
        '中午好，午间适合复盘上午的进展；把卡住的点说出来，我们看看是信息不够还是路径不对。',
        '中午好，别把午饭时间全交给焦虑；我们先把任务拆成能落地的几步，再继续往前。',
        '中午好，如果你刚回来，先不用急着进入高压状态；从一个明确的小目标开始就行。',
        '中午好，上午已经消耗了一部分精力，接下来更要讲方法；我帮你把复杂度降下来。',
        '中午好，适合把半天的混乱整理成清单；你给我现状，我给你下一步。',
        '中午好，别在屏幕前边吃边焦虑；站起来五分钟换回来的效率，比硬撑一小时还多。',
        '中午好，上午如果有没搞定的事，别急着继续撞墙；换个角度想，或者先放一放。',
        '中午好，离下午还有一点时间；适合做不用脑子的杂务，也适合什么都不做。',
        '中午好，别让一个卡壳毁了一整天的节奏；卡住了就告诉我，我们一起找突破口。',
        '中午好，如果上午效率很高，下午就稍微降低预期；保持可持续比冲刺更重要。',
        '中午好，适当休息不是偷懒，是对注意力的一种管理；你先歇着，我帮你把下一步准备一下。',
      ],
    },
    {
      start: 14,
      end: 18,
      lines: [
        '下午好，这个时间容易被各种事情拉扯；我们把注意力收回来，先解决最影响进度的问题。',
        '下午好，如果精力有点下滑，就更需要清晰的步骤；我会把任务拆到能直接执行。',
        '下午好，很多问题不是难，而是堆在一起显得乱；我们先分层，再逐个处理。',
        '下午好，适合推进实现、补测试、修边角；把你想完成的结果说清楚，我来协助落地。',
        '下午好，别让一个小 bug 偷走整段时间；我们先建立反馈信号，再定位原因。',
        '下午好，今天还有一段可用时间，足够把关键事情往前推一截；从最有价值的部分开始。',
        '下午好，这个时段容易犯困，也容易烦躁；我们先做那些不需要太多创造力的任务。',
        '下午好，如果脑子已经转不动了，就别硬写逻辑；改改文案、清清冗余、补补注释，都是推进。',
        '下午好，别被"下午效率低"的心理暗示困住；把大目标切成小动作，一个一个来。',
        '下午好，适合复盘、重构和清理旧问题；把历史债务还一点，项目的利息就会低一些。',
        '下午好，如果今天的任务太多，就分两类："必须今天交的"和"今天做了明天会轻松的"。',
        '下午好，有时候慢下来才是快的捷径；先把方向确认对，再用力。',
      ],
    },
    {
      start: 18,
      end: 21,
      lines: [
        '晚上好，白天已经够忙了，接下来尽量把事情处理得利落一点；你说目标，我帮你收尾。',
        '晚上好，这个时间适合整理、修复和把未完成的事关上门；我们不拖泥带水地推进。',
        '晚上好，如果你还在工作，至少让流程轻一点；我会帮你少绕路、多确认。',
        '晚上好，适合把一天里最烦的那个问题拿出来解决；解决不了也要把原因查清楚。',
        '晚上好，别让任务在脑子里过夜；我们先把它写清楚、拆清楚、处理清楚。',
        '晚上好，今天剩下的时间很宝贵，适合做明确、有边界的事；把范围给我，我们开始。',
        '晚上好，白天没做完的事不用全在今天消化；挑一件最有收尾价值的，做完就收工。',
        '晚上好，适合梳理一天的工作，也适合把遗留问题明确化；给明天的自己留一份清楚的任务书。',
        '晚上好，如果你还在为某个问题烦躁，说明它触动了一个真问题；我们一起找到它到底是什么。',
        '晚上好，别把"今天不够高效"当作自责的理由；能推进一点就是胜利。',
        '晚上好，这个时间做的每一件事，都是在给明天铺路；走稳一步就值了。',
        '晚上好，如果今天没什么产出，那就至少把原因总结出来；有时候排除错误的路径也是一种进度。',
      ],
    },
    {
      start: 21,
      end: 24,
      lines: [
        '夜里好，今天已经走到尾声了；如果还要做事，我们就尽量做得克制、准确、可收尾。',
        '夜里好，别把自己逼到太晚；先处理最关键的一步，剩下的可以规划到明天。',
        '夜里好，适合安静地排查问题，也适合给今天做个清楚的结论；我们从事实开始。',
        '夜里好，如果你只是想把心里的任务放下来，那也可以；我帮你整理成明天能接上的状态。',
        '夜里好，屏幕前的人辛苦了；我们把问题处理得稳一点，别让它继续消耗你。',
        '夜里好，今天无论顺不顺，都可以先把眼前这件事做好一点；把需求说出来，我在。',
        '夜里好，今天的辛苦到这里就可以了；我们把还没收尾的事记下来，明天再续。',
        '夜里好，越晚越容易低估问题的难度或高估自己的精力；先停下来，明天看会更清楚。',
        '夜里好，如果你还在改东西，记得改完这一版就保存休息；好的代码需要清醒的头脑。',
        '夜里好，今天解决不了的问题，不代表明天也解决不了；你只是需要一觉的时间。',
        '夜里好，睡前给自己三分钟想想今天做对了什么；哪怕只有一件，也值得肯定。',
        '夜里好，关机之前告诉自己：今天已经尽力了，剩下的交给明天的自己。',
      ],
    },
  ];
  const group = greetingGroups.find((item) => hour >= item.start && hour < item.end) || greetingGroups[0];
  const seed = now.getFullYear() * 10000 + (now.getMonth() + 1) * 100 + now.getDate() + hour;
  const greeting = group.lines[seed % group.lines.length];
  return `今天是 ${now.getMonth() + 1}月${now.getDate()}日 周${days[now.getDay()]}，现在是 ${hh}:${mm}。${greeting}`;
}

function buildWelcomeMessage(workspacePath = '') {
  const skillCount = availableSkills.value.length;
  const rows = [];
  if (workspacePath !== null) {
    rows.push({ label: '工作区', value: workspacePath || '未选择' });
  }
  rows.push({ label: '模型', value: `${config.providerName || '-'} · ${config.model || '-'}` });
  rows.push({ label: 'MCP', value: formatMcpSummary() });
  if (skillCount > 0) {
    rows.push({ label: '技能', value: `${skillCount} 个可用，输入 /<skillname> 加载` });
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
  if (servers.length === 0) return '0 个服务';
  if (connected === 0) return `${servers.length} 个服务，未连接`;
  return `${connected}/${servers.length} 个服务，${tools} 个工具`;
}

function updateWelcomeMcpRows() {
  const value = formatMcpSummary();
  for (const session of sessions.value) {
    for (const msg of session.messages || []) {
      if (!msg.welcome || !Array.isArray(msg.welcome.rows)) continue;
      const rows = msg.welcome.rows.filter((row) => row.label !== '指令');
      const existing = rows.find((row) => row.label === 'MCP');
      if (existing) existing.value = value;
      else {
        const modelIndex = rows.findIndex((row) => row.label === '模型');
        rows.splice(modelIndex >= 0 ? modelIndex + 1 : rows.length, 0, { label: 'MCP', value });
      }
      msg.welcome.rows = rows;
      msg.content = buildWelcomeContent(msg.welcome);
    }
  }
}

function buildWelcomeContent(welcome) {
  const table = (welcome.rows || []).map((row) => `| ${row.label} | ${row.value} |`).join('\n');
  return `${welcome.title || 'Ally'}\n\n| 项目 | 信息 |\n|------|------|\n${table}\n\n${welcome.greeting || ''}`;
}

function workspaceLabel(path) {
  return path ? (path.split(/[/\\]/).filter(Boolean).pop() || path) : '未选择工作区';
}

function inferSessionWorkspace(session) {
  if (!session) return '';
  if (session.workspace) return session.workspace;
  for (const msg of session.messages || []) {
    const rows = msg?.welcome?.rows;
    if (!Array.isArray(rows)) continue;
    const row = rows.find((item) => item?.label === '工作区');
    const value = String(row?.value || '').trim();
    if (value && value !== '未选择') return value;
  }
  return '';
}

function applySessionWorkspace(session) {
  const workspace = inferSessionWorkspace(session);
  if (!session || !workspace) return false;
  session.workspace = workspace;

  const tab = workspaceTabs.value.find((item) => item.id === activeWorkspaceId.value);
  if (tab) {
    tab.path = workspace;
    tab.label = workspaceLabel(workspace);
    tab.sessionId = session.id;
  }

  config.workspace = workspace;
  configDraft.workspace = workspace;
  addToHistory(workspace);
  loadPromptHistory(workspace);
  refreshFiles(workspace);
  refreshWorkspaceTokenUsage(workspace);
  SaveConfig({ ...config })
    .then(() => refreshGitStatus())
    .catch(() => { gitStatus.value = { isRepo: false }; });
  return true;
}

function createWorkspaceTab(path) {
  const id = crypto.randomUUID ? crypto.randomUUID() : `ws-${Date.now()}-${Math.random()}`;
  const label = workspaceLabel(path);
  // Create a linked session for this tab
  const sessionId = crypto.randomUUID ? crypto.randomUUID() : `s-${Date.now()}-${Math.random()}`;
  const now = Date.now();
  const session = { id: sessionId, title: label, workspace: path || '', messages: [], runId: '', isRunning: false, grillMode: false, createdAt: now, updatedAt: now };
  session.messages.push(buildWelcomeMessage(path || '未选择'));
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

function closeWorkspaceTab(id) {
  if (workspaceTabs.value.length <= 1) return;
  const idx = workspaceTabs.value.findIndex((t) => t.id === id);
  if (idx === -1) return;
  const tab = workspaceTabs.value[idx];
  workspaceTabs.value.splice(idx, 1);
  // Remove linked session to free memory
  if (tab && tab.sessionId) {
    const sidx = sessions.value.findIndex(s => s.id === tab.sessionId);
    if (sidx !== -1) {
      releaseSessionAttachments(sessions.value[sidx]);
      sessions.value.splice(sidx, 1);
    }
    delete sessionPromptTexts[tab.sessionId];
    delete todosBySession[tab.sessionId];
    delete todoRevisionsBySession[tab.sessionId];
    delete sessionScrollTops[tab.sessionId];
    DeleteSession(tab.sessionId).catch(() => {});
  }
  saveSessions();
  if (activeWorkspaceId.value === id) {
    const newIdx = Math.min(idx, workspaceTabs.value.length - 1);
    switchWorkspaceTab(workspaceTabs.value[newIdx].id);
  }
}

const sessionScrollTops = {};

function switchWorkspaceTab(id) {
  const tab = workspaceTabs.value.find((t) => t.id === id);
  if (!tab) return;
  // Save current session's scroll position before switching
  const prevTop = chatMessagesRef.value?.saveScrollPosition();
  if (activeSessionId.value && prevTop != null) {
    sessionScrollTops[activeSessionId.value] = prevTop;
  }
  saveSessions();
  activeWorkspaceId.value = id;
  config.workspace = tab.path;
  configDraft.workspace = tab.path;
  subRuns.value = [];
  loadPromptHistory(tab.path);
  refreshFiles(tab.path);
  refreshWorkspaceTokenUsage(tab.path);
  SaveConfig({ ...config })
    .then(() => refreshGitStatus())
    .catch(() => { gitStatus.value = { isRepo: false }; });
  // Switch to linked session
  if (tab.sessionId) {
    activeSessionId.value = tab.sessionId;
    loadTodos(tab.sessionId);
    // Restore saved scroll position for this session, or stay at default
    const savedTop = sessionScrollTops[tab.sessionId];
    if (savedTop != null) {
      nextTick(() => chatMessagesRef.value?.restoreScrollPosition(savedTop));
    }
  }
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

function defaultModelDraft(source = {}) {
  return {
    providerName: configDraft?.providerName || 'OpenAI Compatible',
    apiFormat: normalizeApiFormat(configDraft?.apiFormat),
    baseUrl: configDraft?.baseUrl || '',
    apiKey: configDraft?.apiKey || '',
    model: '',
    temperature: configDraft?.temperature ?? 0.2,
    maxTokens: configDraft?.maxTokens || 128000,
    contextWindow: configDraft?.contextWindow || 1048576,
    ...source,
  };
}

function normalizedProviderName(value) {
  return (value || '').trim() || 'OpenAI Compatible';
}

const providerTabs = computed(() => {
  const groups = new Map();
  (configDraft.models || []).forEach((model, index) => {
    const provider = normalizedProviderName(model.providerName);
    if (!groups.has(provider)) {
      groups.set(provider, { name: provider, label: provider, models: [] });
    }
    groups.get(provider).models.push({ model, index });
  });
  return Array.from(groups.values());
});

function alignActiveProviderTab(preferred = '') {
  const tabs = providerTabs.value;
  if (!tabs.length) {
    activeProviderTab.value = '';
    return;
  }
  const names = new Set(tabs.map((tab) => tab.name));
  const candidates = [
    preferred,
    activeProviderTab.value,
    normalizedProviderName(configDraft.providerName),
    tabs[0]?.name || '',
  ];
  activeProviderTab.value = candidates.find((name) => name && names.has(name)) || tabs[0].name;
}

function assignModelDraft(source = {}) {
  Object.assign(modelDraft, defaultModelDraft({
    ...source,
    apiFormat: normalizeApiFormat(source.apiFormat || configDraft.apiFormat),
  }));
}

function startAddModelDraft() {
  modelEditorIndex.value = -1;
  const provider = activeProviderTab.value || configDraft.providerName || 'OpenAI Compatible';
  assignModelDraft({
    providerName: provider,
    apiFormat: normalizeApiFormat(configDraft.apiFormat),
    baseUrl: configDraft.baseUrl || '',
    apiKey: configDraft.apiKey || '',
    maxTokens: configDraft.maxTokens || 128000,
    contextWindow: configDraft.contextWindow || 1048576,
  });
  modelEditorVisible.value = true;
}

function editModelDraft(index) {
  if (!configDraft.models || !configDraft.models[index]) return;
  modelEditorIndex.value = index;
  assignModelDraft(configDraft.models[index]);
  modelEditorVisible.value = true;
}

function cancelModelDraft() {
  modelEditorVisible.value = false;
  modelEditorIndex.value = -1;
}

function commitModelDraft() {
  if (!configDraft.models) configDraft.models = [];
  const model = (modelDraft.model || '').trim();
  if (!model) {
    message.warning('请填写 Model');
    return;
  }
  const providerName = normalizedProviderName(modelDraft.providerName);
  const apiFormat = normalizeApiFormat(modelDraft.apiFormat);
  const nextModel = {
    providerName,
    apiFormat,
    baseUrl: (modelDraft.baseUrl || '').trim(),
    apiKey: modelDraft.apiKey || '',
    model,
    temperature: modelDraft.temperature ?? configDraft.temperature ?? 0.2,
    maxTokens: modelDraft.maxTokens || configDraft.maxTokens || 128000,
    contextWindow: modelDraft.contextWindow || configDraft.contextWindow || 1048576,
  };
  const wasActive = modelEditorIndex.value >= 0 && isDraftModelActive(configDraft.models[modelEditorIndex.value]);
  if (modelEditorIndex.value >= 0) {
    configDraft.models.splice(modelEditorIndex.value, 1, nextModel);
  } else {
    configDraft.models.push(nextModel);
  }
  if (wasActive) {
    applyModelToDraft(nextModel);
  }
  alignActiveProviderTab(providerName);
  modelEditorVisible.value = false;
}

function applyModelToDraft(model) {
  if (!model) return;
  configDraft.providerName = normalizedProviderName(model.providerName);
  configDraft.apiFormat = normalizeApiFormat(model.apiFormat);
  configDraft.baseUrl = model.baseUrl || '';
  configDraft.apiKey = model.apiKey || '';
  configDraft.model = model.model || '';
  configDraft.temperature = model.temperature ?? configDraft.temperature ?? 0.2;
  configDraft.maxTokens = model.maxTokens || configDraft.maxTokens || 128000;
  configDraft.contextWindow = model.contextWindow || configDraft.contextWindow || 1048576;
  alignActiveProviderTab(normalizedProviderName(model.providerName));
}

function isDraftModelActive(model) {
  if (!model) return false;
  return normalizedProviderName(model.providerName) === normalizedProviderName(configDraft.providerName)
    && normalizeApiFormat(model.apiFormat) === normalizeApiFormat(configDraft.apiFormat)
    && (model.model || '') === (configDraft.model || '')
    && (model.baseUrl || '') === (configDraft.baseUrl || '');
}

function removeModelDraft(index) {
  if (!configDraft.models) return;
  const removed = configDraft.models[index];
  const removedProvider = normalizedProviderName(removed?.providerName);
  configDraft.models.splice(index, 1);
  if (modelEditorIndex.value === index) {
    cancelModelDraft();
  } else if (modelEditorIndex.value > index) {
    modelEditorIndex.value -= 1;
  }
  const activeStillExists = providerTabs.value.some((tab) => tab.name === activeProviderTab.value);
  alignActiveProviderTab(activeStillExists ? activeProviderTab.value : removedProvider);
}

watch(() => modelDraft.apiFormat, (next, previous) => {
  if (!modelEditorVisible.value) return;
  const nextFormat = normalizeApiFormat(next);
  const previousDefault = apiFormatDefaultBaseUrl(previous);
  const knownDefaults = new Set(apiFormatOptions.map((item) => apiFormatDefaultBaseUrl(item.value)));
  const currentBase = (modelDraft.baseUrl || '').trim();
  if (!currentBase || currentBase === previousDefault || knownDefaults.has(currentBase)) {
    modelDraft.baseUrl = apiFormatDefaultBaseUrl(nextFormat);
  }
  if (nextFormat === 'anthropic_messages') {
    if (!modelDraft.maxTokens || modelDraft.maxTokens > 64000) modelDraft.maxTokens = 8192;
  } else if (normalizeApiFormat(previous) === 'anthropic_messages' && modelDraft.maxTokens === 8192) {
    modelDraft.maxTokens = 128000;
  }
});

function newSession(title) {
  saveSessions();
  const id = crypto.randomUUID ? crypto.randomUUID() : `s-${Date.now()}-${Math.random()}`;
  const now = Date.now();
  const workspace = config.workspace || '';
  sessions.value.unshift({ id, title: title || '新会话', workspace, messages: [], runId: '', isRunning: false, grillMode: false, createdAt: now, updatedAt: now });
  activeSessionId.value = id;
  promptText.value = '';
  addWelcome(workspace);
  // Reset workspace token usage for new session
  const ws = workspace;
  if (ws) {
    ResetWorkspaceTokenUsage(ws);
    workspaceTokenUsage.value = { inputTokens: 0, outputTokens: 0 };
  }
}

function selectSession(index) {
  if (index < 0 || index >= sessions.value.length) return;
  const target = sessions.value[index];
  saveSessions();
  activeSessionId.value = target.id;
  applySessionWorkspace(target);
  promptText.value = '';
  activeRunId.value = target.runId || '';
  sessionsVisible.value = false;
  subRuns.value = [];
  scrollMessagesToBottom();
}

function createReplacementSession(title = '新会话', workspacePath = '') {
  const id = crypto.randomUUID ? crypto.randomUUID() : `s-${Date.now()}-${Math.random()}`;
  const now = Date.now();
  const session = { id, title, workspace: workspacePath || '', messages: [], runId: '', isRunning: false, grillMode: false, createdAt: now, updatedAt: now };
  session.messages.push(buildWelcomeMessage(workspacePath || '未选择'));
  return session;
}

function deleteSession(index) {
  if (index < 0 || index >= sessions.value.length) return;
  const target = sessions.value[index];
  if (!target) return;
  if (target.runId || target.isRunning) {
    message.warning('会话运行中，不能删除');
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
    replacement = createReplacementSession(tab?.label || '新会话', tab?.path || '');
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
    activeRunId.value = '';
    promptText.value = '';
    subRuns.value = [];
    loadTodos(replacementId);
    scrollMessagesToBottom();
  }

  delete todosBySession[deletedId];
  delete todoRevisionsBySession[deletedId];
  delete sessionPromptTexts[deletedId];
  delete sessionScrollTops[deletedId];
  DeleteSession(deletedId).catch(() => {});
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
  session.messages.push(buildWelcomeMessage(workspacePath || '未选择'));
}

async function init() {
  try {
    const loaded = await GetConfig();
    assignConfig(config, loaded);
    assignConfig(configDraft, loaded);
  } catch (err) {
    message.error(`读取配置失败：${err}`);
  }

  // Init workspace tabs from config
  const ws = config.workspace || '';
  const tab = createWorkspaceTab(ws);
  workspaceTabs.value.push(tab);
  activeWorkspaceId.value = tab.id;
  if (tab.sessionId) activeSessionId.value = tab.sessionId;
  if (ws) addToHistory(ws);
  loadPromptHistory(ws);
  refreshGitStatus();

  loadSavedSessions();
  loadMcpConfig();
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

function markSessionRunning(session) {
  if (!session) return;
  if (!session.workspace && config.workspace) session.workspace = config.workspace;
  session.isRunning = true;
}

function queueStreamDelta(data, field) {
  const runId = data?.runId || '';
  if (!runId) return;
  let buffer = streamBuffers.get(runId);
  if (!buffer) {
    buffer = { runId, content: '', reasoning: '' };
    streamBuffers.set(runId, buffer);
  }
  if (field === 'reasoning') buffer.reasoning += data.content || '';
  else buffer.content += data.content || '';
  scheduleStreamFlush();
}

function scheduleStreamFlush() {
  if (streamFlushScheduled) return;
  streamFlushScheduled = true;
  requestAnimationFrame(() => {
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
  if (session.id === activeSessionId.value) thinking.value = false;
  let last = session.messages[session.messages.length - 1];
  if (!last || last.role !== 'assistant' || last.error || last.system || last.done) {
    last = { role: 'assistant', content: '', reasoningBody: '', streaming: true };
    session.messages.push(last);
  }
  last.streaming = true;
  if (buffer.content) last.content += buffer.content;
  if (buffer.reasoning) {
    if (last.reasoningBody === undefined) last.reasoningBody = '';
    last.reasoningBody += buffer.reasoning;
  }
  if (session.id === activeSessionId.value) {
    scrollMessagesToBottom();
  }
}

function setLastAssistantRoundDuration(session, durationMs) {
  if (!session) return;
  const text = formatDurationShort(durationMs);
  if (!text) return;
  for (let i = session.messages.length - 1; i >= 0; i--) {
    const msg = session.messages[i];
    if (msg.role === 'assistant') {
      msg.roundDurationMs = Number(durationMs || 0);
      msg.roundDurationText = text;
      return;
    }
  }
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
  const off = EventsOn(eventName, handler);
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
    const sid = data.sessionId || '';
    const session = sessions.value.find(s => s.id === sid) || activeSession.value;
    if (session) {
      session.runId = data.runId;
      session.isRunning = true;
    }
    if (session && session.id === activeSessionId.value) {
      activeRunId.value = data.runId;
    }
  });

  onRuntimeEvent('run:llm_wait', (data) => {
    const session = sessionByRunId(data.runId);
    if (!session || session.id !== activeSessionId.value) return;
    thinking.value = true;
  });

  onRuntimeEvent('tokens:update', (data) => {
    if ((data.workspace || '') !== (config.workspace || '')) return;
    workspaceTokenUsage.value = {
      inputTokens: data.inputTokens || 0,
      outputTokens: data.outputTokens || 0,
    };
  });

  onRuntimeEvent('mcp:status', (data) => {
    mcpServers.value = data?.servers || [];
    refreshToolList();
    updateWelcomeMcpRows();
  });

  onRuntimeEvent('dependency:missing', (data) => {
    const tool = data?.tool || data?.name || 'dependency';
    if (missingDependencyWarningsShown.has(tool)) return;
    missingDependencyWarningsShown.add(tool);
    const steps = Array.isArray(data?.installSteps) ? data.installSteps : [];
    const detail = steps.length ? '\n\n' + steps.join('\n') : '';
    message.warning(`${data?.message || `${tool} 未安装`}${detail}`, { duration: 18000 });
  });

  onRuntimeEvent('run:delta', (data) => {
    queueStreamDelta(data, 'content');
  });
  onRuntimeEvent('run:reasoning', (data) => {
    queueStreamDelta(data, 'reasoning');
  });
  const applyToolProgressEvent = (data) => {
    flushStreamBuffer(data.runId);
    const session = sessionByRunId(data.runId);
    if (!session) return;
    if (session.id === activeSessionId.value) thinking.value = false;
    const title = makeToolTitle(data.name, data.args, data);
    updateToolEvent(toolEventId(data), data.name, title, data.args || '', 'running', data, session);
  };
  onRuntimeEvent('tool:start', applyToolProgressEvent);
  onRuntimeEvent('tool:update', applyToolProgressEvent);
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
      : `${existing.askQuestions.length} 个问题`;
    saveSessions();
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
      existing.body = '提问已取消';
    }
    saveSessions();
  });
  onRuntimeEvent('tool:result', (data) => {
    flushStreamBuffer(data.runId);
    const session = sessionByEvent(data);
    if (!session) return;
    const eventId = toolEventId(data);
    let existing = findToolEventMessage(session, data);
    if (!existing) existing = appendToolEventFallback(session, data, 'running');
    if (existing) {
      existing.eventId = eventId;
      existing.runId = data.runId || existing.runId || '';
      existing.toolBatchId = data.toolBatchId || existing.toolBatchId || '';
      existing.toolCallId = data.toolCallId || existing.toolCallId || '';
      if (data.toolCallIndex !== undefined && data.toolCallIndex !== null) existing.toolCallIndex = data.toolCallIndex;
      existing.status = 'success';
      existing.body = formatToolBody(data.name, data.result);
      existing.chip = formatToolChip(data.name, data.result);
      existing.durationMs = Number(data.durationMs || 0);
      existing.durationText = formatDurationShort(existing.durationMs);
      if (data.mcpServer) existing.mcpServer = data.mcpServer;
      if (data.mcpTool) existing.mcpTool = data.mcpTool;
      existing.time = new Date().toLocaleTimeString();
      const resultData = parseToolResultData(data.result);
      if (data.name === 'ask') {
        existing.askReady = false;
        existing.askSubmitting = false;
        existing.askSubmitted = true;
        existing.askAnswers = Array.isArray(resultData.answers) ? resultData.answers : existing.askAnswers || [];
      }
      if (!existing.title) existing.title = makeToolResultTitle(data.name, data.result, data);
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
            existing.readTokenCount = estimateTokenCount(d.content || d.output || '');
          }
        } catch (_) { /* ignore */ }
      }
      // Store file entries for batch_read (used in tree display)
      if (data.name === 'batch_read') {
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
                chip: f.error ? `failed: ${f.error}` : formatReadRangeChip(f.startLine || 1, f.endLine || f.totalLines || 0, f.totalLines || 0, !!f.truncated, estimateTokenCount(f.content || f.output || '')),
                tokenCount: estimateTokenCount(f.content || f.output || ''),
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
  });
  onRuntimeEvent('tool:error', (data) => {
    flushStreamBuffer(data.runId);
    const session = sessionByEvent(data);
    if (!session) return;
    const eventId = toolEventId(data);
    let existing = findToolEventMessage(session, data);
    if (!existing) existing = appendToolEventFallback(session, data, 'error');
    if (existing) {
      existing.eventId = eventId;
      existing.runId = data.runId || existing.runId || '';
      existing.toolBatchId = data.toolBatchId || existing.toolBatchId || '';
      existing.toolCallId = data.toolCallId || existing.toolCallId || '';
      if (data.toolCallIndex !== undefined && data.toolCallIndex !== null) existing.toolCallIndex = data.toolCallIndex;
      existing.status = 'error';
      existing.body = data.error || '';
      existing.errorCode = data.errorCode || '';
      if (data.name === 'ask') {
        existing.askReady = false;
        existing.askSubmitting = false;
        if (existing.errorCode === 'E_ASK_CANCELLED') existing.body = '提问已取消';
      }
      existing.durationMs = Number(data.durationMs || 0);
      existing.durationText = formatDurationShort(existing.durationMs);
      if (data.mcpServer) existing.mcpServer = data.mcpServer;
      if (data.mcpTool) existing.mcpTool = data.mcpTool;
      existing.time = new Date().toLocaleTimeString();
    }
  });
  onRuntimeEvent('run:done', (data) => {    flushStreamBuffer(data.runId);
    thinking.value = false;
	  refreshGitStatus();

    const sid = data.sessionId || '';
    const session = sessions.value.find(s => s.id === sid) || activeSession.value;
    if (!session) return;
    let i = session.messages.length - 1;
    while (i >= 0) {
      const msg = session.messages[i];
      if (msg.role === 'assistant' && msg.streaming) {
        msg.streaming = false;
        msg.done = true;
        // If the model only emitted reasoning content and no regular content,
        // promote reasoning to main content so it shows in the body.
        if (msg.reasoningBody && !msg.content) {
          msg.content = msg.reasoningBody;
          msg.reasoningBody = '';
        }
        break;
      }
      i--;
    }
    setLastAssistantRoundDuration(session, data.durationMs);
    session.runId = '';
    session.isRunning = false;
    if (session.id === activeSessionId.value) {
      activeRunId.value = '';
      playCompletionSound('done');
    }
    saveSessions();
    refreshContextTokens(sid);
    refreshWorkspaceTokenUsage(config.workspace || '');
  });
  onRuntimeEvent('run:error', (data) => {    flushStreamBuffer(data.runId);
    thinking.value = false;

    const sid = data.sessionId || '';
    const session = sessions.value.find(s => s.id === sid) || activeSession.value;
    if (!session) return;
    let i = session.messages.length - 1;
    while (i >= 0) {
      const msg = session.messages[i];
      if (msg.role === 'assistant' && msg.streaming) {
        msg.streaming = false;
        msg.done = true;
        break;
      }
      i--;
    }
    session.runId = '';
    session.isRunning = false;
    if (session.id === activeSessionId.value) {
      activeRunId.value = '';
      const err = data.error || 'unknown error';
      const cancelled = err === '已取消' || String(err).toLowerCase().includes('context canceled');
      session.messages.push({ role: 'assistant', content: cancelled ? '已取消。' : '运行失败：' + err, error: !cancelled, system: cancelled });
      setLastAssistantRoundDuration(session, data.durationMs);
      playCompletionSound(cancelled ? 'cancelled' : 'error');
    } else {
      setLastAssistantRoundDuration(session, data.durationMs);
    }
    saveSessions();
  });
  onRuntimeEvent('run:cancelled', (data) => {    flushStreamBuffer(data.runId);
    thinking.value = false;

    const sid = data.sessionId || '';
    const session = sessions.value.find(s => s.id === sid) || activeSession.value;
    if (!session) return;
    let i = session.messages.length - 1;
    while (i >= 0) {
      const msg = session.messages[i];
      if (msg.role === 'assistant' && msg.streaming) {
        msg.streaming = false;
        msg.done = true;
        break;
      }
      i--;
    }
    setLastAssistantRoundDuration(session, data.durationMs);
    session.runId = '';
    session.isRunning = false;
    if (session.id === activeSessionId.value) {
      activeRunId.value = '';
      playCompletionSound('cancelled');
    }
    saveSessions();
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
  for (const eventName of ['scheduled:update', 'scheduled:run_start', 'scheduled:run_done', 'scheduled:run_error']) {
    onRuntimeEvent(eventName, (data) => applyScheduledTaskEvent(data));
  }

  // ── Sub-agent events ──

  function findSubagentMsg(id, sessionId = '') {
    const session = sessionId ? sessions.value.find(s => s.id === sessionId) : activeSession.value;
    if (!session) return null;
    return session.messages.find(m => m.kind === 'subagent' && m.eventId === id) || null;
  }

  onRuntimeEvent('sub:spawn', (data) => {
    const session = sessionByEvent(data);
    const isActiveSession = session && session.id === activeSessionId.value;
    if (session) {
      session.messages = session.messages.filter(m => !(m.role === 'tool_call' && m.name === 'agent_delegate'));
    }
    // Sidebar tracking
    if (isActiveSession) {
      subRuns.value.push({
        id: data.id,
        description: data.description || '',
        profile: data.profile || 'coder',
        status: 'running',
        steps: 0,
        maxSteps: data.maxSteps || 5,
        summary: '',
        filesRead: [],
        filesEdited: [],
        error: '',
        toolCalls: [],
        startTime: Date.now(),
        durationMs: 0,
        durationText: '',
      });
    }
    // Main chat message
    if (session) {
      session.messages.push({
        role: 'tool_call',
        kind: 'subagent',
        eventId: data.id,
        status: 'running',
        description: data.description || '',
        profile: data.profile || 'coder',
        steps: 0,
        maxSteps: data.maxSteps || 5,
        summary: '',
        filesEdited: [],
        error: '',
        toolCalls: [],
        time: new Date().toLocaleTimeString(),
        durationMs: 0,
        durationText: '',
      });
    }
  });
  onRuntimeEvent('sub:step', (data) => {
    const r = subRuns.value.find(s => s.id === data.id);
    if (r) r.steps = data.step || 0;
    const msg = findSubagentMsg(data.id, data.sessionId || '');
    if (msg) msg.steps = data.step || 0;
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
    last = { role: 'assistant', content: '', reasoningBody: '', streaming: true };
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
    last = { role: 'assistant', content: '', reasoningBody: '', streaming: true };
    session.messages.push(last);
  }
  last.streaming = true;
  if (last.reasoningBody === undefined) last.reasoningBody = '';
  last.reasoningBody += content;
  scrollMessagesToBottom();
}

function scrollMessagesToBottom(options = {}) {
  nextTick(() => chatMessagesRef.value?.scrollToBottom(options));
}

function jumpToUserQuestion(direction) {
  nextTick(() => chatMessagesRef.value?.scrollToUserQuestion(direction));
}

function focusTool(eventId) {
  focusedToolId.value = eventId;
}

function clearFocus() {
  focusedToolId.value = '';
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
      message.warning(`单次最多添加 ${MAX_ATTACHMENTS_PER_MESSAGE} 个文件`);
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
      base.dataUrl = await readFileAsDataUrl(file);
    } else if (kind === 'image' && file.size > MAX_IMAGE_INPUT_BYTES) {
      base.truncated = true;
      base.error = '图片超过可发送上限，仅保留预览和元信息';
    }
    if (isTextAttachment(file) && file.size <= MAX_TEXT_ATTACHMENT_BYTES) {
      base.kind = kind === 'file' ? 'text' : kind;
      base.text = await readFileAsText(file);
    } else if (isTextAttachment(file) && file.size > MAX_TEXT_ATTACHMENT_BYTES) {
      base.kind = kind === 'file' ? 'text' : kind;
      base.truncated = true;
      base.error = '文本文件超过可发送上限，仅保留元信息';
    }
  } catch (err) {
    base.error = String(err?.message || err || '文件读取失败');
  }
  return base;
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

function readFileAsDataUrl(file) {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve(String(reader.result || ''));
    reader.onerror = () => reject(reader.error || new Error('读取文件失败'));
    reader.readAsDataURL(file);
  });
}

function readFileAsText(file) {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve(String(reader.result || ''));
    reader.onerror = () => reject(reader.error || new Error('读取文件失败'));
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
  if (attachments.length === 1) return `附件：${attachments[0].name}`;
  return `附件：${attachments[0].name} 等 ${attachments.length} 个文件`;
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
  if (!msg || msg.system || msg.error || msg.welcome || msg.role === 'archive' || msg.role === 'tool_call') return false;
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
  try { await WindowMinimise(); } catch (_) {}
}

async function refreshWindowMaximisedState() {
  try {
    isMaximised.value = await WindowIsMaximised();
  } catch (_) { /* ignore runtime state read failures */ }
}

async function toggleMaximiseWindow() {
  try {
    const maximised = await WindowIsMaximised();
    if (maximised) {
      await WindowUnmaximise();
    } else {
      await WindowMaximise();
    }
    await refreshWindowMaximisedState();
  } catch (_) {}
}

async function closeWindow() {
  try { await Quit(); } catch (_) {}
}

async function switchToModel(index) {
  try {
    await SwitchModel(index);
    const loaded = await GetConfig();
    assignConfig(config, loaded);
    message.success(`已切换到 ${loaded.model}`);
  } catch (err) {
    message.error(`切换模型失败：${err}`);
  }
}

const mcpConfigParseResult = computed(() => parseMcpConfigText(mcpConfigText.value));
const mcpConfigValid = computed(() => mcpConfigParseResult.value.valid);
const mcpConfigValidationText = computed(() => {
  const result = mcpConfigParseResult.value;
  if (!result.valid) return `JSON 格式错误：${result.error}`;
  const servers = Object.keys(result.config.mcpServers || {}).length;
  return `JSON 格式正确 · ${servers} 个服务配置`;
});

function parseMcpConfigText(text) {
  try {
    const parsed = JSON.parse(text || '{"mcpServers":{}}');
    if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) {
      return { valid: false, error: '根节点必须是对象', config: { mcpServers: {} } };
    }
    if (parsed.mcpServers === undefined) parsed.mcpServers = {};
    if (!parsed.mcpServers || typeof parsed.mcpServers !== 'object' || Array.isArray(parsed.mcpServers)) {
      return { valid: false, error: 'mcpServers 必须是对象', config: { mcpServers: {} } };
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
    message.error(`读取 MCP 配置失败：${err}`);
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
    message.success('MCP 配置已保存并重连');
  } catch (err) {
    message.error(`保存 MCP 配置失败：${err}`);
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

function estimateTokenCount(text) {
  if (!text) return 0;
  let ascii = 0, nonAscii = 0;
  for (let i = 0; i < text.length; i++) {
    if (text.charCodeAt(i) <= 127) ascii++;
    else nonAscii++;
  }
  return Math.max(1, Math.ceil(ascii / 4) + nonAscii);
}

function formatTokenCount(tokens) {
  const n = Number(tokens) || 0;
  if (n >= 1000000) return (n / 1000000).toFixed(1) + 'M';
  if (n >= 1000) return (n / 1000).toFixed(1) + 'k';
  return String(n);
}

function formatReadChip(lines, tokens = 0) {
  const lineCount = Number(lines) || 0;
  const parts = [];
  if (lineCount > 0) parts.push(lineCount + ' line' + (lineCount !== 1 ? 's' : ''));
  if (tokens > 0) parts.push('~' + formatTokenCount(tokens) + 't');
  return parts.length ? '\u00B7 ' + parts.join(' \u00B7 ') : '';
}

function formatReadRangeChip(startLine, endLine, totalLines, truncated, tokens = 0) {
  const start = Number(startLine) || 1;
  const end = Number(endLine) || Number(totalLines) || 0;
  const total = Number(totalLines) || 0;
  const tk = Number(tokens) || 0;
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
  if (tk > 0) parts.push(`~${formatTokenCount(tk)}t`);
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
      const builtinPrefixes = ['new','plan','goal','skills','clear','switch','sessions','reload','init','note','remember','compact'];
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

  if (!config.apiKey) {
    settingsPage.value = 'models';
    configVisible.value = true;
    message.warning('请先填写 API Key');
    return;
  }
  if (!session) return;
  if (config.workspace) session.workspace = config.workspace;
  const userMessage = { role: 'user', content: displayText, attachments, done: true };
  session.messages.push(userMessage);
  if (session.title === '默认会话' || session.title.startsWith('会话')) {
    session.title = displayText.length > 20 ? `${displayText.slice(0, 20)}…` : displayText;
  }
  // Save to workspace-scoped prompt history
  addPromptHistory(displayText);
  commandHistoryIndex.value = -1;
  promptText.value = '';
  pendingAttachments.value = [];
  commandMenuVisible.value = false;
  scrollMessagesToBottom();

  try {
    const history = buildSessionMessagesForModel(session, {
      message: userMessage,
      content: sendText,
      attachments: attachmentsForModel(attachments),
    });
    markSessionRunning(session);
    await StartChat({ sessionId: session.id, message: sendText, messages: history, grillMode: !!session.grillMode, config: { ...config } });
  } catch (err) {
    session.runId = '';
    session.isRunning = false;
    if (session.id === activeSessionId.value) {
      activeRunId.value = '';
    }
    pushMessage('assistant', '启动失败：' + err, { error: true });
  }
}

async function stopRun() {
  const session = activeSession.value;
  if (!session || !session.runId) return;
  try {
    await CancelRun(session.runId);
  } catch (err) {
    message.error(`终止失败：${err}`);
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
    await refreshFiles(workspace);
    return workspace;
  } catch (err) {
    message.error(`选择工作区失败：${err}`);
    return null;
  }
}

async function refreshFiles(path = '') {
  currentFileDir.value = path || '';
  try {
    files.value = await ListFiles({ path, maxDepth: 3, limit: 300, includeHidden: false, includeIgnored: false });
  } catch (err) {
    files.value = [];
  }
}

async function previewFile(path) {
  previewTitle.value = path;
  filePreview.value = '读取中...';
  try {
    const result = await ReadFile({ path, startLine: 1, lineCount: 220 });
    currentPreview.value = result.content || '';
    filePreview.value = `${result.content || ''}\n\n---\nmd5: ${result.md5}\nlines: ${result.totalLines}\nending: ${result.lineEnding}${result.truncated ? '\n(truncated)' : ''}`;
  } catch (err) {
    currentPreview.value = '';
    filePreview.value = `读取失败：${err}`;
  }
}

async function copyPreview() {
  if (!currentPreview.value) return;
  await navigator.clipboard.writeText(currentPreview.value);
  message.success('已复制');
}

async function onSettingsSave(draftData) {
  assignConfig(config, draftData);
  assignConfig(configDraft, draftData);
  try {
    await SaveConfig({ ...configDraft });
    syncConfigToActiveTab();
    message.success('配置已保存');
  } catch (err) {
    message.error(`保存失败：${err}`);
  }
}

function onSkillsChanged() {
  refreshSkillState();
}

function onMcpSaved() {
  loadMcpConfig();
}


async function reloadConfigFromFile() {
  try {
    const loaded = await ReloadConfig();
    assignConfig(config, loaded);
    assignConfig(configDraft, loaded);
    syncConfigToActiveTab();
    message.success(`已重新加载配置：${loaded.model || '-'}`);
  } catch (err) {
    message.error(`重新加载配置失败：${err}`);
  }
}

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
    commandMenuVisible.value = true;
    selectedCommandIndex.value = 0;
    commandHistoryIndex.value = -1;
    sessionsVisible.value = false;
  } else {
    commandMenuVisible.value = false;
  }
}

function handlePromptKeydown(event) {
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
    commandMenuVisible.value = true;
    selectedCommandIndex.value = 0;
    return;
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
    promptText.value = '请帮我设定一个目标模式：';
    commandMenuVisible.value = false;
    nextTick(() => promptInputRef.value?.focus());
    return true;
  }
  if (command.special === 'skills') {
    await loadAndShowSkills();
    promptText.value = '';
    commandMenuVisible.value = false;
    return true;
  }
  if (command.special === 'clear_skills') {
    await clearLoadedSkills();
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
  if (command.special === 'reload') {
    await reloadConfigFromFile();
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
  nextTick(() => promptInputRef.value?.focus());
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
    expanded: false,
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

function makeToolResultTitle(name, result, meta = {}) {
  const d = parseToolResultData(result);
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
  if (meta.mcpTool) return meta.mcpTool;
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
  const existing = session.messages.find((item) => item.role === 'tool_call' && item.eventId === eventId);

  // Rich display data
  let editOldString = '';
  let editNewString = '';
  let editFilePath = '';
  let editStats = '';
  let editAdded = 0;
  let editRemoved = 0;
	let editEntries = existing?.editEntries || [];
  let codeContent = '';
  let chip = existing?.chip || '';
  let waitSeconds = Number(existing?.waitSeconds || 0);
  let waitStartedAt = Number(existing?.waitStartedAt || 0);
  let askQuestions = existing?.askQuestions || [];
  const expanded = existing ? existing.expanded : false;

  const raw = String(body || '');
  const parsed = parseToolArgsBestEffort(raw);
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
	  chip = `${files.length} file${files.length !== 1 ? 's' : ''} · ${changes.length} change${changes.length !== 1 ? 's' : ''}`;
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
  }

  const payload = {
    role: 'tool_call',
    eventId,
    runId: meta.runId || existing?.runId || '',
    toolBatchId: meta.toolBatchId || existing?.toolBatchId || '',
    toolCallId: meta.toolCallId || existing?.toolCallId || '',
    toolCallIndex: meta.toolCallIndex ?? existing?.toolCallIndex,
    name: name || 'tool',
    title,
    body: formatToolBody(name, displayToolBodyForStatus(name, body, status)),
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
    waitSeconds,
    waitStartedAt,
    askId: existing?.askId || '',
    askQuestions,
    askReady: existing?.askReady || false,
    askSubmitting: existing?.askSubmitting || false,
    askSubmitted: existing?.askSubmitted || false,
    askAnswers: existing?.askAnswers || [],
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
    if (!payload.waitSeconds && existing.waitSeconds) payload.waitSeconds = existing.waitSeconds;
    if (!payload.waitStartedAt && existing.waitStartedAt) payload.waitStartedAt = existing.waitStartedAt;
    if ((!payload.askQuestions || payload.askQuestions.length === 0) && existing.askQuestions) payload.askQuestions = existing.askQuestions;
    Object.assign(existing, payload);
  } else {
    session.messages.push(payload);
  }
  if (session.id === activeSessionId.value) scrollMessagesToBottom();
}

function toggleToolExpand(msg) {
  msg.expanded = !msg.expanded;
}

async function submitAskResponse(msg, answers) {
  if (!msg?.askId || msg.askSubmitting || msg.askSubmitted) return;
  msg.askSubmitting = true;
  try {
    await SubmitAskResponse({
      askId: msg.askId,
      sessionId: activeSessionId.value,
      answers,
    });
    msg.askSubmitted = true;
    msg.askReady = false;
    saveSessions();
  } catch (err) {
    msg.askSubmitting = false;
    message.error(`提交回答失败：${err}`);
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
    message.error(`加载定时任务失败：${err}`);
  } finally {
    scheduledTasksLoading.value = false;
  }
}

async function openScheduledTasks() {
  scheduledTasksVisible.value = true;
  await loadScheduledTasks();
}

async function deleteScheduledTask(id) {
  if (!id || scheduledTaskDeletingIds.value.includes(id)) return;
  scheduledTaskDeletingIds.value = [...scheduledTaskDeletingIds.value, id];
  try {
    await DeleteScheduledTask(id);
    scheduledTasks.value = scheduledTasks.value.filter((task) => task.id !== id);
    message.success('定时任务已删除');
  } catch (err) {
    message.error(`删除定时任务失败：${err}`);
  } finally {
    scheduledTaskDeletingIds.value = scheduledTaskDeletingIds.value.filter((item) => item !== id);
  }
}

// ── Session persistence ──

const SESSIONS_STORAGE_KEY = 'ally_sessions';
const MAX_STORED_SESSIONS = 20;
const MAX_STORED_MESSAGES = 120;
const MAX_STORED_CONVERSATION_MESSAGES = 160;
const MAX_MODEL_HISTORY_MESSAGES = 400;
const MAX_RUNTIME_SESSIONS = 30;
const MAX_RUNTIME_RENDERABLE_MESSAGES = 260;
const MAX_STORED_MESSAGE_CHARS = 60000;
const MAX_STORED_TOOL_BODY_CHARS = 20000;
const MAX_STORED_SESSION_CHARS = 240000;

function saveSessions() {
  try {
    trimRuntimeSessions();
    const data = sessions.value.slice(0, MAX_STORED_SESSIONS).map(s => ({
      id: s.id,
      title: s.title,
      workspace: inferSessionWorkspace(s) || (s.id === activeSessionId.value ? config.workspace || '' : ''),
      messages: sanitizeStoredMessages(s.messages || []),
      isRunning: s.isRunning || false,
      grillMode: !!s.grillMode,
      createdAt: s.createdAt || Date.now(),
      updatedAt: s.updatedAt || s.createdAt || Date.now(),
    }));
    localStorage.setItem(SESSIONS_STORAGE_KEY, JSON.stringify(data));
  } catch (_) { /* quota exceeded */ }
}

function trimRuntimeSessions() {
  for (const session of sessions.value) trimRuntimeSessionMessages(session);
  if (sessions.value.length <= MAX_RUNTIME_SESSIONS) return;

  const protectedIds = new Set([
    activeSessionId.value,
    ...workspaceTabs.value.map((tab) => tab.sessionId || ''),
    ...sessions.value.filter((session) => session.runId || session.isRunning).map((session) => session.id),
  ]);
  for (let index = sessions.value.length - 1; index >= 0 && sessions.value.length > MAX_RUNTIME_SESSIONS; index--) {
    const session = sessions.value[index];
    if (protectedIds.has(session.id)) continue;
    releaseSessionAttachments(session);
    delete todosBySession[session.id];
    delete todoRevisionsBySession[session.id];
    delete sessionPromptTexts[session.id];
    delete sessionScrollTops[session.id];
    ReleaseSession(session.id).catch(() => {});
    const expanded = new Set(expandedArchiveSessions.value);
    expanded.delete(session.id);
    expandedArchiveSessions.value = expanded;
    sessions.value.splice(index, 1);
  }
}

function trimRuntimeSessionMessages(session) {
  if (!session || session.runId || session.isRunning) return;
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
  const recentRenderable = messages.filter(isRenderableMessage).slice(-MAX_STORED_MESSAGES);
  const recentConversation = messages.filter(isModelHistoryMessage).slice(-MAX_STORED_CONVERSATION_MESSAGES);
  const keep = new Set([...recentRenderable, ...recentConversation]);
  const candidates = messages.filter((msg) => keep.has(msg));
  const stored = [];
  let storedChars = 0;
  for (let index = candidates.length - 1; index >= 0; index--) {
    const next = sanitizeStoredMessage(candidates[index]);
    const chars = estimateStoredMessageChars(next);
    if (stored.length > 0 && storedChars + chars > MAX_STORED_SESSION_CHARS) continue;
    stored.push(next);
    storedChars += chars;
  }
  return stored.reverse();
}

function sanitizeStoredMessage(msg) {
  const next = { ...msg };
  next.content = truncateStoredText(next.content, MAX_STORED_MESSAGE_CHARS, '[内容过长，已裁剪本地缓存]');
  next.reasoningBody = truncateStoredText(next.reasoningBody, MAX_STORED_MESSAGE_CHARS, '[思考内容过长，已裁剪本地缓存]');
  next.body = truncateStoredText(next.body, MAX_STORED_TOOL_BODY_CHARS, '[工具输出过长，已裁剪本地缓存]');
  next.codeContent = truncateStoredText(next.codeContent, MAX_STORED_TOOL_BODY_CHARS, '[文件预览过长，已裁剪本地缓存]');
  next.editDiff = truncateStoredText(next.editDiff, MAX_STORED_TOOL_BODY_CHARS, '[Diff 过长，已裁剪本地缓存]');
  next.editOldString = '';
  next.editNewString = '';
  if (Array.isArray(next.editEntries)) {
    next.editEntries = next.editEntries.map((entry) => ({
      ...entry,
      changes: [],
      diff: truncateStoredText(entry?.diff, MAX_STORED_TOOL_BODY_CHARS, '[Diff 过长，已裁剪本地缓存]'),
    }));
  }
  if (Array.isArray(next.attachments)) {
    next.attachments = next.attachments.map((att) => {
      const keepPreview = typeof att.previewUrl === 'string'
        && att.previewUrl.startsWith('data:')
        && att.previewUrl.length <= 500000;
      const text = truncateStoredText(att.text, MAX_STORED_ATTACHMENT_TEXT_CHARS, '[附件文本过长，已裁剪本地缓存]');
      return { ...att, previewUrl: keepPreview ? att.previewUrl : '', dataUrl: '', text };
    });
  }
  return next;
}

function truncateStoredText(value, limit, marker) {
  if (typeof value !== 'string' || value.length <= limit) return value || '';
  return `${value.slice(0, limit)}\n\n${marker}`;
}

function estimateStoredMessageChars(msg) {
  let chars = 200;
  for (const key of ['content', 'reasoningBody', 'body', 'codeContent', 'editDiff']) {
    chars += String(msg?.[key] || '').length;
  }
  for (const entry of Array.isArray(msg?.editEntries) ? msg.editEntries : []) chars += String(entry?.diff || '').length + 100;
  for (const att of Array.isArray(msg?.attachments) ? msg.attachments : []) {
    chars += String(att?.previewUrl || '').length + String(att?.text || '').length + 200;
  }
  return chars;
}

function loadSavedSessions() {
  try {
    const raw = localStorage.getItem(SESSIONS_STORAGE_KEY);
    if (!raw) return;
    const saved = JSON.parse(raw);
    if (!Array.isArray(saved)) return;
    for (const s of saved) {
      const existing = sessions.value.find(x => x.id === s.id);
      if (!existing) {
        const restored = {
          id: s.id,
          title: s.title || '历史会话',
          workspace: s.workspace || '',
          messages: s.messages || [],
          runId: '',
          isRunning: false,
          grillMode: !!s.grillMode,
          createdAt: s.createdAt || Date.now(),
          updatedAt: s.updatedAt || s.createdAt || Date.now(),
        };
        if (!restored.workspace) restored.workspace = inferSessionWorkspace(restored);
        sessions.value.push(restored);
      }
    }
  } catch (_) { /* ignore */ }
}

function showSessionList() {
  saveSessions();
  promptText.value = '';
  sessionsVisible.value = true;
  sessionsSelectedIndex.value = 0;
  commandMenuVisible.value = false;
  nextTick(() => promptInputRef.value?.focus());
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
  activeSessionId.value = target.id;
  applySessionWorkspace(target);
  promptText.value = '';
  if (target.runId) {
    activeRunId.value = target.runId;
  } else {
    activeRunId.value = '';
  }
  scrollMessagesToBottom();
}



function handleInitCommand() {
  const session = activeSession.value;
  if (!session) return;
  // Send the init exploration prompt to the LLM
  session.messages.push({ role: 'user', content: INIT_PROMPT, done: true });
  if (session.title === '默认会话' || session.title.startsWith('会话')) {
    session.title = '/init - 项目分析';
  }
  scrollMessagesToBottom();
  saveSessions();

  const history = session.messages
    .filter(isModelHistoryMessage)
    .map((msg) => ({ role: msg.role, content: msg.content }));

  const text = INIT_PROMPT;
  markSessionRunning(session);
  StartChat({ sessionId: session.id, message: text, messages: history, grillMode: !!session.grillMode, config: { ...config } })
    .catch((err) => {
      session.runId = '';
      session.isRunning = false;
      pushMessage('assistant', '初始化失败：' + err, { error: true });
    });
}

function handleRememberCommand() {
  const session = activeSession.value;
  if (!session) return;
  const history = session.messages
    .filter(isModelHistoryMessage)
    .map((msg) => ({ role: msg.role, content: msg.content }));
  history.push({ role: 'user', content: REMEMBER_PROMPT });

  session.messages.push({ role: 'user', content: '/note 保存长期记忆', done: true });
  if (session.title === '默认会话' || session.title.startsWith('会话')) {
    session.title = '/note - 保存长期记忆';
  }
  scrollMessagesToBottom();
  saveSessions();

  markSessionRunning(session);
  StartChat({ sessionId: session.id, message: '', messages: history, grillMode: !!session.grillMode, config: { ...config } })
    .catch((err) => {
      session.runId = '';
      session.isRunning = false;
      pushMessage('assistant', '保存项目知识失败：' + err, { error: true });
    });
}

async function handleCompactCommand() {
  const session = activeSession.value;
  if (!session) return;
  if (session.runId) { message.warning('请先等待当前任务完成'); return; }

  pushMessage('system', '## 正在压缩对话上下文...\n\n正在生成摘要并替换历史消息，请稍候。', { system: true });
  saveSessions();

  try {
    const result = await CompactSession(session.id, '');
    const tBefore = result.tokensBefore || 0;
    const tAfter = result.tokensAfter || 0;
    const saved = tBefore - tAfter > 0 ? ` (节省 ${fmtK(tBefore - tAfter)} tokens)` : '';

    // Replace messages with the compacted summary
    session.messages = [
      {
        role: 'assistant',
        content: `## 对话已压缩${saved}\n\n<details><summary>摘要预览</summary>\n\n${result.summary || ''}\n\n</details>\n\n上下文已从 ${fmtK(tBefore)} tokens 压缩至 ${fmtK(tAfter)} tokens。可以继续对话。`,
        system: true,
      },
    ];

    // Refresh context
    refreshContextTokens(session.id);
    scrollMessagesToBottom();
    message.success(`压缩完成：${fmtK(tBefore)} → ${fmtK(tAfter)} tokens`);
  } catch (err) {
    pushMessage('assistant', '压缩失败：' + (err?.message || err), { error: true });
  }
}

function createNewSession() {
  newSession('新会话 ' + (sessions.value.length + 1));
  message.success('已创建新会话');
  scrollMessagesToBottom();
}

async function loadAndShowSkills() {
  try {
    await refreshSkillState();
    const skills = availableSkills.value;
    if (skills && skills.length > 0) {
      let table = '| 命令 | 描述 | 来源 |\n|------|------|------|\n';
      for (const s of skills) {
        const status = isSkillActive(s.name) ? '已启用' : '已停用';
        table += `| \`/${s.name}\` | ${s.description || '-'} | ${s.source || '-'} · ${status} |\n`;
      }
      pushMessage('assistant', `## 可用技能（${skills.length}）\n\n${table}\n\n输入 /skillname 加载对应技能。`, { system: true });
    } else {
      pushMessage('assistant', '当前没有找到可用技能。技能文件存放在 `.agents/skills/` 目录下。', { system: true });
    }
  } catch (err) {
    message.error(`读取技能列表失败：${err}`);
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
    message.error(`读取技能状态失败：${err}`);
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
        session.messages.push({ role: 'user', content: xmlBlock, done: true, system: true });
        scrollMessagesToBottom();
        if (config.apiKey) {
          markSessionRunning(session);
          await StartChat({ sessionId: session.id, message: '', messages: [{ role: 'user', content: xmlBlock }], grillMode: !!session.grillMode, config: { ...config } }).catch(() => {
            session.isRunning = false;
          });
        }
      }
    }
    if (!alreadyActive) {
      message.success(`技能 "${skillName}" 已启用`);
    } else if (alreadyActive) {
      message.info(injectIntoChat ? `技能 "${skillName}" 已加载` : `技能 "${skillName}" 已启用`);
    }
    await refreshSkillState();
  } catch (err) {
    message.error(`启用技能失败：${err}`);
  }
  scrollMessagesToBottom();
}

async function deactivateSkillByName(skillName) {
  try {
    await DeactivateSkill(skillName);
    await refreshSkillState();
    message.success(`技能 "${skillName}" 已停用`);
  } catch (err) {
    message.error(`停用技能失败：${err}`);
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

async function clearLoadedSkills(announce = true) {
  try {
    await ClearSkills();
    // Refresh skill list to update command menu
    try { await refreshSkillState(); } catch (_) { /* ignore */ }
    if (announce) {
      pushMessage('assistant', '所有技能已停用。', { system: true });
    }
    message.success('技能已停用');
  } catch (err) {
    message.error(`停用技能失败：${err}`);
  }
  if (announce) {
    scrollMessagesToBottom();
  }
}

async function setRunMode(mode) {
  const session = activeSession.value;
  if (!session) return;
  if (session.runId || session.isRunning) {
    message.warning('请等待当前任务完成后再切换模式');
    return;
  }
  const nextPlan = mode === 'plan';
  const nextGrill = mode === 'grill';
  try {
    if (planModeActive.value !== nextPlan) {
      await SetPlanMode(nextPlan);
      planModeActive.value = nextPlan;
      await refreshToolList();
    }
    session.grillMode = nextGrill;
    session.updatedAt = Date.now();
    saveSessions();
    refreshContextTokens(activeSessionId.value);
    message.success(`已切换到 ${String(mode || 'yolo').toUpperCase()} 模式`);
  } catch (err) {
    message.error(`切换模式失败：${err}`);
  }
}

async function initPlanMode() {
  try {
    const active = await GetPlanMode();
    planModeActive.value = active;
  } catch (_) { /* ignore */ }
}

// Goal mode helpers
function trackGoal(objective) {
  pushMessage('user', `## Goal\n\n${objective}\n\n请确认任务是否达成，并在完成后告诉我。`, { system: false });
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
  if (name === 'read_file' || name === 'remote_read_file' || name === 'list_files' || name === 'remote_list_files' || name === 'batch_read' || name === 'document_read') return 'read';
  if (name === 'Glob') return 'glob';
  if (name === 'grep_files') return 'grep';
  if (name === 'run') return 'run';
  if (name === 'todo_write') return 'todo';
  if (name === 'scheduled_task') return 'scheduled';
  if (name === 'memory_read' || name === 'memory_write') return 'memory';
  if (name === 'agent_delegate') return 'other';

  return 'other';
}

function isMcpToolName(name) {
  return typeof name === 'string' && name.startsWith('mcp__');
}

function fallbackMcpToolName(name) {
  if (!isMcpToolName(name)) return '';
  const parts = String(name).split('__');
  return parts.length >= 3 ? parts.slice(2).join('__') : '';
}

function makeToolTitle(name, args, meta = {}) {
  if (isMcpToolName(name)) {
    return meta.mcpTool || fallbackMcpToolName(name);
  }
  const parsed = parseToolArgsBestEffort(args);
  if (name === 'run_command' || name === 'remote_run_command' || name === 'Bash') {
    const command = parsed.command || parsed.cmd || '';
    if (name === 'remote_run_command' && parsed.target) return `${parsed.target} · ${command}`;
    return command;
  }
  if (name === 'background_process') {
    if (parsed.action === 'stop') return `stop · ${parsed.id || ''}`;
    const parts = ['start'];
    if (parsed.name) parts.push(parsed.name);
    if (parsed.command) parts.push(parsed.command);
    if (parsed.cwd) parts.push(`cwd: ${parsed.cwd}`);
    if (parsed.port) parts.push(`port: ${parsed.port}`);
    return parts.join(' · ');
  }
  if (name === 'wait') {
    return parsed.reason || (parsed.seconds ? `${parsed.seconds}s` : '');
  }
  if (name === 'ask') {
    const questions = Array.isArray(parsed.questions) ? parsed.questions : [];
    if (questions.length === 1) return questions[0]?.question || '';
    return questions.length ? `${questions.length} 个问题` : '';
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
    return parsed.pattern || '';
  }
  if (name === 'Glob') {
    return parsed.pattern || '';
  }
  if (name === 'list_files' || name === 'remote_list_files') {
    if (parsed.target) return `${parsed.target}${parsed.path ? ' · ' + parsed.path : ''}`;
    return parsed.path || parsed.pattern || '';
  }
  if (name === 'batch_read') {
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
        const tokenCount = estimateTokenCount(d.content || d.output || '');
        const isPartial = startLine > 1 || endLine < (d.totalLines || 0);
        if (isPartial && d.totalLines > linesReturned) {
          return '\u00B7 lines ' + startLine + '-' + endLine + ' (of ' + d.totalLines + ')' + (tokenCount > 0 ? ' \u00B7 ~' + formatTokenCount(tokenCount) + 't' : '');
        }
        return formatReadChip(linesReturned, tokenCount);
      }
    }
    // batch_read: list each file as separate line
    if (name === 'batch_read' && parsed.data) {
      if (!parsed.data.files || !Array.isArray(parsed.data.files)) return '';
      const lines = parsed.data.files.map(f => {
        const path = f.path || '';
        const total = f.totalLines || 0;
        if (f.error) return path + ' \u00B7 failed';
        if (f.kind === 'document' || f.contentFormat === 'plain') {
          return path + ' \u00B7 ' + formatCharCount(String(f.text || f.content || '').length);
        }
        return path + ' ' + formatReadChip(total, estimateTokenCount(f.content || f.output || ''));
      });
      return '\u00B7 ' + lines.join('  ');
    }
    // grep_files: · N occurrences / lines
    if (name === 'grep_files' && parsed.data) {
      const count = parsed.data.count || parsed.data.matches?.length || 0;
      const occurrences = parsed.data.occurrences || 0;
      const ms = parsed.data.durationMs || 0;
      let chip = '';
      if (occurrences > 0) {
        chip = '\u00B7 ' + occurrences + ' occurrence' + (occurrences > 1 ? 's' : '');
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
      return '\u00B7 ' + (parsed.data.text || parsed.data.value);
    }
    if (name === 'scheduled_task' && parsed.data) {
      if (parsed.data.task) return '\u00B7 created';
      if (parsed.data.deleted) return '\u00B7 deleted';
      const count = Number(parsed.data.count ?? parsed.data.tasks?.length ?? 0);
      return '\u00B7 ' + count + ' task' + (count === 1 ? '' : 's');
    }
    if ((name === 'background_process' || name === 'start_service' || name === 'stop_service') && parsed.data) {
      return formatServiceChip(parsed.data);
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
  if (isMcpToolName(name)) return '';
  try {
    const parsed = JSON.parse(text);
    // todo_write: keep tool cards compact; title/chip is enough.
    if (name === 'todo_write' && parsed.data) return '';
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
    // batch_read result: show each file's content
    if (name === 'batch_read' && parsed.data) {
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
      return formatServiceInfo(parsed.data);
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
      return (d.path || '') + ' updated: ' + (d.replacements || 1) + ' replacement(s)';
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
    // grep result: show occurrences and matching lines
    if (name === 'grep_files' && parsed.data && parsed.data.matches) {
      const d = parsed.data;
      let out = '';
      if (d.occurrences) {
        out = d.occurrences + ' occurrences';
        if (d.count && d.count !== d.occurrences) out += ' in ' + d.count + ' matching lines';
      } else {
        out = d.count + ' matches';
      }
      if (d.files) out += ' in ' + d.files + ' files';
      if (d.samplesTruncated || d.truncated) out += d.statsExact ? ' (sample truncated)' : ' (truncated)';
      out += '\n';
      const shown = d.matches.slice(0, 50);
      for (const m of shown) {
        out += m.path + ':' + m.lineNum + '  ' + m.content + '\n';
      }
      if (d.matches.length > 50) {
        out += '... and ' + (d.matches.length - 50) + ' more';
      }
      return out.slice(0, 12000);
    }
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
    if (name === 'Skill' && parsed.data) {
      if (parsed.data.message) return parsed.data.message;
      if (parsed.data.name) return 'Skill loaded: ' + parsed.data.name;
    }
    // Fallback: pretty-print JSON
    return compactJSON(text).slice(0, 12000);
  } catch (_) {
    return text.slice(0, 12000);
  }
}

function formatScheduledTaskToolDetail(task = {}) {
  const lines = [];
  if (task.name) lines.push(`Task: ${task.name}`);
  if (task.id) lines.push(`ID: ${task.id}`);
  lines.push(`Schedule: ${formatScheduledToolSchedule(task.schedule || {})}`);
  if (task.workspace) lines.push(`Workspace: ${task.workspace}`);
  lines.push('Mode: YOLO');
  if (task.nextRunAt) lines.push(`Next run: ${new Date(Number(task.nextRunAt)).toLocaleString()}`);
  if (task.lastRunAt) lines.push(`Last run: ${new Date(Number(task.lastRunAt)).toLocaleString()}`);
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
  if (schedule.type === 'cron') return `cron ${schedule.cron || '-'} (${schedule.timezone || 'local timezone'})`;
  return '-';
}

function formatServiceChip(service) {
  const status = String(service?.status || '').trim() || 'unknown';
  const parts = [status];
  if (service?.pid) parts.push('pid ' + service.pid);
  if (service?.port) parts.push('port ' + service.port);
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
  if (service?.port) lines.push(`port: ${service.port}`);
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

function formatUnixTimestamp(value) {
  const seconds = Number(value) || 0;
  if (!seconds) return '';
  try {
    return new Date(seconds * 1000).toLocaleString();
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

function exportAllMessages() {
  const msgs = activeMessages.value;
  if (!msgs || !msgs.length) return;
  const parts = [];
  parts.push(`# ${activeSession.value?.title || 'Ally 会话'}\n`);
  parts.push(`> 导出时间: ${new Date().toLocaleString()}`);
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

function compactJSON(raw) {
  if (!raw) return '';
  try {
    return JSON.stringify(JSON.parse(raw), null, 2);
  } catch (_) {
    return raw;
  }
}

function fmtTime(ts) {
  if (!ts) return '';
  const d = new Date(ts);
  const pad = (n) => String(n).padStart(2, '0');
  const month = pad(d.getMonth() + 1);
  const day = pad(d.getDate());
  const hour = pad(d.getHours());
  const min = pad(d.getMinutes());
  return `${month}/${day} ${hour}:${min}`;
}

function msgCount(s) {
  if (!s.messages) return 0;
  return s.messages.filter(m => m.role === 'user' || m.role === 'assistant').length;
}

function ctxSize(s) {
  if (!s.messages) return '0';
  let chars = 0;
  for (const m of s.messages) {
    if (m.content) chars += m.content.length;
  }
  const tokens = Math.round(chars / 4);
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

watch(configVisible, (visible) => {
  if (visible) {
    assignConfig(configDraft, config);
    cancelModelDraft();
    alignActiveProviderTab(normalizedProviderName(configDraft.providerName));
    refreshSkillState();
  }
});

watch(providerTabs, (tabs) => {
  if (!tabs.length) {
    activeProviderTab.value = '';
    return;
  }
  alignActiveProviderTab();
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

function handleGlobalKeydown(event) {
  if (event.key === 'Escape' && activeSession.value?.runId) {
    event.preventDefault();
    event.stopPropagation();
    stopRun();
    return;
  }
  if (!(event.ctrlKey || event.metaKey) || event.altKey) return;
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

async function checkForUpdates() {
  const controller = new AbortController();
  const timeout = window.setTimeout(() => controller.abort(), 8000);
  try {
    const response = await fetch(ALLY_LATEST_RELEASE_API, {
      signal: controller.signal,
      cache: 'no-store',
      headers: { Accept: 'application/vnd.github+json' },
    });
    if (!response.ok) return;
    const release = await response.json();
    const latest = String(release?.tag_name || '').trim();
    if (!isNewerReleaseVersion(latest, buildVersion)) return;
    latestReleaseVersion.value = latest;
    updateAvailable.value = true;
  } catch (_) {
    // Update checks are best-effort and must never block startup.
  } finally {
    window.clearTimeout(timeout);
  }
}

function openUpdatePage() {
  BrowserOpenURL(ALLY_REPOSITORY_URL);
}

async function applyPlatformClass() {
  let platform = '';
  try {
    const env = await Environment();
    platform = env?.platform || '';
  } catch (_) {
    platform = /macintosh|mac os x/i.test(navigator.userAgent || '') ? 'darwin' : '';
  }
  if (!platform) return;
  document.body.classList.add(`platform-${platform}`);
}

onMounted(async () => {
  applyPlatformClass();
  window.addEventListener('keydown', handleGlobalKeydown, true);
  document.addEventListener('click', handleMermaidToolbarClick, true);
  window.addEventListener('pointerdown', handleAudioUnlock, { once: true, passive: true });
  window.addEventListener('keydown', handleAudioUnlock, { once: true });
  window.addEventListener('resize', refreshWindowMaximisedState);
  window.addEventListener('focus', refreshWindowMaximisedState);
  bindRuntimeEvents();
  void checkForUpdates();
  // Pre-load skills before init so welcome message has the count
  try { await refreshSkillState(); } catch (_) { /* ignore */ }
  await init();
  await initPlanMode();
  await loadScheduledTasks();
  await refreshWindowMaximisedState();
});

onUnmounted(() => {
  window.removeEventListener('keydown', handleGlobalKeydown, true);
  document.removeEventListener('click', handleMermaidToolbarClick, true);
  window.removeEventListener('pointerdown', handleAudioUnlock);
  window.removeEventListener('keydown', handleAudioUnlock);
  window.removeEventListener('resize', refreshWindowMaximisedState);
  window.removeEventListener('focus', refreshWindowMaximisedState);
  cleanupRuntimeEvents();
  streamBuffers.clear();
  for (const att of pendingAttachments.value) releaseAttachmentPreview(att);
  for (const session of sessions.value) releaseSessionAttachments(session);
  closeCompletionAudio();
});
</script>
