<!--
SPDX-License-Identifier: GPL-3.0-only

Copyright (C) 2026 tangssst <tangssst@qq.com>
GitHub: https://github.com/Bronya0/ally-agent

This file is part of ally-agent, licensed under the GNU General
Public License v3. See the LICENSE file for details.
-->
<template>
  <div class="config-inline-panel">
    <header class="config-inline-header">
      <div class="panel-header-copy">
        <span class="config-inline-title">{{ t('app.mode.sshCluster') }}</span>
        <span class="config-inline-subtitle">{{ t('sshCluster.panel.hint') }}</span>
      </div>
      <div class="panel-header-actions">
        <n-input
          v-model:value="searchQuery"
          class="panel-search-input"
          size="small"
          clearable
          :placeholder="t('common.searchPlaceholder')"
        >
          <template #prefix><SearchOutlined class="panel-search-icon" /></template>
        </n-input>
        <n-button size="small" secondary :loading="importing" @click="openImport">
          {{ t('sshCluster.panel.import') }}
        </n-button>
        <n-button size="small" secondary :disabled="!servers.length" :title="t('sshCluster.panel.exportHint')" @click="exportServers">
          {{ t('sshCluster.panel.export') }}
        </n-button>
        <n-button size="small" secondary :loading="loading" @click="loadServers">
          {{ t('common.refresh') }}
        </n-button>
        <n-button size="small" type="primary" @click="openAddDialog">
          {{ t('sshCluster.panel.add') }}
        </n-button>
      </div>
    </header>

    <input
      v-show="false"
      ref="importInput"
      class="ssh-import-input"
      type="file"
      accept="application/json,.json"
      @change="importServers"
    />

    <div class="panel-scroll-body">
      <div v-if="loading" class="ssh-cluster-loading">
        <n-spin size="medium" />
      </div>

      <div v-else-if="servers.length === 0" class="ssh-cluster-empty">
        <p>{{ $t('sshCluster.panel.empty') }}</p>
        <n-button type="primary" dashed size="small" @click="openAddDialog">
          {{ $t('sshCluster.panel.add') }}
        </n-button>
      </div>

      <div v-else-if="filteredServers.length === 0" class="ssh-cluster-empty">
        {{ $t('common.searchEmpty') }}
      </div>

      <div v-else class="ssh-table-wrap">
        <div class="ssh-table-head">
          <span>{{ t('sshCluster.table.colAlias') }}</span>
          <span>{{ t('sshCluster.table.colEndpoint') }}</span>
          <span>{{ t('sshCluster.table.colAuth') }}</span>
          <span>{{ t('sshCluster.table.colDescription') }}</span>
          <span>{{ t('sshCluster.table.colRisk') }}</span>
          <span class="ssh-cell-actions">{{ t('sshCluster.table.colActions') }}</span>
        </div>
        <div
          v-for="s in filteredServers"
          :key="s.alias"
          class="ssh-table-row"
          :class="{ 'risk-high': s.riskLevel === 'high' }"
        >
          <span class="ssh-cell-alias">
            <span class="ssh-alias-text" :title="s.alias">{{ s.alias }}</span>
            <span v-if="s.status === 'pending_approval'" class="ssh-status-badge pending">
              {{ $t('sshCluster.table.pending') }}
            </span>
          </span>
          <span class="ssh-cell-endpoint" :title="endpointOf(s)">{{ endpointOf(s) }}</span>
          <span class="ssh-cell-auth">{{ authTypeLabel(s.authType) }}</span>
          <span class="ssh-cell-desc" :title="s.description">{{ s.description || '-' }}</span>
          <span class="ssh-cell-risk">
            <span v-if="s.riskLevel === 'high'" class="ssh-risk-badge high">
              {{ t('sshCluster.table.riskHigh') }}
            </span>
            <span v-else class="ssh-risk-badge low">{{ t('sshCluster.table.riskLow') }}</span>
          </span>
          <span class="ssh-cell-actions">
            <n-button size="tiny" secondary @click="openCloneDialog(s)">
              {{ t('sshCluster.table.clone') }}
            </n-button>
            <n-button size="tiny" secondary @click="openEditDialog(s)">
              {{ t('common.edit') }}
            </n-button>
            <n-popconfirm
              :positive-text="t('common.delete')"
              :negative-text="t('common.cancel')"
              @positive-click="handleDelete(s.alias)"
            >
              <template #trigger>
                <n-button size="tiny" quaternary type="error">
                  {{ t('common.delete') }}
                </n-button>
              </template>
              {{ t('sshCluster.modal.deleteConfirm') }}
            </n-popconfirm>
          </span>
        </div>
      </div>
    </div>

    <!-- Add / Edit Dialog -->
    <n-modal
      :show="editorVisible"
      preset="card"
      class="ssh-editor-modal"
      :title="editorTitle"
      style="width: min(560px, 90vw)"
      @update:show="(v) => editorVisible = v"
    >
      <div class="ssh-editor-form">
        <div class="ssh-form-item">
          <label class="ssh-form-label">{{ $t('sshCluster.modal.alias') }} *</label>
          <n-input
            v-model:value="form.alias"
            :placeholder="$t('sshCluster.modal.aliasPlaceholder')"
            :disabled="editorMode === 'edit'"
          />
          <span v-if="editorMode === 'clone'" class="ssh-form-hint">
            {{ $t('sshCluster.modal.cloneHint', { alias: cloneSourceAlias }) }}
          </span>
        </div>

        <div class="ssh-form-row">
          <div class="ssh-form-item flex-2">
            <label class="ssh-form-label">{{ $t('sshCluster.modal.host') }} *</label>
            <n-input
              v-model:value="form.host"
              :placeholder="$t('sshCluster.modal.hostPlaceholder')"
            />
          </div>
          <div class="ssh-form-item flex-1">
            <label class="ssh-form-label">{{ $t('sshCluster.modal.port') }}</label>
            <n-input-number
              v-model:value="form.port"
              :min="1"
              :max="65535"
              placeholder="22"
            />
          </div>
        </div>

        <div class="ssh-form-item">
          <label class="ssh-form-label">{{ $t('sshCluster.modal.username') }} *</label>
          <n-input
            v-model:value="form.username"
            placeholder="root"
          />
        </div>

        <div class="ssh-form-item">
          <label class="ssh-form-label">{{ $t('sshCluster.modal.authType') }}</label>
          <n-radio-group v-model:value="form.authType" name="authType">
            <n-space>
              <n-radio value="agent">{{ $t('sshCluster.modal.authTypeAgent') }}</n-radio>
              <n-radio value="key">{{ $t('sshCluster.modal.authTypeKey') }}</n-radio>
              <n-radio value="password">{{ $t('sshCluster.modal.authTypePassword') }}</n-radio>
            </n-space>
          </n-radio-group>
          <span v-if="form.authType === 'agent'" class="ssh-form-hint">
            {{ $t('sshCluster.modal.authTypeAgentHint') }}
          </span>
        </div>

        <div v-if="form.authType === 'key'" class="ssh-form-item">
          <label class="ssh-form-label">{{ $t('sshCluster.modal.keyPath') }}</label>
          <n-input
            v-model:value="form.keyPath"
            placeholder="~/.ssh/id_rsa"
          />
        </div>

        <!-- key 模式下此字段只用于解锁本机私钥；绝不会作为 SSH 账号密码发送给服务器。 -->
        <div v-if="form.authType === 'password' || form.authType === 'key'" class="ssh-form-item">
          <label class="ssh-form-label">
            {{ form.authType === 'key' ? $t('sshCluster.modal.passwordPassphrase') : $t('sshCluster.modal.password') }}
          </label>
          <n-input
            v-model:value="form.password"
            type="password"
            show-password-on="click"
            placeholder="Password"
          />
          <span class="ssh-form-hint">{{ $t('sshCluster.modal.plaintextNote') }}</span>
        </div>

        <div class="ssh-form-item">
          <label class="ssh-form-label">{{ $t('sshCluster.modal.description') }} *</label>
          <n-input
            v-model:value="form.description"
            type="textarea"
            :rows="2"
            :placeholder="$t('sshCluster.modal.descriptionPlaceholder')"
          />
        </div>

        <div class="ssh-form-item">
          <label class="ssh-form-label">{{ $t('sshCluster.modal.riskLevel') }}</label>
          <n-radio-group v-model:value="form.riskLevel" name="riskLevel">
            <n-space>
              <n-radio value="low">{{ $t('sshCluster.modal.riskLow') }}</n-radio>
              <n-radio value="high">{{ $t('sshCluster.modal.riskHigh') }}</n-radio>
            </n-space>
          </n-radio-group>
        </div>

        <div class="ssh-editor-actions">
          <n-button @click="editorVisible = false">{{ $t('common.cancel') }}</n-button>
          <n-button type="primary" :loading="saving" @click="saveForm">
            {{ $t('common.save') }}
          </n-button>
        </div>
      </div>
    </n-modal>
  </div>
