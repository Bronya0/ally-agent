<!--
SPDX-License-Identifier: GPL-3.0-only

Copyright (C) 2026 tangssst <tangssst@qq.com>
GitHub: https://github.com/Bronya0/ally-agent

This file is part of ally-agent, licensed under the GNU General
Public License v3. See the LICENSE file for details.
-->
<template>
  <!-- Inline settings panel: shown directly inside the main-area container
       (App.vue mode === 'settings' via v-show) instead of a modal dialog.
       Always mounted so in-page state survives mode switches. Only the model
       editor sub-modal below stays a dialog. -->
  <div class="config-inline-panel">
    <header class="config-inline-header">
      <span class="config-inline-title">{{ $t('settings.title') }}</span>
    </header>
    <n-layout has-sider class="config-inline-body">
      <n-layout-sider bordered :width="150" :native-scrollbar="false" content-style="padding: 14px 10px;">
        <aside class="settings-nav" :aria-label="$t('settings.navigation')">
        <button :class="['settings-nav-item', { active: page === 'general' }]" @click="page = 'general'">
          <span class="settings-nav-title">{{ $t('settings.general') }}</span>
        </button>
        <button :class="['settings-nav-item', { active: page === 'advanced' }]" @click="page = 'advanced'">
          <span class="settings-nav-title">{{ $t('settings.advanced') }}</span>
        </button>
        <button :class="['settings-nav-item', { active: page === 'network' }]" @click="page = 'network'">
          <span class="settings-nav-title">{{ $t('settings.network') }}</span>
        </button>
        <button :class="['settings-nav-item', { active: page === 'api' }]" @click="page = 'api'">
          <span class="settings-nav-title">API</span>
        </button>
        <button :class="['settings-nav-item', { active: page === 'about' }]" @click="page = 'about'">
          <span class="settings-nav-title">{{ $t('settings.about') }}</span>
        </button>
      </aside>
      </n-layout-sider>
      <n-layout-content :native-scrollbar="false" content-style="padding: 16px 24px 28px;" class="config-inline-content">
        <n-form class="settings-content" label-placement="top">
        <!-- General -->
        <section v-if="page === 'general'" class="settings-page">
          <div class="config-section-header">
            <div>
              <div class="config-section-title">{{ $t('settings.generalTitle') }}</div>
              <div class="config-section-subtitle">{{ $t('settings.generalSubtitle') }}</div>
            </div>
          </div>
          <n-form-item :label="$t('settings.appearance')">
            <div class="settings-field-stack">
              <div class="appearance-mode-row">
                <button
                  :class="['appearance-mode-btn', { active: colorMode === 'dark' }]"
                  @click="selectColorMode('dark')"
                >{{ $t('settings.modeDark') }}</button>
                <button
                  :class="['appearance-mode-btn', { active: colorMode === 'light' }]"
                  @click="selectColorMode('light')"
                >{{ $t('settings.modeLight') }}</button>
              </div>
              <span class="settings-field-hint">{{ $t('settings.appearanceHint') }}</span>
            </div>
          </n-form-item>
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
          <n-form-item :label="$t('settings.kbRoot')">
            <div class="settings-field-stack">
              <div class="background-image-row">
                <n-button
                  size="small"
                  :loading="kbRootSelecting"
                  @click="selectKBRoot"
                >{{ $t('settings.kbRootSelect') }}</n-button>
                <n-button
                  v-if="draft.kbRoot"
                  size="small"
                  secondary
                  @click="clearKBRoot"
                >{{ $t('settings.kbRootClear') }}</n-button>
                <span class="background-image-status">
                  {{ draft.kbRoot || $t('settings.kbRootNone') }}
                </span>
              </div>
              <span class="settings-field-hint">{{ $t('settings.kbRootHint') }}</span>
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
          <n-form-item :label="$t('settings.userAgent')">
            <div class="settings-field-stack">
              <n-select
                :value="draft.userAgent || ''"
                :options="userAgentOptions"
                filterable
                tag
                :placeholder="$t('settings.userAgentPlaceholder')"
                :render-label="renderUserAgentOptionLabel"
                @update:value="onUserAgentSelected"
              />
              <span class="settings-field-hint">{{ $t('settings.userAgentHint') }}</span>
            </div>
          </n-form-item>
          <n-form-item :label="$t('settings.llmRetries')">
            <div class="settings-field-stack">
              <n-input-number
                v-model:value="draft.llmRetries"
                :min="0"
                :max="10"
                :step="1"
                style="width: 140px"
              />
              <span class="settings-field-hint">{{ $t('settings.llmRetriesHint') }}</span>
            </div>
          </n-form-item>
          <n-form-item :label="$t('settings.compactThreshold')">
            <div class="settings-field-stack">
              <n-input-number
                v-model:value="draft.compactThreshold"
                :min="0.2"
                :max="0.95"
                :step="0.05"
                :precision="2"
                :formatter="v => `${Math.round((Number(v) || 0) * 100)}%`"
                :parser="s => Number(String(s).replace('%', '').trim()) / 100"
                style="width: 140px"
              />
              <span class="settings-field-hint">{{ $t('settings.compactThresholdHint') }}</span>
            </div>
          </n-form-item>
          <n-form-item :label="$t('settings.compactTimeout')">
            <div class="settings-field-stack">
              <n-input-number
                v-model:value="draft.compactTimeoutSeconds"
                :min="30"
                :max="3600"
                :step="30"
                :precision="0"
                style="width: 140px"
              />
              <span class="settings-field-hint">{{ $t('settings.compactTimeoutHint') }}</span>
            </div>
          </n-form-item>
          <n-form-item v-if="isWindows" :label="$t('settings.autoUpdate')">
            <div class="settings-toggle-row">
              <n-switch v-model:value="draft.autoUpdate" />
              <span class="settings-toggle-hint">{{ $t('settings.autoUpdateHint') }}</span>
            </div>
          </n-form-item>
          <n-form-item :label="$t('settings.checkUpdate')">
            <div class="settings-toggle-row">
              <n-button
                size="small"
                :loading="checkUpdateBusy"
                @click="checkForUpdates"
              >{{ checkUpdateBusy ? $t('settings.checkUpdateBusy') : $t('settings.checkUpdate') }}</n-button>
              <span v-if="checkUpdateMessage" class="settings-field-hint">{{ checkUpdateMessage }}</span>
            </div>
          </n-form-item>
          <!-- Close-to-tray is disabled for the current release; keep the
               setting code for a future re-enable.
          <n-form-item :label="$t('settings.closeToTray')">
            <div class="settings-toggle-row">
              <n-switch v-model:value="draft.closeToTray" />
              <span class="settings-toggle-hint">{{ $t('settings.closeToTrayHint') }}</span>
            </div>
          </n-form-item>
          -->
          <n-form-item :label="$t('settings.autostart')">
            <div class="settings-toggle-row">
              <n-switch :value="autostartEnabled" :loading="autostartBusy" @update:value="toggleAutostart" />
              <span class="settings-toggle-hint">{{ $t('settings.autostartHint') }}</span>
            </div>
          </n-form-item>
          <n-form-item :label="$t('settings.backgroundImage')">
            <div class="settings-field-stack">
              <div class="background-image-row">
                <n-button
                  size="small"
                  :loading="backgroundSelecting"
                  @click="selectBackground"
                >{{ draft.backgroundImage ? $t('settings.backgroundImageReplace') : $t('settings.backgroundImageSelect') }}</n-button>
                <n-button
                  v-if="draft.backgroundImage"
                  size="small"
                  secondary
                  :loading="backgroundClearing"
                  @click="clearBackground"
                >{{ $t('settings.backgroundImageClear') }}</n-button>
                <span class="background-image-status">
                  {{ draft.backgroundImage
                      ? $t('settings.backgroundImageSet', { name: draft.backgroundImage })
                      : $t('settings.backgroundImageNone') }}
                </span>
              </div>
              <span class="settings-field-hint">{{ $t('settings.backgroundImageHint') }}</span>
            </div>
          </n-form-item>
          <n-form-item :label="$t('settings.backgroundOpacity')">
            <div class="settings-field-stack">
              <n-input-number
                v-model:value="draft.backgroundOpacity"
                :min="0"
                :max="1"
                :step="0.05"
                :precision="2"
                :formatter="v => `${Math.round((Number(v) || 0) * 100)}%`"
                :parser="s => Number(String(s).replace('%', '').trim()) / 100"
                style="width: 140px"
              />
              <span class="settings-field-hint">{{ $t('settings.backgroundOpacityHint') }}</span>
            </div>
          </n-form-item>
          <div class="font-size-grid">
            <div class="font-size-field">
              <span class="font-size-label">{{ $t('settings.messageFontSize') }}</span>
              <n-input-number
                v-model:value="draft.messageFontSize"
                :min="12"
                :max="24"
                :step="0.5"
                :precision="1"
                :formatter="v => `${Number(v) || 15.5}px`"
                :parser="s => Number(String(s).replace('px', '').trim())"
                size="small"
                :placeholder="'15.5'"
              />
            </div>
            <div class="font-size-field">
              <span class="font-size-label">{{ $t('settings.codeFontSize') }}</span>
              <n-input-number
                v-model:value="draft.codeFontSize"
                :min="12"
                :max="24"
                :step="0.5"
                :precision="1"
                :formatter="v => `${Number(v) || 14}px`"
                :parser="s => Number(String(s).replace('px', '').trim())"
                size="small"
                :placeholder="'14'"
              />
            </div>
            <div class="font-size-field">
              <span class="font-size-label">{{ $t('settings.toolFontSize') }}</span>
              <n-input-number
                v-model:value="draft.toolFontSize"
                :min="12"
                :max="24"
                :step="0.5"
                :precision="1"
                :formatter="v => `${Number(v) || 15}px`"
                :parser="s => Number(String(s).replace('px', '').trim())"
                size="small"
                :placeholder="'15'"
              />
            </div>
            <div class="font-size-field">
              <span class="font-size-label">{{ $t('settings.subFontSize') }}</span>
              <n-input-number
                v-model:value="draft.subFontSize"
                :min="11"
                :max="18"
                :step="0.5"
                :precision="1"
                :formatter="v => `${Number(v) || 13}px`"
                :parser="s => Number(String(s).replace('px', '').trim())"
                size="small"
                :placeholder="'13'"
              />
            </div>
            <div class="font-size-field">
              <span class="font-size-label">{{ $t('settings.auxFontSize') }}</span>
              <n-input-number
                v-model:value="draft.auxFontSize"
                :min="10"
                :max="20"
                :step="0.5"
                :precision="1"
                :formatter="v => `${Number(v) || 12}px`"
                :parser="s => Number(String(s).replace('px', '').trim())"
                size="small"
                :placeholder="'12'"
              />
            </div>
          </div>
          <div class="settings-field-hint">{{ $t('settings.fontSizeGridHint') }}</div>
          <div class="settings-page-actions">
            <n-button type="primary" @click="onSave">{{ $t('common.save') }}</n-button>
          </div>
        </section>

        <!-- Advanced -->
        <section v-else-if="page === 'advanced'" class="settings-page">
          <div class="config-section-header">
            <div>
              <div class="config-section-title">{{ $t('settings.advancedTitle') }}</div>
              <div class="config-section-subtitle">{{ $t('settings.advancedSubtitle') }}</div>
            </div>
          </div>
          <div class="validation-settings-list">
            <div v-for="item in validationSettings" :key="item.key" class="validation-setting-row">
              <div class="validation-setting-copy">
                <div class="validation-setting-label">{{ item.label }}</div>
                <div class="validation-setting-hint">{{ item.hint }}</div>
              </div>
              <n-switch v-model:value="draft[item.key]" />
            </div>
          </div>
          <div class="settings-page-actions">
            <n-button type="primary" @click="onSave">{{ $t('common.save') }}</n-button>
          </div>
        </section>

        <!-- Network -->
        <section v-else-if="page === 'network'" class="settings-page">
          <div class="config-section-header">
            <div>
              <div class="config-section-title">{{ $t('settings.proxyTitle') }}</div>
              <div class="config-section-subtitle">{{ $t('settings.proxySubtitle') }}</div>
            </div>
          </div>
          <n-form-item :label="$t('settings.proxyMode')">
            <n-select v-model:value="draft.proxyMode" :options="proxyModeOptions" />
          </n-form-item>
          <n-form-item v-if="draft.proxyMode === 'manual'" :label="$t('settings.proxyUrl')">
            <n-input v-model:value="draft.proxyUrl" clearable placeholder="http://127.0.0.1:7890 / socks5://127.0.0.1:7891" />
          </n-form-item>
          <n-form-item v-if="draft.proxyMode !== 'off'" :label="$t('settings.proxyNoProxy')">
            <n-input v-model:value="draft.proxyNoProxy" clearable placeholder="localhost,127.0.0.1,::1" />
          </n-form-item>
          <n-alert type="warning" :show-icon="true" class="proxy-warning">
            {{ $t('settings.proxySecurityHint') }}
          </n-alert>
          <div class="proxy-actions">
            <n-button secondary :loading="proxyDetecting" @click="detectProxy">{{ $t('settings.proxyDetect') }}</n-button>
            <n-button secondary :loading="proxyTesting" :disabled="draft.proxyMode === 'off'" @click="testProxy">{{ $t('settings.proxyTest') }}</n-button>
          </div>
          <div v-if="proxyStatus" class="proxy-status-card">
            <div><span>{{ $t('settings.proxySource') }}</span><strong>{{ proxyStatus.source || '-' }}</strong></div>
            <div><span>HTTP</span><strong>{{ proxyStatus.httpProxy || '-' }}</strong></div>
            <div><span>HTTPS</span><strong>{{ proxyStatus.httpsProxy || '-' }}</strong></div>
            <div><span>NO_PROXY</span><strong>{{ proxyStatus.noProxy || '-' }}</strong></div>
            <div v-if="proxyStatus.pacUrl"><span>PAC</span><strong>{{ proxyStatus.pacUrl }}</strong></div>
            <n-alert v-if="proxyStatus.error || proxyStatus.pacUnsupported" type="warning" :show-icon="false">
              {{ proxyStatus.error || $t('settings.proxyPacUnsupported') }}
            </n-alert>
          </div>
        </section>

        <!-- API -->
        <section v-else-if="page === 'api'" class="settings-page">
          <div class="config-section-header">
            <div>
              <div class="config-section-title">{{ $t('settings.apiTitle') }}</div>
              <div class="config-section-subtitle">{{ $t('settings.apiSubtitle') }}</div>
            </div>
            <div class="api-header-actions">
              <n-button size="small" secondary type="success" @click="openApiDocs">{{ $t('settings.apiDocsButton') }}</n-button>
              <n-button size="small" secondary :loading="apiLoading" @click="loadApiState">{{ $t('common.refresh') }}</n-button>
            </div>
          </div>

          <div class="api-config-row">
            <n-switch :value="!!apiState?.enabled" :disabled="apiToggleDisabled || apiBusy" @update:value="toggleApiService" />
            <n-input-number v-model:value="apiPortDraft" :min="1024" :max="65535" :show-button="false" class="api-port-input" @blur="saveApiConfig" />
            <n-input v-model:value="apiTokenDraft" class="api-token-input" :placeholder="$t('settings.apiTokenPlaceholder')" @blur="saveApiConfig" />
            <n-button size="small" secondary @click="copyApiToken">{{ $t('common.copy') }}</n-button>
            <n-button size="small" secondary :loading="apiBusy" @click="regenerateApiToken">{{ $t('settings.apiRegenerate') }}</n-button>
          </div>
          <div class="api-hint">{{ $t('settings.apiConfigHint') }}</div>
          <div class="api-hint">{{ $t('settings.apiStartupHint') }}</div>
          <div class="api-hint">{{ $t('settings.apiAuthHint', { url: apiState?.baseUrl || 'http://127.0.0.1:47821' }) }}</div>

          <div class="api-endpoints">
            <div class="api-endpoints-title">{{ $t('settings.apiEndpointsTitle') }}</div>
            <div v-for="ep in apiEndpoints" :key="ep.path" class="api-endpoint-row">
              <n-tag size="small" :type="ep.method === 'GET' ? 'info' : (ep.method === 'PUT' ? 'warning' : 'success')" class="api-method">{{ ep.method }}</n-tag>
              <code class="api-path">{{ ep.path }}</code>
              <span class="api-desc">{{ $t(ep.key) }}</span>
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
            <p class="license-copyleft">{{ $t('settings.licenseCopyleft') }}</p>
            <p>{{ $t('settings.licenseWarranty') }}</p>
            <p>{{ $t('settings.licenseSource') }}</p>
            <n-button secondary @click="openSourceRepository">{{ $t('settings.sourceLicense') }}</n-button>
          </div>
          <div class="about-update-check">
            <n-button
              size="small"
              :loading="checkUpdateBusy"
              @click="checkForUpdates"
            >{{ checkUpdateBusy ? $t('settings.checkUpdateBusy') : $t('settings.checkUpdate') }}</n-button>
            <span v-if="checkUpdateMessage" class="about-update-status">{{ checkUpdateMessage }}</span>
          </div>
        </section>
      </n-form>
      </n-layout-content>
    </n-layout>
  </div>
