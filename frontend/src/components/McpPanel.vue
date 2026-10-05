<!--
SPDX-License-Identifier: GPL-3.0-only

Copyright (C) 2026 tangssst <tangssst@qq.com>
GitHub: https://github.com/Bronya0/ally-agent

This file is part of ally-agent, licensed under the GNU General
Public License v3. See the LICENSE file for details.
-->

<template>
  <!-- Inline MCP page: rendered inside the main-area container (App.vue
       mode === 'mcp' via v-show). Always mounted so the server list, live
       statuses, and the editor draft survive mode switches. Extracted from
       the old Settings → MCP page so MCP lives on the mode rail below the
       knowledge base, fully decoupled from the Settings panel. -->
  <div class="config-inline-panel">
    <header class="config-inline-header">
      <span class="config-inline-title">{{ t('app.mode.mcp') }}</span>
      <div class="panel-header-actions">
        <n-input
          v-model:value="mcpSearch"
          class="panel-search-input"
          size="small"
          clearable
          :placeholder="t('common.searchPlaceholder')"
        >
          <template #prefix><SearchOutlined class="panel-search-icon" /></template>
        </n-input>
        <n-button size="small" secondary @click="openMcpImport">{{ t('settings.modelImport') }}</n-button>
        <n-button size="small" secondary :disabled="!mcpFormServers.length" @click="exportMcpConfig">{{ t('settings.modelExport') }}</n-button>
        <n-button size="small" secondary :loading="mcpLoading" @click="loadMcpConfig">{{ t('common.refresh') }}</n-button>
        <n-dropdown trigger="click" :options="mcpMarketOptions" @select="openExternalLink">
          <n-button size="small" secondary :title="t('app.mcp.marketplace')">
            <template #icon><AppstoreOutlined /></template>
            {{ t('app.mcp.marketplace') }}
          </n-button>
        </n-dropdown>
        <n-button size="small" type="primary" @click="openMcpEditor(-1)">{{ t('common.add') }}</n-button>
      </div>
    </header>

    <div class="panel-scroll-body">
      <!-- 与旧高级设置页同一套双 tab 结构：MCP 服务器 / 内置工具启停。
           内置工具（自高级设置迁入）与 MCP 工具注入同页管理。 -->
      <n-tabs v-model:value="mcpPageTab" type="line" size="small" class="mcp-page-tabs">
        <n-tab name="servers">{{ $t('settings.mcpServersTab') }}</n-tab>
        <n-tab name="builtinTools">{{ $t('settings.toolsTitle') }}</n-tab>
      </n-tabs>

      <template v-if="mcpPageTab === 'servers'">
        <div class="config-section-subtitle">{{ t('settings.mcpSubtitle') }}</div>

        <input
          ref="mcpImportInput"
          class="model-import-input"
          type="file"
          accept="application/json,.json"
          @change="importMcpConfig"
        />

        <div class="mcp-save-scope">{{ t('settings.mcpSaveScope') }}</div>

        <!-- Unified server list with live status per row: dot + name + badges
             on the left; tools entry / status / switch / edit / delete pushed
             right in the same line. The built-in tools tab keeps its own
             auto-fill card grid. -->
        <div class="mcp-form-mode">
          <div v-if="!mcpFormServers.length" class="saved-model-empty">{{ t('settings.mcpEmpty') }}</div>
          <div v-else-if="!filteredMcpServers.length" class="saved-model-empty">{{ t('common.searchEmpty') }}</div>
          <div v-for="entry in filteredMcpServers" :key="entry.srv._key" class="mcp-server-row">
            <div class="mcp-row-main">
              <span :class="['mcp-dot', mcpStatusFor(entry.srv).status]"></span>
              <span class="mcp-name" :title="entry.srv.name?.trim() || $t('settings.mcpUnnamedServer')">{{ entry.srv.name?.trim() || $t('settings.mcpUnnamedServer') }}</span>
              <span class="mcp-badge">{{ t(transportLabel(entry.srv.transport)) }}</span>
              <span v-if="entry.srv.enabled === false" class="mcp-badge off">{{ $t('settings.mcpStatusDisabled') }}</span>
              <div class="mcp-row-side">
                <button
                  v-if="(mcpStatusFor(entry.srv).tools || []).length"
                  class="mcp-tools-toggle"
                  :title="$t('settings.mcpToolsHint')"
                  @click="toggleMcpToolsPanel(entry.srv._key)"
                >
                  {{ $t('settings.mcpToolsToggle', { injected: mcpInjectedCount(entry.srv), total: (mcpStatusFor(entry.srv).tools || []).length }) }}
                </button>
                <span v-else-if="mcpStatusFor(entry.srv).toolCount" class="mcp-tools">{{ $t('tools.count', { count: mcpStatusFor(entry.srv).toolCount }) }}</span>
                <span :class="['mcp-status-text', mcpStatusFor(entry.srv).status]" :title="mcpStatusFor(entry.srv).error || ''">{{ $t(mcpStatusLabel(mcpStatusFor(entry.srv).status)) }}</span>
                <n-switch :value="entry.srv.enabled" size="small" @update:value="(value) => toggleMcpEnabled(entry.srv, value)" />
                <n-button size="tiny" quaternary @click="openMcpEditor(entry.idx)">{{ $t('common.edit') }}</n-button>
                <n-button size="tiny" quaternary type="error" @click="removeMcpServer(entry.idx)">{{ $t('common.delete') }}</n-button>
              </div>
            </div>
            <div v-if="mcpStatusFor(entry.srv).error" class="mcp-list-error" :title="mcpStatusFor(entry.srv).error">{{ mcpStatusFor(entry.srv).error }}</div>
            <!-- Per-server tool injection toggles. Checkbox = injected
                 (checked by default); storage stays a blacklist
                 (disabledTools) so server-side additions default on. -->
            <div v-if="mcpToolsPanelOpen(entry.srv._key) && (mcpStatusFor(entry.srv).tools || []).length" class="mcp-tools-panel">
              <div class="mcp-tools-hint">{{ $t('settings.mcpToolsHint') }}</div>
              <n-checkbox-group :value="mcpInjectedTools(entry.srv)" @update:value="(value) => setMcpInjectedTools(entry.srv, value)">
                <n-checkbox v-for="tool in mcpStatusFor(entry.srv).tools" :key="tool.name" :value="tool.name" class="mcp-tool-check">
                  <span class="mcp-tool-line">
                    <span class="mcp-tool-name">{{ tool.name }}</span>
                    <span v-if="tool.description" class="mcp-tool-desc" :title="tool.description">{{ tool.description }}</span>
                  </span>
                </n-checkbox>
              </n-checkbox-group>
            </div>
          </div>
        </div>
      </template>

      <template v-else>
        <!-- 内置工具启停（自高级设置迁入）：停用名单存 config.disabledTools
             （核心五件锁死），开关即持久化（经 App.vue 的 queueConfigSave），
             后端只在**新会话**注入 schema 时过滤。布局一行多卡（auto-fill）。 -->
        <div class="config-section-subtitle">{{ $t('settings.toolsSubtitle') }}</div>
        <div class="builtin-tool-grid">
          <div v-for="tool in builtinToolSettings" :key="tool.name" class="mcp-server-row builtin-tool-card">
            <div class="mcp-row-main">
              <span class="mcp-name" :title="tool.label">{{ tool.label }}</span>
              <div class="mcp-row-side">
                <n-switch
                  :value="builtinToolEnabled(tool.name)"
                  :disabled="tool.disabled"
                  size="small"
                  @update:value="(value) => setBuiltinToolEnabled(tool.name, value)"
                />
              </div>
            </div>
            <div class="builtin-tool-meta">
              <span class="mcp-badge">{{ tool.name }}</span>
              <span v-if="tool.hint" class="builtin-tool-hint">{{ tool.hint }}</span>
            </div>
          </div>
        </div>
      </template>
    </div>
  </div>

  <!-- MCP server editor sub-modal: adding and editing both happen in a
       sub-modal, committed on Save. -->
  <n-modal
    :show="mcpEditorVisible"
    preset="card"
    :title="mcpEditorIndex >= 0 ? $t('settings.mcpEditServer') : $t('settings.mcpAddServerTitle')"
    :style="mcpFormModalStyle"
    :mask-closable="false"
    @update:show="(v) => { if (!v) mcpEditorVisible = false; }"
  >
    <n-form label-placement="top">
      <n-grid :cols="2" :x-gap="12">
        <n-form-item-gi :label="$t('settings.mcpServerName')" :span="1">
          <n-input v-model:value="mcpEditorDraft.name" :placeholder="$t('settings.mcpServerName')" />
        </n-form-item-gi>
        <n-form-item-gi :label="$t('settings.mcpTransport')" :span="1">
          <n-select
            v-model:value="mcpEditorDraft.transport"
            :options="[
              { label: $t('settings.mcpTransportStdio'), value: 'stdio' },
              { label: $t('settings.mcpTransportSse'), value: 'sse' },
              { label: $t('settings.mcpTransportStreamableHttp'), value: 'streamable-http' },
            ]"
          />
        </n-form-item-gi>
        <template v-if="mcpEditorDraft.transport === 'stdio'">
          <n-form-item-gi :label="$t('settings.mcpCommand')" :span="2">
            <n-input v-model:value="mcpEditorDraft.command" :placeholder="$t('settings.mcpCommand')" spellcheck="false" />
          </n-form-item-gi>
          <n-form-item-gi :label="$t('settings.mcpArgs')" :span="2">
            <n-input v-model:value="mcpEditorDraft.args" :placeholder="$t('settings.mcpArgsPlaceholder')" spellcheck="false" />
          </n-form-item-gi>
          <n-form-item-gi :label="$t('settings.mcpEnv')" :span="2">
            <n-input v-model:value="mcpEditorDraft.env" type="textarea" :rows="2" :placeholder="$t('settings.mcpEnv')" spellcheck="false" />
          </n-form-item-gi>
        </template>
        <template v-else>
          <n-form-item-gi :label="$t('settings.mcpUrl')" :span="2">
            <n-input v-model:value="mcpEditorDraft.url" :placeholder="$t('settings.mcpUrl')" spellcheck="false" />
          </n-form-item-gi>
          <n-form-item-gi :label="$t('settings.mcpHeaders')" :span="2">
            <n-input v-model:value="mcpEditorDraft.headers" type="textarea" :rows="2" :placeholder="$t('settings.mcpHeaders')" spellcheck="false" />
          </n-form-item-gi>
        </template>
      </n-grid>
    </n-form>
    <template #footer>
      <n-space justify="end">
        <n-button @click="mcpEditorVisible = false">{{ $t('common.cancel') }}</n-button>
        <n-button type="primary" @click="commitMcpEditor">{{ $t('common.save') }}</n-button>
      </n-space>
    </template>
  </n-modal>