</template>

<script setup>
import { computed, onMounted, reactive, ref, watch } from 'vue';
import { useMessage } from 'naive-ui';
import SearchOutlined from '@vicons/antd/SearchOutlined';
import { ListAllSSHServers, SaveSSHServer, DeleteSSHServer } from '../../bindings/ally-dev/internal/app/app';
import { t } from '../i18n.mjs';
import { buildSSHClusterExport, parseSSHClusterImport } from '../utils/sshClusterIO.mjs';
import { saveTextFile } from '../utils/download.mjs';

const props = defineProps({
  show: { type: Boolean, default: false },
});

const emit = defineEmits(['servers-changed']);

const message = useMessage();
const loading = ref(false);
const saving = ref(false);
const servers = ref([]);
const searchQuery = ref('');
const importInput = ref(null);
const importing = ref(false);
const filteredServers = computed(() => {
  const q = searchQuery.value.trim().toLowerCase();
  if (!q) return servers.value;
  return servers.value.filter((s) => {
    return (
      (s.alias && s.alias.toLowerCase().includes(q)) ||
      (s.host && s.host.toLowerCase().includes(q)) ||
      (s.username && s.username.toLowerCase().includes(q)) ||
      (s.description && s.description.toLowerCase().includes(q))
    );
  });
});