</template>

<script setup>
import { computed, h, onUnmounted, reactive, ref, watch } from 'vue';
import { createDiscreteApi, darkTheme } from 'naive-ui';
import { naiveDateLocale, naiveLocale, t } from '../i18n.mjs';
import { getStoredMode } from '../utils/theme.mjs';
import { normalizeApiKeysArray } from '../utils/modelConfigIO.mjs';
import { Browser } from '@wailsio/runtime';
import {
  DetectSystemProxy, TestProxy,
  SelectBackgroundImage, ClearBackgroundImage,
  SelectKnowledgeBaseRoot,
  GetAutostartEnabled, SetAutostartEnabled,
  GetApiServiceState, SaveApiSettings, SetApiServiceEnabled,
} from '../../bindings/ally-dev/internal/app/app';

// The discrete message API follows the active color mode so toasts never
// render dark-on-dark / light-on-light after a mode switch.
const colorModeState = ref(getStoredMode());
const { message } = createDiscreteApi(['message'], {
  configProviderProps: computed(() => ({
    theme: colorModeState.value === 'light' ? null : darkTheme,
    locale: naiveLocale,
    dateLocale: naiveDateLocale,
  })),
});

function selectColorMode(mode) {
  colorModeState.value = mode;
  emit('set-mode', mode);
}

