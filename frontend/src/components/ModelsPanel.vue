<!--
SPDX-License-Identifier: GPL-3.0-only

Copyright (C) 2026 tangssst <tangssst@qq.com>
GitHub: https://github.com/Bronya0/ally-agent

This file is part of ally-agent, licensed under the GNU General
Public License v3. See the LICENSE file for details.
-->

<template>
  <!-- Inline models page: rendered inside the main-area container (App.vue
       mode === 'models' via v-show). Always mounted so the provider tabs,
       editor draft, and lazy catalog survive mode switches. Extracted from
       the old Settings → Models page; every mutation saves immediately
       through the `save` emit (App.vue onSettingsSave). -->
  <div class="config-inline-panel">
    <header class="config-inline-header">
      <span class="config-inline-title">{{ t('app.mode.models') }}</span>
      <div class="panel-header-actions">
        <n-button size="small" secondary @click="openModelImport">{{ t('settings.modelImport') }}</n-button>
        <n-button size="small" secondary :disabled="!draft.models?.length" @click="exportModelConfigs">{{ t('settings.modelExport') }}</n-button>
        <n-button size="small" type="primary" @click="startAddModelDraft">{{ t('settings.modelAdd') }}</n-button>
      </div>
    </header>

    <div class="panel-scroll-body">
      <div class="config-section-header">
        <div>
          <div class="config-section-title">{{ t('settings.modelsTitle') }}</div>
          <div class="config-section-subtitle">{{ t('settings.modelsSubtitle') }}</div>
        </div>
      </div>

      <input
        ref="modelImportInput"
        class="model-import-input"
        type="file"
        accept="application/json,.json"
        @change="importModelConfigs"
      />

      <div class="current-model-panel">
        <div class="current-model-main">
          <div class="current-model-label">{{ t('settings.modelCurrent') }}</div>
          <div class="current-model-name">{{ draft.providerName || 'Provider' }} · {{ draft.model || t('settings.modelNone') }}</div>
          <div class="current-model-url">{{ apiFormatLabel(draft.apiFormat) }}</div>
          <div class="current-model-url">{{ draft.baseUrl || t('settings.baseUrlNone') }}</div>
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
            <div v-for="item in tab.models" :key="item.index" class="saved-model-item">
              <div class="saved-model-main">
                <div class="saved-model-name">{{ normalizedProviderName(item.model.providerName) }} · {{ item.model.model || t('settings.modelNone') }}</div>
                <div class="saved-model-meta">{{ apiFormatLabel(item.model.apiFormat) }} · {{ item.model.model || 'Model' }}</div>
                <div class="saved-model-url">{{ item.model.baseUrl || t('settings.baseUrlNone') }}</div>
                <div class="saved-model-url">max {{ item.model.maxTokens || '-' }} · context {{ item.model.contextWindow || '-' }}</div>
              </div>
              <n-space :size="4">
                <n-button size="tiny" quaternary @click="editModelDraft(item.index)">{{ t('common.edit') }}</n-button>
                <n-button size="tiny" type="error" quaternary @click="removeModelDraft(item.index)">{{ t('common.delete') }}</n-button>
              </n-space>
            </div>
          </div>
        </n-tab-pane>
      </n-tabs>
      <div v-else class="saved-model-empty">{{ t('settings.modelsEmpty') }}</div>
    </div>

    <!-- Model editor sub-modal -->
    <n-modal
      :show="modelEditorVisible"
      preset="card"
      :title="modelEditorIndex >= 0 ? t('settings.modelEdit') : t('settings.modelAdd')"
      class="model-form-modal"
      :style="modelFormModalStyle"
      :mask-closable="false"
      @update:show="(v) => { if (!v) cancelModelDraft(); }"
    >
      <n-form :model="modelDraft" label-placement="top">
        <n-grid :cols="2" :x-gap="12">
          <n-form-item-gi :label="t('settings.providerPreset')" :span="2">
            <n-select
              v-model:value="selectedCatalogProviderId"
              :options="catalogProviderOptions"
              :loading="modelCatalogLoading"
              filterable
              :placeholder="t('settings.providerPresetPlaceholder')"
              @update:value="selectCatalogProvider"
            />
          </n-form-item-gi>
          <n-form-item-gi v-if="selectedCatalogProvider" :label="t('settings.catalogModel')" :span="2">
            <n-select
              :value="modelDraft.model"
              :options="selectedCatalogModelOptions"
              filterable
              tag
              :placeholder="t('settings.catalogModelPlaceholder')"
              @update:value="selectCatalogModel"
            />
          </n-form-item-gi>
          <n-form-item-gi :label="t('settings.providerName')">
            <n-input v-model:value="modelDraft.providerName" placeholder="OpenAI Compatible" />
          </n-form-item-gi>
          <n-form-item-gi :label="t('settings.apiFormat')">
            <n-select v-model:value="modelDraft.apiFormat" :options="apiFormatOptions" />
          </n-form-item-gi>
          <n-form-item-gi v-if="!selectedCatalogProvider" label="Model">
            <n-select
              v-model:value="modelDraft.model"
              class="model-input-select"
              :options="remoteModelOptions"
              filterable
              tag
              :placeholder="modelPlaceholder(modelDraft.apiFormat)"
              :loading="modelListLoading"
              @update:value="onModelDraftSelected"
            >
              <template #action>
                <n-button
                  size="tiny"
                  block
                  secondary
                  :loading="modelListLoading"
                  @click="fetchRemoteModels"
                >
                  <template #icon><CloudDownloadOutlined /></template>
                  {{ modelListLoading ? t('settings.fetchingModels') : t('settings.fetchModels') }}
                </n-button>
              </template>
            </n-select>
          </n-form-item-gi>
          <n-form-item-gi :label="normalizeApiFormat(modelDraft.apiFormat) === 'anthropic_messages' ? t('settings.baseUrlNoV1') : 'Base URL'">
            <n-input v-model:value="modelDraft.baseUrl" :placeholder="apiFormatDefaultBaseUrl(modelDraft.apiFormat)" autocomplete="off" />
          </n-form-item-gi>
          <n-form-item-gi :label="t('settings.apiKeys')" :span="2">
            <div class="api-key-list">
              <div v-for="(key, ki) in modelDraft.apiKeys" :key="ki" class="api-key-row">
                <span class="api-key-index">{{ ki + 1 }}</span>
                <n-input
                  v-model:value="modelDraft.apiKeys[ki]"
                  type="password"
                  show-password-on="click"
                  autocomplete="new-password"
                  :placeholder="t('settings.apiKeyPlaceholder')"
                />
                <n-button
                  quaternary
                  size="small"
                  :disabled="(modelDraft.apiKeys || []).length <= 1"
                  :title="t('settings.apiKeyRemove')"
                  @click="removeModelApiKey(ki)"
                >
                  <template #icon><CloseOutlined /></template>
                </n-button>
              </div>
              <n-button size="small" dashed class="api-key-add" @click="addModelApiKey">
                <template #icon><PlusOutlined /></template>
                {{ t('settings.apiKeyAdd') }}
              </n-button>
              <div class="api-key-hint">{{ t('settings.apiKeysHint') }}</div>
            </div>
          </n-form-item-gi>
          <n-form-item-gi label="Max Tokens" :span="1">
            <n-select
              :value="modelDraft.maxTokens"
              :options="maxTokensOptions"
              class="model-input-select"
              filterable
              tag
              @update:value="onMaxTokensSelected"
            />
          </n-form-item-gi>
          <n-form-item-gi :label="t('settings.reasoningTag')" :span="1">
            <n-select
              :value="modelDraft.reasoningTag"
              :options="reasoningTagOptions"
              class="model-input-select"
              filterable
              tag
              :placeholder="t('settings.reasoningTagHint')"
              @update:value="onReasoningTagSelected"
            />
          </n-form-item-gi>
          <n-form-item-gi
            v-if="normalizeApiFormat(modelDraft.apiFormat) === 'openai_chat'"
            :label="t('settings.tokenParam')"
            :span="2"
          >
            <n-select v-model:value="modelDraft.tokenParam" :options="tokenParamOptions" />
          </n-form-item-gi>
          <n-form-item-gi :label="t('settings.reasoningEffort')" :span="1">
            <n-select v-model:value="modelDraft.reasoningEffort" :options="reasoningEffortOptions" />
          </n-form-item-gi>
          <n-form-item-gi :label="t('settings.contextWindow')" :span="1">
            <n-select
              :value="modelDraft.contextWindow"
              :options="contextWindowOptions"
              class="model-input-select"
              filterable
              tag
              @update:value="onContextWindowSelected"
            />
          </n-form-item-gi>
        </n-grid>
        <!-- Advanced: per-model custom HTTP headers (collapsed by default). -->
        <div class="model-advanced-toggle">
          <button type="button" class="model-advanced-button" @click="modelAdvancedOpen = !modelAdvancedOpen">
            <span class="model-advanced-arrow" :class="{ open: modelAdvancedOpen }">›</span>
            <span>{{ t('settings.modelAdvanced') }}</span>
            <span v-if="customHeaderRowCount" class="model-advanced-count">{{ customHeaderRowCount }}</span>
          </button>
        </div>
        <div v-if="modelAdvancedOpen" class="model-advanced-body">
          <n-form-item :label="t('settings.customHeaders')" :show-feedback="false">
            <div class="api-key-list custom-header-list">
              <div v-for="(row, hi) in customHeaderRows" :key="row.id" class="api-key-row custom-header-row">
                <n-input
                  v-model:value="row.name"
                  class="custom-header-name"
                  :placeholder="t('settings.customHeaderName')"
                  spellcheck="false"
                  @blur="row.name = row.name.trim()"
                />
                <n-input
                  v-model:value="row.value"
                  class="custom-header-value"
                  :placeholder="t('settings.customHeaderValue')"
                  spellcheck="false"
                  @blur="row.value = row.value.trim()"
                />
                <n-button
                  quaternary
                  size="small"
                  :title="t('settings.apiKeyRemove')"
                  @click="removeCustomHeaderRow(row.id)"
                >
                  <template #icon><CloseOutlined /></template>
                </n-button>
              </div>
              <n-button size="small" dashed class="api-key-add" @click="addCustomHeaderRow">
                <template #icon><PlusOutlined /></template>
                {{ t('settings.customHeaderAdd') }}
              </n-button>
              <div class="api-key-hint">{{ t('settings.customHeadersHint') }}</div>
            </div>
          </n-form-item>
        </div>
        <n-alert v-if="selectedCatalogProvider" type="info" :show-icon="false" class="model-format-hint">
          <span>{{ t('settings.providerPresetHint', { provider: selectedCatalogProvider.name }) }}</span>
          <n-button v-if="selectedCatalogProvider.doc" text type="primary" class="provider-doc-button" @click="openProviderDocumentation">
            {{ t('settings.providerDocumentation') }}
          </n-button>
        </n-alert>
        <n-alert v-else-if="normalizeApiFormat(modelDraft.apiFormat) === 'anthropic_messages'" type="info" :show-icon="false" class="model-format-hint">
          {{ t('settings.anthropicHint') }}
        </n-alert>
      </n-form>
      <template #footer>
        <n-space justify="end">
          <n-button secondary :loading="testingModel" @click="testModelConnection">{{ t('settings.testConnection') }}</n-button>
          <n-button @click="modelEditorVisible = false">{{ t('common.cancel') }}</n-button>
          <n-button type="primary" @click="commitModelDraft">{{ modelEditorIndex >= 0 ? t('settings.saveChanges') : t('common.add') }}</n-button>
        </n-space>
      </template>
    </n-modal>
  </div>