</template>

<script setup>
import { computed, onMounted, onUnmounted, reactive, ref, watch } from 'vue';
import { useDialog, useMessage } from 'naive-ui';
import { SearchOutlined, AppstoreOutlined } from '@vicons/antd';
import { saveTextFile } from '../utils/download.mjs';
import { protectedToolNames, toggleableToolNames } from '../utils/builtinTools.mjs';
import { t } from '../i18n.mjs';
import { Browser, Events } from '@wailsio/runtime';
import { unwrapWailsEvent } from '../utils/wailsEvent.mjs';
import {
  GetMcpConfig, GetMcpServers, SaveMcpConfig, ReconcileMcpServers,
} from '../../bindings/ally-dev/internal/app/app';

// Inline MCP page (App.vue mode === 'mcp'): owns the whole MCP editing
// experience — server form rows, per-server tool injection toggles, the
// editor sub-modal, and live connection statuses. Saves go straight through
// SaveMcpConfig + ReconcileMcpServers (incremental reconnect) and are
// reported to App.vue via `mcp-saved` so tool inventories refresh.
const props = defineProps({
  show: { type: Boolean, default: false },
  // config.disabledTools（后端已落盘的内置工具停用名单）：开关层只接受已知
  // 工具名，核心五件在名单层就锁死不可停用。
  disabledTools: { type: Array, default: () => [] },
});
const emit = defineEmits(['mcp-saved', 'builtin-tools-changed']);
const message = useMessage();
const dialog = useDialog();

