<template>
  <n-modal
    :show="visible"
    preset="card"
    :title="$t('settings.title')"
    class="config-modal"
    :style="settingsModalStyle"
    :bordered="true"
    @update:show="onClose"
  >
    <div class="settings-layout">
      <aside class="settings-nav" :aria-label="$t('settings.navigation')">
        <button :class="['settings-nav-item', { active: page === 'general' }]" @click="page = 'general'">
          <span class="settings-nav-title">{{ $t('settings.general') }}</span>
          <span class="settings-nav-desc">{{ $t('settings.prompt') }}</span>
        </button>
        <button :class="['settings-nav-item', { active: page === 'models' }]" @click="page = 'models'">
          <span class="settings-nav-title">{{ $t('settings.models') }}</span>
          <span class="settings-nav-desc">{{ $t('settings.providerKeys') }}</span>
        </button>
        <button :class="['settings-nav-item', { active: page === 'skills' }]" @click="page = 'skills'">
          <span class="settings-nav-title">Skills</span>
          <span class="settings-nav-desc">{{ $t('settings.skillsDescription') }}</span>
        </button>
        <button :class="['settings-nav-item', { active: page === 'mcp' }]" @click="page = 'mcp'">
          <span class="settings-nav-title">MCP</span>
          <span class="settings-nav-desc">{{ $t('settings.mcpDescription') }}</span>
        </button>
        <button :class="['settings-nav-item', { active: page === 'about' }]" @click="page = 'about'">
          <span class="settings-nav-title">{{ $t('settings.about') }}</span>
          <span class="settings-nav-desc">{{ $t('settings.versionLicense') }}</span>
        </button>
      </aside>

      <n-form class="settings-content" label-placement="top">
        <!-- General -->
        <section v-if="page === 'general'" class="settings-page">
          <div class="config-section-header">
            <div>
              <div class="config-section-title">{{ $t('settings.generalTitle') }}</div>
              <div class="config-section-subtitle">{{ $t('settings.generalSubtitle') }}</div>
            </div>
          </div>
          <n-form-item :label="$t('settings.customPrompt')">
            <n-input
              v-model:value="draft.customPrompt"
              type="textarea"
              :autosize="{ minRows: 8, maxRows: 16 }"
              :placeholder="$t('settings.customPromptPlaceholder')"
            />
          </n-form-item>
          <n-form-item :label="$t('settings.allowPrivateNetwork')">
            <div class="settings-toggle-row">
              <n-switch v-model:value="draft.allowPrivateNetwork" />
              <span class="settings-toggle-hint">{{ $t('settings.allowPrivateNetworkHint') }}</span>
            </div>
          </n-form-item>
          <n-form-item v-if="isWindows" :label="$t('settings.gitBashPath')">
            <div class="settings-field-stack">
              <n-input
                v-model:value="draft.gitBashPath"
                :placeholder="$t('settings.gitBashPathPlaceholder')"
              />
              <span class="settings-field-hint">{{ $t('settings.gitBashPathHint') }}</span>
            </div>
          </n-form-item>
        </section>

        <!-- Models -->
        <section v-else-if="page === 'models'" class="settings-page">
          <div class="config-section-header">
            <div>
              <div class="config-section-title">{{ $t('settings.modelsTitle') }}</div>
              <div class="config-section-subtitle">{{ $t('settings.modelsSubtitle') }}</div>
            </div>
            <n-button size="small" type="primary" @click="startAddModelDraft">{{ $t('settings.modelAdd') }}</n-button>
          </div>

          <div class="current-model-panel">
            <div class="current-model-main">
              <div class="current-model-label">{{ $t('settings.modelCurrent') }}</div>
              <div class="current-model-name">{{ draft.providerName || 'Provider' }} · {{ draft.model || $t('settings.modelNone') }}</div>
              <div class="current-model-url">{{ apiFormatLabel(draft.apiFormat) }}</div>
              <div class="current-model-url">{{ draft.baseUrl || $t('settings.baseUrlNone') }}</div>
            </div>
            <div class="current-model-meta">
              <span>max {{ draft.maxTokens || '-' }}</span>
              <span>context {{ draft.contextWindow || '-' }}</span>
            </div>
          </div>

          <n-tabs
            v-if="providerTabs.length"
            v-model:value="activeProviderTab"
            type="line"
            animated
            :default-value="providerTabs[0]?.name"
            class="provider-tabs"
          >
            <n-tab-pane v-for="tab in providerTabs" :key="tab.name" :name="tab.name" :tab="tab.label">
              <div class="saved-model-list">
                <div v-for="item in tab.models" :key="item.index" :class="['saved-model-item', { active: isDraftModelActive(item.model) }]">
                  <div class="saved-model-main">
                    <div class="saved-model-name">{{ normalizedProviderName(item.model.providerName) }} · {{ item.model.model || $t('settings.modelNone') }}</div>
                    <div class="saved-model-meta">{{ apiFormatLabel(item.model.apiFormat) }} · {{ item.model.model || 'Model' }}</div>
                    <div class="saved-model-url">{{ item.model.baseUrl || $t('settings.baseUrlNone') }}</div>
                    <div class="saved-model-url">max {{ item.model.maxTokens || '-' }} · context {{ item.model.contextWindow || '-' }}</div>
                  </div>
                  <n-space :size="4">
                    <n-button size="tiny" secondary :disabled="isDraftModelActive(item.model)" @click="applyModelToDraft(item.model)">{{ $t('settings.modelUse') }}</n-button>
                    <n-button size="tiny" quaternary @click="editModelDraft(item.index)">{{ $t('common.edit') }}</n-button>
                    <n-button size="tiny" type="error" quaternary @click="removeModelDraft(item.index)">{{ $t('common.delete') }}</n-button>
                  </n-space>
                </div>
              </div>
            </n-tab-pane>
          </n-tabs>
          <div v-else class="saved-model-empty">{{ $t('settings.modelsEmpty') }}</div>
        </section>

        <!-- Skills -->
        <section v-else-if="page === 'skills'" class="settings-page">
          <div class="config-section-header">
            <div>
              <div class="config-section-title">Skills</div>
              <div class="config-section-subtitle">{{ $t('settings.skillsSummary', { enabled: activeSkillNames.length, available: availableSkills.length }) }}</div>
            </div>
            <n-space>
              <n-button size="small" secondary :loading="skillsLoading" @click="refreshSkillState">{{ $t('common.refresh') }}</n-button>
              <n-button size="small" secondary :disabled="!activeSkillNames.length || skillsLoading" @click="clearLoadedSkills(false)">{{ $t('settings.skillsDisableAll') }}</n-button>
            </n-space>
          </div>
          <div class="skill-settings-list">
            <div v-if="skillsLoading && !availableSkills.length" class="saved-model-empty">{{ $t('settings.skillsLoading') }}</div>
            <div v-else-if="!availableSkills.length" class="saved-model-empty">{{ $t('settings.skillsEmpty') }}</div>
            <div v-for="sk in availableSkills" :key="`${sk.source || 'skill'}:${sk.name}`" :class="['skill-settings-item', { active: isSkillActive(sk.name) }]">
              <div class="skill-settings-main">
                <div class="skill-title-row">
                  <span class="skill-name">{{ sk.name }}</span>
                  <span :class="['skill-badge', sk.source || 'unknown']">{{ sk.source || 'unknown' }}</span>
                  <span v-if="isSkillActive(sk.name)" class="skill-badge loaded">{{ $t('common.enabled') }}</span>
                </div>
                <div class="skill-description">{{ sk.description || sk.whenToUse || $t('common.noDescription') }}</div>
                <div
                  class="skill-meta clickable"
                  :title="$t('settings.openSkillPath', { path: sk.path || sk.dir })"
                  @click="handleOpenSkillPath(sk)"
                >{{ sk.path || sk.dir || '-' }}</div>
              </div>
              <n-switch
                :value="isSkillActive(sk.name)"
                :disabled="skillsLoading || skillToggleInFlight === sk.name"
                @update:value="(value) => toggleSkillFromSettings(sk, value)"
              />
            </div>
          </div>
        </section>

        <!-- MCP -->
        <section v-else-if="page === 'mcp'" class="settings-page">
          <div class="config-section-header">
            <div>
              <div class="config-section-title">{{ $t('settings.mcpTitle') }}</div>
              <div class="config-section-subtitle">{{ $t('settings.mcpSubtitle') }}</div>
            </div>
          </div>
          <div class="mcp-toolbar">
            <div class="mcp-toolbar-mode">
              <n-button-group size="small">
                <n-button :type="mcpEditMode === 'form' ? 'primary' : 'default'" @click="switchMcpMode('form')">{{ $t('settings.mcpForm') }}</n-button>
                <n-button :type="mcpEditMode === 'json' ? 'primary' : 'default'" @click="switchMcpMode('json')">{{ $t('settings.mcpJson') }}</n-button>
              </n-button-group>
            </div>
            <div class="mcp-toolbar-actions">
              <n-button size="small" secondary :loading="mcpLoading" @click="loadMcpConfig">{{ $t('common.refresh') }}</n-button>
              <n-button size="small" type="primary" :loading="mcpLoading" :disabled="!mcpSaveEnabled" @click="saveMcpConfig">{{ $t('settings.mcpApplyReconnect') }}</n-button>
            </div>
          </div>
          <div class="mcp-save-scope">{{ $t('settings.mcpSaveScope') }}</div>
          <!-- Form mode -->
          <div v-if="mcpEditMode === 'form'" class="mcp-form-mode">
            <div v-for="(srv, idx) in mcpFormServers" :key="idx" class="mcp-server-card">
              <div class="mcp-server-card-header">
                <n-input v-model:value="srv.name" :placeholder="$t('settings.mcpServerName')" size="small" class="mcp-server-name-input" />
                <n-select v-model:value="srv.transport" :options="[
                  { label: $t('settings.mcpTransportStdio'), value: 'stdio' },
                  { label: $t('settings.mcpTransportSse'), value: 'sse' },
                  { label: $t('settings.mcpTransportStreamableHttp'), value: 'streamable-http' },
                ]" size="small" class="mcp-transport-select" />
                <n-switch v-model:value="srv.enabled" size="small" />
                <n-button size="small" quaternary type="error" @click="removeMcpServer(idx)">✕</n-button>
              </div>
              <!-- stdio fields -->
              <template v-if="srv.transport === 'stdio'">
                <n-input v-model:value="srv.command" :placeholder="$t('settings.mcpCommand')" size="small" />
                <n-input v-model:value="srv.args" type="textarea" :rows="2" :placeholder="$t('settings.mcpArgs')" size="small" spellcheck="false" />
                <n-input v-model:value="srv.env" type="textarea" :rows="2" :placeholder="$t('settings.mcpEnv')" size="small" spellcheck="false" />
              </template>
              <!-- HTTP fields -->
              <template v-else>
                <n-input v-model:value="srv.url" :placeholder="$t('settings.mcpUrl')" size="small" />
                <n-input v-model:value="srv.headers" type="textarea" :rows="2" :placeholder="$t('settings.mcpHeaders')" size="small" spellcheck="false" />
              </template>
            </div>
            <n-button size="small" dashed block @click="addMcpServer">{{ $t('settings.mcpAddServer') }}</n-button>
          </div>
          <!-- JSON mode -->
          <n-input
            v-else
            v-model:value="mcpConfigText"
            class="mcp-config-editor"
            type="textarea"
            :autosize="false"
            :rows="10"
            spellcheck="false"
            placeholder='{"mcpServers":{"serviceName":{"enabled":true,"transport":"streamable-http","url":"https://...","headers":{"Authorization":"Bearer ***"}}}}'
          />
          <div v-if="mcpEditMode === 'json'" :class="['mcp-json-check', mcpConfigValid ? 'valid' : 'invalid']">
            {{ mcpConfigValidationText }}
          </div>
          <div class="mcp-status-list">
            <div v-if="!mcpServers.length" class="saved-model-empty">{{ $t('settings.mcpEmpty') }}</div>
            <div v-for="srv in mcpServers" :key="srv.name" class="mcp-status-item">
              <span :class="['mcp-dot', srv.status]"></span>
              <span class="mcp-name">{{ srv.name }}</span>
              <span class="mcp-status">{{ srv.status }}</span>
              <span v-if="srv.transport" class="mcp-transport">{{ srv.transport }}</span>
              <span class="mcp-tools">{{ $t('tools.count', { count: srv.toolCount || 0 }) }}</span>
              <span v-if="srv.error" class="mcp-error" :title="srv.error">{{ srv.error }}</span>
            </div>
          </div>
        </section>

        <!-- About -->
        <section v-else-if="page === 'about'" class="settings-page">
          <div class="config-section-header">
            <div>
              <div class="config-section-title">{{ $t('settings.aboutTitle') }}</div>
              <div class="config-section-subtitle">{{ $t('settings.aboutSubtitle') }}</div>
            </div>
          </div>
          <div class="license-notice">
            <div class="license-notice-title">GNU General Public License v3.0 only</div>
            <p>{{ $t('settings.licenseFreedom') }}</p>
            <p>{{ $t('settings.licenseWarranty') }}</p>
            <p>{{ $t('settings.licenseSource') }}</p>
            <n-button secondary @click="openSourceRepository">{{ $t('settings.sourceLicense') }}</n-button>
          </div>
        </section>
      </n-form>
    </div>
    <template #footer>
      <n-space justify="end">
        <n-button @click="onClose">{{ $t('common.cancel') }}</n-button>
        <n-button type="primary" @click="onSave">{{ $t('settings.saveAppSettings') }}</n-button>
      </n-space>
    </template>
  </n-modal>

  <!-- Model editor sub-modal -->
  <n-modal
    :show="modelEditorVisible"
    preset="card"
    :title="modelEditorIndex >= 0 ? $t('settings.modelEdit') : $t('settings.modelAdd')"
    class="model-form-modal"
    :style="modelFormModalStyle"
    :mask-closable="false"
    @update:show="(v) => { if (!v) cancelModelDraft(); }"
  >
    <n-form :model="modelDraft" label-placement="top">
      <n-grid :cols="2" :x-gap="12">
        <n-form-item-gi :label="$t('settings.providerName')">
          <n-input v-model:value="modelDraft.providerName" placeholder="OpenAI Compatible" />
        </n-form-item-gi>
        <n-form-item-gi :label="$t('settings.apiFormat')">
          <n-select v-model:value="modelDraft.apiFormat" :options="apiFormatOptions" />
        </n-form-item-gi>
        <n-form-item-gi label="Model">
          <n-input v-model:value="modelDraft.model" :placeholder="modelPlaceholder(modelDraft.apiFormat)" />
        </n-form-item-gi>
        <n-form-item-gi :label="normalizeApiFormat(modelDraft.apiFormat) === 'anthropic_messages' ? $t('settings.baseUrlNoV1') : 'Base URL'">
          <n-input v-model:value="modelDraft.baseUrl" :placeholder="apiFormatDefaultBaseUrl(modelDraft.apiFormat)" />
        </n-form-item-gi>
        <n-form-item-gi label="API Key" :span="2">
          <n-input v-model:value="modelDraft.apiKey" type="password" show-password-on="click" />
        </n-form-item-gi>
        <n-form-item-gi label="Temperature">
          <n-input-number v-model:value="modelDraft.temperature" :min="0" :max="1" :step="0.1" style="width: 100%" />
        </n-form-item-gi>
        <n-form-item-gi label="Max Tokens">
          <n-input-number v-model:value="modelDraft.maxTokens" :min="1" :step="1024" style="width: 100%" />
        </n-form-item-gi>
        <n-form-item-gi :label="$t('settings.contextWindow')" :span="2">
          <n-input-number v-model:value="modelDraft.contextWindow" :min="1024" :step="1024" style="width: 100%" />
        </n-form-item-gi>
        <n-form-item-gi :label="$t('settings.reasoningTag')" :span="2">
          <n-input
            v-model:value="modelDraft.reasoningTag"
            :placeholder="$t('settings.reasoningTagHint')"
          />
        </n-form-item-gi>
      </n-grid>
      <n-alert v-if="normalizeApiFormat(modelDraft.apiFormat) === 'anthropic_messages'" type="info" :show-icon="false" class="model-format-hint">
        {{ $t('settings.anthropicHint') }}
      </n-alert>
    </n-form>
    <template #footer>
      <n-space justify="end">
        <n-button secondary :loading="testingModel" @click="testModelConnection">{{ $t('settings.testConnection') }}</n-button>
        <n-button @click="modelEditorVisible = false">{{ $t('common.cancel') }}</n-button>
        <n-button type="primary" @click="commitModelDraft">{{ modelEditorIndex >= 0 ? $t('settings.saveChanges') : $t('common.add') }}</n-button>
      </n-space>
    </template>
  </n-modal>
