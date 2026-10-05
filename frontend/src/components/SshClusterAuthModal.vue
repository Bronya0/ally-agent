<!--
SPDX-License-Identifier: GPL-3.0-only

Copyright (C) 2026 tangssst <tangssst@qq.com>
GitHub: https://github.com/Bronya0/ally-agent

This file is part of ally-agent, licensed under the GNU General
Public License v3. See the LICENSE file for details.
-->
<template>
  <!-- 工作区 SSH 授权模态框：替代旧的 360px 气泡——勾选区太窄，别名/地址/描述
       全被裁成省略号。表头与行结构复用 SSH 集群页（SSHClusterPanel）的同一套
       grid 表格，最左列换成授权勾选。 -->
  <n-modal
    :show="show"
    preset="card"
    :title="$t('sshCluster.panel.title')"
    :style="{ width: 'min(760px, calc(100vw - 48px))' }"
    :mask-closable="false"
    @update:show="(value) => !value && $emit('close')"
  >
    <template #header-extra>
      <n-button size="small" secondary @click="$emit('open-manager')">
        {{ $t('sshCluster.panel.manage') }}
      </n-button>
    </template>

    <div class="ssh-auth-body">
      <div class="ssh-auth-hints">
        <span class="ssh-auth-hint">{{ $t('sshCluster.panel.hint') }}</span>
        <span v-if="!allowedSshServersReady" class="ssh-auth-hint ssh-auth-loading">{{ $t('sshCluster.loading') }}</span>
      </div>
      <n-input
        v-model:value="searchQuery"
        size="small"
        clearable
        :placeholder="$t('common.searchPlaceholder')"
        class="ssh-auth-search"
      >
        <template #prefix><SearchOutlined class="ssh-auth-search-icon" /></template>
      </n-input>

      <div v-if="sshServers.length === 0" class="ssh-auth-empty">
        {{ $t('sshCluster.panel.empty') }}
      </div>
      <div v-else-if="filteredServers.length === 0" class="ssh-auth-empty">
        {{ $t('common.searchEmpty') }}
      </div>
      <!-- 表体限高独立滚动：头部（说明+搜索+表头）保持可见，列表区自己滚，
           不能让整个模态框跟着内容长高/整体滚动。 -->
      <div v-else class="ssh-auth-scroll">
        <div class="ssh-auth-table">
        <div class="ssh-auth-head">
          <span class="ssh-auth-col-check">{{ $t('sshCluster.panel.title') }}</span>
          <span>{{ $t('sshCluster.table.colAlias') }}</span>
          <span>{{ $t('sshCluster.table.colEndpoint') }}</span>
          <span>{{ $t('sshCluster.table.colDescription') }}</span>
          <span class="ssh-auth-col-risk">{{ $t('sshCluster.table.colRisk') }}</span>
        </div>
        <div
          v-for="s in sortedServers"
          :key="s.alias"
          class="ssh-auth-row"
          :class="{ 'risk-high': s.riskLevel === 'high', disabled: !allowedSshServersReady }"
          @click="toggleServer(s.alias)"
        >
          <span class="ssh-auth-col-check">
            <n-checkbox
              :checked="isServerAllowed(s.alias)"
              :disabled="!allowedSshServersReady"
              @click.stop
              @update:checked="() => toggleServer(s.alias)"
            />
          </span>
          <span class="ssh-auth-cell-alias" :title="s.alias">
            {{ s.alias }}
          </span>
          <span class="ssh-auth-cell-endpoint" :title="endpointOf(s)">{{ endpointOf(s) }}</span>
          <span class="ssh-auth-cell-desc" :title="s.description">{{ s.description || '-' }}</span>
          <span class="ssh-auth-col-risk">
            <span :class="['ssh-auth-risk-badge', s.riskLevel === 'high' ? 'high' : 'low']">
              {{ s.riskLevel === 'high' ? $t('sshCluster.table.riskHigh') : $t('sshCluster.table.riskLow') }}
            </span>
          </span>
        </div>
        </div>
      </div>
    </div>
  </n-modal>
</template>

<script setup>
import { computed, ref } from 'vue';
import SearchOutlined from '@vicons/antd/SearchOutlined';

const props = defineProps({
  show: { type: Boolean, default: false },
  sshServers: { type: Array, default: () => [] },
  allowedSshServers: { type: Array, default: () => [] },
  // 授权列表未从后端读到时勾选必须禁用：后端保存是整份替换，把“未知”当“空”
  // 会一次点击撤掉该工作区其它节点的授权。
  allowedSshServersReady: { type: Boolean, default: false },
});

const emit = defineEmits(['close', 'toggle-ssh-server', 'open-manager']);

const searchQuery = ref('');

const filteredServers = computed(() => {
  const q = searchQuery.value.trim().toLowerCase();
  if (!q) return props.sshServers;
  return props.sshServers.filter((s) => {
    return (
      (s.alias && s.alias.toLowerCase().includes(q)) ||
      (s.host && s.host.toLowerCase().includes(q)) ||
      (s.username && s.username.toLowerCase().includes(q)) ||
      (s.description && s.description.toLowerCase().includes(q))
    );
  });
});