const editorVisible = ref(false);
// 弹窗形态：add / edit / clone。clone 与 add 同形（别名可编辑，保存时才新增节点），
// 只多一条来源提示；只有 edit 锁死别名——别名是节点身份（集群清单、工作区授权、
// 会话放行、凭据槽位都以它为键），改名需要级联搬运，不提供半吊子改名。
const editorMode = ref('add');
const cloneSourceAlias = ref('');
const editorTitle = computed(() => {
  if (editorMode.value === 'edit') return t('sshCluster.modal.editTitle');
  if (editorMode.value === 'clone') return t('sshCluster.modal.cloneTitle');
  return t('sshCluster.modal.addTitle');
});

const form = reactive({
  id: '',
  alias: '',
  host: '',
  port: 22,
  username: '',
  authType: 'agent',
  keyPath: '',
  password: '',
  description: '',
  riskLevel: 'low',
  status: 'approved',
  createdBy: 'user',
  // 编辑时原样带回，否则 SaveSSHServer 见到 0 就把创建时间重置成“现在”
  // （新增/复制/导入恒为 0，由后端现取）。
  createdAtMs: 0,
  updatedAtMs: 0,
});

async function loadServers() {
  loading.value = true;
  try {
    const list = await ListAllSSHServers();
    servers.value = Array.isArray(list) ? list : [];
  } catch (err) {
    message.error(err?.message || 'Failed to load servers');
  } finally {
    loading.value = false;
  }
}

onMounted(() => {
  loadServers();
});

watch(
  () => props.show,
  (active) => {
    if (active) {
      loadServers();
    }
  }
);