</template>

<script setup>
import { computed, reactive, ref, watch } from 'vue';
import { useMessage } from 'naive-ui';
import { reasoningEffortLabel, t } from '../i18n.mjs';
import { buildModelConfigExport, mergeModelConfigs, modelConfigIdentity, normalizeApiKeysArray, normalizeCustomHeaders, normalizeReasoningEffort, parseModelConfigImport, reasoningEffortLevels } from '../utils/modelConfigIO.mjs';
import { saveTextFile } from '../utils/download.mjs';
import CloseOutlined from '@vicons/antd/CloseOutlined';
import PlusOutlined from '@vicons/antd/PlusOutlined';
import CloudDownloadOutlined from '@vicons/antd/CloudDownloadOutlined';
import {
  CUSTOM_PROVIDER_ID,
  applyCatalogPreset,
  findCatalogModel,
  findCatalogProvider,
  providerCatalogOptions,
  providerModelOptions,
} from '../utils/modelProviderCatalog.mjs';
import { Browser } from '@wailsio/runtime';
import { TestModelConnection, FetchModelList } from '../../bindings/ally-dev/internal/app/app';

// Inline models page (App.vue mode === 'models'): owns the provider-grouped
// preset list, the model editor sub-modal, and the lazy provider catalog.
// Mutations (add/edit/remove/import) save immediately through the `save`
// emit, which lands in App.vue's onSettingsSave — the same path the old
// Settings → Models page used, including silent saves without a toast.
const props = defineProps({
  show: { type: Boolean, default: false },
  configDraft: { type: Object, required: true },
});
const emit = defineEmits(['save']);
const message = useMessage();