function openSourceRepository() {
  Browser.OpenURL('https://github.com/Bronya0/ally-agent');
}

const autostartEnabled = ref(false);
const autostartBusy = ref(false);

// Update check state for the About page button. The actual check is
// delegated to the parent (App.vue) via the check-update emit so the result
// also updates the top-right update icon and triggers auto-download if
// needed. Parent reports back through the checkUpdateResult prop.
const props = defineProps({
  visible: Boolean,
  configDraft: { type: Object, required: true },
  // Optional result object reported by the parent after a check-update emit:
  //   { state: 'idle' | 'busy' | 'latest' | 'found' | 'failed', version?: string }
  checkUpdateResult: { type: Object, default: () => ({ state: 'idle' }) },
  // Active color mode ('dark' | 'light'), owned by App.vue.
  colorMode: { type: String, default: 'dark' },
});
const emit = defineEmits(['close', 'save', 'background-changed', 'check-update', 'set-mode']);
const checkUpdateBusy = ref(false);
const checkUpdateMessage = ref('');
let checkUpdateTimer = 0;

// Keep the local mode state in sync when the parent changes it elsewhere.
watch(() => props.colorMode, (mode) => {
  colorModeState.value = mode === 'light' ? 'light' : 'dark';
}, { immediate: true });

watch(() => props.checkUpdateResult, (result) => {
  if (!result) return;
  checkUpdateBusy.value = result.state === 'busy';
  if (result.state !== 'busy' && checkUpdateTimer) {
    window.clearTimeout(checkUpdateTimer);
    checkUpdateTimer = 0;
  }
  switch (result.state) {
    case 'busy': checkUpdateMessage.value = ''; break;
    case 'latest': checkUpdateMessage.value = t('settings.checkUpdateLatest'); break;
    case 'found': checkUpdateMessage.value = t('settings.checkUpdateFound', { version: result.version || '' }); break;
    case 'failed': checkUpdateMessage.value = t('settings.checkUpdateFailed'); break;
    default: checkUpdateMessage.value = '';
  }
}, { immediate: true });