defineExpose({
  loadServers,
});

// 表单字段的唯一填充入口：新增/编辑/复制三处弹窗都走这里，字段清单不再各写一份。
function fillForm(s) {
  form.id = s.id || '';
  form.alias = s.alias || '';
  form.host = s.host || '';
  form.port = s.port || 22;
  form.username = s.username || '';
  form.authType = s.authType || 'agent';
  form.keyPath = s.keyPath || '';
  form.password = s.password || '';
  form.description = s.description || '';
  form.riskLevel = s.riskLevel || 'low';
  form.status = s.status || 'approved';
  form.createdBy = s.createdBy || 'user';
  form.createdAtMs = s.createdAtMs || 0;
  form.updatedAtMs = s.updatedAtMs || 0;
}

function openAddDialog() {
  editorMode.value = 'add';
  cloneSourceAlias.value = '';
  fillForm({});
  editorVisible.value = true;
}

function openEditDialog(s) {
  editorMode.value = 'edit';
  cloneSourceAlias.value = '';
  fillForm(s);
  editorVisible.value = true;
}

// 复制：只打开新增形态的弹窗并预填源节点，别名给一个未被占用的建议值，用户改完再
// 保存才落盘。id 留空 → 保存时新增节点，源节点不受影响。
function openCloneDialog(s) {
  editorMode.value = 'clone';
  cloneSourceAlias.value = s.alias || '';
  // 复制是新增节点：id 与创建时间都从零开始（否则会把源节点的创建时间带过去）。
  fillForm({ ...s, id: '', alias: nextCloneAlias(s.alias), createdAtMs: 0, updatedAtMs: 0 });
  editorVisible.value = true;
}

async function saveForm() {
  if (!form.alias.trim()) {
    message.warning(t('sshCluster.modal.alias') + ' required');
    return;
  }
  if (!form.host.trim()) {
    message.warning(t('sshCluster.modal.host') + ' required');
    return;
  }
  if (!form.username.trim()) {
    message.warning(t('sshCluster.modal.username') + ' required');
    return;
  }
  if (!form.description.trim()) {
    message.warning(t('sshCluster.modal.descriptionRequired'));
    return;
  }
  // 认证方式与凭据必须一致：后端只按“密码/密钥字段是否为空”选认证方式，选了密码
  // 却留空会静默退化成免密登录，看配置像是配好了，连不上的原因却指向密码。
  if (form.authType === 'password' && !form.password) {
    message.warning(t('sshCluster.modal.passwordRequired'));
    return;
  }
  if (form.authType === 'key' && !form.keyPath.trim()) {
    message.warning(t('sshCluster.modal.keyPathRequired'));
    return;
  }

  saving.value = true;
  // 认证方式是唯一真实源：key 模式的 password 字段是本地私钥口令，不是 SSH 账号密码；
  // 切换认证方式时仍须清空旧凭据，避免旧账号密码被继续使用。
  const credentials = form.authType === 'agent'
    ? { keyPath: '', password: '' }
    : form.authType === 'key'
      ? { keyPath: form.keyPath.trim(), password: form.password }
      : { keyPath: '', password: form.password };
  try {
    await SaveSSHServer({
      id: form.id,
      alias: form.alias.trim(),
      host: form.host.trim(),
      port: form.port || 22,
      username: form.username.trim(),
      authType: form.authType,
      keyPath: credentials.keyPath,
      password: credentials.password,
      description: form.description.trim(),
      riskLevel: form.riskLevel,
      status: form.status,
      createdBy: form.createdBy,
      createdAtMs: form.createdAtMs || 0,
      updatedAtMs: form.updatedAtMs || 0,
    });
    message.success(t('sshCluster.saved'));
    editorVisible.value = false;
    await loadServers();
    emit('servers-changed');
  } catch (err) {
    message.error(err?.message || 'Save failed');
  } finally {
    saving.value = false;
  }
}

