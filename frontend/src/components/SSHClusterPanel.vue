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
          {{ t('common.add') }}
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
          {{ $t('common.add') }}
        </n-button>
      </div>

      <div v-else-if="filteredServers.length === 0" class="ssh-cluster-empty">
        {{ $t('common.searchEmpty') }}
      </div>

      <n-tabs
        v-else
        :key="groupTabSetKey"
        v-model:value="activeGroupTab"
        type="line"
        class="ssh-group-tabs panel-content-tabs"
      >
        <n-tab-pane v-for="group in groupTabs" :key="group.name" :name="group.name" :tab="group.name">
          <div class="ssh-card-list">
            <div
              v-for="s in group.servers"
              :key="s.id"
              class="ally-row-card ssh-node-card"
            >
              <div class="ally-row-main">
                <span class="ally-row-name ssh-card-host" :title="endpointOf(s)">{{ s.host }}</span>
                <span
                  class="ally-row-badge"
                  :class="s.riskLevel === 'high' ? 'ssh-badge-high' : 'ssh-badge-low'"
                >
                  {{ s.riskLevel === 'high' ? t('sshCluster.table.riskHigh') : t('sshCluster.table.riskLow') }}
                </span>
                <span class="ally-row-badge" :title="`${t('sshCluster.modal.username')}: ${s.username}`">{{ s.username }}</span>
                <span class="ally-row-badge" :title="`${t('sshCluster.modal.port')}: ${s.port || 22}`">{{ s.port || 22 }}</span>
                <span class="ally-row-badge ssh-card-nickname" :title="`${t('sshCluster.modal.alias')}: ${s.alias}`">{{ s.alias }}</span>
                <span v-if="s.status === 'pending_approval'" class="ally-row-badge ssh-badge-pending">
                  {{ t('sshCluster.table.pending') }}
                </span>
                <div class="ally-row-side">
                  <n-button size="tiny" secondary :loading="testingAlias === s.alias" @click="handleTest(s)">
                    {{ t('sshCluster.table.test') }}
                  </n-button>
                  <n-button size="tiny" secondary @click="openCloneDialog(s)">
                    {{ t('sshCluster.table.clone') }}
                  </n-button>
                  <n-button size="tiny" secondary @click="openEditDialog(s)">
                    {{ t('common.edit') }}
                  </n-button>
                  <n-popconfirm
                    :positive-text="t('common.delete')"
                    :negative-text="t('common.cancel')"
                    @positive-click="handleDelete(s.id)"
                  >
                    <template #trigger>
                      <n-button size="tiny" quaternary type="error">
                        {{ t('common.delete') }}
                      </n-button>
                    </template>
                    {{ t('sshCluster.modal.deleteConfirm') }}
                  </n-popconfirm>
                </div>
              </div>
              <div class="ssh-card-desc" :title="s.description">{{ s.description || '-' }}</div>
            </div>
          </div>
        </n-tab-pane>
      </n-tabs>
    </div>

    <!-- Add / Edit Dialog -->
    <!-- mask-closable=false：这是表单弹窗，点遮罩就把填了一半的内容丢掉，
         与 MCP / 模型 / 任务中心几个表单弹窗保持一致（ESC 仍可关闭）。 -->
    <n-modal
      :show="editorVisible"
      preset="card"
      :title="editorTitle"
      style="width: min(560px, 90vw)"
      :mask-closable="false"
      @update:show="(v) => editorVisible = v"
    >
      <div class="ssh-editor-form">
        <div class="ssh-form-item">
          <label class="ssh-form-label">{{ $t('sshCluster.modal.alias') }} *</label>
          <n-input
            v-model:value="form.alias"
            :placeholder="$t('sshCluster.modal.aliasPlaceholder')"
          />
          <span v-if="editorMode === 'clone'" class="ssh-form-hint">
            {{ $t('sshCluster.modal.cloneHint', { alias: cloneSourceAlias }) }}
          </span>
        </div>

        <div class="ssh-form-item">
          <label class="ssh-form-label">{{ $t('sshCluster.modal.group') }}</label>
          <n-select
            v-model:value="form.group"
            :options="groupOptions"
            filterable
            tag
            :placeholder="$t('sshCluster.modal.groupPlaceholder')"
          />
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

        <div class="ssh-form-row">
          <div class="ssh-form-item flex-1">
            <label class="ssh-form-label">{{ $t('sshCluster.modal.username') }} *</label>
            <n-input
              v-model:value="form.username"
              placeholder="root"
            />
          </div>
          <div class="ssh-form-item flex-1">
            <label class="ssh-form-label">{{ $t('sshCluster.modal.authType') }}</label>
            <n-select
              v-model:value="form.authType"
              :options="authTypeOptions()"
            />
          </div>
        </div>

        <span v-if="form.authType === 'agent'" class="ssh-form-hint hint-under-row">
          {{ $t('sshCluster.modal.authTypeAgentHint') }}
        </span>

        <div v-if="form.authType === 'key'" class="ssh-form-item">
          <label class="ssh-form-label">{{ $t('sshCluster.modal.keyPath') }}</label>
          <div class="ssh-keypath-picker">
            <n-input
              v-model:value="form.keyPath"
              placeholder="~/.ssh/id_rsa"
              clearable
            />
            <n-button secondary @click="choosePrivateKeyFile">
              {{ $t('sshCluster.modal.selectKeyFile') }}
            </n-button>
          </div>
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
            :placeholder="$t('sshCluster.modal.password')"
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
import { ListAllSSHServers, SaveSSHServer, DeleteSSHServer, TestSSHServer, SelectPrivateKeyFile } from '../../bindings/ally-dev/internal/app/app';
import { t } from '../i18n.mjs';
import { buildSSHClusterExport, parseSSHClusterImport } from '../utils/sshClusterIO.mjs';
import { saveTextFile } from '../utils/download.mjs';
import {
  SSH_UNGROUPED_GROUP,
  groupSSHServers,
  normalizeSSHGroup,
  resolveActiveGroup,
  sshGroupOptions,
  sshServerMatchesQuery,
} from '../utils/sshClusterGroups.mjs';

