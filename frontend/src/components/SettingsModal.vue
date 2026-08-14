<!--
SPDX-License-Identifier: GPL-3.0-only

Copyright (C) 2026 tangssst <tangssst@qq.com>
GitHub: https://github.com/Bronya0/ally-agent

This file is part of ally-agent, licensed under the GNU General
Public License v3. See the LICENSE file for details.
-->
<template>
  <n-modal
    :show="visible"
    preset="card"
    :title="$t('settings.title')"
    class="config-modal"
    :style="settingsModalStyle"
    :bordered="true"
    @update:show="onClose"
    @after-leave="emit('closed')"
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
        <button :class="['settings-nav-item', { active: page === 'network' }]" @click="page = 'network'">
          <span class="settings-nav-title">{{ $t('settings.network') }}</span>
          <span class="settings-nav-desc">{{ $t('settings.networkDescription') }}</span>
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
          <n-form-item :label="$t('settings.userAgent')">
            <div class="settings-field-stack">
              <n-input
                v-model:value="draft.userAgent"
                :placeholder="$t('settings.userAgentPlaceholder')"
              />
              <div class="user-agent-presets">
                <span class="user-agent-presets-label">{{ $t('settings.userAgentPresets') }}</span>
                <button
                  v-for="ua in userAgentPresets"
                  :key="ua"
                  type="button"
                  class="user-agent-preset-chip"
                  :title="ua"
                  @click="draft.userAgent = ua"
                >{{ userAgentPresetLabel(ua) }}</button>
              </div>
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
          <n-alert v-if="proxyTestResult" :type="proxyTestResult.ok ? 'success' : 'error'" :show-icon="false" class="proxy-test-result">
            {{ proxyTestResult.ok
              ? $t('settings.proxyTestSuccess', { status: proxyTestResult.statusCode, duration: proxyTestResult.durationMs, proxy: proxyTestResult.proxy || $t('settings.proxyDirect') })
              : $t('settings.proxyTestFailed', { error: proxyTestResult.error || '-' }) }}
          </n-alert>
        </section>

        <!-- Models -->
        <section v-else-if="page === 'models'" class="settings-page">
          <div class="config-section-header">
            <div>
              <div class="config-section-title">{{ $t('settings.modelsTitle') }}</div>
              <div class="config-section-subtitle">{{ $t('settings.modelsSubtitle') }}</div>
            </div>
            <n-space :size="8">
              <n-button size="small" secondary @click="openModelImport">{{ $t('settings.modelImport') }}</n-button>
              <n-button size="small" secondary :disabled="!draft.models?.length" @click="exportModelConfigs">{{ $t('settings.modelExport') }}</n-button>
              <n-button size="small" type="primary" @click="startAddModelDraft">{{ $t('settings.modelAdd') }}</n-button>
            </n-space>
            <input
              ref="modelImportInput"
              class="model-import-input"
              type="file"
              accept="application/json,.json"
              @change="importModelConfigs"
            />
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
    </div>
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
        <n-form-item-gi :label="$t('settings.providerPreset')" :span="2">
          <n-select
            v-model:value="selectedCatalogProviderId"
            :options="catalogProviderOptions"
            :loading="modelCatalogLoading"
            filterable
            :placeholder="$t('settings.providerPresetPlaceholder')"
            @update:value="selectCatalogProvider"
          />
        </n-form-item-gi>
        <n-form-item-gi v-if="selectedCatalogProvider" :label="$t('settings.catalogModel')" :span="2">
          <n-select
            :value="modelDraft.model"
            :options="selectedCatalogModelOptions"
            filterable
            tag
            :placeholder="$t('settings.catalogModelPlaceholder')"
            @update:value="selectCatalogModel"
          />
        </n-form-item-gi>
        <n-form-item-gi :label="$t('settings.providerName')">
          <n-input v-model:value="modelDraft.providerName" placeholder="OpenAI Compatible" />
        </n-form-item-gi>
        <n-form-item-gi :label="$t('settings.apiFormat')">
          <n-select v-model:value="modelDraft.apiFormat" :options="apiFormatOptions" />
        </n-form-item-gi>
        <n-form-item-gi v-if="!selectedCatalogProvider" label="Model">
          <n-input v-model:value="modelDraft.model" :placeholder="modelPlaceholder(modelDraft.apiFormat)" />
        </n-form-item-gi>
        <n-form-item-gi :label="normalizeApiFormat(modelDraft.apiFormat) === 'anthropic_messages' ? $t('settings.baseUrlNoV1') : 'Base URL'">
          <n-input v-model:value="modelDraft.baseUrl" :placeholder="apiFormatDefaultBaseUrl(modelDraft.apiFormat)" autocomplete="off" />
        </n-form-item-gi>
        <n-form-item-gi :label="$t('settings.apiKeys')" :span="2">
          <div class="api-key-list">
            <div v-for="(key, ki) in modelDraft.apiKeys" :key="ki" class="api-key-row">
              <span class="api-key-index">{{ ki + 1 }}</span>
              <n-input
                v-model:value="modelDraft.apiKeys[ki]"
                type="password"
                show-password-on="click"
                autocomplete="new-password"
                :placeholder="$t('settings.apiKeyPlaceholder')"
              />
              <n-button
                quaternary
                size="small"
                :disabled="(modelDraft.apiKeys || []).length <= 1"
                :title="$t('settings.apiKeyRemove')"
                @click="removeModelApiKey(ki)"
              >
                ×
              </n-button>
            </div>
            <n-button size="small" dashed class="api-key-add" @click="addModelApiKey">
              + {{ $t('settings.apiKeyAdd') }}
            </n-button>
            <div class="api-key-hint">{{ $t('settings.apiKeysHint') }}</div>
          </div>
        </n-form-item-gi>
        <n-form-item-gi label="Max Tokens">
          <n-input-number v-model:value="modelDraft.maxTokens" :min="0" style="width: 100%" />
        </n-form-item-gi>
        <n-form-item-gi :label="$t('settings.reasoningTag')">
          <n-input
            v-model:value="modelDraft.reasoningTag"
            :placeholder="$t('settings.reasoningTagHint')"
          />
        </n-form-item-gi>
        <n-form-item-gi
          v-if="normalizeApiFormat(modelDraft.apiFormat) === 'openai_chat'"
          :label="$t('settings.tokenParam')"
        >
          <n-select v-model:value="modelDraft.tokenParam" :options="tokenParamOptions" />
        </n-form-item-gi>
        <n-form-item-gi
          :label="$t('settings.reasoningEffort')"
          :span="normalizeApiFormat(modelDraft.apiFormat) === 'openai_chat' ? 1 : 2"
        >
          <n-select v-model:value="modelDraft.reasoningEffort" :options="reasoningEffortOptions" />
        </n-form-item-gi>
        <n-form-item-gi :label="$t('settings.contextWindow')" :span="2">
          <div class="context-window-field">
            <n-input-number v-model:value="modelDraft.contextWindow" :min="0" />
            <div class="context-window-presets" :aria-label="$t('settings.contextWindowPresets')">
              <span class="context-window-presets-label">{{ $t('settings.contextWindowPresets') }}</span>
              <n-button-group size="small">
                <n-button
                  v-for="preset in contextWindowQuickOptions"
                  :key="preset.value"
                  :type="modelDraft.contextWindow === preset.value ? 'primary' : 'default'"
                  :aria-pressed="modelDraft.contextWindow === preset.value"
                  @click="modelDraft.contextWindow = preset.value"
                >
                  {{ preset.label }}
                </n-button>
              </n-button-group>
            </div>
          </div>
        </n-form-item-gi>
      </n-grid>
      <n-alert v-if="selectedCatalogProvider" type="info" :show-icon="false" class="model-format-hint">
        <span>{{ $t('settings.providerPresetHint', { provider: selectedCatalogProvider.name }) }}</span>
        <n-button v-if="selectedCatalogProvider.doc" text type="primary" class="provider-doc-button" @click="openProviderDocumentation">
          {{ $t('settings.providerDocumentation') }}
        </n-button>
      </n-alert>
      <n-alert v-else-if="normalizeApiFormat(modelDraft.apiFormat) === 'anthropic_messages'" type="info" :show-icon="false" class="model-format-hint">
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
import { naiveDateLocale, naiveLocale, reasoningEffortLabel, t } from '../i18n.mjs';
import { buildModelConfigExport, mergeModelConfigs, modelConfigIdentity, normalizeApiKeysArray, normalizeReasoningEffort, parseModelConfigImport, reasoningEffortLevels } from '../utils/modelConfigIO.mjs';
import { saveTextFile } from '../utils/download.mjs';
import {
  CUSTOM_PROVIDER_ID,
  applyCatalogPreset,
  findCatalogModel,
  findCatalogProvider,
  providerCatalogOptions,
  providerModelOptions,
} from '../utils/modelProviderCatalog.mjs';
import { Browser } from '@wailsio/runtime';
import {
  GetMcpConfig, GetMcpServers, SaveMcpConfig, RestartMcpServers,
  ListSkills, ActivateSkill, DeactivateSkill, ClearSkills, GetActiveSkills,
  ListTools, OpenPathInFileManager,
  TestModelConnection,
  DetectSystemProxy, TestProxy,
  SelectBackgroundImage, ClearBackgroundImage,
  GetAutostartEnabled, SetAutostartEnabled,
} from '../../bindings/ally-dev/internal/app/app';