async function handleDelete(alias) {
  try {
    await DeleteSSHServer(alias);
    message.success(t('sshCluster.deleted'));
    await loadServers();
    emit('servers-changed');
  } catch (err) {
    message.error(err?.message || 'Delete failed');
  }
}

function endpointOf(server) {
  return `${server.username}@${server.host}:${server.port || 22}`;
}

function authTypeLabel(authType) {
  const normalized = String(authType || 'agent').toLowerCase();
  if (normalized === 'key') return t('sshCluster.table.authKey');
  if (normalized === 'password') return t('sshCluster.table.authPassword');
  return t('sshCluster.table.authAgent');
}

// 复制弹窗里预填的建议别名：`<原名>-copy`，被占用就往后排 -2、-3……（大小写不敏感，
// 与后端的小写化键一致）。上限用完后回落到时间戳，避免死循环。
function nextCloneAlias(baseAlias) {
  const taken = new Set(servers.value.map((s) => String(s.alias || '').toLowerCase().trim()));
  const stem = `${String(baseAlias || 'node').trim()}-copy`;
  if (!taken.has(stem.toLowerCase())) return stem;
  for (let i = 2; i <= 999; i += 1) {
    const candidate = `${stem}-${i}`;
    if (!taken.has(candidate.toLowerCase())) return candidate;
  }
  return `${stem}-${Date.now()}`;
}

function openImport() {
  if (!importInput.value) return;
  importInput.value.value = '';
  importInput.value.click();
}

// 导出/导入错误码 -> 文案键的唯一映射表（未知码回落到 UNKNOWN，不把裸键名抛给用户）。
const IMPORT_ERROR_KEYS = {
  FILE_TOO_LARGE: 'sshCluster.importError.FILE_TOO_LARGE',
  INVALID: 'sshCluster.importError.INVALID',
};

function exportServers() {
  if (!servers.value.length) {
    message.warning(t('sshCluster.exportEmpty'));
    return;
  }
  const payload = buildSSHClusterExport(servers.value);
  saveTextFile({
    filename: `ally-ssh-cluster-${new Date().toISOString().slice(0, 10)}.json`,
    content: `${JSON.stringify(payload, null, 2)}\n`,
    filterName: 'JSON (*.json)',
    filterPattern: '*.json',
  }).then((result) => {
    if (result.saved) message.success(t('sshCluster.exportSuccess', { count: payload.servers.length }));
  });
}

// 导入按别名合并：已存在的别名覆盖更新，新别名新增。逐条走 SaveSSHServer 是为了
// 复用后端的必填校验与落盘（节点数量级很小，每次都会原子重写整份文件，可接受）。
async function importServers(event) {
  const input = event?.target;
  const file = input?.files?.[0];
  if (!file) return;
  importing.value = true;
  try {
    if (file.size > 2 * 1024 * 1024) {
      throw Object.assign(new Error('FILE_TOO_LARGE'), { code: 'FILE_TOO_LARGE' });
    }
    const nodes = parseSSHClusterImport(await file.text());
    const known = new Set(servers.value.map((s) => String(s.alias || '').toLowerCase().trim()));
    let added = 0;
    let updated = 0;
    let failed = 0;
    for (const node of nodes) {
      const key = node.alias.toLowerCase();
      try {
        await SaveSSHServer({ ...node, id: '', createdAtMs: 0, updatedAtMs: 0 });
        if (known.has(key)) {
          updated += 1;
        } else {
          added += 1;
          known.add(key);
        }
      } catch (_) {
        failed += 1;
      }
    }
    await loadServers();
    emit('servers-changed');
    if (failed) message.warning(t('sshCluster.importPartial', { added, updated, failed }));
    else message.success(t('sshCluster.importSuccess', { added, updated }));
  } catch (err) {
    const code = String(err?.code || '');
    message.error(t(IMPORT_ERROR_KEYS[code] || 'sshCluster.importError.UNKNOWN'));
  } finally {
    importing.value = false;
    if (input) input.value = '';
  }
}
</script>

<style scoped>
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

