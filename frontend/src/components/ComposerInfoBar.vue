<!--
SPDX-License-Identifier: GPL-3.0-only

Copyright (C) 2026 tangssst <tangssst@qq.com>
GitHub: https://github.com/Bronya0/ally-agent

This file is part of ally-agent, licensed under the GNU General
Public License v3. See the LICENSE file for details.
-->
<template>
  <div class="composer-info">
    <button
      type="button"
      class="composer-icon-btn composer-new-session-btn"
      :title="$t('app.sessions.newSession')"
      :aria-label="$t('app.sessions.newSession')"
      @click.stop="$emit('newSession')"
    ><PlusOutlined /></button>
    <button
      type="button"
      class="composer-icon-btn composer-sessions-btn"
      :title="$t('commands.sessions')"
      :aria-label="$t('commands.sessions')"
      @click.stop="$emit('showSessions')"
    ><MenuOutlined /></button>
    <ModelMenu
      :models="props.config.models || []"
      :active-identity="modelConfigIdentity(props.config)"
      :disabled="running"
      placement="top-start"
      @select="(index) => emit('switchModel', index)"
      @manage="emit('openConfig')"
    >
      <span class="info-model" style="cursor:pointer" :title="running ? $t('composer.lockedWhileRunning') : ''">{{ currentModelLabel }}</span>
    </ModelMenu>
    <n-dropdown
      trigger="click"
      placement="top-start"
      :disabled="running"
      :options="reasoningEffortOptions"
      @select="onReasoningEffortSelect"
    >
      <span class="info-effort" :title="running ? $t('composer.lockedWhileRunning') : $t('composer.effort.title')">
        <span class="info-effort-label">{{ currentEffortLabel }}</span>
        <span class="info-effort-caret">▾</span>
      </span>
    </n-dropdown>
    <span class="info-workspace">
      <button class="info-workspace-btn" type="button" :title="activeWorkspacePath || $t('composer.workspace.open')" @click.stop="$emit('openWorkspace')">
        {{ activeWorkspaceName || $t('composer.workspace.none') }}
      </button>
      <n-popover
        v-if="activeWorkspacePath"
        trigger="click"
        placement="top-start"
        :show-arrow="false"
        :width="360"
        class="extra-roots-popover"
      >
        <template #trigger>
          <button
            type="button"
            class="composer-icon-btn extra-roots-btn"
            :class="{ 'has-roots': extraRoots.length > 0 }"
            :title="$t('extraRoots.button.title')"
            :aria-label="$t('extraRoots.button.title')"
            @click.stop
          >
            <FolderAddOutlined class="extra-roots-icon" />
            <span v-if="extraRoots.length > 0" class="extra-roots-count">{{ extraRoots.length }}</span>
          </button>
        </template>
        <div class="extra-roots-panel">
          <div class="extra-roots-header">
            <span class="extra-roots-title">{{ $t('extraRoots.panel.title') }}</span>
            <span class="extra-roots-hint">{{ $t('extraRoots.panel.hint') }}</span>
          </div>
          <div v-if="extraRoots.length === 0" class="extra-roots-empty">
            {{ $t('extraRoots.panel.empty') }}
          </div>
          <ul v-else class="extra-roots-list">
            <li v-for="root in extraRoots" :key="root" class="extra-roots-item">
              <span class="extra-roots-path" :title="root">{{ root }}</span>
              <button
                type="button"
                class="extra-roots-remove"
                :title="$t('extraRoots.panel.remove')"
                @click.stop="$emit('removeExtraRoot', root)"
              ><CloseOutlined /></button>
            </li>
          </ul>
          <button
            type="button"
            class="extra-roots-add"
            @click.stop="$emit('addExtraRoot')"
          >{{ $t('extraRoots.panel.add') }}</button>
        </div>
      </n-popover>
      <button
        type="button"
        class="composer-icon-btn workspace-terminal-btn"
        :title="$t('composer.workspace.terminal')"
        :aria-label="$t('composer.workspace.terminal')"
        @click.stop="$emit('openTerminal')"
      ><CodeOutlined /></button>
    </span>
    <button
      type="button"
      :class="['scheduled-task-chip', { running: taskCenterRunningCount > 0 }]"
      :title="$t('composer.taskCenter.open')"
      @click.stop="$emit('openTaskCenter')"
    >
      <AppstoreOutlined class="scheduled-task-icon" />
      <span>{{ taskCenterCount }}</span>
    </button>
    <template v-if="gitStatus.isRepo">
      <span class="info-sep">·</span>
      <n-popover
        v-if="gitStatus.isMultiRepo && gitStatus.repos && gitStatus.repos.length > 0"
        trigger="hover"
        placement="top-start"
        :show-arrow="false"
        :delay="120"
        :duration="150"
        class="multi-repo-popover"
      >
        <template #trigger>
          <span class="info-git" :title="$t('composer.git.open')" @click.stop="$emit('openGitDiff', gitStatus.repos[0]?.path || '')">
            <span class="info-git-branch">{{ `${gitStatus.repos.length} repos` }}</span>
            <span v-if="gitStatus.ahead > 0" class="git-stat ahead" :title="$t('composer.git.ahead')">↑{{ gitStatus.ahead }}</span>
            <span v-if="gitStatus.behind > 0" class="git-stat behind" :title="$t('composer.git.behind')">↓{{ gitStatus.behind }}</span>
            <span v-if="gitStatus.added > 0" class="git-stat added" :title="$t('composer.git.added')">+{{ gitStatus.added }}</span>
            <span v-if="gitStatus.modified > 0" class="git-stat modified" :title="$t('composer.git.modified')">~{{ gitStatus.modified }}</span>
            <span v-if="gitStatus.deleted > 0" class="git-stat deleted" :title="$t('composer.git.deleted')">-{{ gitStatus.deleted }}</span>
          </span>
        </template>
        <div class="multi-repo-panel">
          <ul class="multi-repo-list">
            <li
              v-for="repo in gitStatus.repos"
              :key="repo.path"
              class="multi-repo-item"
              @click.stop="$emit('openGitDiff', repo.path)"
            >
              <span class="multi-repo-name" :title="repo.name">{{ repo.name }}/</span>
              <span class="multi-repo-branch" :title="repo.branch">{{ repo.branch }}</span>
              <span class="multi-repo-stats">
                <span v-if="repo.ahead > 0" class="git-stat ahead">↑{{ repo.ahead }}</span>
                <span v-if="repo.behind > 0" class="git-stat behind">↓{{ repo.behind }}</span>
                <span v-if="repo.added > 0" class="git-stat added">+{{ repo.added }}</span>
                <span v-if="repo.modified > 0" class="git-stat modified">~{{ repo.modified }}</span>
                <span v-if="repo.deleted > 0" class="git-stat deleted">-{{ repo.deleted }}</span>
                <span v-if="!repo.added && !repo.modified && !repo.deleted && !repo.ahead && !repo.behind" class="multi-repo-clean">clean</span>
              </span>
            </li>
          </ul>
        </div>
      </n-popover>
      <span v-else class="info-git" :title="$t('composer.git.open')" @click.stop="$emit('openGitDiff')">
        <span class="info-git-branch">{{ gitStatus.branch }}</span>
        <span v-if="gitStatus.ahead > 0" class="git-stat ahead" :title="$t('composer.git.ahead')">↑{{ gitStatus.ahead }}</span>
        <span v-if="gitStatus.behind > 0" class="git-stat behind" :title="$t('composer.git.behind')">↓{{ gitStatus.behind }}</span>
        <span v-if="gitStatus.added > 0" class="git-stat added" :title="$t('composer.git.added')">+{{ gitStatus.added }}</span>
        <span v-if="gitStatus.modified > 0" class="git-stat modified" :title="$t('composer.git.modified')">~{{ gitStatus.modified }}</span>
        <span v-if="gitStatus.deleted > 0" class="git-stat deleted" :title="$t('composer.git.deleted')">-{{ gitStatus.deleted }}</span>
      </span>
    </template>
    <n-popover v-if="!footerStatsLoading && contextBreakdown" :show="contextPopoverVisible" trigger="manual" placement="top" :show-arrow="false" @clickoutside="contextPopoverVisible = false">
      <template #trigger>
        <span class="info-context" style="cursor:pointer" @click.stop="contextPopoverVisible = !contextPopoverVisible">
          <ContextUsageInline
            :context-percent="contextPercent"
            :context-used="contextUsed"
            :context-max="contextMax"
            :context-usage-style="contextUsageStyle"
          />
        </span>
      </template>
      <div class="context-breakdown">
        <div class="context-breakdown-row">
          <span>{{ $t('composer.context.system') }}</span>
          <span>{{ fmtK(contextBreakdown.systemPrompt) }}</span>
        </div>
        <div
          v-for="part in systemPromptParts"
          :key="part.label"
          class="context-breakdown-row context-breakdown-subrow"
        >
          <span>{{ contextPartLabel(part.label) }}</span>
          <span>{{ fmtK(part.tokens) }}</span>
        </div>
        <div class="context-breakdown-row">
          <span>{{ $t('composer.context.tools') }}</span>
          <span>{{ fmtK(contextBreakdown.toolSchemas) }}</span>
        </div>
        <div class="context-breakdown-row">
          <span>{{ $t('composer.context.user') }}</span>
          <span>{{ fmtK(contextBreakdown.userMessages) }}</span>
        </div>
        <div class="context-breakdown-row">
          <span>{{ $t('composer.context.assistant') }}</span>
          <span>{{ fmtK(contextBreakdown.assistantMsgs) }}</span>
        </div>
        <div class="context-breakdown-row">
          <span>{{ $t('composer.context.results') }}</span>
          <span>{{ fmtK(contextBreakdown.toolResults) }}</span>
        </div>
        <div v-if="contextBreakdown.reasoning" class="context-breakdown-row">
          <span>{{ $t('composer.context.reasoning') }}</span>
          <span>{{ fmtK(contextBreakdown.reasoning) }}</span>
        </div>
        <div class="context-breakdown-row context-breakdown-total">
          <span>{{ $t('composer.context.input') }}</span>
          <span>{{ workspaceInputTokens }}</span>
        </div>
        <div class="context-breakdown-row">
          <span>{{ $t('composer.context.output') }}</span>
          <span>{{ workspaceOutputTokens }}</span>
        </div>
        <div class="context-breakdown-footer">
          <button
            type="button"
            class="context-compact-btn"
            :disabled="running"
            :title="running ? $t('app.compact.wait') : $t('composer.context.compact')"
            @click.stop="onCompactClick"
          >{{ $t('composer.context.compact') }}</button>
          <button
            type="button"
            class="context-compact-btn"
            :disabled="running"
            :title="running ? $t('app.compact.wait') : $t('composer.context.compactLessons')"
            @click.stop="onLessonClick"
          >{{ $t('composer.context.compactLessons') }}</button>
          <button
            type="button"
            class="context-compact-btn"
            :disabled="running"
            :title="running ? $t('app.compact.wait') : $t('composer.context.updateCodegraph')"
            @click.stop="onCodegraphClick"
          >{{ $t('composer.context.updateCodegraph') }}</button>
        </div>
      </div>
    </n-popover>
    <span v-else-if="!footerStatsLoading" class="info-context">
      <ContextUsageInline
        :context-percent="contextPercent"
        :context-used="contextUsed"
        :context-max="contextMax"
        :context-usage-style="contextUsageStyle"
      />
    </span>
    <span v-else class="info-context info-context-loading" aria-hidden="true">...</span>
    <!-- 文件树开关停靠信息栏最右（界面右下角），紧邻右侧树面板。图标用
         FolderOpenTwotone（张开文件夹，次色为 currentColor + 15% 透明，随主题色
         变化）。历史注记：曾因与“在文件管理器里打开工作区”（左下工作区名字
         按钮的职责）混淆而改用层级树 ApartmentOutlined，现按用户指定换回
         FolderOpen 系，若再收混淆反馈可回退。 -->
    <button
      type="button"
      :class="['composer-icon-btn', 'composer-explorer-btn', { active: explorerVisible }]"
      :title="$t('app.workspaceExplorer.open')"
      :aria-label="$t('app.workspaceExplorer.open')"
      @click.stop="$emit('toggleExplorer')"
    ><FolderOpenTwotone /></button>
  </div>