// Deep-clone the config draft so changes don't mutate the parent reactively
// until a save emits. Re-synced from the parent every time the page becomes
// visible — models edits always save immediately, so there are no unsaved
// draft edits to preserve across mode switches.
const draft = reactive(cloneConfigDraft(props.configDraft));

const modelFormModalStyle = {
  width: 'min(580px, calc(100vw - 48px))',
  maxWidth: 'calc(100vw - 48px)',
};

const modelEditorVisible = ref(false);
const modelEditorIndex = ref(-1);
const modelCatalog = ref({ providers: [] });
const modelCatalogLoading = ref(false);
const selectedCatalogProviderId = ref(CUSTOM_PROVIDER_ID);
const modelImportInput = ref(null);
const testingModel = ref(false);

function defaultModelDraft(source = {}) {
  // When the source explicitly carries key fields (even empty), respect them:
  // "add new model" passes blank keys and must not inherit the current
  // draft's credentials. Only a source without key fields at all falls back
  // to the draft keys.
  const sourceHasKeyField = 'apiKeys' in source || 'apiKey' in source;
  const rawKeys = sourceHasKeyField
    ? (Array.isArray(source.apiKeys) && source.apiKeys.length
      ? source.apiKeys
      : source.apiKey
        ? [source.apiKey]
        : [])
    : (Array.isArray(draft?.apiKeys) && draft.apiKeys.length
      ? draft.apiKeys
      : draft?.apiKey ? [draft.apiKey] : []);
  const normalizedKeys = normalizeModelApiKeys(rawKeys);
  return {
    providerName: draft?.providerName || 'OpenAI Compatible',
    apiFormat: normalizeApiFormat(draft?.apiFormat),
    baseUrl: draft?.baseUrl || '',
    apiKey: draft?.apiKey || '',
    model: '',
    temperature: draft?.temperature ?? 0.2,
    ...source,
    // Keep at least one (possibly empty) row so the form always shows a key
    // input; empty strings are stripped again on save/test.
    apiKeys: normalizedKeys.length ? normalizedKeys : [''],
    maxTokens: Number.isFinite(Number(source.maxTokens)) && Number(source.maxTokens) > 0 ? Number(source.maxTokens) : (draft?.maxTokens || 131072),
    contextWindow: Number.isFinite(Number(source.contextWindow)) && Number(source.contextWindow) > 0 ? Number(source.contextWindow) : (draft?.contextWindow || 1000000),
    reasoningTag: String(source.reasoningTag || draft?.reasoningTag || 'reasoning_content').trim() || 'reasoning_content',
    // "auto" and the legacy "max_tokens" both send max_tokens, so collapse the
    // explicit legacy value onto "auto" — otherwise the two-option select would
    // render blank for an imported config that stored "max_tokens".
    tokenParam: normalizeDraftTokenParam(source.tokenParam),
    reasoningEffort: normalizeReasoningEffort(source.reasoningEffort || 'max'),
  };
}