// MCP 市场下拉：均为公开免费站点，点击经系统默认浏览器打开（Browser.OpenURL
// 由 Wails 走宿主 shell，不经过 WebView 导航）。
const mcpMarketOptions = [
  { label: '魔搭 ModelScope', key: 'https://www.modelscope.cn/mcp' },
  { label: '火山引擎 Volcengine', key: 'https://www.volcengine.com/ats' },
  { label: 'Glama', key: 'https://glama.ai/mcp/servers?sort=recent-github-stargazers%3Adesc' },
  { label: 'PulseMCP', key: 'https://www.pulsemcp.com/servers' },
];

function openExternalLink(url) {
  if (url) Browser.OpenURL(url);
}

const mcpFormModalStyle = {
  width: 'min(580px, calc(100vw - 48px))',
  maxWidth: 'calc(100vw - 48px)',
};

// MCP state
const mcpConfigText = ref('');
const mcpLastAppliedJson = ref('');
const mcpServers = ref([]);
const mcpLoading = ref(false);
const mcpFormServers = ref([]); // array of {name, command, args, env, transport, url, headers, enabled}
const mcpImportInput = ref(null);
// 稳定 key 生成器：卡片支持删除/新增，索引 key 会让 Vue 就地复用 DOM，
// n-switch/n-select 等内部状态可能错位到相邻卡片上
let mcpServerKeyCounter = 0;
function nextMcpServerKey() {
  mcpServerKeyCounter += 1;
  return `mcp-srv-${mcpServerKeyCounter}`;
}

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