const { message } = createDiscreteApi(['message'], {
  configProviderProps: { theme: darkTheme, locale: naiveLocale, dateLocale: naiveDateLocale },
});

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
});
const emit = defineEmits(['close', 'closed', 'save', 'skills-changed', 'mcp-saved', 'background-changed', 'check-update']);
const checkUpdateBusy = ref(false);
const checkUpdateMessage = ref('');
let checkUpdateTimer = 0;

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

// Deep-clone the config draft so changes don't mutate parent reactively until save
const draft = reactive(cloneConfigDraft(props.configDraft));

// Accent theme is a pure front-end preference (localStorage), independent of the
// backend config draft. Applied live on selection.

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
const modelCatalog = ref({ providers: [] });
const modelCatalogLoading = ref(false);
const selectedCatalogProviderId = ref(CUSTOM_PROVIDER_ID);
const modelImportInput = ref(null);
const testingModel = ref(false);
const proxyDetecting = ref(false);
const proxyTesting = ref(false);
const proxyStatus = ref(null);
const proxyTestResult = ref(null);
const proxyModeOptions = computed(() => [
  { label: t('settings.proxyOff'), value: 'off' },
  { label: t('settings.proxySystem'), value: 'system' },
  { label: t('settings.proxyManual'), value: 'manual' },
]);

