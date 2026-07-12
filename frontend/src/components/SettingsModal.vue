<template>
  <n-modal
    :show="visible"
    preset="card"
    title="设置"
    class="config-modal"
    :style="settingsModalStyle"
    :bordered="true"
    @update:show="onClose"
  >
    <div class="settings-layout">
      <aside class="settings-nav" aria-label="设置导航">
        <button :class="['settings-nav-item', { active: page === 'general' }]" @click="page = 'general'">
          <span class="settings-nav-title">通用</span>
          <span class="settings-nav-desc">提示词</span>
        </button>
        <button :class="['settings-nav-item', { active: page === 'models' }]" @click="page = 'models'">
          <span class="settings-nav-title">模型</span>
          <span class="settings-nav-desc">Provider 与密钥</span>
        </button>
        <button :class="['settings-nav-item', { active: page === 'skills' }]" @click="page = 'skills'">
          <span class="settings-nav-title">Skills</span>
          <span class="settings-nav-desc">加载与注入</span>
        </button>
        <button :class="['settings-nav-item', { active: page === 'mcp' }]" @click="page = 'mcp'">
          <span class="settings-nav-title">MCP</span>
          <span class="settings-nav-desc">工具服务</span>
        </button>
        <button :class="['settings-nav-item', { active: page === 'about' }]" @click="page = 'about'">
          <span class="settings-nav-title">关于</span>
          <span class="settings-nav-desc">版本与许可证</span>
        </button>
      </aside>

      <n-form class="settings-content" label-placement="top">
        <!-- General -->
        <section v-if="page === 'general'" class="settings-page">
          <div class="config-section-header">
            <div>
              <div class="config-section-title">通用配置</div>
              <div class="config-section-subtitle">控制模型每次对话都会看到的附加偏好。</div>
            </div>
          </div>
          <n-form-item label="自定义提示词">
            <n-input
              v-model:value="draft.customPrompt"
              type="textarea"
              :autosize="{ minRows: 8, maxRows: 16 }"
              placeholder="例如：请用中文回答、保持回复简洁"
            />
          </n-form-item>
        </section>

        <!-- Models -->
        <section v-else-if="page === 'models'" class="settings-page">
          <div class="config-section-header">
            <div>
              <div class="config-section-title">模型配置</div>
              <div class="config-section-subtitle">按 Provider 管理模型，选择列表中的模型作为当前配置。</div>
            </div>
            <n-button size="small" type="primary" @click="startAddModelDraft">添加模型</n-button>
          </div>

          <div class="current-model-panel">
            <div class="current-model-main">
              <div class="current-model-label">当前模型</div>
              <div class="current-model-name">{{ draft.providerName || 'Provider' }} · {{ draft.model || '未选择模型' }}</div>
              <div class="current-model-url">{{ apiFormatLabel(draft.apiFormat) }}</div>
              <div class="current-model-url">{{ draft.baseUrl || '未配置 Base URL' }}</div>
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
                    <div class="saved-model-name">{{ normalizedProviderName(item.model.providerName) }} · {{ item.model.model || '未选择模型' }}</div>
                    <div class="saved-model-meta">{{ apiFormatLabel(item.model.apiFormat) }} · {{ item.model.model || 'Model' }}</div>
                    <div class="saved-model-url">{{ item.model.baseUrl || '未配置 Base URL' }}</div>
                    <div class="saved-model-url">max {{ item.model.maxTokens || '-' }} · context {{ item.model.contextWindow || '-' }}</div>
                  </div>
                  <n-space :size="4">
                    <n-button size="tiny" secondary :disabled="isDraftModelActive(item.model)" @click="applyModelToDraft(item.model)">使用</n-button>
                    <n-button size="tiny" quaternary @click="editModelDraft(item.index)">编辑</n-button>
                    <n-button size="tiny" type="error" quaternary @click="removeModelDraft(item.index)">删除</n-button>
                  </n-space>
                </div>
              </div>
            </n-tab-pane>
          </n-tabs>
          <div v-else class="saved-model-empty">暂无已保存模型，点击"添加模型"创建第一个模型。</div>
        </section>

        <!-- Skills -->
        <section v-else-if="page === 'skills'" class="settings-page">
          <div class="config-section-header">
            <div>
              <div class="config-section-title">Skills</div>
              <div class="config-section-subtitle">{{ activeSkillNames.length }} 个已启用 · {{ availableSkills.length }} 个可用</div>
            </div>
            <n-space>
              <n-button size="small" secondary :loading="skillsLoading" @click="refreshSkillState">刷新</n-button>
              <n-button size="small" secondary :disabled="!activeSkillNames.length || skillsLoading" @click="clearLoadedSkills(false)">全部停用</n-button>
            </n-space>
          </div>
          <div class="skill-settings-list">
            <div v-if="skillsLoading && !availableSkills.length" class="saved-model-empty">正在读取技能...</div>
            <div v-else-if="!availableSkills.length" class="saved-model-empty">暂无可用技能。</div>
            <div v-for="sk in availableSkills" :key="`${sk.source || 'skill'}:${sk.name}`" :class="['skill-settings-item', { active: isSkillActive(sk.name) }]">
              <div class="skill-settings-main">
                <div class="skill-title-row">
                  <span class="skill-name">{{ sk.name }}</span>
                  <span :class="['skill-badge', sk.source || 'unknown']">{{ sk.source || 'unknown' }}</span>
                  <span v-if="isSkillActive(sk.name)" class="skill-badge loaded">enabled</span>
                </div>
                <div class="skill-description">{{ sk.description || sk.whenToUse || '无描述' }}</div>
                <div class="skill-meta" :title="sk.path">{{ sk.path || sk.dir || '-' }}</div>
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
              <div class="config-section-title">MCP 服务</div>
              <div class="config-section-subtitle">手动编辑 JSON；只有连接成功的 MCP 工具会注入给 AI，跨会话共享。</div>
            </div>
            <n-space>
              <n-button size="small" secondary :loading="mcpLoading" @click="loadMcpConfig">刷新</n-button>
              <n-button size="small" type="primary" :loading="mcpLoading" :disabled="!mcpConfigValid" @click="saveMcpConfigText">保存并重连</n-button>
            </n-space>
          </div>
          <n-input
            v-model:value="mcpConfigText"
            class="mcp-config-editor"
            type="textarea"
            :autosize="false"
            :rows="10"
            spellcheck="false"
            placeholder='{"mcpServers":{"serviceName":{"enabled":true,"transport":"sse","url":"https://...","headers":{"Authorization":"Bearer ***"}}}}'
          />
          <div :class="['mcp-json-check', mcpConfigValid ? 'valid' : 'invalid']">
            {{ mcpConfigValidationText }}
          </div>
          <div class="mcp-status-list">
            <div v-if="!mcpServers.length" class="saved-model-empty">暂无已连接 MCP 服务</div>
            <div v-for="srv in mcpServers" :key="srv.name" class="mcp-status-item">
              <span :class="['mcp-dot', srv.status]"></span>
              <span class="mcp-name">{{ srv.name }}</span>
              <span class="mcp-status">{{ srv.status }}</span>
              <span class="mcp-tools">{{ srv.toolCount || 0 }} tools</span>
              <span v-if="srv.error" class="mcp-error" :title="srv.error">{{ srv.error }}</span>
            </div>
          </div>
        </section>

        <!-- About -->
        <section v-else-if="page === 'about'" class="settings-page">
          <div class="config-section-header">
            <div>
              <div class="config-section-title">关于 Ally</div>
              <div class="config-section-subtitle">自由、开源的桌面 AI 编程助手。</div>
            </div>
          </div>
          <div class="license-notice">
            <div class="license-notice-title">GNU General Public License v3.0 only</div>
            <p>Ally 是自由软件：你可以依照 GNU GPL 第 3 版的条款使用、研究、修改和重新分发。</p>
            <p>本程序不提供任何担保；在适用法律允许的范围内，也不保证适销性或特定用途适用性。</p>
            <p>发行包内附完整 GPLv3 与第三方许可证文本。对应源代码发布于项目仓库。</p>
            <n-button secondary @click="openSourceRepository">查看源代码与许可证</n-button>
          </div>
        </section>
      </n-form>
    </div>
    <template #footer>
      <n-space justify="end">
        <n-button @click="onClose">取消</n-button>
        <n-button type="primary" @click="onSave">保存</n-button>
      </n-space>
    </template>
  </n-modal>

  <!-- Model editor sub-modal -->
  <n-modal
    :show="modelEditorVisible"
    preset="card"
    :title="modelEditorIndex >= 0 ? '编辑模型' : '添加模型'"
    class="model-form-modal"
    :style="modelFormModalStyle"
    :mask-closable="false"
    @update:show="(v) => { if (!v) cancelModelDraft(); }"
  >
    <n-form :model="modelDraft" label-placement="top">
      <n-grid :cols="2" :x-gap="12">
        <n-form-item-gi label="Provider 名称">
          <n-input v-model:value="modelDraft.providerName" placeholder="OpenAI Compatible" />
        </n-form-item-gi>
        <n-form-item-gi label="接口格式">
          <n-select v-model:value="modelDraft.apiFormat" :options="apiFormatOptions" />
        </n-form-item-gi>
        <n-form-item-gi label="Model">
          <n-input v-model:value="modelDraft.model" :placeholder="modelPlaceholder(modelDraft.apiFormat)" />
        </n-form-item-gi>
        <n-form-item-gi :label="normalizeApiFormat(modelDraft.apiFormat) === 'anthropic_messages' ? 'Base URL（无需 /v1）' : 'Base URL'">
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
        <n-form-item-gi label="上下文窗口" :span="2">
          <n-input-number v-model:value="modelDraft.contextWindow" :min="1024" :step="1024" style="width: 100%" />
        </n-form-item-gi>
      </n-grid>
      <n-alert v-if="normalizeApiFormat(modelDraft.apiFormat) === 'anthropic_messages'" type="info" :show-icon="false" class="model-format-hint">
        官方地址填写 https://api.anthropic.com，末尾无需添加 /v1。当前接入支持 Messages、图片和工具调用；尚未开放 Extended Thinking 配置。
      </n-alert>
    </n-form>
    <template #footer>
      <n-space justify="end">
        <n-button secondary :loading="testingModel" @click="testModelConnection">测试连接</n-button>
        <n-button @click="modelEditorVisible = false">取消</n-button>
        <n-button type="primary" @click="commitModelDraft">{{ modelEditorIndex >= 0 ? '保存修改' : '添加' }}</n-button>
      </n-space>
    </template>
  </n-modal>