async function loadMcpConfig() {
  mcpLoading.value = true;
  try {
    mcpConfigText.value = await GetMcpConfig();
    mcpLastAppliedJson.value = mcpConfigText.value;
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
    const parsed = mcpConfigParseResult.value;
    if (!parsed.valid) {
      message.warning(parsed.text);
      return;
    }
    await SaveMcpConfig(mcpConfigText.value);
    // Incremental reconcile: only added/removed/changed servers reconnect;
    // untouched servers keep their live connections.
    await ReconcileMcpServers();
    mcpLastAppliedJson.value = mcpConfigText.value;
    mcpServers.value = await GetMcpServers() || [];
    message.success(t('app.mcp.saved'));
    emit('mcp-saved');
  } catch (err) {
    message.error(t('app.mcp.saveFailed', { error: err }));
  } finally {
    mcpLoading.value = false;
  }
}

// Auto-apply: switch toggles, editor commits, and deletions save and
// reconnect without an explicit apply button. Triggers are serialized so
// overlapping restarts cannot race, and a no-op change (the serialized config
// equals what is already applied) skips the reconnect.
let mcpApplyChain = Promise.resolve();

function autoApplyMcpConfig() {
  syncFormToJson();
  if (mcpConfigText.value === mcpLastAppliedJson.value) return;
  mcpApplyChain = mcpApplyChain.then(() => saveMcpConfigText()).catch(() => {});
}

// 只接受普通对象：外部/手写的 mcp.json 里出现 null、数组、字符串时不能让表单
// 构建半道抛错——外层 catch 会保住旧表单，导入看着成功、实际什么都没变，
// 随后 auto-apply 还会反过来用旧行盖掉用户刚导入的整份配置。
function mcpPlainObject(value) {
  return value && typeof value === 'object' && !Array.isArray(value) ? value : {};
}

function syncJsonToForm() {
  try {
    const parsed = mcpPlainObject(JSON.parse(mcpConfigText.value || '{}'));
    const servers = mcpPlainObject(parsed.mcpServers);
    mcpFormServers.value = Object.entries(servers).map(([name, rawCfg]) => {
      const cfg = mcpPlainObject(rawCfg);
      return {
        _key: nextMcpServerKey(),
        name,
        command: typeof cfg.command === 'string' ? cfg.command : '',
        // 模态框参数输入框是单行空格分隔格式；历史多行写法（每行一个参数）
        // 在这里一并归一化为空格分隔，保存时再按空白切分。
        args: Array.isArray(cfg.args) ? cfg.args.join(' ') : '',
        env: Object.entries(mcpPlainObject(cfg.env)).map(([k, v]) => `${k}=${v}`).join('\n'),
        transport: normalizeMcpTransport(cfg),
        url: typeof cfg.url === 'string' ? cfg.url : '',
        headers: Object.entries(mcpPlainObject(cfg.headers)).map(([k, v]) => `${k}: ${v}`).join('\n'),
        enabled: cfg.enabled !== false,
        disabledTools: Array.isArray(cfg.disabledTools) ? cfg.disabledTools.map((name) => String(name).trim()).filter(Boolean) : [],
        // 原样留着这一条服务端的完整 JSON：表单只掌管它认识的那几个字段，其余
        // （用户手写的 cwd / 超时、从 Claude/Cursor 导入的专有键）回写时原样带回。
        _raw: cfg,
      };
    });
    // 加载/刷新时排一次：开启在前、关闭沉底；之后开关切换不再重排，
    // 组内顺序保持配置文件里的原顺序（sort 稳定）。
    mcpFormServers.value.sort((a, b) => Number(a.enabled === false) - Number(b.enabled === false));
  } catch {
    // keep existing form data on parse error
  }
}