</template>

<script setup>
import { computed, ref } from 'vue';
import ContextUsageInline from './ContextUsageInline.vue';
import ModelMenu from './ModelMenu.vue';
import PlusOutlined from '@vicons/antd/PlusOutlined';
import MenuOutlined from '@vicons/antd/MenuOutlined';
import FolderOpenTwotone from '@vicons/antd/FolderOpenTwotone';
import FolderAddOutlined from '@vicons/antd/FolderAddOutlined';
import CodeOutlined from '@vicons/antd/CodeOutlined';
import AppstoreOutlined from '@vicons/antd/AppstoreOutlined';
import CloseOutlined from '@vicons/antd/CloseOutlined';
import { formatDateTime, reasoningEffortLabel, t } from '../i18n.mjs';
import { formatModelLabel } from '../utils/modelLabel.mjs';
import { modelConfigIdentity, reasoningEffortLevels } from '../utils/modelConfigIO.mjs';
import { saveTextFile } from '../utils/download.mjs';

function formatMessageContent(msg) {
  if (!msg) return '';
  if (msg.welcome) return msg.content || '';
  if (msg.content) return msg.content;
  return '';
}

function formatMessageAsMD(msg) {
  const roleLabel = msg.role === 'user' ? 'User' : 'Assistant';
  const content = formatMessageContent(msg);
  if (!content) return '';
  return `> **${roleLabel}:**

${content}
`;
}