// One Set per allowedSshServers change: the checkbox list calls isServerAllowed
// for every row on every render (and on every search keystroke), so the per-call
// map()/includes() walk made the list O(rows × servers).
const allowedSshAliasSet = computed(() => new Set(
  (Array.isArray(props.allowedSshServers) ? props.allowedSshServers : []).map((s) => String(s || '').toLowerCase().trim()),
));

// 已授权的排在顶部，且最近勾选的排最前（用户勾完立刻能在表头看到自己刚点的那台）。
// recentAllowed 记录本次会话内勾选的先后（最近在前）；之前就授权过、本次没动过的
// 节点没有“勾选时间”，按原始顺序排在已授权组的后面。取消勾选即移出记录。
const recentAllowed = ref([]);

const sortedServers = computed(() => {
  const allowed = allowedSshAliasSet.value;
  return filteredServers.value
    .map((s, i) => {
      const key = String(s.alias || '').toLowerCase().trim();
      const isAllowed = allowed.has(key);
      const hit = recentAllowed.value.indexOf(key);
      const rank = isAllowed ? (hit >= 0 ? hit : recentAllowed.value.length + i) : 0;
      return { s, i, isAllowed, rank };
    })
    .sort((a, b) => {
      if (a.isAllowed !== b.isAllowed) return a.isAllowed ? -1 : 1;
      return a.isAllowed ? a.rank - b.rank : a.i - b.i;
    })
    .map(({ s }) => s);
});

function isServerAllowed(alias) {
  return allowedSshAliasSet.value.has(String(alias || '').toLowerCase().trim());
}

function toggleServer(alias) {
  if (!props.allowedSshServersReady) return;
  const key = String(alias).toLowerCase().trim();
  if (allowedSshAliasSet.value.has(key)) {
    recentAllowed.value = recentAllowed.value.filter((a) => a !== key);
  } else {
    recentAllowed.value = [key, ...recentAllowed.value];
  }
  emit('toggle-ssh-server', alias);
}

function endpointOf(server) {
  return `${server.username}@${server.host}:${server.port || 22}`;
}
</script>

<style scoped>
.ssh-auth-body {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.ssh-auth-hints {
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.ssh-auth-hint {
  font-size: 12px;
  color: var(--ally-text-muted);
  line-height: 1.4;
}

/* 授权列表尚未读到时给一句明确的等待说明：此时勾选是禁用的，不能让“一个都没勾”
   看起来像是真的没有授权。 */
.ssh-auth-loading {
  color: var(--ally-warning);
}

.ssh-auth-search {
  width: 240px;
}

.ssh-auth-search .ssh-auth-search-icon {
  color: var(--ally-text-muted);
  font-size: 12px;
}

.ssh-auth-empty {
  padding: 32px 0;
  font-size: 12px;
  color: var(--ally-text-muted);
  text-align: center;
}

/* 表体限高独立滚动：几百个节点时整个模态框跟着内容撑爆视口，头部表头也滚走了。 */
.ssh-auth-scroll {
  max-height: 52vh;
  overflow: auto;
  border: 1px solid var(--ally-border-subtle);
  border-radius: 6px;
}

/* 表头与数据行共用同一套 grid 列宽（与 SSHClusterPanel 的表格同一骨架，
   掐掉了认证/操作两列，最左换成授权勾选）。 */
.ssh-auth-table {
  display: flex;
  flex-direction: column;
}

.ssh-auth-head,
.ssh-auth-row {
  display: grid;
  grid-template-columns:
    56px
    minmax(120px, 1fr)
    minmax(170px, 1.2fr)
    minmax(180px, 1.6fr)
    64px;
  gap: 12px;
  align-items: center;
  padding: 9px 12px;
}

.ssh-auth-head {
  font-size: 11px;
  font-weight: 600;
  letter-spacing: 0.4px;
  color: var(--ally-text-muted);
  border-bottom: 1px solid var(--ally-border);
}

.ssh-auth-row {
  border-bottom: 1px solid var(--ally-border-subtle);
  border-left: 3px solid transparent;
  font-size: 12px;
  color: var(--ally-text-body);
  cursor: pointer;
  transition: background 0.15s, border-color 0.15s;
}

.ssh-auth-row:hover {
  background: var(--ally-hover-faint);
}

.ssh-auth-row.risk-high {
  border-left-color: #ef4444;
}

/* 授权列表未加载时整行不可点，去掉手型避免暗示可勾。 */
.ssh-auth-row.disabled {
  cursor: default;
}

.ssh-auth-row:last-child {
  border-bottom: 0;
}

.ssh-auth-cell-alias {
  display: flex;
  align-items: center;
  gap: 6px;
  min-width: 0;
  font-weight: 600;
  color: var(--ally-text-high);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.ssh-auth-cell-endpoint {
  font-family: var(--ally-mono-font, ui-monospace, monospace);
  color: var(--ally-text-muted);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.ssh-auth-cell-desc {
  color: var(--ally-text-soft);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.ssh-auth-col-risk {
  display: flex;
  align-items: center;
}

.ssh-auth-risk-badge {
  font-size: 10px;
  padding: 1px 6px;
  border-radius: 4px;
  font-weight: 500;
  white-space: nowrap;
}

.ssh-auth-risk-badge.high {
  background: rgba(239, 68, 68, 0.15);
  color: #ef4444;
}

.ssh-auth-risk-badge.low {
  background: rgba(16, 185, 129, 0.15);
  color: #10b981;
}
</style>
