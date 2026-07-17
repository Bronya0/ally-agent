<template>
  <div class="composer-info">
    <n-dropdown
      trigger="click"
      placement="top-start"
      :options="runModeOptions"
      :disabled="running && !grillModeActive"
      @select="selectRunMode"
    >
      <button
        type="button"
        :class="['run-mode-chip', currentRunMode]"
        :title="currentRunModeLabel"
        :disabled="running && !grillModeActive"
        @click.stop
      >
        <span class="run-mode-dot"></span>
        <span>{{ currentRunModeLabel }}</span>
        <span class="run-mode-caret">▾</span>
      </button>
    </n-dropdown>
    <n-popover :show="modelMenuVisible" trigger="manual" placement="top-start" :show-arrow="false" :style="{ padding: '6px' }" @clickoutside="modelMenuVisible = false">
      <template #trigger>
        <span class="info-model" @click.stop="toggleModelMenu" style="cursor:pointer">{{ currentModelLabel }}</span>
      </template>
      <div class="model-menu-inner" @click.stop>
        <div v-if="modelGroups.length" class="model-groups">
          <section v-for="group in modelGroups" :key="group.key" class="model-group">
            <button
              type="button"
              :class="['model-group-header', { active: group.hasActiveModel }]"
              :aria-expanded="isModelGroupExpanded(group.key)"
              :title="isModelGroupExpanded(group.key) ? $t('composer.models.collapse') : $t('composer.models.expand')"
              @click="toggleModelGroup(group.key)"
            >
              <span class="model-group-caret">{{ isModelGroupExpanded(group.key) ? '▾' : '▸' }}</span>
              <span class="model-group-name">{{ group.label }}</span>
              <span class="model-group-count">{{ group.models.length }}</span>
            </button>
            <div v-if="isModelGroupExpanded(group.key)" class="model-group-items">
              <button
                v-for="item in group.models"
                :key="item.index"
                type="button"
                :class="['model-item', { active: isActiveModel(item.model) }]"
                @click="selectModel(item.index)"
              >
                <span class="model-item-model">{{ item.model.model || '-' }}</span>
                <span v-if="isActiveModel(item.model)" class="model-item-active-mark">✓</span>
              </button>
            </div>
          </section>
        </div>
        <div v-else class="model-empty">{{ $t('composer.models.empty') }}</div>
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
      :class="['scheduled-task-chip', { running: taskCenterRunningCount > 0 }]"
      :title="$t('composer.taskCenter.open')"
      @click.stop="$emit('openTaskCenter')"
    >
      <span class="scheduled-task-icon">▤</span>
      <span>{{ taskCenterCount }}</span>
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
  grillModeActive: { type: Boolean, default: false },
  taskCenterCount: { type: Number, default: 0 },
  taskCenterRunningCount: { type: Number, default: 0 },
  fmtK: { type: Function, required: true },
});

const emit = defineEmits(['switchModel', 'openConfig', 'openGitDiff', 'openWorkspace', 'jumpQuestion', 'setRunMode', 'openTaskCenter']);

const modelMenuVisible = ref(false);
const expandedModelGroups = ref(new Set());
const contextPopoverVisible = ref(false);
const currentModelLabel = computed(() => `${props.config.providerName || '-'} · ${props.config.model || '-'}`);
const modelGroups = computed(() => {
  const groups = new Map();
  (props.config.models || []).forEach((model, index) => {
    const label = providerLabel(model);
    const key = modelProviderKey(model);
    if (!groups.has(key)) groups.set(key, { key, label, models: [], hasActiveModel: false });
    const group = groups.get(key);
    group.models.push({ model, index });
    if (isActiveModel(model)) group.hasActiveModel = true;
  });

  return [...groups.values()]
    .map((group) => ({
      ...group,
      models: group.models.sort((left, right) => compareModelLabels(left.model?.model, right.model?.model)),
    }))
    .sort((left, right) => compareModelLabels(left.label, right.label));
});
const currentRunMode = computed(() => {
  if (props.grillModeActive) return 'grill';
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

function compareModelLabels(left, right) {
  return String(left || '').localeCompare(String(right || ''), undefined, {
    sensitivity: 'base',
    numeric: true,
  });
}

function modelProviderKey(model) {
  return providerLabel(model).toLocaleLowerCase();
}

function toggleModelMenu() {
  const nextVisible = !modelMenuVisible.value;
  modelMenuVisible.value = nextVisible;
  if (!nextVisible) return;
  const currentProvider = modelProviderKey(props.config);
  const nextExpanded = new Set(expandedModelGroups.value);
  if (modelGroups.value.some((group) => group.key === currentProvider)) nextExpanded.add(currentProvider);
  expandedModelGroups.value = nextExpanded;
}

function isModelGroupExpanded(key) {
  return expandedModelGroups.value.has(key);
}

function toggleModelGroup(key) {
  const nextExpanded = new Set(expandedModelGroups.value);
  if (nextExpanded.has(key)) nextExpanded.delete(key);
  else nextExpanded.add(key);
  expandedModelGroups.value = nextExpanded;
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
  return modelProviderKey(model) === modelProviderKey(props.config)
    && String(model?.model || '').trim().toLocaleLowerCase() === String(props.config.model || '').trim().toLocaleLowerCase();
}

function openConfig() {
  modelMenuVisible.value = false;
  emit('openConfig');
}

</script>
