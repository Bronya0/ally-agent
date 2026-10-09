<!--
SPDX-License-Identifier: GPL-3.0-only

Copyright (C) 2026 tangssst <tangssst@qq.com>
GitHub: https://github.com/Bronya0/ally-agent

This file is part of ally-agent, licensed under the GNU General
Public License v3. See the LICENSE file for details.
-->
<template>
  <!-- 插件管理页：导入 / 导出 / 启停 / 删除，以及每一条的权限摘要与失败原因。
       权限摘要不是装饰：安装时不弹确认框，所以这里是用户唯一的事后感知手段。 -->
  <div class="config-inline-panel">
    <header class="config-inline-header">
      <div class="panel-header-copy">
        <span class="config-inline-title">{{ t('plugins.title') }}</span>
        <span class="config-inline-subtitle">{{ t('plugins.subtitle', { count: plugins.length }) }}</span>
      </div>
      <div class="panel-header-actions">
        <n-button size="small" text :title="t('plugins.guideHint')" @click="openGuide">
          <template #icon><ReadOutlined /></template>
          {{ t('plugins.guide') }}
        </n-button>
        <n-button size="small" secondary :loading="loading" @click="refresh">{{ t('common.refresh') }}</n-button>
        <n-button size="small" secondary :loading="busy === 'dir'" :title="t('plugins.importFromDirHint')" @click="importFromDir">
          <template #icon><FolderOpenOutlined /></template>
          {{ t('plugins.importFromDir') }}
        </n-button>
        <n-button size="small" type="primary" :loading="busy === 'import'" @click="importPackage">
          <template #icon><InboxOutlined /></template>
          {{ t('plugins.import') }}
        </n-button>
      </div>
    </header>

    <div class="panel-scroll-body">
      <div v-if="loading && !plugins.length" class="plugin-loading">{{ t('plugins.loading') }}</div>
      <PluginEmptyState v-else-if="!plugins.length" @guide="openGuide" />

      <div
        v-for="plugin in plugins"
        :key="plugin.id"
        class="plugin-row"
        :class="{ 'is-disabled': !plugin.enabled, 'is-broken': !plugin.valid }"
      >
        <div class="plugin-row-main">
          <div class="plugin-row-title">
            <span class="plugin-name">{{ plugin.name || plugin.id }}</span>
            <span class="plugin-id">{{ plugin.id }}@{{ plugin.version || '-' }}</span>
            <n-tag v-if="!plugin.valid" size="tiny" type="error">{{ t('plugins.invalid') }}</n-tag>
            <n-tag v-else-if="!plugin.enabled" size="tiny">{{ t('plugins.disabled') }}</n-tag>
          </div>
          <div class="plugin-row-desc">{{ plugin.description || t('common.noDescription') }}</div>
          <div class="plugin-row-meta">
            <span v-if="plugin.author">{{ t('plugins.author', { author: plugin.author }) }}</span>
            <span v-if="plugin.hasPackage">{{ t('plugins.packageSize', { size: formatBytes(plugin.packageBytes) }) }}</span>
            <span v-if="plugin.updatedAt">{{ t('plugins.updatedAt', { time: formatDateTime(plugin.updatedAt) }) }}</span>
          </div>
          <div class="plugin-row-perm">
            <span class="plugin-perm-label">{{ t('plugins.permissions') }}</span>
            <span class="plugin-perm-item">
              {{ t('plugins.permissionHttp') }}:
              <template v-if="plugin.permissions && plugin.permissions.http && plugin.permissions.http.length">
                <code v-for="host in plugin.permissions.http" :key="host" class="plugin-perm-host">{{ host }}</code>
              </template>
              <template v-else>{{ t('plugins.none') }}</template>
            </span>
            <span class="plugin-perm-item">{{ t('plugins.permissionWorkspace') }}: {{ workspaceLabel(plugin) }}</span>
          </div>
          <div class="plugin-row-path" :title="plugin.dir">{{ plugin.dir }}</div>
          <div v-if="plugin.loadError" class="plugin-row-error">{{ plugin.loadError }}</div>
          <div v-if="mountErrors[plugin.id]" class="plugin-row-error">{{ mountErrors[plugin.id] }}</div>
          <div v-if="denials[plugin.id]" class="plugin-row-denied">
            {{ t('plugins.denied', { count: denials[plugin.id].count, message: denials[plugin.id].message }) }}
          </div>
        </div>

        <div class="plugin-row-actions">
          <n-switch
            size="small"
            :value="plugin.enabled"
            :disabled="busy === plugin.id || !plugin.valid"
            @update:value="(value) => toggle(plugin, value)"
          />
          <n-dropdown
            trigger="click"
            :options="exportOptions"
            :disabled="busy === plugin.id || !plugin.valid"
            @select="(key) => exportPlugin(plugin, key)"
          >
            <n-button size="small" secondary>{{ t('plugins.export') }}</n-button>
          </n-dropdown>
          <n-button size="small" secondary :disabled="busy === plugin.id" @click="askDelete(plugin)">
            {{ t('common.delete') }}
          </n-button>
        </div>
      </div>
    </div>

    <!-- 删除确认：是否连带删掉插件自己的数据（账号、令牌一类）单独问一次。 -->
    <n-modal
      :show="!!deleteTarget"
      preset="card"
      :title="t('plugins.deleteTitle', { name: deleteTarget ? deleteTarget.name || deleteTarget.id : '' })"
      style="width: min(420px, 90vw)"
      :mask-closable="false"
      @update:show="(value) => { if (!value) closeDelete(); }"
    >
      <div class="plugin-delete-body">{{ t('plugins.deleteConfirm') }}</div>
      <n-checkbox v-model:checked="deletePurgeData">{{ t('plugins.deletePurgeData') }}</n-checkbox>
      <template #footer>
        <div class="plugin-delete-footer">
          <n-button size="small" secondary @click="closeDelete">{{ t('common.cancel') }}</n-button>
          <n-button size="small" type="error" :loading="busy === 'delete'" @click="confirmDelete">{{ t('common.delete') }}</n-button>
        </div>
      </template>
    </n-modal>
  </div>