async function refreshAutostart() {
  try {
    autostartEnabled.value = await GetAutostartEnabled();
  } catch (_) { /* best-effort; OS may not support it */ }
}

async function toggleAutostart(value) {
  autostartBusy.value = true;
  try {
    await SetAutostartEnabled(Boolean(value));
    autostartEnabled.value = Boolean(value);
  } catch (err) {
    message.error(t('settings.autostartFailed', { error: err }));
  } finally {
    autostartBusy.value = false;
  }
}

function checkForUpdates() {
  if (checkUpdateBusy.value) return;
  checkUpdateBusy.value = true;
  emit('check-update');
  // Safety net: if the parent doesn't report back within 15s (e.g. backend
  // hung on a network request), stop spinning so the user isn't stuck.
  if (checkUpdateTimer) window.clearTimeout(checkUpdateTimer);
  checkUpdateTimer = window.setTimeout(() => {
    if (checkUpdateBusy.value) {
      checkUpdateBusy.value = false;
      checkUpdateMessage.value = t('settings.checkUpdateFailed');
    }
  }, 15000);
}

onUnmounted(() => {
  if (checkUpdateTimer) window.clearTimeout(checkUpdateTimer);
  checkUpdateTimer = 0;
});

const validationSettingKeys = [
  'autoValidationPython',
  'autoValidationGo',
  'autoValidationJavaScript',
  'autoValidationTypeScript',
  'autoValidationVue',
  'autoValidationJava',
  'autoValidationJson',
];