</template>

<script setup>
import { computed, reactive, ref, watch } from 'vue';
import { createDiscreteApi, darkTheme } from 'naive-ui';
import { naiveDateLocale, naiveLocale, t } from '../i18n.mjs';
import { BrowserOpenURL } from '../../wailsjs/runtime/runtime';
import {
  GetMcpConfig, GetMcpServers, SaveMcpConfig, RestartMcpServers,
  ListSkills, ActivateSkill, DeactivateSkill, ClearSkills, GetActiveSkills,
  ListTools, OpenPathInFileManager,
  TestModelConnection,
} from '../../wailsjs/go/main/App';

const { message } = createDiscreteApi(['message'], {
  configProviderProps: { theme: darkTheme, locale: naiveLocale, dateLocale: naiveDateLocale },
});

function openSourceRepository() {
  BrowserOpenURL('https://github.com/Bronya0/ally-agent');
}

const props = defineProps({
  visible: Boolean,
  configDraft: { type: Object, required: true },
});
const emit = defineEmits(['close', 'save', 'skills-changed', 'mcp-saved']);

// Deep-clone the config draft so changes don't mutate parent reactively until save
const draft = reactive(cloneConfigDraft(props.configDraft));

const settingsModalStyle = {
  width: 'min(820px, calc(100vw - 48px))',
  maxWidth: 'calc(100vw - 48px)',
};