</template>

<script setup>
import { onMounted, ref, watch } from 'vue';
import { useMessage } from 'naive-ui';
import { FolderOpenOutlined, InboxOutlined, ReadOutlined } from '@vicons/antd';
import { Browser } from '@wailsio/runtime';
import { t, formatDateTime } from '../i18n.mjs';
import { formatBytes } from '../utils/format.mjs';
import { fetchPlugins, pluginAdmin } from '../utils/pluginHost.mjs';
import PluginEmptyState from './PluginEmptyState.vue';
import { SelectDirectory } from '../../bindings/ally-dev/internal/app/app';

const props = defineProps({
  show: { type: Boolean, default: false },
  // 插件页挂载失败的记录（App.vue 持有），按插件 id 展示在这里。
  mountErrors: { type: Object, default: () => ({}) },
  // 白名单拒绝的请求（次数 + 最后一条）：默认拒绝下，这是用户知道插件试过什么
  // 的唯一途径——插件自己的 catch 盖不住它。
  denials: { type: Object, default: () => ({}) },
});
const emit = defineEmits(['plugins-changed']);
const message = useMessage();

const plugins = ref([]);
const loading = ref(false);
const busy = ref('');
const deleteTarget = ref(null);
const deletePurgeData = ref(false);

const exportOptions = [
  { label: t('plugins.exportPackage'), key: 'package' },
  { label: t('plugins.exportWithData'), key: 'data' },
];

// 只有 none / read 两种：清单校验阶段就把 workspace="write" 拒了（见 internal/tools/plugin 的
// Validate），所以这里不需要也不能有 write 分支。
function workspaceLabel(plugin) {
  const level = (plugin.permissions && plugin.permissions.workspace) || 'none';
  if (level === 'read') return t('plugins.workspaceRead');
  return t('plugins.none');
}

async function refresh() {
  loading.value = true;
  try {
    plugins.value = await fetchPlugins();
  } catch (err) {
    message.error(String((err && err.message) || err));
  } finally {
    loading.value = false;
  }
}

async function afterChange(successText) {
  await refresh();
  emit('plugins-changed');
  if (successText) message.success(successText);
}

// 安装专用：装上了但不可用（入口缺失、目录与清单 id 不一致…）不能报成功——否则绿色
// 提示和红色「不可用」同时出现，用户不知道该信哪个。原因直接取自后端给的 loadError。
async function afterInstall(info, fallbackName) {
  await refresh();
  emit('plugins-changed');
  const name = (info && info.name) || fallbackName;
  if (info && info.valid === false) {
    message.error(t('plugins.importUnusable', { name, error: (info && info.loadError) || '' }));
    return;
  }
  message.success(t('plugins.imported', { name }));
}

async function importPackage() {
  busy.value = 'import';
  try {
    const picked = await pluginAdmin.selectPackage();
    if (!picked) return;
    const info = await pluginAdmin.import(picked);
    await afterInstall(info, picked);
  } catch (err) {
    message.error(String((err && err.message) || err));
  } finally {
    busy.value = '';
  }
}

async function importFromDir() {
  busy.value = 'dir';
  try {
    const dir = await SelectDirectory('');
    if (!dir) return;
    const info = await pluginAdmin.importFromDir(dir);
    await afterInstall(info, dir);
  } catch (err) {
    message.error(String((err && err.message) || err));
  } finally {
    busy.value = '';
  }
}

async function toggle(plugin, enabled) {
  busy.value = plugin.id;
  try {
    await pluginAdmin.setEnabled(plugin.id, enabled);
    await afterChange(enabled ? t('plugins.enabledToast', { name: plugin.name || plugin.id }) : t('plugins.disabledToast', { name: plugin.name || plugin.id }));
  } catch (err) {
    message.error(String((err && err.message) || err));
  } finally {
    busy.value = '';
  }
}