// Deep-clone the config draft so changes don't mutate parent reactively until save
const draft = reactive(cloneConfigDraft(props.configDraft));

// Accent theme is a pure front-end preference (localStorage), independent of the
// backend config draft. Applied live on selection.

const page = ref('general');
const proxyDetecting = ref(false);
const proxyTesting = ref(false);
const proxyStatus = ref(null);
const proxyModeOptions = computed(() => [
  { label: t('settings.proxyOff'), value: 'off' },
  { label: t('settings.proxySystem'), value: 'system' },
  { label: t('settings.proxyManual'), value: 'manual' },
]);
const validationSettings = computed(() => [
  { key: 'autoValidationPython', label: t('settings.validationPython'), hint: t('settings.validationPythonHint') },
  { key: 'autoValidationGo', label: t('settings.validationGo'), hint: t('settings.validationGoHint') },
  { key: 'autoValidationJavaScript', label: t('settings.validationJavaScript'), hint: t('settings.validationJavaScriptHint') },
  { key: 'autoValidationTypeScript', label: t('settings.validationTypeScript'), hint: t('settings.validationTypeScriptHint') },
  { key: 'autoValidationVue', label: t('settings.validationVue'), hint: t('settings.validationVueHint') },
  { key: 'autoValidationJava', label: t('settings.validationJava'), hint: t('settings.validationJavaHint') },
  { key: 'autoValidationJson', label: t('settings.validationJson'), hint: t('settings.validationJsonHint') },
]);

// Background image picker state. Selecting/clearing persists immediately on
// the backend (the file write cannot be deferred to Save), so these actions
// also emit background-changed to let App.vue refresh the data URL live.
const backgroundSelecting = ref(false);
const backgroundClearing = ref(false);

// User-Agent 下拉预设：label 用工具名，value 是从各工具源码核实的真实 UA
// 字符串（opencode: session/llm/request.ts `opencode/${version}`；
// pi: utils/pi-user-agent.ts `pi (${platform} ${release}; ${arch})`，无版本号）。
// 空字符串 = 后台默认 AllyAgent；tag 模式允许自定义输入。
const userAgentOptions = [
  { label: t('settings.userAgentDefaultLabel'), value: '' },
  { label: 'Codex CLI', value: 'codex_cli_rs/0.76.0 (Debian 13.0.0; x86_64) WindowsTerminal' },
  { label: 'Claude Code', value: 'claude-cli/2.1.161 (external, cli)' },
  { label: 'OpenCode', value: 'opencode/1.18.25' },
  { label: 'Pi', value: 'pi (win32 10.0.26100; x64)' },
];

function renderUserAgentOptionLabel(option) {
  const ua = String(option.value || '');
  return h('span', { class: 'user-agent-option', title: ua || undefined }, option.label);
}

function onUserAgentSelected(value) {
  draft.userAgent = String(value ?? '').trim();
}

async function selectBackground() {
  backgroundSelecting.value = true;
  try {
    const filename = await SelectBackgroundImage();
    if (filename) {
      draft.backgroundImage = filename;
      emit('background-changed');
    }
  } catch (err) {
    message.error(t('settings.backgroundSelectFailed', { error: err }));
  } finally {
    backgroundSelecting.value = false;
  }
}

// Knowledge-base root: pick a directory into the draft; the value persists
// through the modal's normal save flow. Clearing just empties the draft
// field — the effective save happens on 保存.
const kbRootSelecting = ref(false);

async function selectKBRoot() {
  kbRootSelecting.value = true;
  try {
    const selected = await SelectKnowledgeBaseRoot();
    if (selected) {
      draft.kbRoot = selected;
    }
  } catch (err) {
    message.error(t('settings.kbRootSelectFailed', { error: err }));
  } finally {
    kbRootSelecting.value = false;
  }
}

function clearKBRoot() {
  draft.kbRoot = '';
}

async function clearBackground() {
  backgroundClearing.value = true;
  try {
    await ClearBackgroundImage();
    draft.backgroundImage = '';
    draft.backgroundOpacity = 0.15;
    emit('background-changed');
    message.success(t('settings.backgroundCleared'));
  } catch (err) {
    message.error(t('settings.backgroundClearFailed', { error: err }));
  } finally {
    backgroundClearing.value = false;
  }
}

async function detectProxy() {
  proxyDetecting.value = true;
  try {
    proxyStatus.value = await DetectSystemProxy();
  } catch (err) {
    proxyStatus.value = { error: String(err) };
  } finally {
    proxyDetecting.value = false;
  }
}

