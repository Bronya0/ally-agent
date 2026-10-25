<!-- SPDX-License-Identifier: GPL-3.0-only -->
<template>
  <div class="image-model-panel">
    <div class="image-model-toolbar">
      <n-checkbox :checked="draft.enabled" @update:checked="setEnabled">{{ t('imageModels.use') }}</n-checkbox>
      <span class="image-model-note">{{ t('imageModels.nextSession') }}</span>
    </div>
    <div class="image-model-defaults">
      <div class="image-default-field">
        <div class="image-default-label">
          <span>{{ t('imageModels.defaultGenerate') }}</span>
          <n-button size="tiny" quaternary :disabled="!defaultGenerateModel" @click="testModel = { ...defaultGenerateModel }">{{ t('imageModels.test') }}</n-button>
        </div>
        <n-select
          :value="imageModelKey(draft.generateModel) || null"
          :options="generateOptions"
          :disabled="!generateOptions.length"
          :placeholder="!generateOptions.length ? t('imageModels.empty') : undefined"
          size="small" filterable clearable
          @update:value="selectDefault('generateModel', $event)"
        />
      </div>
      <div class="image-default-field">
        <span>{{ t('imageModels.defaultEdit') }}</span>
        <n-select
          :value="imageModelKey(draft.editModel) || null"
          :options="editOptions"
          :disabled="!editOptions.length"
          :placeholder="!editOptions.length ? t('imageModels.noEditModel') : undefined"
          size="small" filterable clearable
          @update:value="selectDefault('editModel', $event)"
        />
      </div>
    </div>
    <p class="image-model-hint">{{ t('imageModels.hint') }}</p>
    <n-tabs
      v-if="groups.length"
      :key="groups.map(g => g.name).join('|')"
      v-model:value="activeProvider"
      type="line"
      class="panel-content-tabs"
    >
      <n-tab-pane v-for="group in groups" :key="group.name" :name="group.name" :tab="group.name">
        <div class="image-model-list">
          <div v-for="model in group.models" :key="imageModelKey(model)" class="ally-row-card">
            <div class="ally-row-main">
              <span class="ally-row-name">{{ model.model }}</span>
              <span class="ally-row-badge">{{ protocolLabel(model.protocol) }}</span>
              <span v-if="imageModelKey(draft.generateModel) === imageModelKey(model)" class="ally-row-badge">{{ t('imageModels.defaultGenerate') }}</span>
              <span v-if="imageModelKey(draft.editModel) === imageModelKey(model)" class="ally-row-badge">{{ t('imageModels.defaultEdit') }}</span>
              <div class="ally-row-side">
                <n-button size="tiny" quaternary @click="edit(model)">{{ t('common.edit') }}</n-button>
                <n-button size="tiny" quaternary type="error" @click="remove(model)">{{ t('common.delete') }}</n-button>
              </div>
            </div>
            <div class="image-model-url">{{ model.baseUrl }}</div>
          </div>
        </div>
      </n-tab-pane>
    </n-tabs>
    <n-empty v-else class="image-model-empty" :description="search ? t('common.searchEmpty') : t('imageModels.empty')" />
    <input ref="importInput" type="file" accept="application/json,.json" hidden @change="importModels" />
    <ImageModelTestModal v-if="testModel" :model="testModel" @close="testModel = null" />
    <n-modal
      v-model:show="editorVisible"
      preset="card"
      :title="previous ? t('imageModels.edit') : t('imageModels.add')"
      :style="{ width: 'min(580px, calc(100vw - 48px))' }"
      :mask-closable="false"
    >
      <n-form label-placement="top">
        <n-form-item label="Model">
          <n-input v-model:value="form.model" placeholder="gpt-image-2 / grok-imagine-image / image-01" @update:value="matchProtocol" />
        </n-form-item>
        <n-grid :cols="2" :x-gap="12">
          <n-form-item-gi :label="t('settings.providerName')">
            <n-input v-model:value="form.providerName" />
          </n-form-item-gi>
          <n-form-item-gi :label="t('imageModels.protocol')">
            <n-select :value="form.protocol" :options="imageProtocols" @update:value="chooseProtocol" />
          </n-form-item-gi>
        </n-grid>
        <n-form-item label="Base URL">
          <n-input v-model:value="form.baseUrl" placeholder="https://api.openai.com/v1" />
        </n-form-item>
        <n-form-item :label="t('settings.apiKeys')">
          <n-dynamic-input v-model:value="form.apiKeys" :min="1">
            <template #default="{ index }">
              <n-input v-model:value="form.apiKeys[index]" type="password" show-password-on="click" autocomplete="new-password" />
            </template>
          </n-dynamic-input>
        </n-form-item>
        <n-collapse>
          <n-collapse-item :title="t('settings.modelAdvanced')" name="headers">
            <n-form-item :label="t('settings.customHeaders')">
              <n-input v-model:value="form.headers" type="textarea" placeholder='{"X-Header": "value"}' />
            </n-form-item>
          </n-collapse-item>
        </n-collapse>
      </n-form>
      <template #footer>
        <n-space justify="end">
          <n-button @click="editorVisible = false">{{ t('common.cancel') }}</n-button>
          <n-button type="primary" @click="save">{{ t('settings.saveChanges') }}</n-button>
        </n-space>
      </template>
    </n-modal>
  </div>