// normalizeModelApiKeys 归一化 key 列表:去除空白、空项并按出现顺序去重。
// 复用 modelConfigIO 的 normalizeApiKeysArray,与后端 normalizeAPIKeys 语义
// 保持一致(单一归一化边界)。
function normalizeModelApiKeys(keys) {
  return normalizeApiKeysArray(keys || []);
}

// ── Custom header rows ──
// customHeaderRows 是自定义头的行编辑状态(有序、可空行)，提交时经
// normalizeCustomHeaders 归一化为 map；与后端归一化边界共用同一套语义
// (modelConfigIO.normalizeCustomHeaders 镜像 Go normalizeCustomHeaders)。
let customHeaderRowSeq = 0;
const customHeaderRows = ref([]);
const modelAdvancedOpen = ref(false);
const customHeaderRowCount = computed(() => customHeaderRows.value.filter((row) => row.name.trim() && row.value.trim()).length);

function resetCustomHeaderRows(headers) {
  const normalized = normalizeCustomHeaders(headers) || {};
  customHeaderRows.value = Object.keys(normalized).sort().map((name) => ({
    id: ++customHeaderRowSeq,
    name,
    value: normalized[name],
  }));
}

function addCustomHeaderRow() {
  customHeaderRows.value.push({ id: ++customHeaderRowSeq, name: '', value: '' });
}

function removeCustomHeaderRow(id) {
  customHeaderRows.value = customHeaderRows.value.filter((row) => row.id !== id);
}

function collectCustomHeaders() {
  const raw = {};
  for (const row of customHeaderRows.value) {
    const name = String(row.name || '').trim();
    if (!name) continue;
    raw[name] = String(row.value || '').trim();
  }
  return normalizeCustomHeaders(raw);
}

function addModelApiKey() {
  if (!Array.isArray(modelDraft.apiKeys)) modelDraft.apiKeys = [];
  modelDraft.apiKeys.push('');
}

function removeModelApiKey(index) {
  if (!Array.isArray(modelDraft.apiKeys) || modelDraft.apiKeys.length <= 1) return;
  modelDraft.apiKeys.splice(index, 1);
}

// normalizeDraftTokenParam keeps only the two values the select exposes:
// "max_completion_tokens" (opt-in) and "auto" (everything else, incl. the
// equivalent legacy "max_tokens").
function normalizeDraftTokenParam(value) {
  const v = String(value || '').trim().toLowerCase().replace(/[-\s]+/g, '_');
  return v === 'max_completion_tokens' ? 'max_completion_tokens' : 'auto';
}