async function testProxy() {
  proxyTesting.value = true;
  try {
    const result = await TestProxy({
      mode: draft.proxyMode,
      url: draft.proxyUrl || '',
      noProxy: draft.proxyNoProxy || '',
      targetUrl: draft.baseUrl || '',
    });
    message.success(t('settings.proxyTestSuccess', { status: result.statusCode, duration: result.durationMs, proxy: result.proxy || t('settings.proxyDirect') }));
  } catch (err) {
    message.error(t('settings.proxyTestFailed', { error: String(err) }));
  } finally {
    proxyTesting.value = false;
  }
}

// Network page: persist proxy settings immediately when the user changes any
// of proxyMode / proxyUrl / proxyNoProxy. Previously these fields were only
// saved when the user clicked "Test Proxy" or navigated to General and hit
// Save — so flipping proxyMode to "off" and closing the modal would silently
// drop the change, and the next launch would fall back to the saved value
// (often "system"). Silent save (third arg true) matches the Models page.
//
// Guard with props.visible so the initial syncDraftFromProps() call (which
// bulk-assigns all proxy fields at once when the modal opens) doesn't leak
// through as a spurious save. Real user edits happen only while the modal
// is visible.
watch([
  () => draft.proxyMode,
  () => draft.proxyUrl,
  () => draft.proxyNoProxy,
], () => {
  if (!props.visible) return;
  emit('save', { ...draft }, true);
});

const isWindows = computed(() => {
  return document.body.classList.contains('platform-windows') ||
    document.body.classList.contains('platform-win32');
});

// normalizeModelApiKeys 归一化 key 列表:去除空白、空项并按出现顺序去重。
// 复用 modelConfigIO 的 normalizeApiKeysArray,与后端 normalizeAPIKeys 语义
// 保持一致(单一归一化边界)。
function normalizeModelApiKeys(keys) {
  return normalizeApiKeysArray(keys || []);
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
  for (const key of validationSettingKeys) {
    // Auto validation is opt-in: only an explicit true keeps a check enabled.
    next[key] = next[key] === true;
  }
  return next;
}

function syncDraftFromProps() {
  const next = cloneConfigDraft(props.configDraft);
  for (const key of Object.keys(draft)) {
    delete draft[key];
  }
  Object.assign(draft, next);
}

function onClose() {
  emit('close');
}

function onSave() {
  emit('save', { ...draft });
}

// ── API 服务页 ──
// 端口与 token 是独立于 ConfigState 的持久化设置（~/.ally_agent/api.json），
// 服务开关是运行时状态：每次启动默认关闭，需手动开启。

const apiState = ref(null);
const apiLoading = ref(false);
const apiBusy = ref(false);
const apiPortDraft = ref(null);
const apiTokenDraft = ref('');

const apiEndpoints = [
  { method: 'GET', path: '/api/v1/health', key: 'settings.apiEpHealth' },
  { method: 'GET', path: '/api/v1/sessions', key: 'settings.apiEpSessionsList' },
  { method: 'POST', path: '/api/v1/sessions', key: 'settings.apiEpSessionsCreate' },
  { method: 'GET', path: '/api/v1/sessions/{id}', key: 'settings.apiEpSessionStatus' },
  { method: 'GET', path: '/api/v1/sessions/{id}/result', key: 'settings.apiEpSessionResult' },
  { method: 'GET', path: '/api/v1/sessions/{id}/messages', key: 'settings.apiEpSessionMessages' },
  { method: 'GET', path: '/api/v1/sessions/{id}/todos', key: 'settings.apiEpSessionTodos' },
  { method: 'POST', path: '/api/v1/sessions/{id}/messages', key: 'settings.apiEpSessionSend' },
  { method: 'POST', path: '/api/v1/sessions/{id}/cancel', key: 'settings.apiEpSessionCancel' },
  { method: 'POST', path: '/api/v1/sessions/{id}/compact', key: 'settings.apiEpSessionCompact' },
  { method: 'DELETE', path: '/api/v1/sessions/{id}', key: 'settings.apiEpSessionDelete' },
  { method: 'GET', path: '/api/v1/models', key: 'settings.apiEpModelsList' },
  { method: 'POST', path: '/api/v1/models', key: 'settings.apiEpModelsSave' },
  { method: 'POST', path: '/api/v1/models/activate', key: 'settings.apiEpModelsActivate' },
  { method: 'GET', path: '/api/v1/mcp', key: 'settings.apiEpMcpGet' },
  { method: 'PUT', path: '/api/v1/mcp/config', key: 'settings.apiEpMcpPut' },
  { method: 'GET', path: '/api/v1/skills', key: 'settings.apiEpSkillsList' },
  { method: 'GET', path: '/api/v1/skills/{name}', key: 'settings.apiEpSkillGet' },
  { method: 'POST', path: '/api/v1/skills/{name}/enable', key: 'settings.apiEpSkillEnable' },
  { method: 'POST', path: '/api/v1/skills/{name}/disable', key: 'settings.apiEpSkillDisable' },
  { method: 'GET', path: '/api/v1/tools', key: 'settings.apiEpTools' },
  { method: 'GET', path: '/api/v1/subagents', key: 'settings.apiEpSubagents' },
  { method: 'GET', path: '/api/v1/workspace', key: 'settings.apiEpWorkspace' },
  { method: 'GET', path: '/api/v1/services', key: 'settings.apiEpServices' },
  { method: 'GET', path: '/api/v1/services/{id}/output', key: 'settings.apiEpServiceOutput' },
  { method: 'POST', path: '/api/v1/services/{id}/stop', key: 'settings.apiEpServiceStop' },
  { method: 'GET', path: '/api/v1/tasks', key: 'settings.apiEpTasks' },
  { method: 'DELETE', path: '/api/v1/tasks/{id}', key: 'settings.apiEpTaskDelete' },
];