</template>

<script setup>
import { computed, reactive, ref, watch } from 'vue';
import { createDiscreteApi, darkTheme } from 'naive-ui';
import { BrowserOpenURL } from '../../wailsjs/runtime/runtime';
import {
  GetMcpConfig, GetMcpServers, SaveMcpConfig, RestartMcpServers,
  ListSkills, ActivateSkill, DeactivateSkill, ClearSkills, GetActiveSkills,
  ListTools,
  TestModelConnection,
} from '../../wailsjs/go/main/App';

const { message } = createDiscreteApi(['message'], {
  configProviderProps: { theme: darkTheme },
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
    message.warning('请填写 Model');
    return;
  }
  if (!apiKey) {
    message.warning('请填写 API Key');
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
    });
    message.success('连接成功');
  } catch (err) {
    message.error(`连接失败：${err}`);
  } finally {
    testingModel.value = false;
  }
}

function commitModelDraft() {
  if (!draft.models) draft.models = [];
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
    temperature: modelDraft.temperature ?? draft.temperature ?? 0.2,
    maxTokens: modelDraft.maxTokens || draft.maxTokens || 128000,
    contextWindow: modelDraft.contextWindow || draft.contextWindow || 1048576,
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

const mcpConfigParseResult = computed(() => {
  const raw = mcpConfigText.value || '';
  if (!raw.trim()) return { valid: false, text: 'JSON 为空，请输入有效配置' };
  try {
    const parsed = JSON.parse(raw);
    if (parsed && typeof parsed === 'object' && parsed.mcpServers) {
      return { valid: true, text: 'JSON 格式正确' };
    }
    return { valid: false, text: 'JSON 中需要包含 mcpServers 字段' };
  } catch (e) {
    return { valid: false, text: `JSON 格式错误：${e.message}` };
  }
});
const mcpConfigValid = computed(() => mcpConfigParseResult.value.valid);
const mcpConfigValidationText = computed(() => mcpConfigParseResult.value.text);

function cloneConfigDraft(source) {
  return JSON.parse(JSON.stringify(source || {}));
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
    mcpServers.value = await GetMcpServers() || [];
  } catch (err) {
    message.error(`读取 MCP 配置失败：${err}`);
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
    message.success('MCP 配置已保存并重连');
    emit('mcp-saved');
  } catch (err) {
    message.error(`保存 MCP 配置失败：${err}`);
  } finally {
    mcpLoading.value = false;
  }
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

async function refreshSkillState() {
  skillsLoading.value = true;
  try {
    const [skills, active] = await Promise.all([ListSkills(), GetActiveSkills()]);
    availableSkills.value = skills || [];
    activeSkillNames.value = active || [];
  } catch (err) {
    message.error(`读取技能状态失败：${err}`);
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
    message.error(`${active ? '启用' : '停用'}技能失败：${err}`);
  } finally {
    skillToggleInFlight.value = '';
  }
}

async function clearLoadedSkills(announce = true) {
  try {
    await ClearSkills();
    await refreshSkillState();
    emit('skills-changed');
    message.success('技能已停用');
  } catch (err) {
    message.error(`停用技能失败：${err}`);
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

.mcp-config-editor {
  font-family: var(--ally-mono-font);
  font-size: 13px;
  line-height: 1.5;
  margin-bottom: 4px;
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