const reasoningEffortOptions = computed(() =>
  reasoningEffortLevels.map((level) => ({ label: reasoningEffortLabel(level), value: level }))
);

const modelDraft = reactive(defaultModelDraft());
const catalogProviderOptions = computed(() => providerCatalogOptions(modelCatalog.value, t('settings.providerCustom')));
const selectedCatalogProvider = computed(() => findCatalogProvider(modelCatalog.value, selectedCatalogProviderId.value));
const selectedCatalogModelOptions = computed(() => providerModelOptions(selectedCatalogProvider.value));

const activeProviderTab = ref('');

const apiFormatOptions = [
  { label: 'OpenAI Chat Completions', value: 'openai_chat' },
  { label: 'OpenAI Responses', value: 'openai_responses' },
  { label: 'Anthropic Messages', value: 'anthropic_messages' },
];

const tokenParamOptions = computed(() => [
  { label: `max_tokens (${t('settings.tokenParamDefault')})`, value: 'auto' },
  { label: 'max_completion_tokens', value: 'max_completion_tokens' },
]);

// Max Tokens / 上下文窗口下拉预设：label 与 value 一致，全部使用具体数字
// （K=1000、M=1000000 的十进制换算，不做 K/M 缩写也不取 2 的幂）；
// 自定义输入走 tag 模式并转回数字（onNumericTagSelect）。
const maxTokensOptions = [
  { label: '8000', value: 8000 },
  { label: '16000', value: 16000 },
  { label: '32000', value: 32000 },
  { label: '64000', value: 64000 },
  { label: '128000', value: 128000 },
  { label: '384000', value: 384000 },
];

const contextWindowOptions = [
  { label: '64000', value: 64000 },
  { label: '128000', value: 128000 },
  { label: '256000', value: 256000 },
  { label: '512000', value: 512000 },
  { label: '1000000', value: 1000000 },
  { label: '1500000', value: 1500000 },
];

const reasoningTagOptions = ['reasoning_content', 'reasoning', 'think', 'thinking', 'thought', 'reason']
  .map((tag) => ({ label: tag, value: tag }));

// tag 模式下自定义输入是字符串；转成正整数，非法输入保持原值。
function normalizeNumericTagSelect(value, fallback) {
  if (typeof value === 'number' && Number.isFinite(value)) return value;
  const num = Number(String(value ?? '').trim());
  return Number.isFinite(num) && num > 0 ? num : fallback;
}

function onMaxTokensSelected(value) {
  modelDraft.maxTokens = normalizeNumericTagSelect(value, modelDraft.maxTokens);
}

function onContextWindowSelected(value) {
  modelDraft.contextWindow = normalizeNumericTagSelect(value, modelDraft.contextWindow);
}

function onReasoningTagSelected(value) {
  modelDraft.reasoningTag = String(value ?? '').trim() || 'reasoning_content';
}

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
    const group = groups.get(provider);
    group.models.push({ model, index });
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

async function ensureModelCatalog() {
  if (modelCatalog.value.providers.length || modelCatalogLoading.value) return;
  modelCatalogLoading.value = true;
  try {
    const loaded = await import('../data/modelCatalog.json');
    modelCatalog.value = loaded.default || loaded;
  } catch (err) {
    message.error(t('settings.providerCatalogLoadFailed', { error: err }));
  } finally {
    modelCatalogLoading.value = false;
  }
}

const remoteModels = ref([]);
const modelListLoading = ref(false);
const remoteModelOptions = computed(() => remoteModels.value.map((name) => ({ label: name, value: name })));

function onModelDraftSelected(value) {
  modelDraft.model = typeof value === 'string' ? value : (value == null ? modelDraft.model : String(value));
}

async function fetchRemoteModels() {
  if (modelListLoading.value) return;
  const baseUrl = (modelDraft.baseUrl || '').trim() || apiFormatDefaultBaseUrl(modelDraft.apiFormat);
  const apiKeys = normalizeModelApiKeys(modelDraft.apiKeys || []);
  const customHeaders = collectCustomHeaders();
  modelListLoading.value = true;
  try {
    remoteModels.value = await FetchModelList(baseUrl, apiKeys[0] || '', customHeaders || undefined);
    if (!remoteModels.value.length) {
      message.warning(t('settings.fetchModelsEmpty'));
    }
  } catch (err) {
    remoteModels.value = [];
    message.error(t('settings.fetchModelsFailed', { error: String(err && err.message ? err.message : err) }));
  } finally {
    modelListLoading.value = false;
  }
}