.panel-header-copy {
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.config-inline-title {
  font-size: 18px;
  font-weight: 700;
  letter-spacing: 0.5px;
  color: var(--ally-text-primary);
}

.config-inline-subtitle {
  font-size: 12px;
  color: var(--ally-text-muted);
}

.panel-header-actions {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-shrink: 0;
}

.panel-search-input {
  width: 200px;
}

.panel-scroll-body {
  flex: 1;
  min-height: 0;
  overflow: auto;
  padding: 16px 24px 28px;
}

.ssh-cluster-loading,
.ssh-cluster-empty {
  padding: 48px 0;
  text-align: center;
  color: var(--ally-text-muted);
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 12px;
  font-size: 13px;
}

/* 表格行布局：表头与数据行共用同一套 grid 列宽。列宽带下限、整表有 min-width，
   窄窗口下由 .panel-scroll-body 横向滚动，不让别名/地址被挤成竖排。 */
.ssh-table-wrap {
  min-width: 880px;
}

.ssh-table-head,
.ssh-table-row {
  display: grid;
  grid-template-columns:
    minmax(140px, 1.1fr)
    minmax(180px, 1.3fr)
    104px
    minmax(200px, 1.8fr)
    84px
    196px;
  gap: 12px;
  align-items: center;
  padding: 9px 12px;
}

.ssh-table-head {
  font-size: 11px;
  font-weight: 600;
  letter-spacing: 0.4px;
  color: var(--ally-text-muted);
  border-bottom: 1px solid var(--ally-border);
}

.ssh-table-row {
  border-bottom: 1px solid var(--ally-border-subtle);
  border-left: 3px solid transparent;
  font-size: 12px;
  color: var(--ally-text-body);
  transition: background 0.15s, border-color 0.15s;
}

.ssh-table-row:hover {
  background: var(--ally-hover-faint);
}

.ssh-table-row.risk-high {
  border-left-color: #ef4444;
}

.ssh-table-row:last-child {
  border-bottom: 0;
}

.ssh-cell-alias {
  display: flex;
  align-items: center;
  gap: 6px;
  min-width: 0;
}

.ssh-alias-text {
  font-weight: 600;
  color: var(--ally-text-high);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.ssh-cell-endpoint {
  font-family: var(--ally-mono-font, ui-monospace, monospace);
  color: var(--ally-text-muted);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.ssh-cell-auth,
.ssh-cell-desc {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.ssh-cell-auth {
  color: var(--ally-text-faint);
}

.ssh-cell-desc {
  color: var(--ally-text-soft);
}

.ssh-cell-risk {
  display: flex;
  align-items: center;
}

.ssh-cell-actions {
  display: inline-flex;
  align-items: center;
  justify-content: flex-end;
  gap: 6px;
}

.ssh-risk-badge {
  font-size: 10px;
  padding: 1px 6px;
  border-radius: 4px;
  font-weight: 500;
}

.ssh-risk-badge.high {
  background: rgba(239, 68, 68, 0.15);
  color: #ef4444;
}

.ssh-risk-badge.low {
  background: rgba(16, 185, 129, 0.15);
  color: #10b981;
}

.ssh-status-badge.pending {
  font-size: 10px;
  padding: 1px 6px;
  border-radius: 4px;
  font-weight: 500;
  background: rgba(234, 179, 8, 0.15);
  color: #eab308;
}

.ssh-editor-form {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.ssh-form-item {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.ssh-form-row {
  display: flex;
  gap: 12px;
}

.flex-1 {
  flex: 1;
}

.flex-2 {
  flex: 2;
}

.ssh-form-label {
  font-size: 12px;
  font-weight: 500;
  color: var(--ally-text-high);
}

.ssh-form-hint {
  font-size: 11px;
  color: var(--ally-text-muted);
  line-height: 1.4;
  margin-top: 2px;
}

.ssh-editor-actions {
  display: flex;
  justify-content: flex-end;
  gap: 10px;
  margin-top: 8px;
}
</style>