function exportLastResponse() {
  const msgs = props.getSessionMessages();
  if (!msgs || !msgs.length) return;
  // Find the last assistant message with content
  let lastAssistant = null;
  for (let i = msgs.length - 1; i >= 0; i--) {
    const m = msgs[i];
    if (m.role === 'assistant' && m.content && !m.welcome) {
      lastAssistant = m;
      break;
    }
  }
  if (!lastAssistant) return;
  const md = formatMessageAsMD(lastAssistant);
  downloadMD(md, `ally-response.md`);
}

function exportFullSession() {
  const msgs = props.getSessionMessages();
  if (!msgs || !msgs.length) return;
  const parts = [];
  parts.push(`# ${props.sessionTitle || t('app.export.sessionTitle')}\n`);
  parts.push(`> ${t('app.export.time', { time: formatDateTime(new Date()) })}`);
  parts.push('');
  for (const msg of msgs) {
    if (msg.welcome) continue;
    if (msg.role === 'tool_call') continue;
    const md = formatMessageAsMD(msg);
    if (md) parts.push(md);
  }
  downloadMD(parts.join('\n---\n\n'), `ally-session.md`);
}

function downloadMD(content, filename) {
  // Native save dialog via the Wails binding: WKWebView (macOS) ignores
  // <a download>, so the old blob-download approach silently did nothing
  // there. Falls back to blob download when the binding is unavailable.
  saveTextFile({ filename, content, filterName: 'Markdown (*.md)', filterPattern: '*.md' });
}

