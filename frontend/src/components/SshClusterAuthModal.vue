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
    @update:show="(value) => { if (!value) commitOnClose(); $emit('close'); }"
    @after-leave="onAfterLeave"
  >
    <template #header-extra>
      <!-- 跳转管理页前先提交草稿：App 侧关模态框是外部改 show，不会走
           update:show 的关闭路径，不在这里补提交，勾选就被静默丢了。 -->
      <n-button size="small" secondary @click="() => { commitOnClose(); $emit('open-manager'); }">
        {{ $t('sshCluster.panel.manage') }}
      </n-button>
    </template>

    <div class="ssh-auth-body">
      <div class="ssh-auth-hints">
        <span class="ssh-auth-hint">{{ $t('sshCluster.panel.hint') }}</span>
        <span v-if="!allowedSshServersReady" class="ssh-auth-hint ssh-auth-loading">{{ $t('sshCluster.loading') }}</span>
      </div>
      <div class="ssh-auth-toolbar">
        <n-input
          v-model:value="searchQuery"
          size="small"
          clearable
          :placeholder="$t('common.searchPlaceholder')"
          class="ssh-auth-search"
        >
          <template #prefix><SearchOutlined class="ssh-auth-search-icon" /></template>
        </n-input>
        <n-checkbox v-model:checked="onlyAllowed" size="small" class="ssh-auth-only-allowed">
          {{ $t('sshCluster.panel.onlyAllowed') }}
        </n-checkbox>
      </div>

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
import { computed, ref, watch } from 'vue';
import SearchOutlined from '@vicons/antd/SearchOutlined';

const props = defineProps({
  show: { type: Boolean, default: false },
  sshServers: { type: Array, default: () => [] },
  allowedSshServers: { type: Array, default: () => [] },
  // 授权列表未从后端读到时勾选必须禁用：后端保存是整份替换，把“未知”当“空”
  // 会一次点击撤掉该工作区其它节点的授权。
  allowedSshServersReady: { type: Boolean, default: false },
});

const emit = defineEmits(['close', 'commit', 'open-manager']);

const searchQuery = ref('');
// 只看已授权：过滤开关，不动列表顺序。
const onlyAllowed = ref(false);

// ── 草稿机制：勾选期间只改内存里的 Set，绝不逐次落盘 ──
// 每次勾选都调 SetWorkspaceAllowedServers 会触发后端 ssh:clusters-changed
// 广播 → 前端整份重拉集群列表 → 上百行的列表在勾选期间不停重渲染，行为
// 不可预测。现在打开模态框时把授权快照进 draft，勾选/取消只改 draft；
// 关闭模态框时一次性 emit('commit')，由父级整份保存一份。
const draftAllowed = ref(new Set());
const draftReady = ref(false);
// 首次打开时的排序快照：仅打开那一刻的授权状态参与排序。
const openOrderSnapshot = ref(null);

function normAlias(alias) {
  return String(alias || '').toLowerCase().trim();
}

watch(
  () => props.show,
  (visible) => {
    if (visible) {
      draftAllowed.value = new Set(
        (Array.isArray(props.allowedSshServers) ? props.allowedSshServers : []).map(normAlias),
      );
      draftReady.value = props.allowedSshServersReady;
      // 打开瞬间的授权快照只用来排一次序（已授权的置顶，组内保持原顺序）。
      // 之后勾选/取消不再参与排序，行不会动；重开模态框才会重新快照。
      openOrderSnapshot.value = new Set(draftAllowed.value);
    }
  },
  { immediate: true },
);

// 授权列表比模态框后到：用户在 ready=false 时（勾选全禁用）就打开了模态框，
// 后端读到后如果不在原地补快照，勾选会一直禁用、置顶排序也不生效，只能关掉重开。
watch(
  () => props.allowedSshServersReady,
  (ready) => {
    if (props.show && ready && !draftReady.value) {
      draftAllowed.value = new Set(
        (Array.isArray(props.allowedSshServers) ? props.allowedSshServers : []).map(normAlias),
      );
      openOrderSnapshot.value = new Set(draftAllowed.value);
      draftReady.value = true;
    }
  },
);

const sortedServers = computed(() => {
  const snapshot = openOrderSnapshot.value;
  if (!snapshot) return filteredServers.value;
  return filteredServers.value
    .map((s, i) => ({
      s,
      i,
      atOpen: snapshot.has(normAlias(s.alias)),
    }))
    .sort((a, b) => {
      if (a.atOpen !== b.atOpen) return a.atOpen ? -1 : 1;
      return a.i - b.i;
    })
    .map(({ s }) => s);
});

const filteredServers = computed(() => {
  const q = searchQuery.value.trim().toLowerCase();
  let list = props.sshServers;
  if (onlyAllowed.value) {
    list = list.filter((s) => draftAllowed.value.has(normAlias(s.alias)));
  }
  if (!q) return list;
  return list.filter((s) => {
    return (
      (s.alias && s.alias.toLowerCase().includes(q)) ||
      (s.host && s.host.toLowerCase().includes(q)) ||
      (s.username && s.username.toLowerCase().includes(q)) ||
      (s.description && s.description.toLowerCase().includes(q))
    );
  });
});

function isServerAllowed(alias) {
  return draftAllowed.value.has(normAlias(alias));
}

function toggleServer(alias) {
  if (!draftReady.value) return;
  const key = normAlias(alias);
  const next = new Set(draftAllowed.value);
  if (next.has(key)) {
    next.delete(key);
  } else {
    next.add(key);
  }
  draftAllowed.value = next;
}

// 关闭时先记账，等淡出动画结束（after-leave）再真正 emit 提交：提交会触发
// 后端广播 + 前端整份重拉集群列表（301 行重渲染），落在淡出动画的半秒里
// 就是用户看到的“关框瞬间列表闪一下”。
let commitPending = false;

function commitOnClose() {
  if (draftReady.value) {
    commitPending = true;
  }
}

function onAfterLeave() {
  if (!commitPending) return;
  commitPending = false;
  // 脏检查：草稿和打开时完全一致就不提交——避免每次关框都触发一次完整落盘
  // （后端重写文件 + 广播 + 前端重拉）和一条多余的“已更新”提示。
  const snapshot = openOrderSnapshot.value;
  if (snapshot && snapshot.size === draftAllowed.value.size) {
    let same = true;
    for (const key of snapshot) {
      if (!draftAllowed.value.has(key)) { same = false; break; }
    }
    if (same) return;
  }
  emit('commit', [...draftAllowed.value]);
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

.ssh-auth-toolbar {
  display: flex;
  align-items: center;
  gap: 12px;
}

.ssh-auth-search {
  flex: 1;
  max-width: 280px;
}

.ssh-auth-toolbar .ssh-auth-search-icon {
  color: var(--ally-text-muted);
  font-size: 12px;
}

.ssh-auth-only-allowed {
  flex: none;
  white-space: nowrap;
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
  /* 几百个测试节点时离屏行跳过渲染（铁律：先限长、再跳过）。行高固定可估：
     padding 9*2 + 12px 单行文本 ≈ 38px；行仍在 DOM，查找/Tab/勾选态不受影响。 */
  content-visibility: auto;
  contain-intrinsic-size: auto 38px;
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