</template>

<script setup>
import { computed, reactive, ref, watch } from 'vue';
import { useMessage } from 'naive-ui';
import { t } from '../i18n.mjs';
import { saveTextFile } from '../utils/download.mjs';
import ImageModelTestModal from './ImageModelTestModal.vue';
import {
  cloneImageGeneration, imageProtocols, imageModelIdentity, imageModelKey,
  inferImageProtocol, normalizeImageModel, parseImageModelImport,
  replaceImageModel, supportsImageEdit,
} from '../utils/imageModels.mjs';

const props = defineProps({ config: { type: Object, default: null }, search: { type: String, default: '' } });
const emit = defineEmits(['save']);
const message = useMessage();
const draft = reactive({ enabled: false, models: [], generateModel: null, editModel: null });
watch(() => props.config, (value) => Object.assign(draft, cloneImageGeneration(value)), { immediate: true, deep: true });
const activeProvider = ref('');
const groups = computed(() => {
  const result = new Map();
  const needle = props.search.trim().toLowerCase();
  for (const model of draft.models || []) {
    if (needle && ![model.providerName, model.model, model.baseUrl].some(value => String(value || '').toLowerCase().includes(needle))) continue;
    if (!result.has(model.providerName)) result.set(model.providerName, { name: model.providerName, models: [] });
    result.get(model.providerName).models.push(model);
  }
  return [...result.values()];
});
watch(groups, (value) => {
  if (!value.some(group => group.name === activeProvider.value)) {
    activeProvider.value = value[0]?.name || '';
  }
}, { immediate: true });
const option = (model) => ({ label: `${model.providerName} / ${model.model}`, value: imageModelKey(model) });
const generateOptions = computed(() => (draft.models || []).map(option));
const defaultGenerateModel = computed(() => draft.models.find(model => imageModelKey(model) === imageModelKey(draft.generateModel)));
const testModel = ref(null);
const editOptions = computed(() => (draft.models || []).filter(supportsImageEdit).map(option));
const protocolLabel = (value) => imageProtocols.find(item => item.value === value)?.label || value;
const persist = () => emit('save', { imageGeneration: JSON.parse(JSON.stringify(draft)) }, true);
function setEnabled(value) {
  draft.enabled = value;
  persist();
  message.info(t('imageModels.nextSession'));
}
function selectDefault(field, key) {
  draft[field] = imageModelIdentity(draft.models.find(model => imageModelKey(model) === key));
  persist();
}
const editorVisible = ref(false);
const previous = ref(null);
const form = reactive({});
let manualProtocol = false;
function edit(model = null) {
  previous.value = model;
  manualProtocol = !!model;
  Object.assign(form, model ? JSON.parse(JSON.stringify(model)) : { providerName: 'OpenAI', protocol: 'openai-images', baseUrl: imageProtocols[0].baseUrl, model: '', apiKeys: [''] });
  form.apiKeys = model?.apiKeys?.length ? [...model.apiKeys] : [model?.apiKey || ''];
  form.headers = model?.customHeaders ? JSON.stringify(model.customHeaders, null, 2) : '';
  editorVisible.value = true;
}
function applyProtocol(value) {
  const previousProtocol = imageProtocols.find(item => item.value === form.protocol);
  const protocol = imageProtocols.find(item => item.value === value);
  if (!protocol) return;
  if (!form.baseUrl || imageProtocols.some(item => item.baseUrl === form.baseUrl)) form.baseUrl = protocol.baseUrl;
  if (!form.providerName || form.providerName === previousProtocol?.label || imageProtocols.some(item => item.label === form.providerName)) form.providerName = protocol.label;
  form.protocol = value;
}
function chooseProtocol(value) {
  manualProtocol = true;
  applyProtocol(value);
}
function matchProtocol(value) {
  if (manualProtocol) return;
  const protocol = inferImageProtocol(value);
  if (protocol) applyProtocol(protocol);
}
function save() {
  try {
    const customHeaders = form.headers.trim() ? JSON.parse(form.headers) : null;
    if (customHeaders && (Array.isArray(customHeaders) || typeof customHeaders !== 'object' || Object.values(customHeaders).some(value => typeof value !== 'string'))) throw new Error('headers');
    const next = normalizeImageModel({ ...form, customHeaders });
    if (!next.apiKeys.length) throw new Error('key');
    replaceImageModel(draft, previous.value, next);
    editorVisible.value = false;
    activeProvider.value = next.providerName;
    persist();
  } catch {
    message.error(t('imageModels.invalid'));
  }
}
function remove(model) {
  replaceImageModel(draft, model, null);
  persist();
}
const importInput = ref(null);
async function importModels(event) {
  const file = event.target.files?.[0];
  if (!file) return;
  try {
    if (file.size > 2 * 1024 * 1024) throw new Error('size');
    const models = parseImageModelImport(await file.text());
    for (const model of models) {
      replaceImageModel(draft, draft.models.find(item => imageModelKey(item) === imageModelKey(model)), model);
    }
    persist();
  } catch {
    message.error(t('imageModels.invalidImport'));
  } finally {
    event.target.value = '';
  }
}
async function exportModels() {
  try {
    await saveTextFile({
      filename: 'ally-image-models.json',
      content: JSON.stringify({ version: 1, kind: 'ally-image-models', models: draft.models }, null, 2),
      filterName: 'JSON (*.json)',
      filterPattern: '*.json',
    });
  } catch {
    message.error(t('settings.modelExportFailed'));
  }
}
defineExpose({ add: () => edit(), import: () => importInput.value?.click(), export: exportModels });
</script>

<style scoped>
.image-model-panel { display: flex; flex: 1; flex-direction: column; min-height: 0; gap: 16px; }
.image-model-toolbar { display: flex; flex-wrap: wrap; align-items: center; gap: 8px 16px; }
.image-model-note { font-size: 12px; color: var(--ally-text-faint); }
.image-model-defaults { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 16px; width: 100%; max-width: 800px; }
.image-default-field { display: flex; min-width: 0; flex-direction: column; gap: 6px; font-size: 12px; color: var(--ally-text-muted); }
.image-default-label { display: flex; align-items: center; gap: 8px; min-height: 22px; }
.image-default-field > span { min-height: 22px; display: flex; align-items: center; }
.image-model-hint { margin: 0; font-size: 12px; color: var(--ally-text-muted); }
.image-model-list { display: flex; flex-direction: column; gap: 6px; }
.image-model-url { font-size: 11px; color: var(--ally-text-faint); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.image-model-empty { padding: 32px 0; }
@media (max-width: 700px) {
  .image-model-defaults { grid-template-columns: minmax(0, 1fr); }
}
</style>