const props = defineProps({
  config: { type: Object, required: true },
  // Workspace path shown in the info bar. KB tabs pass the KB root here so
  // the path does not fall back to the shared chat workspace.
  workspace: { type: String, default: '' },
  running: { type: Boolean, default: false },
  gitStatus: { type: Object, default: () => ({ isRepo: false }) },
  contextBreakdown: { type: Object, default: null },
  footerStatsLoading: { type: Boolean, default: false },
  // Lazy getter for the session's messages array — only invoked when the user
  // actually triggers an export. Passing the full array as a prop forced Vue
  // to diff the entire array on every streaming delta / history mutation,
  // even though ComposerInfoBar's template never reads sessionMessages.
  getSessionMessages: { type: Function, default: () => [] },
  sessionTitle: { type: String, default: '' },
  contextPercent: { type: String, default: '0.0%' },
  contextUsed: { type: String, default: '0' },
  contextMax: { type: String, default: '0' },
  contextUsageStyle: { type: Object, default: () => ({}) },
  workspaceInputTokens: { type: String, default: '0' },
  workspaceOutputTokens: { type: String, default: '0' },
  taskCenterCount: { type: Number, default: 0 },
  taskCenterRunningCount: { type: Number, default: 0 },
  extraRoots: { type: Array, default: () => [] },
  explorerVisible: { type: Boolean, default: false },
  fmtK: { type: Function, required: true },
});

