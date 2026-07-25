<template>
  <div class="composer-info">
    <button
      type="button"
      class="composer-icon-btn composer-new-session-btn"
      :title="$t('app.sessions.new')"
      :aria-label="$t('app.sessions.new')"
      @click.stop="$emit('newSession')"
    >+</button>
    <button
      type="button"
      class="composer-icon-btn composer-sessions-btn"
      :title="$t('commands.sessions')"
      :aria-label="$t('commands.sessions')"
      @click.stop="$emit('showSessions')"
    >☰</button>
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
        <span>{{ currentRunModeLabel }}</span>
        <span class="run-mode-caret">▾</span>
      </button>
    </n-dropdown>
    <n-dropdown
      trigger="click"
      placement="top-start"
      scrollable
      :options="modelMenuOptions"
      :render-label="renderModelMenuLabel"
      :menu-props="modelMenuProps"
      @select="onModelMenuSelect"
    >
      <span class="info-model" style="cursor:pointer">{{ currentModelLabel }}</span>
    </n-dropdown>
    <span class="info-workspace">
      <button class="info-workspace-btn" type="button" :title="$t('composer.workspace.open')" @click.stop="$emit('openWorkspace')">
        {{ config.workspace || $t('composer.workspace.none') }}
      </button>
      <n-popover
        v-if="config.workspace"
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
            <span class="extra-roots-icon">⊞</span>
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
              >×</button>
            </li>
          </ul>
          <button
            type="button"
            class="extra-roots-add"
            @click.stop="$emit('addExtraRoot')"
          >{{ $t('extraRoots.panel.add') }}</button>
        </div>
      </n-popover>
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
import { computed, h, ref } from 'vue';
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
  grillModeActive: { type: Boolean, default: false },
  taskCenterCount: { type: Number, default: 0 },
  taskCenterRunningCount: { type: Number, default: 0 },
  extraRoots: { type: Array, default: () => [] },
  fmtK: { type: Function, required: true },
});

const emit = defineEmits(['switchModel', 'openConfig', 'openGitDiff', 'openWorkspace', 'setRunMode', 'openTaskCenter', 'newSession', 'showSessions', 'addExtraRoot', 'removeExtraRoot']);

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
const modelMenuOptions = computed(() => {
  const options = [];
  const groups = modelGroups.value;
  if (groups.length === 0) {
    options.push({ key: 'empty', label: t('composer.models.empty'), disabled: true, isEmpty: true });
  } else {
    for (const group of groups) {
      options.push({
        key: `group:${group.key}`,
        label: group.label,
        count: group.models.length,
        hasActiveModel: group.hasActiveModel,
        children: group.models.map((item) => ({
          key: `model:${item.index}`,
          label: item.model.model || '-',
          active: isActiveModel(item.model),
        })),
      });
    }
  }
  options.push({ type: 'divider', key: 'divider' });
  options.push({ key: 'manage', label: t('composer.models.manage'), isManage: true });
  return options;
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

function onModelMenuSelect(key) {
  if (key === 'manage') {
    emit('openConfig');
    return;
  }
  if (typeof key === 'string' && key.startsWith('model:')) {
    const index = parseInt(key.slice(6), 10);
    if (!Number.isNaN(index)) emit('switchModel', index);
  }
}

function renderModelMenuLabel(option) {
  if (option.isManage) {
    return h('span', { class: 'model-menu-manage' }, option.label);
  }
  if (option.isEmpty) {
    return h('span', { class: 'model-menu-empty' }, option.label);
  }
  if (option.children && option.children.length) {
    return h('span', { class: ['model-menu-group', { active: option.hasActiveModel }] }, [
      h('span', { class: 'model-menu-group-name' }, option.label),
      h('span', { class: 'model-menu-group-count' }, String(option.count)),
    ]);
  }
  return h('span', { class: ['model-menu-item', { active: option.active }] }, [
    h('span', { class: 'model-menu-item-name' }, option.label),
    option.active ? h('span', { class: 'model-menu-item-mark' }, '✓') : null,
  ]);
}

function modelMenuProps() {
  return {
    class: 'model-menu',
    style: { minWidth: '240px', maxHeight: 'min(420px, calc(100vh - 160px))' },
  };
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

</script>