async function exportPlugin(plugin, key) {
  busy.value = plugin.id;
  try {
    const saved = await pluginAdmin.export(plugin.id, key === 'data');
    if (saved) message.success(t('plugins.exported', { path: saved }));
  } catch (err) {
    message.error(String((err && err.message) || err));
  } finally {
    busy.value = '';
  }
}

// 插件开发文档（仓库里的 docs/plugin-system.md）：包结构、清单字段、入口契约、
// host API 与权限声明都在里面。放在管理页按钮旁而不是埋进设置——写插件的人第一眼
// 要拿的就是契约，找不到他就只能猜。
const PLUGIN_GUIDE_URL = 'https://github.com/Bronya0/ally-agent/blob/main/docs/plugin-system.md';

function openGuide() {
  Browser.OpenURL(PLUGIN_GUIDE_URL);
}

function askDelete(plugin) {
  deleteTarget.value = plugin;
  deletePurgeData.value = false;
}

function closeDelete() {
  deleteTarget.value = null;
  deletePurgeData.value = false;
}

async function confirmDelete() {
  const target = deleteTarget.value;
  if (!target) return;
  busy.value = 'delete';
  try {
    await pluginAdmin.remove(target.id, deletePurgeData.value);
    closeDelete();
    await afterChange(t('plugins.deleted', { name: target.name || target.id }));
  } catch (err) {
    message.error(String((err && err.message) || err));
  } finally {
    busy.value = '';
  }
}

onMounted(refresh);
// 面板常驻（v-show 保状态）：进入页面时刷新一次，避免停在上次导入前的旧列表。
watch(
  () => props.show,
  (value) => {
    if (value) refresh();
  },
);
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

.config-inline-title {
  font-size: 18px;
  font-weight: 700;
  letter-spacing: 0.5px;
  color: var(--ally-text-primary);
}

.panel-header-copy {
  display: flex;
  align-items: baseline;
  gap: 10px;
  min-width: 0;
}

.config-inline-subtitle {
  color: var(--ally-text-faint);
  font-size: var(--ally-sub-font-size);
}

.panel-header-actions {
  display: flex;
  align-items: center;
  gap: 8px;
}

.panel-scroll-body {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  padding: 16px 24px 28px;
}

.plugin-loading {
  color: var(--ally-text-faint);
  font-size: var(--ally-sub-font-size);
  padding: 28px 0;
  text-align: center;
}

.plugin-row {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 16px;
  padding: 12px 14px;
  border: 1px solid var(--ally-border);
  border-radius: 8px;
  background: var(--ally-surface-panel);
  margin-bottom: 10px;
}

.plugin-row.is-disabled {
  opacity: 0.6;
}

.plugin-row.is-broken {
  border-color: var(--ally-danger);
}

.plugin-row-main {
  display: flex;
  flex-direction: column;
  gap: 4px;
  min-width: 0;
  flex: 1;
}

.plugin-row-title {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}

.plugin-name {
  font-weight: 600;
  color: var(--ally-text-primary);
}

.plugin-id {
  color: var(--ally-text-faint);
  font-size: var(--ally-aux-font-size);
  font-family: var(--ally-mono-font), monospace;
}

.plugin-row-desc {
  color: var(--ally-text-soft);
  font-size: var(--ally-sub-font-size);
}

.plugin-row-meta {
  display: flex;
  flex-wrap: wrap;
  gap: 12px;
  color: var(--ally-text-muted);
  font-size: var(--ally-aux-font-size);
}

.plugin-row-perm {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px;
  color: var(--ally-text-muted);
  font-size: var(--ally-aux-font-size);
}

.plugin-perm-label {
  color: var(--ally-text-tertiary);
}

.plugin-perm-host {
  margin-left: 4px;
  padding: 1px 5px;
  border-radius: 4px;
  background: var(--ally-surface-raised);
  color: var(--ally-text-soft);
  font-family: var(--ally-mono-font), monospace;
}

.plugin-row-path {
  color: var(--ally-text-ghost);
  font-size: var(--ally-aux-font-size);
  font-family: var(--ally-mono-font), monospace;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.plugin-row-error {
  color: var(--ally-danger-text);
  font-size: var(--ally-aux-font-size);
  white-space: pre-wrap;
  word-break: break-word;
}

/* 被拒请求：比报错轻一档，用告警色（它不是故障，是白名单在按声明执行）。 */
.plugin-row-denied {
  color: var(--ally-warning-text);
  font-size: var(--ally-aux-font-size);
  white-space: pre-wrap;
  word-break: break-word;
}

.plugin-row-actions {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-shrink: 0;
}

.plugin-delete-body {
  color: var(--ally-text-soft);
  font-size: var(--ally-sub-font-size);
  margin-bottom: 12px;
}

.plugin-delete-footer {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
}
</style>