const modelFormModalStyle = {
  width: 'min(580px, calc(100vw - 48px))',
  maxWidth: 'calc(100vw - 48px)',
};

const page = ref('general');
const modelEditorVisible = ref(false);
const modelEditorIndex = ref(-1);
const testingModel = ref(false);

const isWindows = computed(() => {
  return document.body.classList.contains('platform-windows') ||
    document.body.classList.contains('platform-win32');
});

function defaultModelDraft(source = {}) {
  return {
    providerName: draft?.providerName || 'OpenAI Compatible',
    apiFormat: normalizeApiFormat(draft?.apiFormat),
    baseUrl: draft?.baseUrl || '',
    apiKey: draft?.apiKey || '',
    model: '',
    temperature: draft?.temperature ?? 0.2,
    maxTokens: draft?.maxTokens || 128000,
    contextWindow: draft?.contextWindow || 1048576,
    ...source,
    reasoningTag: String(source.reasoningTag || draft?.reasoningTag || 'reasoning_content').trim() || 'reasoning_content',
  };
}

const modelDraft = reactive(defaultModelDraft());

const activeProviderTab = ref('');

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
    case 'openai_responses': return 'https://api.openai.com/v1';
    case 'anthropic_messages': return 'https://api.anthropic.com';
    default: return 'https://api.deepseek.com';
  }
}