function syncFormToJson() {
  const servers = {};
  for (const srv of mcpFormServers.value) {
    if (!srv.name?.trim()) continue;
    // 表单掌管的字段每次往返都整体重写（先删后按当前输入写），其余键
    // （cwd / 超时 / 外部客户端专有字段）原样保留。整份重建会静默吃掉未知
    // 字段：导入一份 Claude/Cursor 的 mcp.json，保存一次就少一半。
    const cfg = { ...(srv._raw || {}) };
    for (const key of MCP_FORM_OWNED_KEYS) delete cfg[key];
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
      const args = String(srv.args || '').split(/\s+/).map((a) => a.trim()).filter(Boolean);
      if (args.length) cfg.args = args;
      const env = {};
      (srv.env || '').split('\n').forEach(line => {
        const idx = line.indexOf('=');
        if (idx > 0) env[line.slice(0, idx).trim()] = line.slice(idx + 1).trim();
      });
      if (Object.keys(env).length) cfg.env = env;
    }
    cfg.enabled = srv.enabled !== false;
    // 注入黑名单必须随表单往返保留，否则任何一次 auto-apply 都会把
    // syncFormToJson 重建的 JSON 里丢掉这个字段（勾选静默清零）。
    const disabledTools = (srv.disabledTools || []).map((name) => String(name).trim()).filter(Boolean);
    if (disabledTools.length) cfg.disabledTools = disabledTools;
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

// MCP server editor modal: adding and editing both happen in a sub-modal
// (same pattern as the settings model editor), committed on Save.
const mcpEditorVisible = ref(false);
const mcpEditorIndex = ref(-1);
const mcpEditorDraft = reactive(blankMcpServerForm());

function blankMcpServerForm() {
  return {
    _key: nextMcpServerKey(),
    // 新建的条目没有任何原始 JSON；显式占位，避免上一份草稿的 _raw 残留下来
    // （Object.assign 只覆盖出现过的键）。
    _raw: null,
    name: '',
    command: '',
    args: '',
    env: '',
    transport: 'stdio',
    url: '',
    headers: '',
    enabled: true,
    disabledTools: [],
  };
}

// 表单掌管的 server 字段：每次往返整体重写，其余键一律原样保留（见
// syncFormToJson）。由表单形状派生，加字段时不用记得改两处；name 也在内——
// 服务名以 JSON 外层键为准，条目内若自带 name 不该落盘成自相矛盾的一行。
const MCP_FORM_OWNED_KEYS = Object.keys(blankMcpServerForm()).filter((key) => !key.startsWith('_'));

// 工具勾选面板的展开状态按行 key 记录；默认收起，勾选计数在行侧常显。
const mcpExpandedToolPanels = ref(new Set());
function toggleMcpToolsPanel(key) {
  const next = new Set(mcpExpandedToolPanels.value);
  if (next.has(key)) next.delete(key);
  else next.add(key);
  mcpExpandedToolPanels.value = next;
}
function mcpToolsPanelOpen(key) {
  return mcpExpandedToolPanels.value.has(key);
}
function mcpInjectedCount(srv) {
  return mcpInjectedTools(srv).length;
}

// 勾选框语义 = 「注入」：勾上的是当前进入模型上下文的工具（默认全勾）。
// 存储仍是黑名单 disabledTools —— 差集换算保证对端新增工具默认启用。
function mcpInjectedTools(srv) {
  const tools = mcpStatusFor(srv).tools || [];
  const disabled = new Set(srv.disabledTools || []);
  return tools.map((tool) => tool.name).filter((name) => !disabled.has(name));
}
function setMcpInjectedTools(srv, checkedNames) {
  const checked = new Set(checkedNames);
  const tools = mcpStatusFor(srv).tools || [];
  srv.disabledTools = tools.map((tool) => tool.name).filter((name) => !checked.has(name));
  autoApplyMcpConfig();
}

function openMcpEditor(index) {
  const source = index >= 0 ? mcpFormServers.value[index] : null;
  mcpEditorIndex.value = index;
  // 编辑沿用原行的稳定 _key，提交替换后卡片 DOM 不会错位复用。
  Object.assign(mcpEditorDraft, blankMcpServerForm(), source ? JSON.parse(JSON.stringify(source)) : null);
  mcpEditorVisible.value = true;
}

function commitMcpEditor() {
  if (!String(mcpEditorDraft.name || '').trim()) {
    message.warning(t('settings.mcpNameRequired'));
    return;
  }
  const entry = JSON.parse(JSON.stringify(mcpEditorDraft));
  if (mcpEditorIndex.value >= 0) {
    mcpFormServers.value.splice(mcpEditorIndex.value, 1, entry);
  } else {
    mcpFormServers.value.push(entry);
  }
  mcpEditorVisible.value = false;
  autoApplyMcpConfig();
}

function openMcpImport() {
  if (!mcpImportInput.value) return;
  mcpImportInput.value.value = '';
  mcpImportInput.value.click();
}

async function importMcpConfig(event) {
  const input = event?.target;
  const file = input?.files?.[0];
  if (!file) return;
  try {
    if (file.size > 2 * 1024 * 1024) throw Object.assign(new Error('FILE_TOO_LARGE'), { code: 'FILE_TOO_LARGE' });
    const parsed = JSON.parse(await file.text());
    if (!parsed || typeof parsed !== 'object' || typeof parsed.mcpServers !== 'object' || parsed.mcpServers === null || Array.isArray(parsed.mcpServers)) {
      throw Object.assign(new Error('SERVERS_REQUIRED'), { code: 'SERVERS_REQUIRED' });
    }
    mcpConfigText.value = JSON.stringify(parsed, null, 2);
    syncJsonToForm();
    // Reconcile compares parsed configs, so a formatting-only diff (different
    // key order) keeps live connections and only real changes reconnect.
    autoApplyMcpConfig();
  } catch (err) {
    const code = String(err?.code || '');
    if (code === 'FILE_TOO_LARGE') message.error(t('settings.modelImportError.FILE_TOO_LARGE'));
    else if (code === 'SERVERS_REQUIRED') message.error(t('settings.mcpImportNeedsServers'));
    else message.error(t('settings.modelImportError.JSON_INVALID'));
  } finally {
    if (input) input.value = '';
  }
}

function exportMcpConfig() {
  syncFormToJson();
  const content = `${JSON.stringify(JSON.parse(mcpConfigText.value || '{}'), null, 2)}\n`;
  saveTextFile({
    filename: `ally-mcp-${new Date().toISOString().slice(0, 10)}.json`,
    content,
    filterName: 'JSON (*.json)',
    filterPattern: '*.json',
  }).then((result) => {
    if (result.saved) message.success(t('settings.mcpExportSuccess', { count: mcpFormServers.value.length }));
  });
}

function removeMcpServer(index) {
  mcpFormServers.value.splice(index, 1);
  autoApplyMcpConfig();
}

function toggleMcpEnabled(srv, value) {
  srv.enabled = value;
  autoApplyMcpConfig();
}

// ── 内置工具启停（自高级设置迁入）──
// 页内双 tab：MCP 服务器 / 内置工具启停，结构与旧高级设置页一致。
const mcpPageTab = ref('servers');

// props.disabledTools 就是后端已落盘的状态（App.vue 保存成功后原样传回）：
// 开关即持久化，与 MCP 配置的 auto-apply 同一交互口径。
const builtinToolSettings = computed(() => [
  ...protectedToolNames.map((name) => ({
    name,
    label: t(`settings.tool.${name}`),
    hint: t('settings.toolsProtected'),
    disabled: true,
  })),
  ...toggleableToolNames.map((name) => ({
    name,
    label: t(`settings.tool.${name}`),
    hint: '',
    disabled: false,
  })),
]);

function builtinToolEnabled(name) {
  return !(Array.isArray(props.disabledTools) && props.disabledTools.includes(name));
}

function setBuiltinToolEnabled(name, value) {
  if (value === builtinToolEnabled(name)) return;
  const label = t(`settings.tool.${name}`);
  dialog[value ? 'info' : 'warning']({
    title: t('settings.toolsConfirmTitle'),
    content: value
      ? t('settings.toolsConfirmEnable', { name: label })
      : t('settings.toolsConfirmDisable', { name: label }),
    positiveText: t('common.confirm'),
    negativeText: t('common.cancel'),
    onPositiveClick: () => applyBuiltinToolToggle(name, value),
  });
}

function applyBuiltinToolToggle(name, value) {
  const current = new Set(Array.isArray(props.disabledTools) ? props.disabledTools : []);
  if (value) current.delete(name);
  else current.add(name);
  emit('builtin-tools-changed', Array.from(current));
}

// Status of one form row, merged from the live server status list by name.
// The local enabled switch wins over the persisted status: a row switched off
// but not yet saved shows as disabled instead of its stale connection state.
function mcpStatusFor(srv) {
  const key = String(srv?.name || '').trim().toLowerCase();
  const found = key
    ? (mcpServers.value || []).find((item) => String(item?.name || '').trim().toLowerCase() === key)
    : null;
  if (srv?.enabled === false) {
    return { ...(found || {}), status: 'disabled' };
  }
  return found || { status: '', toolCount: 0, error: '' };
}

// 头部搜索框：按服务器名 / 命令 / URL / 传输方式 / 已发现工具名实时过滤，
// 工具名命中可反查其所属服务器。返回 { srv, idx } 保留 mcpFormServers
// 原始下标——编辑与删除始终按未过滤数组的下标操作。
const mcpSearch = ref('');

const filteredMcpServers = computed(() => {
  const needle = mcpSearch.value.trim().toLowerCase();
  const entries = mcpFormServers.value.map((srv, idx) => ({ srv, idx }));
  const matched = needle
    ? entries.filter(({ srv }) => {
        const tools = (mcpStatusFor(srv).tools || []).map((tool) => tool.name);
        return [srv.name, srv.command, srv.url, srv.transport, ...tools]
          .some((field) => String(field || '').toLowerCase().includes(needle));
      })
    : entries;
  // 不排序：展示顺序 = syncJsonToForm 加载时定下的顺序，开关切换不重排。
  return matched;
});

function mcpStatusLabel(status) {
  switch (status) {
    case 'connected': return 'settings.mcpStatusConnected';
    case 'connecting': return 'settings.mcpStatusConnecting';
    case 'failed': return 'settings.mcpStatusFailed';
    case 'disabled': return 'settings.mcpStatusDisabled';
    default: return 'settings.mcpStatusNone';
  }
}

function transportLabel(transport) {
  if (transport === 'sse') return 'settings.mcpTransportSse';
  if (transport === 'streamable-http') return 'settings.mcpTransportStreamableHttp';
  return 'settings.mcpTransportStdio';
}

// Live MCP connection status: statuses pushed by the backend land in the same
// list the form rows merge from. The page stays mounted (parent v-show), so
// the subscription simply lives for the component lifetime.
let mcpStatusOff = null;
onMounted(() => {
  mcpStatusOff = Events.On('mcp:status', (event) => {
    const data = unwrapWailsEvent(event, 'mcp:status');
    mcpServers.value = data?.servers || [];
  });
});
onUnmounted(() => {
  if (mcpStatusOff) {
    mcpStatusOff();
    mcpStatusOff = null;
  }
});

// The page stays mounted (parent v-show): the server list persists across
// mode switches and every entry triggers an in-place refresh.
watch(
  () => props.show,
  (visible) => {
    if (visible) loadMcpConfig();
  },
);
</script>

<style scoped>
/* Inline MCP page: fills the main-area container like the settings and
   token-stats pages. */
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

/* 页头标题+副标题：与 Skills/Models 面板同构，副标题紧贴标题下方。 */
.panel-header-copy {
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
}

.config-inline-subtitle {
  font-size: 12px;
  color: var(--ally-text-muted);
}

/* 页头操作按钮组：与 Skills/Models 面板同构（导入→导出→刷新→添加）。 */
.panel-header-actions {
  display: flex;
  align-items: center;
  gap: 8px;
  flex: none;
  flex-wrap: wrap;
  justify-content: flex-end;
}

/* 头部搜索框：与 Skills/Models 面板完全同构。 */
.panel-search-input {
  width: 200px;
}

.panel-search-icon {
  font-size: 13px;
  color: var(--ally-text-muted);
}

.config-section-subtitle {
  font-size: 12px;
  color: var(--ally-text-muted);
  margin-top: 2px;
}

.saved-model-empty {
  color: var(--ally-text-faint);
  font-size: var(--ally-sub-font-size);
  padding: 28px 0;
  text-align: center;
}

/* MCP 面板副标题已上移页头，正文不再重复说明文案。 */
.model-import-input {
  display: none;
}

.mcp-form-mode {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

/* 页内双 tab（MCP 服务器 / 内置工具启停）：与旧高级设置页同一套呼吸感，
   小号 n-tabs 默认贴字，撑开点击区。 */
.mcp-page-tabs {
  margin-bottom: 10px;
}

.mcp-page-tabs :deep(.n-tabs-tab) {
  padding: 6px 10px;
  font-size: 15px;
  color: var(--ally-text-primary);
}

/* 内置工具区：一行多卡，容器窄了自动换行（与 CardGrid 同一 auto-fill 思路）。
   卡内两行：名称+开关在上，工具名徽标+提示在下，窄卡也放得下。 */
.builtin-tool-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(230px, 1fr));
  gap: 8px;
}