defineProps({
  show: { type: Boolean, default: false },
});

const emit = defineEmits(['servers-changed']);

const message = useMessage();
const loading = ref(false);
const saving = ref(false);
const testingAlias = ref(null);
const servers = ref([]);
const searchQuery = ref('');
const importInput = ref(null);
const importing = ref(false);
const filteredServers = computed(() => {
  const q = searchQuery.value.trim().toLowerCase();
  if (!q) return servers.value;
  return servers.value.filter((s) => sshServerMatchesQuery(s, searchQuery.value));
});

const activeGroupTab = ref('');
const groupTabs = computed(() => groupSSHServers(filteredServers.value));
// 页签集合变化（搜索过滤、增删分组）时对齐到仍可见的页签；同时作为 n-tabs 的 key，
// 让下划线按新布局重算（与模型页 providerTabSetKey 同一做法）。
const groupTabSetKey = computed(() => groupTabs.value.map((group) => group.name).join('\u0000'));
const groupOptions = computed(() => sshGroupOptions(servers.value));
watch(groupTabSetKey, () => {
  activeGroupTab.value = resolveActiveGroup(groupTabs.value, activeGroupTab.value);
}, { immediate: true });

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
  // 分组是纯归类：历史节点没有该字段，空值一律归入未分组。
  group: SSH_UNGROUPED_GROUP,
  host: '',
  port: 22,
  username: 'root',
  // 默认为密码认证：fillForm 的同一个回落值，新增节点即选中“密码”。
  authType: 'password',
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

// 页面由父级 v-if 卸载式挂载：mount 时 show 已为 true，列表加载只走
// onMounted，无需再监听 show。
onMounted(() => {
  loadServers();
});

defineExpose({
  loadServers,
});

// 表单字段的唯一填充入口：新增/编辑/复制三处弹窗都走这里，字段清单不再各写一份。
function fillForm(s) {
  form.id = s.id || '';
  form.alias = s.alias || '';
  form.group = normalizeSSHGroup(s.group);
  form.host = s.host || '';
  form.port = s.port || 22;
  form.username = s.username !== undefined && s.username !== '' ? s.username : 'root';
  // 新增节点走 fillForm({})，这里就是默认的认证方式；编辑/复制时用节点自带的值。
  // 已存节点在加载时已被后端 normalizeSSHAuthType 归一化，authType 恒有值，此回落不会误伤历史节点。
  form.authType = s.authType || 'password';
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
    message.warning(t('sshCluster.modal.aliasRequired'));
    return;
  }
  if (servers.value.some((s) => s.id !== form.id
    && s.alias.trim().toLowerCase() === form.alias.trim().toLowerCase())) {
    message.warning(t('sshCluster.modal.aliasInUse'));
    return;
  }
  if (!form.host.trim()) {
    message.warning(t('sshCluster.modal.hostRequired'));
    return;
  }
  if (!form.username.trim()) {
    message.warning(t('sshCluster.modal.usernameRequired'));
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
      group: normalizeSSHGroup(form.group),
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
    // 保存的节点落在哪个分组，就停在哪个页签，不让用户在其它分组里找不到它。
    activeGroupTab.value = normalizeSSHGroup(form.group);
    emit('servers-changed');
  } catch (err) {
    message.error(err?.message || 'Save failed');
  } finally {
    saving.value = false;
  }
}