// 都配置了才能启动：端口有效且 token 非空。
const apiToggleDisabled = computed(() => {
  const port = Number(apiPortDraft.value);
  if (!Number.isInteger(port) || port < 1024 || port > 65535) return true;
  return !String(apiTokenDraft.value || '').trim();
});

function syncApiDrafts(state) {
  apiState.value = state;
  apiPortDraft.value = state?.port ?? null;
  apiTokenDraft.value = state?.token || '';
}

async function loadApiState() {
  apiLoading.value = true;
  try {
    syncApiDrafts(await GetApiServiceState());
  } catch (err) {
    message.error(t('settings.apiLoadFailed', { error: err }));
  } finally {
    apiLoading.value = false;
  }
}

async function toggleApiService(enabled) {
  if (enabled && apiToggleDisabled.value) {
    message.warning(t('settings.apiStartupHint'));
    return;
  }
  apiBusy.value = true;
  try {
    apiState.value = await SetApiServiceEnabled(enabled);
    apiPortDraft.value = apiState.value?.port ?? null;
    message.success(t(enabled ? 'settings.apiStarted' : 'settings.apiStopped'));
  } catch (err) {
    message.error(t('settings.apiToggleFailed', { error: err }));
  } finally {
    apiBusy.value = false;
  }
}

// 失焦自动保存端口/token：token 传空值时后端自动生成新 token；服务运行中
// 会用新设置重启监听。成功时静默（同步回填规范化后的值），失败才提示。
async function saveApiConfig() {
  const port = Number(apiPortDraft.value);
  if (!Number.isInteger(port) || port < 1024 || port > 65535) {
    apiPortDraft.value = apiState.value?.port ?? null;
    message.warning(t('settings.apiPortInvalid'));
    return;
  }
  apiBusy.value = true;
  try {
    syncApiDrafts(await SaveApiSettings({ port, token: String(apiTokenDraft.value || '').trim() }));
  } catch (err) {
    message.error(t('settings.apiToggleFailed', { error: err }));
  } finally {
    apiBusy.value = false;
  }
}

async function regenerateApiToken() {
  await saveApiConfig();
  apiBusy.value = true;
  try {
    syncApiDrafts(await SaveApiSettings({ port: Number(apiPortDraft.value) || 0, token: '' }));
    message.success(t('settings.apiRegenerated'));
  } catch (err) {
    message.error(t('settings.apiToggleFailed', { error: err }));
  } finally {
    apiBusy.value = false;
  }
}

async function copyApiToken() {
  const token = apiState.value?.token || '';
  if (!token) return;
  try {
    await navigator.clipboard.writeText(token);
    message.success(t('app.copy.done'));
  } catch {
    message.error(t('app.copy.failed'));
  }
}

// 完整参数文档随仓库发布：docs/api.md。
function openApiDocs() {
  Browser.OpenURL('https://github.com/Bronya0/ally-agent/blob/main/docs/api.md');
}

// The panel stays mounted (parent v-show) so in-page state — active tab,
// scroll position, unsaved draft edits — survives switching sider modes.
// The draft is synced once on first open, afterwards unsaved edits persist
// until the user hits 保存.
let draftSynced = false;

// The settings pages never edit the workspace — it is a pass-through of the
// active tab's path. The draft is synced only once (to preserve unsaved
// edits), so without this mirror a stale workspace would ride along on every
// silent save (proxy watch), flow
// back into App.vue's onSettingsSave and hijack the current workspace tab
// via syncConfigToActiveTab.
watch(() => props.configDraft.workspace, (value) => {
  if (draft.workspace !== value) draft.workspace = value;
});

watch(() => props.visible, (visible) => {
  if (!visible) return;
  if (!draftSynced) {
    syncDraftFromProps();
    draftSynced = true;
  }
  loadApiState();
  if (draft.proxyMode === 'system') detectProxy();
  refreshAutostart();
});
</script>

<style scoped>
/* Inline settings panel: fills the main-area container instead of a modal
   card. The inner layout stretches to the available height and the content
   column scrolls. */
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

.config-inline-body {
  flex: 1;
  min-height: 0;
}

.settings-nav {
  width: 100%;
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
  color: var(--ally-text-muted);
  cursor: pointer;
  text-align: left;
  transition: background 0.12s, color 0.12s;
  line-height: 1.3;
  --wails-draggable: no-drag;
}

.settings-nav-item:hover {
  background: var(--ally-state-hover);
  color: var(--ally-text-body);
}

.settings-nav-item.active {
  background: var(--ally-hover-strong);
  color: var(--ally-text-primary);
}

.settings-nav-title {
  display: block;
  font-size: var(--ally-sub-font-size);
  font-weight: 600;
}

.settings-content {
  min-width: 0;
}

.settings-page {
  padding: 0;
}

.settings-page-actions {
  display: flex;
  justify-content: flex-start;
  margin-top: 16px;
  gap: 8px;
}

