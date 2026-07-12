<template>
  <div class="composer-info">
    <n-dropdown
      trigger="click"
      placement="top-start"
      :options="runModeOptions"
      :disabled="running"
      @select="selectRunMode"
    >
      <button
        type="button"
        :class="['run-mode-chip', currentRunMode]"
        :title="currentRunModeLabel"
        :disabled="running"
        @click.stop
      >
        <span class="run-mode-dot"></span>
        <span>{{ currentRunModeLabel }}</span>
        <span class="run-mode-caret">▾</span>
      </button>
    </n-dropdown>
    <n-popover :show="modelMenuVisible" trigger="manual" placement="top-start" :show-arrow="false" :style="{ padding: '6px' }" @clickoutside="modelMenuVisible = false">
      <template #trigger>
        <span class="info-model" @click.stop="modelMenuVisible = !modelMenuVisible" style="cursor:pointer">{{ currentModelLabel }}</span>
      </template>
      <div class="model-menu-inner" @click.stop>
        <div
          v-for="(m, idx) in (config.models || [])"
          :key="idx"
          class="model-item"
          :class="{ active: isActiveModel(m) }"
          @click="selectModel(idx)"
        >
          <span class="model-item-model">{{ m.model || '-' }}</span>
          <span class="model-item-name">{{ providerLabel(m) }}</span>
        </div>
        <div v-if="!config.models || config.models.length === 0" class="model-empty">{{ $t('composer.models.empty') }}</div>
        <div class="model-menu-actions">
          <n-button size="tiny" quaternary @click="openConfig">{{ $t('composer.models.manage') }}</n-button>
        </div>
      </div>
    </n-popover>
    <span class="info-workspace">
      <button class="info-workspace-btn" type="button" :title="$t('composer.workspace.open')" @click.stop="$emit('openWorkspace')">
        {{ config.workspace || $t('composer.workspace.none') }}
      </button>
    </span>
    <button
      type="button"
      :class="['scheduled-task-chip', { running: scheduledTaskRunningCount > 0 }]"
      :title="$t('composer.scheduled.open')"
      @click.stop="$emit('openScheduledTasks')"
    >
      <span class="scheduled-task-icon">◷</span>
      <span>{{ scheduledTaskCount }}</span>
    </button>
    <span class="question-jump-controls" :title="$t('composer.question.jump')">
      <button type="button" class="question-jump-btn" :title="$t('composer.question.previous')" @click.stop="$emit('jumpQuestion', 'up')">↑</button>
      <button type="button" class="question-jump-btn" :title="$t('composer.question.next')" @click.stop="$emit('jumpQuestion', 'down')">↓</button>
    </span>
    <template v-if="gitStatus.isRepo">
      <span class="info-sep">·</span>
      <span class="info-git" :title="$t('composer.git.open')" @click.stop="$emit('openGitDiff')">
        <span class="info-git-branch">{{ gitStatus.branch }}</span>
        <span v-if="gitStatus.added > 0" class="git-stat added" :title="$t('composer.git.added')">+{{ gitStatus.added }}</span>
        <span v-if="gitStatus.modified > 0" class="git-stat modified" :title="$t('composer.git.modified')">~{{ gitStatus.modified }}</span>
        <span v-if="gitStatus.deleted > 0" class="git-stat deleted" :title="$t('composer.git.deleted')">-{{ gitStatus.deleted }}</span>
      </span>
    </template>
    <n-popover v-if="contextBreakdown" :show="contextPopoverVisible" trigger="manual" placement="top" :show-arrow="false" @clickoutside="contextPopoverVisible = false">
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
      </div>
    </n-popover>
    <span v-else class="info-context">
      <ContextUsageInline
        :context-percent="contextPercent"
        :context-used="contextUsed"
        :context-max="contextMax"
        :context-usage-style="contextUsageStyle"
      />
    </span>
  </div>
</template>

<script setup>
import { computed, ref } from 'vue';
import ContextUsageInline from './ContextUsageInline.vue';
import { formatDateTime, t } from '../i18n.mjs';

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
  const msgs = props.sessionMessages;
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
  const msgs = props.sessionMessages;
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

const props = defineProps({
  config: { type: Object, required: true },
  running: { type: Boolean, default: false },
  gitStatus: { type: Object, default: () => ({ isRepo: false }) },
  contextBreakdown: { type: Object, default: null },
  sessionMessages: { type: Array, default: () => [] },
  sessionTitle: { type: String, default: '' },
  contextPercent: { type: String, default: '0.0%' },
  contextUsed: { type: String, default: '0' },
  contextMax: { type: String, default: '0' },
  contextUsageStyle: { type: Object, default: () => ({}) },
  workspaceInputTokens: { type: String, default: '0' },
  workspaceOutputTokens: { type: String, default: '0' },
  planModeActive: { type: Boolean, default: false },
  grillModeActive: { type: Boolean, default: false },
  scheduledTaskCount: { type: Number, default: 0 },
  scheduledTaskRunningCount: { type: Number, default: 0 },
  fmtK: { type: Function, required: true },
});

const emit = defineEmits(['switchModel', 'openConfig', 'openGitDiff', 'openWorkspace', 'jumpQuestion', 'setRunMode', 'openScheduledTasks']);

const modelMenuVisible = ref(false);
const contextPopoverVisible = ref(false);
const currentModelLabel = computed(() => `${props.config.providerName || '-'} · ${props.config.model || '-'}`);
const currentRunMode = computed(() => {
  if (props.grillModeActive) return 'grill';
  if (props.planModeActive) return 'plan';
  return 'yolo';
});
const currentRunModeLabel = computed(() => currentRunMode.value.toUpperCase());
const runModeOptions = computed(() => [
  {
    label: 'YOLO',
    key: 'yolo',
    disabled: currentRunMode.value === 'yolo',
  },
  {
    label: 'PLAN',
    key: 'plan',
    disabled: currentRunMode.value === 'plan',
  },
  {
    label: 'GRILL',
    key: 'grill',
    disabled: currentRunMode.value === 'grill',
  },
]);
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
    '目标上下文': 'composer.context.part.goal',
  };
  return labels[label] ? t(labels[label]) : label;
}

function selectModel(index) {
  modelMenuVisible.value = false;
  emit('switchModel', index);
}

function selectRunMode(key) {
  if (key === currentRunMode.value) return;
  emit('setRunMode', key);
}

function providerLabel(model) {
  return (model?.providerName || '').trim() || 'OpenAI Compatible';
}

function isActiveModel(model) {
  return providerLabel(model) === providerLabel(props.config)
    && (model?.model || '') === (props.config.model || '');
}

function openConfig() {
  modelMenuVisible.value = false;
  emit('openConfig');
}

</script>