async function handleDelete(id) {
  try {
    await DeleteSSHServer(id);
    message.success(t('sshCluster.deleted'));
    await loadServers();
    emit('servers-changed');
  } catch (err) {
    message.error(err?.message || 'Delete failed');
  }
}

async function handleTest(s) {
  testingAlias.value = s.alias;
  try {
    const res = await TestSSHServer(s);
    if (res && res.ok) {
      message.success(t('sshCluster.testSuccess', { latency: res.durationMs }));
    } else {
      message.error(t('sshCluster.testFailed', { error: res?.error || 'Connection failed' }));
    }
  } catch (err) {
    message.error(t('sshCluster.testFailed', { error: err?.message || 'Connection failed' }));
  } finally {
    testingAlias.value = null;
  }
}

async function choosePrivateKeyFile() {
  try {
    const selected = await SelectPrivateKeyFile();
    if (selected) {
      form.keyPath = selected;
    }
  } catch (err) {
    message.error(err?.message || 'Failed to select private key file');
  }
}

function endpointOf(server) {
  return `${server.username}@${server.host}:${server.port || 22}`;
}

// 认证方式下拉的选项：标签走 t() 现取，切换语言时随重渲染更新。
function authTypeOptions() {
  return [
    { label: t('sshCluster.modal.authTypeAgent'), value: 'agent' },
    { label: t('sshCluster.modal.authTypeKey'), value: 'key' },
    { label: t('sshCluster.modal.authTypePassword'), value: 'password' },
  ];
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
  display: flex;
  flex-direction: column;
  flex: 1;
  min-height: 0;
  overflow: hidden;
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

/* 卡片列表：一行一条，外形与首行结构统一由全局 .ally-row-card / .ally-row-* 提供
   （见 style.css）；这里只补 SSH 特有的地址/昵称与描述。风险等级只由徽标表达，
   不给高风险卡片额外加左侧色条。 */
.ssh-card-list {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.ssh-node-card {
  /* 两行卡片：padding 8×2 + 首行 22 + 描述 14 + gap 6 + 边框 2 = 60px。 */
  content-visibility: auto;
  contain-intrinsic-size: auto 60px;
}

.ssh-card-nickname {
  flex: 0 1 auto;
  min-width: 0;
}

/* 徽标色相按风险/待授权区分：带上卡片前缀提一级权重，稳定压过 .ally-row-badge
   的灰底（两处都是单类选择器时胜负只看样式表顺序）。 */
.ssh-node-card .ssh-badge-high {
  background: rgba(239, 68, 68, 0.15);
  color: #ef4444;
}

.ssh-node-card .ssh-badge-low {
  background: rgba(16, 185, 129, 0.15);
  color: #10b981;
}

.ssh-node-card .ssh-badge-pending {
  background: rgba(234, 179, 8, 0.15);
  color: #eab308;
}

.ssh-card-host {
  font-family: var(--ally-mono-font, ui-monospace, monospace);
  /* 地址保持卡片抬头的等宽字号，空间不足时省略，完整地址由 title 提供。 */
  font-size: 13px;
  font-weight: 600;
  color: var(--ally-text-primary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.ssh-card-desc {
  font-size: 11px;
  color: var(--ally-text-soft);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
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

/* 紧跟在一排字段下方的说明：抵掉表单列间距，让它贴着上面那排。 */
.hint-under-row {
  margin-top: -6px;
}

.ssh-editor-actions {
  display: flex;
  justify-content: flex-end;
  gap: 10px;
  margin-top: 8px;
}

.ssh-keypath-picker {
  display: flex;
  gap: 8px;
  align-items: center;
}

.ssh-keypath-picker .n-input {
  flex: 1;
}
</style>