/* 服务器列表一行一条：圆点+名称+徽标在左，.mcp-row-side 靠 margin-left:auto
   推到行尾（工具入口/状态/开关/编辑/删除同行排布），不套卡片网格。 */

.builtin-tool-card {
  gap: 6px;
}

.builtin-tool-meta {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
}

.builtin-tool-hint {
  font-size: 11px;
  color: var(--ally-text-faint);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.mcp-save-scope {
  color: var(--ally-text-faint);
  font-size: 11px;
  line-height: 1.45;
  margin-bottom: 12px;
}

.mcp-server-row {
  border: 1px solid var(--ally-border);
  border-radius: 8px;
  padding: 8px 10px;
  display: flex;
  flex-direction: column;
  gap: 8px;
  background: var(--ally-hover-faint);
}

.mcp-row-main {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
}

.mcp-badge {
  font-size: 11px;
  line-height: 1;
  padding: 3px 6px;
  border-radius: 4px;
  background: var(--ally-state-hover);
  color: var(--ally-text-muted);
  flex: none;
}

.mcp-badge.off {
  background: rgba(245, 166, 35, 0.14);
  color: var(--ally-warning-text);
}

.mcp-row-side {
  margin-left: auto;
  display: flex;
  align-items: center;
  gap: 8px;
  flex: none;
}

.mcp-status-text {
  flex: none;
  font-size: 12px;
  color: var(--ally-text-muted);
}

.mcp-status-text.connected {
  color: var(--ally-success-pale);
}

.mcp-status-text.connecting {
  color: var(--ally-warning-text);
}

.mcp-status-text.failed {
  color: var(--ally-danger-pale);
}

.mcp-status-text.disabled {
  color: var(--ally-text-faint);
}

.mcp-list-error {
  font-size: 12px;
  color: var(--ally-danger-pale);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

/* 弹窗宽度由 mcpFormModalStyle 内联给到卡片根：preset=card 会把 $attrs 的
   class/style 落到 n-card 上，而 scoped 的 data-v 只加在往外的 .n-modal-container
   上，命中不了卡片——别再写 .mcp-form-modal，那是看着生效的重复定义。 */

.mcp-dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  flex-shrink: 0;
}

.mcp-dot.connected {
  background: var(--ally-success-pale);
}

.mcp-dot.connecting {
  background: var(--ally-warning-text);
  animation: mcp-pulse 1.2s ease-in-out infinite;
}

.mcp-dot.failed {
  background: var(--ally-danger-pale);
}

.mcp-dot.disabled {
  background: var(--ally-text-faint);
}

@keyframes mcp-pulse {
  0%, 100% { opacity: 0.4; }
  50% { opacity: 1; }
}

.mcp-name {
  font-weight: 500;
  color: var(--ally-text-primary);
}

/* 服务器行的名字是这一行里唯一会被用户配置撑长的格：给不出省略规则就会折行把行
   撑高，或直接顶出卡片边框（旧卡片布局的截断规则随卡片样式一起删掉了，这里按新
   单行布局补回；全名靠 title 悬浮看）。min-width: 0 才允许它在 flex 行里被压窄。
   内置工具卡排除在外：卡里标签固定、本来就靠折行容下，保持原样。 */
.mcp-server-row:not(.builtin-tool-card) .mcp-name {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.mcp-tools {
  color: var(--ally-text-faint);
  font-size: 11px;
  margin-left: auto;
}

.mcp-tools-toggle {
  border: none;
  background: none;
  padding: 0;
  margin-left: auto;
  color: var(--ally-accent-dim);
  font-size: 11px;
  cursor: pointer;
}

.mcp-tools-toggle:hover {
  color: var(--ally-accent);
}

.mcp-tools-panel {
  margin-top: 8px;
  padding: 8px 10px;
  border: 1px solid var(--ally-border);
  border-radius: 8px;
  background: var(--ally-hover-faint);
}

.mcp-tools-hint {
  margin-bottom: 8px;
  color: var(--ally-text-faint);
  font-size: 11px;
}

.mcp-tools-panel .n-checkbox {
  display: flex;
  align-items: center;
  width: 100%;
  margin: 0 0 8px;
}

/* .n-checkbox__label 是 Naive 内部元素，scoped 样式必须 :deep 才命中；
   不约束 min-width 的话 flex 项收缩不下，长描述会撑破容器 */
.mcp-tools-panel :deep(.n-checkbox .n-checkbox__label) {
  flex: 1;
  min-width: 0;
}

/* label 行做成 flex：名称固定，描述吃剩余宽度单行省略 */
.mcp-tool-line {
  display: flex;
  align-items: baseline;
  flex: 1;
  min-width: 0;
}

.mcp-tool-name {
  flex-shrink: 0;
  color: var(--ally-text-body);
  font-family: ui-monospace, SFMono-Regular, Consolas, monospace;
  font-size: 12px;
}

.mcp-tool-desc {
  flex: 1;
  min-width: 0;
  margin-left: 8px;
  overflow: hidden;
  color: var(--ally-text-faint);
  font-size: 11px;
  text-overflow: ellipsis;
  white-space: nowrap;
}

/* 窄窗口下服务器行折行：名称独占一行，操作区换行后靠右。:not(.builtin-tool-card)
   把内置工具卡排除在外——它们保持迁入时的卡片网格，不受此折行影响。 */
@media (max-width: 640px) {
  .mcp-server-row:not(.builtin-tool-card) .mcp-row-main {
    align-items: stretch;
    flex-direction: column;
  }

  .mcp-server-row:not(.builtin-tool-card) .mcp-row-side {
    width: 100%;
    justify-content: flex-end;
  }
}
</style>