.license-notice {
  padding: 18px;
  border: 1px solid var(--ally-border);
  border-radius: 10px;
  background: var(--ally-hover-faint);
  color: var(--ally-text-tertiary);
  line-height: 1.65;
}

.license-notice-title {
  margin-bottom: 10px;
  color: var(--ally-text-primary);
  font-size: 15px;
  font-weight: 650;
}

.license-copyleft {
  margin: 8px 0;
  padding: 8px 12px;
  border-left: 3px solid #d8a657;
  background: rgba(216, 166, 87, 0.08);
  border-radius: 0 4px 4px 0;
  color: var(--ally-text-body);
  font-size: 12px;
  line-height: 1.6;
}

.about-update-check {
  display: flex;
  align-items: center;
  gap: 10px;
  margin-top: 14px;
}

.about-update-status {
  color: var(--ally-text-muted);
  font-size: var(--ally-sub-font-size);
}

.proxy-warning { margin-bottom: 12px; }
.proxy-actions { display: flex; gap: 8px; margin-bottom: 12px; }
.proxy-status-card { display: grid; gap: 8px; padding: 12px; border: 1px solid rgba(255,255,255,.08); border-radius: 8px; background: rgba(255,255,255,.025); }
.proxy-status-card > div { display: grid; grid-template-columns: 90px minmax(0,1fr); gap: 10px; font-size: 12px; }
.proxy-status-card span { color: var(--ally-text-faint); }
.proxy-status-card strong { overflow-wrap: anywhere; color: var(--ally-text-body); font-family: var(--ally-mono-font); font-weight: 500; }

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

.settings-toggle-row {
  display: flex;
  align-items: center;
  gap: 12px;
}

/* Appearance mode picker: two quiet segmented buttons. The active segment
   rides on a soft accent wash so the control stays calm in both modes. */
.appearance-mode-row {
  display: inline-flex;
  gap: 6px;
  padding: 3px;
  border-radius: 8px;
  border: 1px solid var(--ally-border);
  background: var(--ally-hover-faint);
}

.appearance-mode-btn {
  padding: 4px 18px;
  border: 0;
  border-radius: 6px;
  background: transparent;
  color: var(--ally-text-muted);
  font-size: var(--ally-sub-font-size);
  line-height: 1.4;
  cursor: pointer;
  transition: background 0.12s ease, color 0.12s ease;
  --wails-draggable: no-drag;
}

.appearance-mode-btn:hover {
  color: var(--ally-text-body);
}

.appearance-mode-btn.active {
  color: var(--ally-accent-strong);
  background: color-mix(in srgb, var(--ally-accent) 14%, transparent);
  font-weight: 600;
}

.settings-toggle-hint {
  font-size: 12px;
  color: var(--ally-text-muted);
  line-height: 1.5;
}

.validation-settings-list {
  border-top: 1px solid var(--ally-border);
}

.validation-setting-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  padding: 12px 2px;
  border-bottom: 1px solid var(--ally-border-subtle);
}

.validation-setting-copy {
  min-width: 0;
}

.validation-setting-label {
  color: var(--ally-text-high);
  font-size: 13px;
  font-weight: 600;
}

.validation-setting-hint {
  margin-top: 3px;
  color: var(--ally-text-muted);
  font-size: 12px;
  line-height: 1.4;
}

.settings-field-stack {
  width: 100%;
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.font-size-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(170px, 1fr));
  gap: 8px 12px;
  width: 100%;
  margin-bottom: 6px;
}

.font-size-field {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
}

.font-size-label {
  flex: none;
  font-size: 12px;
  color: var(--ally-text-secondary);
  white-space: nowrap;
}

.font-size-field .n-input-number {
  flex: 1;
  min-width: 0;
}

.settings-field-hint {
  color: var(--ally-text-faint);
  font-size: 11px;
  line-height: 1.45;
}

.user-agent-option {
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
}

.background-image-row {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}

.background-image-status {
  font-size: 12px;
  color: var(--ally-text-muted);
}

.api-header-actions {
  display: flex;
  align-items: center;
  gap: 8px;
}

.api-config-row {
  display: flex;
  align-items: center;
  gap: 8px;
  width: 100%;
}

.api-config-row .n-switch {
  flex-shrink: 0;
}

.api-token-input {
  flex: 1;
  min-width: 160px;
  font-family: var(--ally-mono-font);
}

.api-port-input {
  width: 120px;
  flex-shrink: 0;
}

.api-hint {
  font-size: 11px;
  color: var(--text-tertiary, var(--ally-text-muted));
  margin: 4px 0 2px;
  line-height: 1.5;
}

.api-endpoints {
  margin-top: 14px;
  border-top: 1px solid var(--border-color, #333);
  padding-top: 10px;
}

.api-endpoints-title {
  font-size: 12px;
  font-weight: 600;
  margin-bottom: 8px;
}

.api-endpoint-row {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 3px 0;
  font-size: 12px;
}

.api-method {
  flex-shrink: 0;
  width: 56px;
  justify-content: center;
}

.api-path {
  flex-shrink: 0;
  color: var(--ally-info-soft);
  font-family: var(--ally-mono-font);
  font-size: 11.5px;
}

.api-desc {
  color: var(--text-tertiary, var(--ally-text-muted));
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

@media (max-width: 640px) {
  .config-modal {
    max-width: calc(100vw - 24px);
  }
}
</style>