function assignModelDraft(source = {}) {
  Object.assign(modelDraft, defaultModelDraft({
    ...source,
    apiFormat: normalizeApiFormat(source.apiFormat || draft.apiFormat),
  }));
  // Always default to "Custom" — never auto-match a catalog preset. The
  // preset dropdown is opt-in; auto-matching was surprising because it
  // silently switched the form into preset mode and disabled Model/Base URL.
  selectedCatalogProviderId.value = CUSTOM_PROVIDER_ID;
}

function selectCatalogProvider(providerId) {
  selectedCatalogProviderId.value = providerId;
  const provider = findCatalogProvider(modelCatalog.value, providerId);
  if (!provider) return;
  const preferredModel = findCatalogModel(provider, modelDraft.model) || provider.models?.[0];
  if (preferredModel) Object.assign(modelDraft, applyCatalogPreset(provider, preferredModel, modelDraft));
}

function selectCatalogModel(modelId) {
  const value = String(modelId || '').trim();
  // Values not found in the catalog (typed via the tag select) are still valid
  // custom model names — keep them and only skip the preset metadata.
  if (value) modelDraft.model = value;
  const provider = selectedCatalogProvider.value;
  const model = findCatalogModel(provider, value);
  if (provider && model) Object.assign(modelDraft, applyCatalogPreset(provider, model, modelDraft));
}

function openProviderDocumentation() {
  if (selectedCatalogProvider.value?.doc) Browser.OpenURL(selectedCatalogProvider.value.doc);
}

async function startAddModelDraft() {
  modelEditorIndex.value = -1;
  await ensureModelCatalog();
  const provider = activeProviderTab.value || 'OpenAI Compatible';
  // 新建模型：按模型设置页当前活跃 tab 下的首个模型进行预填充，包含 API key
  const activeTabModels = (draft.models || []).filter((m) => normalizedProviderName(m.providerName) === provider);
  const templateModel = activeTabModels[0];

  if (templateModel) {
    const rawKeys = Array.isArray(templateModel.apiKeys) && templateModel.apiKeys.length
      ? templateModel.apiKeys
      : (templateModel.apiKey ? [templateModel.apiKey] : []);
    const normalizedKeys = normalizeModelApiKeys(rawKeys);
    resetCustomHeaderRows(templateModel.customHeaders);
    assignModelDraft({
      providerName: provider,
      apiFormat: normalizeApiFormat(templateModel.apiFormat),
      baseUrl: templateModel.baseUrl || '',
      apiKey: normalizedKeys[0] || '',
      apiKeys: normalizedKeys,
      model: '',
      temperature: templateModel.temperature ?? 0.2,
      maxTokens: templateModel.maxTokens || 131072,
      contextWindow: templateModel.contextWindow || 1000000,
      reasoningTag: templateModel.reasoningTag || 'reasoning_content',
      tokenParam: templateModel.tokenParam || 'auto',
      reasoningEffort: normalizeReasoningEffort(templateModel.reasoningEffort || 'max'),
    });
  } else {
    resetCustomHeaderRows(null);
    assignModelDraft({
      providerName: provider,
      apiFormat: normalizeApiFormat(draft.apiFormat),
      baseUrl: draft.baseUrl || '',
      apiKey: '',
      apiKeys: [],
      model: '',
      maxTokens: draft.maxTokens || 131072,
      contextWindow: draft.contextWindow || 1000000,
    });
  }
  modelEditorVisible.value = true;
}

async function editModelDraft(index) {
  if (!draft.models || !draft.models[index]) return;
  modelEditorIndex.value = index;
  await ensureModelCatalog();
  resetCustomHeaderRows(draft.models[index].customHeaders);
  assignModelDraft(draft.models[index]);
  modelEditorVisible.value = true;
}

function cancelModelDraft() {
  modelEditorVisible.value = false;
  modelEditorIndex.value = -1;
  selectedCatalogProviderId.value = CUSTOM_PROVIDER_ID;
  remoteModels.value = [];
  modelListLoading.value = false;
  resetCustomHeaderRows(null);
  modelAdvancedOpen.value = false;
}