// Background image picker state. Selecting/clearing persists immediately on
// the backend (the file write cannot be deferred to Save), so these actions
// also emit background-changed to let App.vue refresh the data URL live.
const backgroundSelecting = ref(false);
const backgroundClearing = ref(false);

// User-Agent presets: click a chip to fill the input above. Kept short for
// the chip label; the full string is shown in the tooltip.
const userAgentPresets = [
  'codex_cli_rs/0.76.0 (Debian 13.0.0; x86_64) WindowsTerminal',
  'claude-cli/2.1.161 (external, cli)',
];
function userAgentPresetLabel(ua) {
  // Show the leading product token (everything before the first space or /).
  const m = String(ua || '').match(/^([^\s/]+)/);
  return m ? m[1] : ua;
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
  proxyTestResult.value = null;
  try {
    proxyTestResult.value = await TestProxy({
      mode: draft.proxyMode,
      url: draft.proxyUrl || '',
      noProxy: draft.proxyNoProxy || '',
      targetUrl: draft.baseUrl || '',
    });
  } catch (err) {
    proxyTestResult.value = { ok: false, error: String(err) };
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

function defaultModelDraft(source = {}) {
  const keys = Array.isArray(source.apiKeys) && source.apiKeys.length
    ? source.apiKeys
    : source.apiKey
      ? [source.apiKey]
      : Array.isArray(draft?.apiKeys) && draft.apiKeys.length
        ? draft.apiKeys
        : draft?.apiKey ? [draft.apiKey] : [];
  return {
    providerName: draft?.providerName || 'OpenAI Compatible',
    apiFormat: normalizeApiFormat(draft?.apiFormat),
    baseUrl: draft?.baseUrl || '',
    apiKey: draft?.apiKey || '',
    model: '',
    temperature: draft?.temperature ?? 0.2,
    maxTokens: draft?.maxTokens || 384000,
    contextWindow: draft?.contextWindow || 1000000,
    ...source,
    apiKeys: normalizeModelApiKeys(keys),
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

const contextWindowQuickOptions = [
  { label: '256K', value: 256000 },
  { label: '512K', value: 512000 },
  { label: '1M', value: 1000000 },
  { label: '1.5M', value: 1500000 },
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
  const provider = selectedCatalogProvider.value;
  const model = findCatalogModel(provider, modelId);
  if (provider && model) Object.assign(modelDraft, applyCatalogPreset(provider, model, modelDraft));
}

function openProviderDocumentation() {
  if (selectedCatalogProvider.value?.doc) Browser.OpenURL(selectedCatalogProvider.value.doc);
}

async function startAddModelDraft() {
  modelEditorIndex.value = -1;
  await ensureModelCatalog();
  const provider = activeProviderTab.value || draft.providerName || 'OpenAI Compatible';
  assignModelDraft({
    providerName: provider,
    apiFormat: normalizeApiFormat(draft.apiFormat),
    baseUrl: draft.baseUrl || '',
    apiKey: draft.apiKey || '',
    apiKeys: Array.isArray(draft.apiKeys) ? draft.apiKeys : [],
    model: draft.model || '',
    maxTokens: draft.maxTokens || 384000,
    contextWindow: draft.contextWindow || 1000000,
  });
  modelEditorVisible.value = true;
}

async function editModelDraft(index) {
  if (!draft.models || !draft.models[index]) return;
  modelEditorIndex.value = index;
  await ensureModelCatalog();
  assignModelDraft(draft.models[index]);
  modelEditorVisible.value = true;
}

function cancelModelDraft() {
  modelEditorVisible.value = false;
  modelEditorIndex.value = -1;
  selectedCatalogProviderId.value = CUSTOM_PROVIDER_ID;
}

async function testModelConnection() {
  if (testingModel.value) return;
  const model = (modelDraft.model || '').trim();
  const apiKeys = normalizeModelApiKeys(modelDraft.apiKeys || []);
  if (!model) {
    message.warning(t('app.config.modelRequired'));
    return;
  }
  if (!apiKeys.length) {
    message.warning(t('settings.apiKeyRequired'));
    return;
  }
  testingModel.value = true;
  try {
    await TestModelConnection({
      providerName: normalizedProviderName(modelDraft.providerName),
      apiFormat: normalizeApiFormat(modelDraft.apiFormat),
      baseUrl: (modelDraft.baseUrl || '').trim(),
      apiKey: apiKeys[0],
      apiKeys,
      model,
      temperature: modelDraft.temperature ?? 0.2,
      maxTokens: modelDraft.maxTokens || 384000,
      contextWindow: modelDraft.contextWindow || 1000000,
      reasoningTag: modelDraft.reasoningTag || 'reasoning_content',
      tokenParam: modelDraft.tokenParam || 'auto',
      reasoningEffort: normalizeReasoningEffort(modelDraft.reasoningEffort),
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
  if (!apiKeys.length) {
    message.warning(t('settings.apiKeyRequired'));
    return;
  }
  const providerName = normalizedProviderName(modelDraft.providerName);
  const apiFormat = normalizeApiFormat(modelDraft.apiFormat);
  const nextModel = {
    providerName,
    apiFormat,
    baseUrl: (modelDraft.baseUrl || '').trim(),
    apiKey: apiKeys[0] || '',
    apiKeys,
    model,
    temperature: modelDraft.temperature ?? draft.temperature ?? 0.2,
    maxTokens: modelDraft.maxTokens || draft.maxTokens || 384000,
    contextWindow: modelDraft.contextWindow || draft.contextWindow || 1000000,
    reasoningTag: modelDraft.reasoningTag || 'reasoning_content',
    tokenParam: modelDraft.tokenParam || 'auto',
    reasoningEffort: normalizeReasoningEffort(modelDraft.reasoningEffort),
  };
  const wasActive = modelEditorIndex.value >= 0 && isDraftModelActive(draft.models[modelEditorIndex.value]);
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
  if (wasActive) applyModelToDraft(nextModel);
  alignActiveProviderTab(providerName);
  modelEditorVisible.value = false;
  emit('save', { ...draft }, true);
}

function applyModelToDraft(model) {
  if (!model) return;
  draft.providerName = normalizedProviderName(model.providerName);
  draft.apiFormat = normalizeApiFormat(model.apiFormat);
  draft.baseUrl = model.baseUrl || '';
  draft.apiKeys = normalizeModelApiKeys(model.apiKeys || (model.apiKey ? [model.apiKey] : []));
  draft.apiKey = draft.apiKeys[0] || '';
  draft.model = model.model || '';
  draft.temperature = model.temperature ?? draft.temperature ?? 0.2;
  draft.maxTokens = model.maxTokens || draft.maxTokens || 384000;
  draft.contextWindow = model.contextWindow || draft.contextWindow || 1000000;
  draft.reasoningTag = model.reasoningTag || 'reasoning_content';
  draft.tokenParam = model.tokenParam || 'auto';
  draft.reasoningEffort = normalizeReasoningEffort(model.reasoningEffort);
  alignActiveProviderTab(normalizedProviderName(model.providerName));
  emit('save', { ...draft }, true);
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
    const activeIdentity = modelConfigIdentity(draft);
    const importedActiveModel = [...imported].reverse().find((model) => modelConfigIdentity(model) === activeIdentity);
    const result = mergeModelConfigs(draft.models, imported);
    draft.models = result.models;
    if (importedActiveModel) applyModelToDraft(importedActiveModel);
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

watch(() => modelDraft.apiFormat, (next, previous) => {
  if (!modelEditorVisible.value) return;
  // Switching the API format never rewrites Base URL: it is left to manual
  // input (or filled by an explicit catalog provider preset). The per-format
  // default is only shown as the input placeholder.
  const nextFormat = normalizeApiFormat(next);
  if (nextFormat === 'anthropic_messages') {
    if (!modelDraft.maxTokens || modelDraft.maxTokens > 64000) modelDraft.maxTokens = 8192;
  } else if (normalizeApiFormat(previous) === 'anthropic_messages' && modelDraft.maxTokens === 8192) {
    modelDraft.maxTokens = 384000;
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
    if (draft.proxyMode === 'system') detectProxy();
    refreshAutostart();
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
  height: 460px;
  min-height: 460px;
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
  font-size: var(--ally-sub-font-size);
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
  height: 460px;
  max-height: 460px;
  overflow-y: auto;
}

.settings-page {
  padding: 0;
}

.settings-page-actions {
  display: flex;
  justify-content: flex-end;
  margin-top: 16px;
  gap: 8px;
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

.license-copyleft {
  margin: 8px 0;
  padding: 8px 12px;
  border-left: 3px solid #d8a657;
  background: rgba(216, 166, 87, 0.08);
  border-radius: 0 4px 4px 0;
  color: rgba(255, 255, 255, 0.85);
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
  color: rgba(255, 255, 255, 0.62);
  font-size: var(--ally-sub-font-size);
}

.model-format-hint {
  margin-top: 4px;
}

.provider-doc-button {
  margin-left: 8px;
  vertical-align: baseline;
}

.proxy-warning { margin-bottom: 12px; }
.proxy-actions { display: flex; gap: 8px; margin-bottom: 12px; }
.proxy-status-card { display: grid; gap: 8px; padding: 12px; border: 1px solid rgba(255,255,255,.08); border-radius: 8px; background: rgba(255,255,255,.025); }
.proxy-status-card > div { display: grid; grid-template-columns: 90px minmax(0,1fr); gap: 10px; font-size: 12px; }
.proxy-status-card span { color: #777; }
.proxy-status-card strong { overflow-wrap: anywhere; color: #d0d0d0; font-family: var(--ally-mono-font); font-weight: 500; }
.proxy-test-result { margin-top: 12px; }

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
  color: #b5b5b5;
  white-space: nowrap;
}

.font-size-field .n-input-number {
  flex: 1;
  min-width: 0;
}

.settings-field-hint,
.mcp-save-scope {
  color: #777;
  font-size: 11px;
  line-height: 1.45;
}

.user-agent-presets {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 6px;
  margin-top: 4px;
}
.user-agent-presets-label {
  color: #777;
  font-size: 11px;
}
.user-agent-preset-chip {
  border: 1px solid rgba(255, 255, 255, 0.12);
  background: rgba(255, 255, 255, 0.04);
  color: #b0b0b0;
  border-radius: 4px;
  padding: 2px 8px;
  font-size: 11px;
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  cursor: pointer;
  transition: background 0.15s, color 0.15s, border-color 0.15s;
}
.user-agent-preset-chip:hover {
  background: rgba(255, 255, 255, 0.1);
  color: #e0e0e0;
  border-color: rgba(255, 255, 255, 0.25);
}

.background-image-row {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}

.background-image-status {
  font-size: 12px;
  color: #8a8a8a;
}

.model-import-input {
  display: none;
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
  font-size: var(--ally-sub-font-size);
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
  font-size: var(--ally-sub-font-size);
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
  font-size: var(--ally-sub-font-size);
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
  font-size: var(--ally-sub-font-size);
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

.api-key-list {
  display: flex;
  flex-direction: column;
  gap: 6px;
  width: 100%;
}

.context-window-field {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px;
  width: 100%;
}

.context-window-field .n-input-number {
  flex: 1 1 140px;
}

.context-window-presets {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 6px;
}

.context-window-presets-label {
  flex-shrink: 0;
  color: #8a8a8a;
  font-size: 12px;
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
  color: #8a8a8a;
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
  color: #8a8a8a;
  font-size: 12px;
  line-height: 1.5;
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
    height: auto;
    min-height: 0;
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
    height: min(460px, calc(100vh - 260px));
    max-height: min(460px, calc(100vh - 260px));
  }
}
</style>