const emit = defineEmits(['switchModel', 'openConfig', 'openGitDiff', 'openWorkspace', 'changeReasoningEffort', 'openTaskCenter', 'newSession', 'showSessions', 'toggleExplorer', 'addExtraRoot', 'removeExtraRoot', 'openTerminal', 'compactContext', 'compactLessons', 'updateCodegraph']);

const contextPopoverVisible = ref(false);
const currentModelLabel = computed(() => formatModelLabel(props.config));
// Single source for the workspace path shown here: explicit prop (KB root on
// KB tabs) wins, otherwise fall back to the persisted chat workspace.
const activeWorkspacePath = computed(() => props.workspace || props.config.workspace || '');
// Show only the last path segment in the info bar; the full absolute path
// stays available via the button's tooltip.
const activeWorkspaceName = computed(() => {
  const segments = activeWorkspacePath.value.split(/[\\/]+/).filter(Boolean);
  return segments.length ? segments[segments.length - 1] : '';
});
const currentEffortLabel = computed(() => reasoningEffortLabel(props.config.reasoningEffort));
const reasoningEffortOptions = computed(() =>
  reasoningEffortLevels.map((level) => ({ label: reasoningEffortLabel(level), key: level }))
);
const systemPromptParts = computed(() => (
  Array.isArray(props.contextBreakdown?.systemPromptParts)
    ? props.contextBreakdown.systemPromptParts.filter((part) => part && part.tokens > 0)
    : []
));

function contextPartLabel(label) {
  const labels = {
    '核心系统提示词': 'composer.context.part.core',
    '技能元数据': 'composer.context.part.skills',
    '全局记忆索引': 'composer.context.part.memory',
    'AGENTS.md / 项目指令': 'composer.context.part.instructions',
    '自定义提示词': 'composer.context.part.custom',
    '工作区文件结构': 'composer.context.part.workspace',
    '计划快照': 'composer.context.part.plan',
    '项目代码图谱 Code Graph': 'composer.context.part.codegraph',
    '项目经验 Lessons': 'composer.context.part.lessons',
    '用户档案 User Profile': 'composer.context.part.profile',
  };
  return labels[label] ? t(labels[label]) : label;
}

function onReasoningEffortSelect(key) {
  emit('changeReasoningEffort', key);
}

function onCompactClick() {
  contextPopoverVisible.value = false;
  emit('compactContext');
}

function onLessonClick() {
  contextPopoverVisible.value = false;
  emit('compactLessons');
}

function onCodegraphClick() {
  contextPopoverVisible.value = false;
  emit('updateCodegraph');
}
</script>