function modelPlaceholder(value) {
  switch (normalizeApiFormat(value)) {
    case 'openai_responses': return 'gpt-4.1-mini';
    case 'anthropic_messages': return 'claude-sonnet-5';
    default: return 'deepseek-v4-flash';
  }
}

function normalizedProviderName(value) {
  return (value || '').trim() || 'OpenAI Compatible';
}

const providerTabs = computed(() => {
  const groups = new Map();
  (draft.models || []).forEach((model, index) => {
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
  const candidates = [preferred, activeProviderTab.value, normalizedProviderName(draft.providerName), tabs[0]?.name || ''];
  activeProviderTab.value = candidates.find((name) => name && names.has(name)) || tabs[0].name;
}

function assignModelDraft(source = {}) {
  Object.assign(modelDraft, defaultModelDraft({
    ...source,
    apiFormat: normalizeApiFormat(source.apiFormat || draft.apiFormat),
  }));
}

function startAddModelDraft() {
  modelEditorIndex.value = -1;
  const provider = activeProviderTab.value || draft.providerName || 'OpenAI Compatible';
  assignModelDraft({
    providerName: provider,
    apiFormat: normalizeApiFormat(draft.apiFormat),
    baseUrl: draft.baseUrl || '',
    apiKey: draft.apiKey || '',
    maxTokens: draft.maxTokens || 128000,
    contextWindow: draft.contextWindow || 1048576,
  });
  modelEditorVisible.value = true;
}

function editModelDraft(index) {
  if (!draft.models || !draft.models[index]) return;
  modelEditorIndex.value = index;
  assignModelDraft(draft.models[index]);
  modelEditorVisible.value = true;
}

function cancelModelDraft() {
  modelEditorVisible.value = false;
  modelEditorIndex.value = -1;
}

async function testModelConnection() {
  if (testingModel.value) return;
  const model = (modelDraft.model || '').trim();
  const apiKey = (modelDraft.apiKey || '').trim();
  if (!model) {
    message.warning(t('app.config.modelRequired'));
    return;
  }
  if (!apiKey) {
    message.warning(t('settings.apiKeyRequired'));
    return;
  }
  testingModel.value = true;
  try {
    await TestModelConnection({
      providerName: normalizedProviderName(modelDraft.providerName),
      apiFormat: normalizeApiFormat(modelDraft.apiFormat),
      baseUrl: (modelDraft.baseUrl || '').trim(),
      apiKey,
      model,
      temperature: modelDraft.temperature ?? 0.2,
      maxTokens: modelDraft.maxTokens || 8192,
      contextWindow: modelDraft.contextWindow || 1048576,
      reasoningTag: modelDraft.reasoningTag || 'reasoning_content',
    });
    message.success(t('settings.connectionSuccess'));
  } catch (err) {
    message.error(t('settings.connectionFailed', { error: err }));
  } finally {
    testingModel.value = false;
  }
}

function commitModelDraft() {
  if (!draft.models) draft.models = [];
  const model = (modelDraft.model || '').trim();
  if (!model) {
    message.warning(t('app.config.modelRequired'));
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
    temperature: modelDraft.temperature ?? draft.temperature ?? 0.2,
    maxTokens: modelDraft.maxTokens || draft.maxTokens || 128000,
    contextWindow: modelDraft.contextWindow || draft.contextWindow || 1048576,
    reasoningTag: modelDraft.reasoningTag || 'reasoning_content',
  };
  const wasActive = modelEditorIndex.value >= 0 && isDraftModelActive(draft.models[modelEditorIndex.value]);
  if (modelEditorIndex.value >= 0) {
    draft.models.splice(modelEditorIndex.value, 1, nextModel);
  } else {
    draft.models.push(nextModel);
  }
  if (wasActive) applyModelToDraft(nextModel);
  alignActiveProviderTab(providerName);
  modelEditorVisible.value = false;
}

function applyModelToDraft(model) {
  if (!model) return;
  draft.providerName = normalizedProviderName(model.providerName);
  draft.apiFormat = normalizeApiFormat(model.apiFormat);
  draft.baseUrl = model.baseUrl || '';
  draft.apiKey = model.apiKey || '';
  draft.model = model.model || '';
  draft.temperature = model.temperature ?? draft.temperature ?? 0.2;
  draft.maxTokens = model.maxTokens || draft.maxTokens || 128000;
  draft.contextWindow = model.contextWindow || draft.contextWindow || 1048576;
  draft.reasoningTag = model.reasoningTag || 'reasoning_content';
  alignActiveProviderTab(normalizedProviderName(model.providerName));
}

function isDraftModelActive(model) {
  if (!model) return false;
  return normalizedProviderName(model.providerName) === normalizedProviderName(draft.providerName)
    && normalizeApiFormat(model.apiFormat) === normalizeApiFormat(draft.apiFormat)
    && (model.model || '') === (draft.model || '')
    && (model.baseUrl || '') === (draft.baseUrl || '');
}

function removeModelDraft(index) {
  if (!draft.models) return;
  const removed = draft.models[index];
  const removedProvider = normalizedProviderName(removed?.providerName);
  draft.models.splice(index, 1);
  if (modelEditorIndex.value === index) cancelModelDraft();
  else if (modelEditorIndex.value > index) modelEditorIndex.value -= 1;
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

// MCP state
const mcpConfigText = ref('');
const mcpServers = ref([]);
const mcpLoading = ref(false);
const mcpEditMode = ref('form'); // 'form' or 'json'
const mcpFormServers = ref([]); // array of {name, command, args, env, transport, url, headers, enabled}

const mcpConfigParseResult = computed(() => {
  const raw = mcpConfigText.value || '';
  if (!raw.trim()) return { valid: false, text: t('settings.jsonEmpty') };
  try {
    const parsed = JSON.parse(raw);
    if (parsed && typeof parsed === 'object' && parsed.mcpServers) {
      return { valid: true, text: t('settings.jsonValid') };
    }
    return { valid: false, text: t('settings.jsonNeedsServers') };
  } catch (e) {
    return { valid: false, text: t('settings.jsonError', { error: e.message }) };
  }
});
const mcpConfigValid = computed(() => mcpConfigParseResult.value.valid);
const mcpConfigValidationText = computed(() => mcpConfigParseResult.value.text);
const mcpSaveEnabled = computed(() => mcpEditMode.value === 'form' || mcpConfigValid.value);

function cloneConfigDraft(source) {
  const next = JSON.parse(JSON.stringify(source || {}));
  next.reasoningTag = String(next.reasoningTag || '').trim() || 'reasoning_content';
  next.models = Array.isArray(next.models) ? next.models.map((model) => ({
    ...model,
    reasoningTag: String(model?.reasoningTag || '').trim() || 'reasoning_content',
  })) : [];
  return next;
}

function syncDraftFromProps() {
  const next = cloneConfigDraft(props.configDraft);
  for (const key of Object.keys(draft)) {
    delete draft[key];
  }
  Object.assign(draft, next);
  cancelModelDraft();
  alignActiveProviderTab(normalizedProviderName(draft.providerName));
}

async function loadMcpConfig() {
  mcpLoading.value = true;
  try {
    mcpConfigText.value = await GetMcpConfig();
    syncJsonToForm();
    mcpServers.value = await GetMcpServers() || [];
  } catch (err) {
    message.error(t('app.mcp.readFailed', { error: err }));
  } finally {
    mcpLoading.value = false;
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
    message.success(t('app.mcp.saved'));
    emit('mcp-saved');
  } catch (err) {
    message.error(t('app.mcp.saveFailed', { error: err }));
  } finally {
    mcpLoading.value = false;
  }
}

async function saveMcpConfig() {
  if (mcpEditMode.value === 'form') {
    syncFormToJson();
  }
  await saveMcpConfigText();
}

function switchMcpMode(mode) {
  if (mode === 'form') {
    // Sync from JSON to form
    syncJsonToForm();
  } else {
    // Sync from form to JSON
    syncFormToJson();
  }
  mcpEditMode.value = mode;
}

function syncJsonToForm() {
  try {
    const parsed = JSON.parse(mcpConfigText.value || '{}');
    const servers = parsed.mcpServers || {};
    mcpFormServers.value = Object.entries(servers).map(([name, cfg]) => ({
      name,
      command: cfg.command || '',
      args: (cfg.args || []).join('\n'),
      env: Object.entries(cfg.env || {}).map(([k, v]) => `${k}=${v}`).join('\n'),
      transport: normalizeMcpTransport(cfg),
      url: cfg.url || '',
      headers: Object.entries(cfg.headers || {}).map(([k, v]) => `${k}: ${v}`).join('\n'),
      enabled: cfg.enabled !== false,
    }));
  } catch {
    // keep existing form data on parse error
  }
}

function syncFormToJson() {
  const servers = {};
  for (const srv of mcpFormServers.value) {
    if (!srv.name?.trim()) continue;
    const cfg = {};
    if (srv.transport !== 'stdio') {
      cfg.transport = srv.transport === 'sse' ? 'sse' : 'streamable-http';
      if (srv.url?.trim()) cfg.url = srv.url.trim();
      const headers = {};
      (srv.headers || '').split('\n').forEach(line => {
        const idx = line.indexOf(':');
        if (idx > 0) headers[line.slice(0, idx).trim()] = line.slice(idx + 1).trim();
      });
      if (Object.keys(headers).length) cfg.headers = headers;
    } else {
      if (srv.command?.trim()) cfg.command = srv.command.trim();
      const args = (srv.args || '').split('\n').map(a => a.trim()).filter(Boolean);
      if (args.length) cfg.args = args;
      const env = {};
      (srv.env || '').split('\n').forEach(line => {
        const idx = line.indexOf('=');
        if (idx > 0) env[line.slice(0, idx).trim()] = line.slice(idx + 1).trim();
      });
      if (Object.keys(env).length) cfg.env = env;
    }
    cfg.enabled = srv.enabled !== false;
    servers[srv.name.trim()] = cfg;
  }
  mcpConfigText.value = JSON.stringify({ mcpServers: servers }, null, 2);
}

function normalizeMcpTransport(cfg = {}) {
  const value = String(cfg.transport || '').trim().toLowerCase();
  if (value === 'sse') return 'sse';
  if (['streamable-http', 'http', 'rest'].includes(value)) return 'streamable-http';
  if (!value && cfg.url && !cfg.command) return 'streamable-http';
  return 'stdio';
}

function addMcpServer() {
  mcpFormServers.value.push({
    name: '',
    command: '',
    args: '',
    env: '',
    transport: 'stdio',
    url: '',
    headers: '',
    enabled: true,
  });
}

function removeMcpServer(index) {
  mcpFormServers.value.splice(index, 1);
}

// Skills state
const availableSkills = ref([]);
const activeSkillNames = ref([]);
const skillsLoading = ref(false);
const skillToggleInFlight = ref('');

function normalizeSkillName(name) {
  return String(name || '').toLowerCase().replace(/[-\s]+/g, '-').replace(/[^a-z0-9-]/g, '');
}

function isSkillActive(name) {
  const target = normalizeSkillName(name);
  return activeSkillNames.value.some((item) => normalizeSkillName(item) === target);
}

async function handleOpenSkillPath(sk) {
  const path = sk?.path || sk?.dir;
  if (!path) return;
  try {
    await OpenPathInFileManager(path);
  } catch (err) {
    message.warning(t('settings.openSkillPathFailed', { error: err }));
  }
}

async function refreshSkillState() {
  skillsLoading.value = true;
  try {
    const [skills, active] = await Promise.all([ListSkills(), GetActiveSkills()]);
    availableSkills.value = skills || [];
    activeSkillNames.value = active || [];
  } catch (err) {
    message.error(t('app.skills.stateFailed', { error: err }));
  } finally {
    skillsLoading.value = false;
  }
}

async function toggleSkillFromSettings(skill, active) {
  const skillName = skill?.name || '';
  if (!skillName) return;
  skillToggleInFlight.value = skillName;
  try {
    if (active) {
      await ActivateSkill(skillName);
    } else {
      await DeactivateSkill(skillName);
    }
    await refreshSkillState();
    emit('skills-changed');
  } catch (err) {
    message.error(t('settings.skillToggleFailed', {
      action: active ? t('settings.skillEnable') : t('settings.skillDisable'),
      error: err,
    }));
  } finally {
    skillToggleInFlight.value = '';
  }
}

async function clearLoadedSkills(announce = true) {
  try {
    await ClearSkills();
    await refreshSkillState();
    emit('skills-changed');
    message.success(t('app.skills.deactivatedToast'));
  } catch (err) {
    message.error(t('app.skills.deactivateFailed', { error: err }));
  }
}

function onClose() {
  emit('close');
}

function onSave() {
  emit('save', { ...draft });
}

// Sync MCP when modal opens
watch(() => props.visible, (visible) => {
  if (visible) {
    syncDraftFromProps();
    loadMcpConfig();
    refreshSkillState();
  }
});
</script>

<style scoped>
.config-modal {
  width: 820px;
  max-width: calc(100vw - 48px);
}

.settings-layout {
  display: flex;
  gap: 20px;
  min-height: 340px;
}

.settings-nav {
  width: 120px;
  flex-shrink: 0;
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.settings-nav-item {
  display: block;
  width: 100%;
  padding: 8px 10px;
  border: none;
  border-radius: 6px;
  background: transparent;
  color: #8a8a8a;
  cursor: pointer;
  text-align: left;
  transition: background 0.12s, color 0.12s;
  line-height: 1.3;
  --wails-draggable: no-drag;
}

.settings-nav-item:hover {
  background: rgba(255, 255, 255, 0.05);
  color: #d4d4d4;
}

.settings-nav-item.active {
  background: rgba(255, 255, 255, 0.08);
  color: #f5f5f5;
}

.settings-nav-title {
  display: block;
  font-size: 13px;
  font-weight: 600;
}

.settings-nav-desc {
  display: block;
  font-size: 11px;
  color: #737373;
  margin-top: 1px;
}

.settings-content {
  flex: 1;
  min-width: 0;
  max-height: 460px;
  overflow-y: auto;
}

.settings-page {
  padding: 0;
}

.license-notice {
  padding: 18px;
  border: 1px solid rgba(255, 255, 255, 0.1);
  border-radius: 10px;
  background: rgba(255, 255, 255, 0.025);
  color: rgba(255, 255, 255, 0.72);
  line-height: 1.65;
}

.license-notice-title {
  margin-bottom: 10px;
  color: rgba(255, 255, 255, 0.94);
  font-size: 15px;
  font-weight: 650;
}

.model-format-hint {
  margin-top: 4px;
}

.config-section-header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  margin-bottom: 16px;
}

.config-section-title {
  font-size: 15px;
  font-weight: 600;
  color: #f5f5f5;
}

.config-section-subtitle {
  font-size: 12px;
  color: #8a8a8a;
  margin-top: 2px;
}

.settings-toggle-row {
  display: flex;
  align-items: center;
  gap: 12px;
}

.settings-toggle-hint {
  font-size: 12px;
  color: #8a8a8a;
  line-height: 1.5;
}

.settings-field-stack {
  width: 100%;
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.settings-field-hint,
.mcp-save-scope {
  color: #777;
  font-size: 11px;
  line-height: 1.45;
}

.current-model-panel {
  background: rgba(255, 255, 255, 0.04);
  border: 1px solid rgba(255, 255, 255, 0.08);
  border-radius: 8px;
  padding: 10px 12px;
  margin-bottom: 12px;
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
}

.current-model-main {
  flex: 1;
  min-width: 0;
}

.current-model-label {
  font-size: 11px;
  color: #737373;
  text-transform: uppercase;
  letter-spacing: 0.5px;
  margin-bottom: 2px;
}

.current-model-name {
  font-size: 14px;
  font-weight: 600;
  color: #f5f5f5;
}

.current-model-url {
  font-size: 12px;
  color: #8a8a8a;
  margin-top: 1px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.current-model-meta {
  flex-shrink: 0;
  display: flex;
  flex-direction: column;
  gap: 2px;
  font-size: 11px;
  color: #737373;
  text-align: right;
}

.saved-model-empty {
  color: #737373;
  font-size: 13px;
  padding: 28px 0;
  text-align: center;
}

.saved-model-list {
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.saved-model-item {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 8px 10px;
  border-radius: 6px;
  transition: background 0.12s;
  gap: 10px;
  border: 1px solid transparent;
}

.saved-model-item:hover {
  background: rgba(255, 255, 255, 0.04);
}

.saved-model-item.active {
  border-color: rgba(255, 255, 255, 0.15);
  background: rgba(255, 255, 255, 0.06);
}

.saved-model-main {
  flex: 1;
  min-width: 0;
}

.saved-model-name {
  font-size: 13px;
  font-weight: 500;
  color: #f5f5f5;
}

.saved-model-meta {
  font-size: 12px;
  color: #8a8a8a;
  margin-top: 1px;
}

.saved-model-url {
  font-size: 11px;
  color: #737373;
  margin-top: 1px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  max-width: 420px;
}

.skill-settings-list {
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.skill-settings-item {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 8px 10px;
  border-radius: 6px;
  transition: background 0.12s;
  gap: 10px;
}

.skill-settings-item:hover {
  background: rgba(255, 255, 255, 0.04);
}

.skill-settings-item.active {
  background: rgba(255, 255, 255, 0.06);
}

.skill-settings-main {
  flex: 1;
  min-width: 0;
}

.skill-title-row {
  display: flex;
  align-items: center;
  gap: 6px;
  flex-wrap: wrap;
}

.skill-name {
  font-size: 13px;
  font-weight: 500;
  color: #f5f5f5;
}

.skill-badge {
  display: inline-block;
  font-size: 10px;
  padding: 1px 5px;
  border-radius: 4px;
  font-weight: 500;
  text-transform: uppercase;
  letter-spacing: 0.3px;
  background: rgba(255, 255, 255, 0.08);
  color: #8a8a8a;
}

.skill-badge.user {
  background: #2a3a5c;
  color: #8ab4ff;
}

.skill-badge.project {
  background: #2a4a3a;
  color: #8fd4b4;
}

.skill-badge.loaded {
  background: #3a4a2a;
  color: #b8d4a0;
}

.skill-description {
  font-size: 12px;
  color: #8a8a8a;
  margin-top: 2px;
}

.skill-meta {
  font-size: 11px;
  color: #555;
  margin-top: 1px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  max-width: 320px;
}

.skill-meta.clickable {
  cursor: pointer;
  color: #888;
}

.skill-meta.clickable:hover {
  color: #18a058;
  text-decoration: underline;
}

.mcp-config-editor {
  font-family: var(--ally-mono-font);
  font-size: 13px;
  line-height: 1.5;
  margin-bottom: 4px;
}

.mcp-form-mode {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.mcp-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  margin: -4px 0 6px;
}

.mcp-toolbar-actions {
  display: flex;
  align-items: center;
  gap: 8px;
  flex: none;
}

.mcp-save-scope {
  margin-bottom: 12px;
}

.mcp-server-card {
  border: 1px solid rgba(255, 255, 255, 0.08);
  border-radius: 8px;
  padding: 12px;
  display: flex;
  flex-direction: column;
  gap: 8px;
  background: rgba(255, 255, 255, 0.02);
}

.mcp-server-card-header {
  display: flex;
  align-items: center;
  gap: 8px;
}

.mcp-server-name-input {
  flex: 1;
  min-width: 140px;
}

.mcp-transport-select {
  width: 164px;
  flex: none;
}

.mcp-server-card-header > :last-child {
  margin-left: auto;
}

.mcp-json-check {
  font-size: 12px;
  padding: 2px 0 6px;
}

.mcp-json-check.valid {
  color: #8fd4b4;
}

.mcp-json-check.invalid {
  color: #f4a4a4;
}

.mcp-status-list {
  margin-top: 8px;
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.mcp-status-item {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 13px;
  padding: 6px 8px;
  background: rgba(255, 255, 255, 0.03);
  border-radius: 6px;
}

.mcp-dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  flex-shrink: 0;
}

.mcp-dot.connected {
  background: #8fd4b4;
}

.mcp-dot.connecting {
  background: #f0d080;
  animation: mcp-pulse 1.2s ease-in-out infinite;
}

.mcp-dot.failed {
  background: #f4a4a4;
}

@keyframes mcp-pulse {
  0%, 100% { opacity: 0.4; }
  50% { opacity: 1; }
}

.mcp-name {
  font-weight: 500;
  color: #f5f5f5;
}

.mcp-status {
  color: #8a8a8a;
  font-size: 12px;
}

.mcp-transport {
  color: #6f91b8;
  font-family: var(--ally-mono-font);
  font-size: 11px;
}

.mcp-tools {
  color: #737373;
  font-size: 11px;
  margin-left: auto;
}

.mcp-error {
  color: #f4a4a4;
  font-size: 11px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  max-width: 200px;
}

.model-form-modal {
  width: 580px;
  max-width: calc(100vw - 48px);
}

@media (max-width: 640px) {
  .config-modal,
  .model-form-modal {
    max-width: calc(100vw - 24px);
  }

  .mcp-toolbar,
  .mcp-server-card-header {
    align-items: stretch;
    flex-direction: column;
  }

  .mcp-toolbar-actions,
  .mcp-server-name-input,
  .mcp-transport-select {
    width: 100%;
  }

  .settings-layout {
    flex-direction: column;
    gap: 14px;
  }

  .settings-nav {
    width: 100%;
    flex-direction: row;
    overflow-x: auto;
    padding-bottom: 2px;
  }

  .settings-nav-item {
    min-width: 92px;
  }

  .settings-content {
    max-height: min(460px, calc(100vh - 260px));
  }
}
</style>