async function testModelConnection() {
  if (testingModel.value) return;
  const model = (modelDraft.model || '').trim();
  const apiKeys = normalizeModelApiKeys(modelDraft.apiKeys || []);
  if (!model) {
    message.warning(t('app.config.modelRequired'));
    return;
  }
  testingModel.value = true;
  try {
    await TestModelConnection({
      providerName: normalizedProviderName(modelDraft.providerName),
      apiFormat: normalizeApiFormat(modelDraft.apiFormat),
      baseUrl: (modelDraft.baseUrl || '').trim(),
      apiKey: apiKeys[0] || '',
      apiKeys,
      model,
      temperature: modelDraft.temperature ?? 0.2,
      maxTokens: modelDraft.maxTokens || 131072,
      contextWindow: modelDraft.contextWindow || 1000000,
      reasoningTag: modelDraft.reasoningTag || 'reasoning_content',
      tokenParam: modelDraft.tokenParam || 'auto',
      reasoningEffort: normalizeReasoningEffort(modelDraft.reasoningEffort),
      customHeaders: collectCustomHeaders(),
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
  const apiKeys = normalizeModelApiKeys(modelDraft.apiKeys || []);
  const providerName = normalizedProviderName(modelDraft.providerName);
  const apiFormat = normalizeApiFormat(modelDraft.apiFormat);
  const customHeaders = collectCustomHeaders();
  const nextModel = {
    providerName,
    apiFormat,
    baseUrl: (modelDraft.baseUrl || '').trim(),
    apiKey: apiKeys[0] || '',
    apiKeys,
    model,
    temperature: modelDraft.temperature ?? draft.temperature ?? 0.2,
    maxTokens: modelDraft.maxTokens || draft.maxTokens || 131072,
    contextWindow: modelDraft.contextWindow || draft.contextWindow || 1000000,
    reasoningTag: modelDraft.reasoningTag || 'reasoning_content',
    tokenParam: modelDraft.tokenParam || 'auto',
    reasoningEffort: normalizeReasoningEffort(modelDraft.reasoningEffort),
    ...(customHeaders ? { customHeaders } : {}),
  };
  if (modelEditorIndex.value >= 0) {
    draft.models.splice(modelEditorIndex.value, 1, nextModel);
    const duplicateIndex = draft.models.findIndex((saved, index) => (
      index !== modelEditorIndex.value && modelConfigIdentity(saved) === modelConfigIdentity(nextModel)
    ));
    if (duplicateIndex >= 0) draft.models.splice(duplicateIndex, 1);
  } else {
    const existingIndex = draft.models.findIndex((saved) => modelConfigIdentity(saved) === modelConfigIdentity(nextModel));
    if (existingIndex >= 0) draft.models.splice(existingIndex, 1, nextModel);
    else draft.models.push(nextModel);
  }
  alignActiveProviderTab(providerName);
  modelEditorVisible.value = false;
  emit('save', { ...draft }, true);
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
  emit('save', { ...draft }, true);
}

function openModelImport() {
  if (!modelImportInput.value) return;
  modelImportInput.value.value = '';
  modelImportInput.value.click();
}

async function importModelConfigs(event) {
  const input = event?.target;
  const file = input?.files?.[0];
  if (!file) return;
  try {
    if (file.size > 2 * 1024 * 1024) throw Object.assign(new Error('FILE_TOO_LARGE'), { code: 'FILE_TOO_LARGE' });
    const imported = parseModelConfigImport(await file.text());
    const result = mergeModelConfigs(draft.models, imported);
    draft.models = result.models;
    alignActiveProviderTab(activeProviderTab.value || normalizedProviderName(draft.providerName));
    emit('save', { ...draft }, true);
    message.success(t('settings.modelImportSuccess', { added: result.added, updated: result.updated }));
  } catch (err) {
    const code = String(err?.code || 'UNKNOWN');
    message.error(t(`settings.modelImportError.${code}`));
  } finally {
    if (input) input.value = '';
  }
}

function exportModelConfigs() {
  const payload = buildModelConfigExport(draft.models);
  const content = `${JSON.stringify(payload, null, 2)}\n`;
  saveTextFile({
    filename: `ally-models-${new Date().toISOString().slice(0, 10)}.json`,
    content,
    filterName: 'JSON (*.json)',
    filterPattern: '*.json',
  }).then((result) => {
    if (result.saved) message.success(t('settings.modelExportSuccess', { count: payload.models.length }));
  });
}

function cloneConfigDraft(source) {
  const next = JSON.parse(JSON.stringify(source || {}));
  next.reasoningTag = String(next.reasoningTag || '').trim() || 'reasoning_content';
  next.models = Array.isArray(next.models) ? next.models.map((model) => ({
    ...model,
    reasoningTag: String(model?.reasoningTag || '').trim() || 'reasoning_content',
    apiKeys: normalizeModelApiKeys(model?.apiKeys || (model?.apiKey ? [model.apiKey] : [])),
  })) : [];
  next.apiKeys = normalizeModelApiKeys(next.apiKeys || (next.apiKey ? [next.apiKey] : []));
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

// The page stays mounted (parent v-show): re-sync the draft from the parent
// config each time the page becomes visible so external changes (settings
// saves, tab-snapshot effort syncs) are picked up; models edits themselves
// always save immediately, so nothing unsaved is lost.
watch(
  () => props.show,
  (visible) => {
    if (visible) syncDraftFromProps();
  },
  { immediate: true },
);
</script>

<style scoped>
/* Inline models page: fills the main-area container like the settings and
   skills pages. */
.config-inline-panel {
  display: flex;
  flex-direction: column;
  width: 100%;
  height: 100%;
  min-width: 0;
  min-height: 0;
  background: var(--ally-surface-content);
  overflow: hidden;
}

.config-inline-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 14px 22px 12px;
  border-bottom: 1px solid var(--ally-border);
  flex-shrink: 0;
}

.config-inline-title {
  font-size: 18px;
  font-weight: 700;
  letter-spacing: 0.5px;
  color: var(--ally-text-primary);
}

.panel-scroll-body {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  padding: 16px 24px 28px;
}

/* 页头操作按钮组：与 Skills/MCP 面板同构（导入→导出→添加，主色最后）。 */
.panel-header-actions {
  display: flex;
  align-items: center;
  gap: 8px;
  flex: none;
  flex-wrap: wrap;
  justify-content: flex-end;
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
  color: var(--ally-text-primary);
}

.config-section-subtitle {
  font-size: 12px;
  color: var(--ally-text-muted);
  margin-top: 2px;
}

.model-import-input {
  display: none;
}

.current-model-panel {
  background: var(--ally-hover-faint);
  border: 1px solid var(--ally-border);
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
  color: var(--ally-text-faint);
  text-transform: uppercase;
  letter-spacing: 0.5px;
  margin-bottom: 2px;
}

.current-model-name {
  font-size: 14px;
  font-weight: 600;
  color: var(--ally-text-primary);
}

.current-model-url {
  font-size: 12px;
  color: var(--ally-text-muted);
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
  color: var(--ally-text-faint);
  text-align: right;
}

.saved-model-empty {
  color: var(--ally-text-faint);
  font-size: var(--ally-sub-font-size);
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
  background: var(--ally-hover-faint);
}

.saved-model-item.active {
  border-color: var(--ally-border-strong);
  background: var(--ally-state-hover);
}

.saved-model-main {
  flex: 1;
  min-width: 0;
}

.saved-model-name {
  font-size: var(--ally-sub-font-size);
  font-weight: 500;
  color: var(--ally-text-primary);
}

.saved-model-meta {
  font-size: 12px;
  color: var(--ally-text-muted);
  margin-top: 1px;
}

.saved-model-url {
  font-size: 11px;
  color: var(--ally-text-faint);
  margin-top: 1px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  max-width: 420px;
}

.api-key-list {
  display: flex;
  flex-direction: column;
  gap: 6px;
  width: 100%;
}

.api-key-row {
  display: flex;
  align-items: center;
  gap: 6px;
  width: 100%;
}

.api-key-index {
  flex-shrink: 0;
  width: 18px;
  color: var(--ally-text-muted);
  font-size: 12px;
  text-align: center;
  font-family: var(--ally-mono-font);
}

.api-key-row .n-input {
  flex: 1;
}

.api-key-add {
  align-self: flex-start;
}

.api-key-hint {
  color: var(--ally-text-muted);
  font-size: 12px;
  line-height: 1.5;
}

.model-advanced-toggle {
  margin-top: 4px;
}

.model-advanced-button {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 4px 0;
  background: none;
  border: none;
  color: var(--ally-text-muted);
  font-size: 12.5px;
  cursor: pointer;
  user-select: none;
}

.model-advanced-button:hover {
  color: var(--ally-accent, #63e2b7);
}

.model-advanced-arrow {
  display: inline-block;
  transition: transform 0.15s ease;
  font-size: 14px;
  line-height: 1;
}

.model-advanced-arrow.open {
  transform: rotate(90deg);
}

.model-advanced-count {
  min-width: 18px;
  padding: 0 5px;
  border-radius: 9px;
  background: var(--ally-accent, #63e2b7);
  color: #111;
  font-size: 11px;
  line-height: 18px;
  text-align: center;
  font-family: var(--ally-mono-font);
}

.model-advanced-body {
  margin-top: 6px;
  padding-top: 4px;
  border-top: 1px dashed var(--ally-border, rgba(255, 255, 255, 0.12));
}

.custom-header-row .custom-header-name {
  flex: 0 0 38%;
}

.custom-header-row .custom-header-value {
  flex: 1;
}

.model-input-select {
  width: 100%;
}

.model-form-modal {
  width: 580px;
  max-width: calc(100vw - 48px);
}

.model-format-hint {
  margin-top: 4px;
}

.provider-doc-button {
  margin-left: 8px;
  vertical-align: baseline;
}

@media (max-width: 640px) {
  .model-form-modal {
    max-width: calc(100vw - 24px);
  }
}
</style>
