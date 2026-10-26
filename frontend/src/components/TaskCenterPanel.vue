<!--
SPDX-License-Identifier: GPL-3.0-only

Copyright (C) 2026 tangssst <tangssst@qq.com>
GitHub: https://github.com/Bronya0/ally-agent

This file is part of ally-agent, licensed under the GNU General
Public License v3. See the LICENSE file for details.
-->
<template>
  <n-modal
    :show="show"
    preset="card"
    :title="$t('taskCenter.title')"
    :style="{ width: 'min(920px, calc(100vw - 48px))' }"
    :mask-closable="true"
    @update:show="(value) => !value && $emit('close')"
    @after-enter="syncTabs"
  >
    <template #header-extra>
      <n-button size="small" quaternary :loading="loading" @click="$emit('refresh')">{{ $t('common.refresh') }}</n-button>
    </template>

    <div class="task-center-body">
      <div class="task-scope">
        <n-tabs class="task-scope-switch" :value="viewScope" type="segment" size="small" @update:value="$emit('update:scope', $event)">
          <n-tab name="workspace" :disabled="!workspace">{{ $t('taskCenter.currentWorkspace') }}</n-tab>
          <n-tab name="all">{{ $t('taskCenter.allWorkspaces') }}</n-tab>
        </n-tabs>
        <span v-if="viewScope === 'workspace'" class="mono-muted ellipsis" :title="workspace">{{ workspace }}</span>
      </div>
      <n-tabs ref="tabsRef" v-model:value="activeTab" class="task-content-tabs" type="line" pane-wrapper-style="padding-top: 4px;">
        <n-tab-pane name="services" :tab="$t('taskCenter.servicesTab', { running: serviceRunningCount, total: services.length })">
          <div class="task-overview">
            <span>{{ $t('service.runningCount', { count: serviceRunningCount }) }}</span>
          </div>
          <n-spin :show="servicesLoading">
            <n-empty v-if="!services.length" :description="$t('service.empty')">
              <template #extra><span class="empty-hint">{{ $t('service.emptyHint') }}</span></template>
            </n-empty>
            <div v-else class="task-list">
              <article v-for="service in services" :key="service.id" class="task-card service-card">
                <div class="card-head">
                  <div class="head-main">
                    <span class="card-title">{{ service.name || service.id }}</span>
                    <div class="card-state">
                      <span class="task-status" :class="`tone-${serviceStatusTone(service)}`">{{ serviceStatusLabel(service) }}</span>
                      <span v-if="service.promoted" class="task-kind">{{ $t('service.promotedBadge') }}</span>
                    </div>
                  </div>
                  <div class="head-side">
                    <n-popconfirm
                      v-if="isServiceActive(service)"
                      :positive-text="$t('service.stop')"
                      :negative-text="$t('common.cancel')"
                      @positive-click="$emit('stopService', service.id)"
                    >
                      <template #trigger>
                        <n-button size="small" type="error" quaternary :loading="stoppingIds.includes(service.id)">{{ $t('service.stop') }}</n-button>
                      </template>
                      {{ $t('service.stopConfirm') }}
                    </n-popconfirm>
                  </div>
                </div>
                <div v-if="serviceDescription(service, $t)" class="service-description" :title="serviceDescription(service, $t)">{{ serviceDescription(service, $t) }}</div>
                <div v-if="viewScope === 'all'" class="mono-muted ellipsis" :title="service.workspace">{{ $t('taskCenter.workspace') }} · {{ service.workspace || $t('taskCenter.noWorkspace') }}</div>
                <details class="service-details">
                  <summary>{{ $t('service.details') }}</summary>
                  <dl class="service-facts">
                    <div><dt>ID</dt><dd>{{ service.id }}</dd></div>
                    <div v-if="service.pid"><dt>PID</dt><dd>{{ service.pid }}</dd></div>
                    <div><dt>{{ $t('taskCenter.workspace') }}</dt><dd>{{ service.workspace || $t('taskCenter.noWorkspace') }}</dd></div>
                    <div v-if="service.cwd"><dt>{{ $t('service.cwd') }}</dt><dd>{{ service.cwd }}</dd></div>
                  </dl>
                  <div v-if="service.command" class="command service-command" v-html="highlightedCommand(service.command)"></div>
                  <div class="meta-line">
                    <span v-if="isServiceActive(service)" class="meta-item"><i>{{ $t('service.startedAt') }}</i>{{ formatUnixSeconds(service.startedAt) }}</span>
                    <template v-else>
                      <span class="meta-item"><i>{{ $t('service.exitCode') }}</i>{{ service.exitCode ?? 0 }}</span>
                      <span class="meta-item"><i>{{ $t('service.stoppedAt') }}</i>{{ formatUnixSeconds(service.stoppedAt) }}</span>
                    </template>
                    <span class="meta-item"><i>{{ $t('service.bufferSize') }}</i>{{ formatBytes(service.outputBytes) }}</span>
                    <span v-if="service.outputTruncated" class="meta-item attention"><i>{{ $t('service.retention') }}</i>{{ $t('taskCenter.latestRetained') }}</span>
                  </div>
                  <div class="service-buffer-policy">{{ $t('service.bufferPolicy') }}</div>
                </details>
                <div class="buffer-preview">
                  <div class="buffer-title service-buffer-title">
                    <span>{{ $t('taskCenter.bufferPreview') }}</span>
                    <n-button size="small" quaternary @click="openServiceLog(service)">{{ $t('taskCenter.viewFullLog') }}</n-button>
                  </div>
                  <pre v-if="service.outputTail" v-html="previewHtml(service.outputTail)"></pre>
                  <span v-else class="empty-hint">{{ $t('taskCenter.emptyBuffer') }}</span>
                </div>
                <div v-if="service.error" class="service-error">{{ service.error }}</div>
              </article>
            </div>
          </n-spin>
        </n-tab-pane>

        <n-tab-pane name="scheduled" :tab="$t('taskCenter.scheduledTab', { running: scheduledRunningCount, total: tasks.length })">
          <div class="task-overview">
            <span>{{ $t('scheduled.runningCount', { count: scheduledRunningCount }) }}</span>
            <span>{{ $t('scheduled.persisted') }}</span>
          </div>
          <n-spin :show="scheduledLoading">
            <n-empty v-if="!tasks.length" :description="$t('scheduled.empty')">
              <template #extra><span class="empty-hint">{{ $t('scheduled.emptyHint') }}</span></template>
            </n-empty>
            <div v-else class="task-list">
              <article v-for="task in tasks" :key="task.id" class="task-card scheduled-card">
                <div class="card-head">
                  <div class="head-main">
                    <span class="card-title">{{ task.name || task.id }}</span>
                    <div class="card-state">
                      <span class="task-status" :class="`tone-${scheduledStatusTone(task)}`">{{ scheduledStatusLabel(task) }}</span>
                      <span class="task-kind">{{ task.command ? $t('scheduled.kind.command') : $t('scheduled.kind.agent') }}</span>
                    </div>
                  </div>
                  <div class="head-side">
                    <n-button v-if="task.lastSummary || task.lastError" size="small" quaternary @click="openScheduledLog(task)">{{ $t('taskCenter.viewFullLog') }}</n-button>
                    <n-popconfirm
                      :positive-text="$t('common.delete')"
                      :negative-text="$t('common.cancel')"
                      @positive-click="$emit('deleteTask', task.id)"
                    >
                      <template #trigger>
                        <n-button size="small" type="error" quaternary :loading="deletingIds.includes(task.id)">{{ $t('scheduled.deleteTask') }}</n-button>
                      </template>
                      {{ $t('scheduled.deleteConfirm') }}
                    </n-popconfirm>
                  </div>
                </div>
                <div class="mono-muted task-id">{{ task.id }}</div>
                <!-- 调度行：“什么时候跑”——下次运行才是行动重点，单独提出来。 -->
                <div class="schedule-line">
                  <span class="schedule-text">{{ scheduleLabel(task.schedule) }}</span>
                  <span v-if="task.nextRunAt" class="next-run"><i>{{ $t('scheduled.nextRun') }}</i>{{ formatTime(task.nextRunAt) }}</span>
                  <span v-else class="next-run idle">{{ $t('scheduled.noNextRun') }}</span>
                </div>
                <!-- 内容行：“跑什么”——command（高亮命令）或 instruction（LLM 委托描述）。 -->
                <div v-if="task.command" class="command" :title="task.command" v-html="highlightedCommand(task.command)"></div>
                <div v-else-if="task.instruction" class="instruction">{{ task.instruction }}</div>
                <div v-if="viewScope === 'all'" class="mono-muted ellipsis" :title="task.workspace">{{ $t('taskCenter.workspace') }} · {{ task.workspace || $t('taskCenter.noWorkspace') }}</div>
                <!-- 事实行：历史计数，异常项（连续失败）才上色。 -->
                <div class="meta-line">
                  <span class="meta-item"><i>{{ $t('scheduled.lastRun') }}</i>{{ formatTime(task.lastRunAt) }}</span>
                  <span class="meta-item"><i>{{ $t('scheduled.runCount') }}</i>{{ task.runCount || 0 }}</span>
                  <span v-if="task.consecutiveFailures" class="meta-item attention"><i>{{ $t('scheduled.failures') }}</i>{{ task.consecutiveFailures }}</span>
                  <span v-if="!task.command" class="meta-item"><i>{{ $t('scheduled.limit') }}</i>{{ $t('common.steps', { count: task.maxSteps }) }} · {{ durationLabel(task.timeoutSeconds) }}</span>
                </div>
                <div v-if="task.lastSummary || task.lastError" class="buffer-preview" :class="{ error: task.lastError && !task.lastSummary }">
                  <div class="buffer-title">{{ task.lastSummary ? $t('scheduled.latestOutput') : $t('scheduled.latestError') }}</div>
                  <pre v-html="previewHtml(task.lastSummary || task.lastError)"></pre>
                </div>
              </article>
            </div>
          </n-spin>
        </n-tab-pane>
      </n-tabs>
    </div>
  </n-modal>

  <n-modal v-model:show="logVisible" preset="card" :title="logTitle" :style="{ width: 'min(960px, 92vw)' }" @after-enter="scrollLogToBottom">
    <div class="log-toolbar">
      <span>{{ logMeta }}</span>
      <span v-if="logRenderNote" class="log-note">{{ logRenderNote }}</span>
      <span v-if="logServiceId && !logFollowing" class="log-follow-state">{{ $t('taskCenter.logPaused') }}</span>
      <n-space>
        <n-button v-if="logServiceId && !logFollowing" size="small" secondary @click="resumeLogFollowing">{{ $t('taskCenter.backToLatest') }}</n-button>
        <n-button v-if="logServiceId" size="small" quaternary :loading="logLoading" @click="refreshServiceLog()">{{ $t('common.refresh') }}</n-button>
        <n-button size="small" quaternary :disabled="!logContent" @click="copyLog">{{ $t('taskCenter.copyBuffer') }}</n-button>
      </n-space>
    </div>
    <n-spin :show="logLoading && !logContent">
      <pre ref="logPre" class="full-log" @scroll.passive="onLogScroll"><span v-if="logContent" v-html="logRenderHtml"></span><template v-else>{{ $t('taskCenter.emptyBuffer') }}</template></pre>
    </n-spin>
  </n-modal>
</template>

<script setup>
import { computed, nextTick, onUnmounted, ref, watch } from 'vue';
import { GetServiceOutput } from '../../bindings/ally-dev/internal/app/app';
import { formatDateTime, t } from '../i18n.mjs';
import { renderAnsiToHtml } from '../utils/ansi.mjs';
import { formatBytes } from '../utils/format.mjs';
import { serviceDescription } from '../utils/servicePresentation.mjs';
import { effectiveTaskScope } from '../utils/taskWorkspace.mjs';
import { highlightShellCommand } from '../utils/shellHighlight.mjs';
import { isScheduledTaskRunning, isServiceActive, scheduledStatusKey, scheduledStatusTone, serviceStatusKey, serviceStatusTone } from '../utils/taskStatus.mjs';

const props = defineProps({
  show: { type: Boolean, default: false },
  tasks: { type: Array, default: () => [] },
  services: { type: Array, default: () => [] },
  scheduledLoading: { type: Boolean, default: false },
  servicesLoading: { type: Boolean, default: false },
  deletingIds: { type: Array, default: () => [] },
  stoppingIds: { type: Array, default: () => [] },
  // 工具栏入口 chip 决定打开哪个 Tab（S=services / C=scheduled）。
  defaultTab: { type: String, default: 'services' },
  scope: { type: String, default: 'all' },
  workspace: { type: String, default: '' },
});

defineEmits(['close', 'refresh', 'deleteTask', 'stopService', 'update:scope']);

// Background services come first: they are the live, interactive part of the
// task center (stop / tail a running dev server), while scheduled tasks are
// mostly static rows you glance at.
const activeTab = ref('services');
const tabsRef = ref(null);
const logVisible = ref(false);
const logLoading = ref(false);
const logTitle = ref('');
const logMeta = ref('');
const logContent = ref('');
const logServiceId = ref('');
const logServiceActive = ref(false);
const logPre = ref(null);
const logFollowing = ref(true);
let logRefreshTimer = 0;
let logRequestVersion = 0;
const pendingLogRequests = new Map();

function invalidateLogRequest() {
  logRequestVersion += 1;
  logLoading.value = false;
}

const scheduledRunningCount = computed(() => props.tasks.filter(isScheduledTaskRunning).length);
const serviceRunningCount = computed(() => props.services.filter(isServiceActive).length);
const loading = computed(() => props.scheduledLoading || props.servicesLoading);
const viewScope = computed(() => effectiveTaskScope(props.scope, props.workspace));

// Service output carries SGR color escapes. Converting the whole buffer to
// styled HTML on every refresh would be wasteful, so the util drops the head of
// oversized buffers and reports how much it dropped.
const logRender = computed(() => renderAnsiToHtml(logContent.value));
const logRenderHtml = computed(() => logRender.value.html);
const logRenderNote = computed(() => (logRender.value.droppedChars > 0
  ? t('taskCenter.renderTruncated', {
    size: formatBytes(logRender.value.renderedChars),
    dropped: formatBytes(logRender.value.droppedChars),
  })
  : ''));

function syncTabs() {
  nextTick(() => tabsRef.value?.syncBarPosition?.());
}

// Changing ownership context invalidates any in-flight log response too.
watch([() => props.scope, () => props.workspace], () => {
  logVisible.value = false;
  syncTabs();
}, { flush: 'sync' });

// 每次打开面板时定位到入口 chip 对应的 Tab；面板内的手动切换在下次打开前一直有效。
watch(() => props.show, (visible) => {
  if (!visible) return;
  const next = props.defaultTab === 'scheduled' ? 'scheduled' : 'services';
  if (activeTab.value !== next) activeTab.value = next;
  syncTabs();
});

function previewHtml(text) {
  return renderAnsiToHtml(text).html;
}

// 卡内命令高亮：与服务卡标题同源的 shell 分词（自带 HTML 转义）。服务列表
// 随 service:update 反复刷新，按原文缓存避免同一条命令每次重渲都重新分词。
const commandHighlightCache = new Map();
function highlightedCommand(command) {
  const text = String(command || '');
  if (!text) return '';
  let html = commandHighlightCache.get(text);
  if (!html) {
    html = highlightShellCommand(text);
    if (commandHighlightCache.size > 128) commandHighlightCache.clear();
    commandHighlightCache.set(text, html);
  }
  return html;
}

function openScheduledLog(task) {
  invalidateLogRequest();
  logFollowing.value = true;
  logServiceId.value = '';
  logServiceActive.value = false;
  logTitle.value = task.name || task.id;
  logMeta.value = t('taskCenter.scheduledBufferMeta');
  const sections = [];
  if (task.lastSummary) sections.push(`${t('scheduled.latestOutput')}\n${task.lastSummary}`);
  if (task.lastError) sections.push(`${t('scheduled.latestError')}\n${task.lastError}`);
  logContent.value = sections.join('\n\n──────────\n\n');
  logVisible.value = true;
  scrollLogToBottom();
}

async function openServiceLog(service) {
  invalidateLogRequest();
  logFollowing.value = true;
  logServiceId.value = service.id;
  logServiceActive.value = isServiceActive(service);
  logTitle.value = service.name || service.id;
  logMeta.value = '';
  logContent.value = service.outputTail || '';
  logVisible.value = true;
  // Scroll to bottom as soon as the modal opens so the user sees the latest
  // output (matching openScheduledTaskLog). Without this, the initial render
  // shows the top of the buffer even though refreshServiceLog will scroll
  // later — the gap is visible and confusing for long-running services.
  scrollLogToBottom();
  await refreshServiceLog();
}

watch([logVisible, logServiceId, logServiceActive], ([visible, serviceId, active]) => {
  if (logRefreshTimer) window.clearInterval(logRefreshTimer);
  logRefreshTimer = 0;
  if (!visible) invalidateLogRequest();
  if (visible && serviceId && active) {
    logRefreshTimer = window.setInterval(() => {
      if (!document.hidden) void refreshServiceLog({ automatic: true });
    }, 1500);
  }
}, { flush: 'sync' });

// Sync logServiceActive with the services prop so the watch above fires when
// the service backing the open log modal stops running. Without this,
// logServiceActive only updates on openServiceLog() and would stay true until
// the modal closes — even though the underlying service had stopped.
watch(() => props.services, () => {
  if (!logServiceId.value) return;
  const svc = props.services.find((s) => s?.id === logServiceId.value);
  const stillActive = svc ? isServiceActive(svc) : false;
  if (logServiceActive.value !== stillActive) {
    logServiceActive.value = stillActive;
  }
}, { deep: false });

onUnmounted(() => {
  invalidateLogRequest();
  if (logRefreshTimer) window.clearInterval(logRefreshTimer);
});

async function refreshServiceLog({ automatic = false } = {}) {
  if (!logVisible.value || !logServiceId.value || logLoading.value) return;
  if (automatic && !logFollowing.value) return;
  const version = logRequestVersion;
  const serviceId = logServiceId.value;
  logLoading.value = true;
  let request;
  try {
    const previous = pendingLogRequests.get(serviceId);
    if (previous) {
      await previous.catch(() => {});
      if (version !== logRequestVersion) return;
    }
    request = GetServiceOutput(serviceId);
    pendingLogRequests.set(serviceId, request);
    const result = await request;
    if (version !== logRequestVersion) return;
    if (automatic && !logFollowing.value) return;
    const scrollTop = logPre.value?.scrollTop || 0;
    logContent.value = result?.output || '';
    logMeta.value = t('taskCenter.serviceBufferMeta', {
      size: formatBytes(result?.bytes),
      retained: result?.truncated ? t('taskCenter.latestRetained') : t('taskCenter.completeRetained'),
    });
    if (logFollowing.value) {
      scrollLogToBottom();
    } else {
      await nextTick();
      if (version === logRequestVersion && logPre.value && !logFollowing.value) {
        logPre.value.scrollTop = scrollTop;
      }
    }
  } catch (err) {
    if (version !== logRequestVersion) return;
    logMeta.value = t('service.outputLoadFailed', { error: err });
  } finally {
    if (request && pendingLogRequests.get(serviceId) === request) pendingLogRequests.delete(serviceId);
    if (version === logRequestVersion) logLoading.value = false;
  }
}

async function copyLog() {
  if (!logContent.value || !navigator?.clipboard) return;
  await navigator.clipboard.writeText(logContent.value);
}

function scrollLogToBottom() {
  const version = logRequestVersion;
  nextTick(() => {
    requestAnimationFrame(() => {
      if (version !== logRequestVersion || !logVisible.value || !logFollowing.value) return;
      if (logPre.value) logPre.value.scrollTop = logPre.value.scrollHeight;
    });
  });
}

function onLogScroll() {
  const pre = logPre.value;
  if (!pre) return;
  logFollowing.value = pre.scrollHeight - pre.clientHeight - pre.scrollTop <= 24;
}

function resumeLogFollowing() {
  logFollowing.value = true;
  scrollLogToBottom();
  void refreshServiceLog();
}

// 状态词表与工具卡共用，状态显示与操作按钮保持独立。
function scheduledStatusLabel(task) {
  const key = scheduledStatusKey(task);
  return (key ? t(key) : '') || task?.lastStatus || t('scheduled.status.waiting');
}

function serviceStatusLabel(service) {
  const key = serviceStatusKey(service?.status);
  return (key ? t(key) : '') || service?.status || '-';
}

function scheduleLabel(schedule = {}) {
  if (schedule.type === 'once') return t('scheduled.once', { at: schedule.at || '-' });
  if (schedule.type === 'interval') return t('scheduled.interval', { interval: schedule.every || '-' });
  if (schedule.type === 'cron') return t('scheduled.cron', { cron: schedule.cron || '-' });
  return '-';
}

function formatTime(value) {
  const timestamp = Number(value || 0);
  return timestamp ? formatDateTime(timestamp) : '-';
}

function formatUnixSeconds(value) {
  const timestamp = Number(value || 0);
  return timestamp ? formatDateTime(timestamp * 1000) : '-';
}

function durationLabel(seconds) {
  const value = Number(seconds || 0);
  if (value >= 3600 && value % 3600 === 0) return `${value / 3600}h`;
  if (value >= 60 && value % 60 === 0) return `${value / 60}m`;
  return `${value}s`;
}

</script>

<style scoped>
/* 描述是模型或用户自己写的，长度没上界（还带 pre-wrap 的原始换行）：服务卡纵向排列，
   长描述只撑高自己，但十几行会把整页挤成一篇。3 行封顶，全文靠 title 悬停看。 */
.service-description { display: -webkit-box; margin-top: 6px; overflow: hidden; color: var(--ally-text-body); font-size: 12px; line-height: 1.5; white-space: pre-wrap; overflow-wrap: anywhere; -webkit-box-orient: vertical; -webkit-line-clamp: 3; }
.task-scope { display: flex; align-items: center; flex-wrap: wrap; gap: 8px 12px; margin-bottom: 12px; }
.task-scope > .ellipsis { min-width: 0; flex: 1; }
.task-scope-switch { width: 250px; max-width: 100%; flex: none; }
.task-center-body { height: min(72vh, 760px); min-height: 360px; overflow: auto; padding-right: 4px; }
.task-center-body :deep(.task-content-tabs) { min-height: 100%; }
.task-overview { display: flex; flex-wrap: wrap; gap: 4px 16px; margin-bottom: 10px; color: var(--ally-text-muted); font-size: 12px; }
.task-list { display: flex; flex-direction: column; gap: 8px; padding-bottom: 8px; }
.task-card { padding: 12px; border: 1px solid var(--ally-border); border-radius: 8px; background: var(--ally-surface-content); }
/* 状态留在标题区，右侧只放统一尺寸的操作按钮。 */
.card-head { display: flex; align-items: center; flex-wrap: wrap; gap: 8px 24px; }
.head-main { display: flex; flex: 1; align-items: center; flex-wrap: wrap; min-width: 0; gap: 6px 12px; }
.head-side { display: flex; align-items: center; flex-wrap: wrap; gap: 8px; margin-left: auto; }
.card-state { display: flex; align-items: center; flex-wrap: wrap; gap: 10px; }
.task-kind { color: var(--ally-text-muted); font-size: 12px; }
.task-id { margin-top: 4px; overflow-wrap: anywhere; }
.task-status { display: inline-flex; align-items: center; gap: 5px; color: var(--ally-text-muted); font-size: 12px; }
.task-status::before { width: 5px; height: 5px; border-radius: 50%; background: currentColor; content: ''; }
.task-status.tone-success { color: var(--ally-success); }
.task-status.tone-info { color: var(--ally-info); }
.task-status.tone-warning { color: var(--ally-warning-text); }
.task-status.tone-danger { color: var(--ally-danger-pale); }
.service-details { margin-top: 8px; color: var(--ally-text-muted); font-size: 12px; }
.service-details > summary { width: fit-content; cursor: pointer; }
.service-details > summary:hover { color: var(--ally-text-primary); }
.service-details > summary:focus-visible { outline: 2px solid var(--ally-accent); outline-offset: 3px; }
.service-facts { display: grid; gap: 5px; margin: 10px 0 0; }
.service-facts > div { display: grid; grid-template-columns: 76px minmax(0, 1fr); gap: 8px; }
.service-facts dt { color: var(--ally-text-muted); }
.service-facts dd { margin: 0; color: var(--ally-text-body); font-family: var(--ally-mono-font); overflow-wrap: anywhere; }
.service-buffer-policy { margin-top: 6px; font-size: 11px; }
.card-title { color: var(--ally-text-primary); font-size: 13px; font-weight: 650; }
.mono-muted { color: var(--ally-text-faint); font-family: ui-monospace, SFMono-Regular, Consolas, monospace; font-size: 11px; }
.ellipsis { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }

/* Dense label/value pairs on one wrapping line instead of a two-column grid. */
.meta-line { display: flex; flex-wrap: wrap; gap: 2px 0; margin: 6px 0 4px; color: var(--ally-text-body); font-size: 11px; }
.meta-item { display: inline-flex; align-items: baseline; gap: 4px; }
.meta-item + .meta-item::before { margin: 0 8px; color: var(--ally-border-strong, var(--ally-text-faint)); content: '\00B7'; }
.meta-item i { color: var(--ally-text-faint); font-size: 11px; font-style: normal; }
/* 只在异常时出现的事实项（输出被截断、连续失败）才上警示色。 */
.meta-item.attention, .meta-item.attention i { color: var(--ally-warning-text); }

/* 定时任务的调度行：“什么时候跑”单独成行，下次运行加粗为行动重点。 */
.schedule-line { display: flex; align-items: baseline; flex-wrap: wrap; margin-top: 6px; font-size: 12px; }
.schedule-text { color: var(--ally-text-body); }
.next-run { color: var(--ally-accent-strong); font-weight: 600; }
.next-run::before { margin: 0 8px; color: var(--ally-text-ghost); content: '\00B7'; }
.next-run i { margin-right: 4px; color: var(--ally-text-faint); font-size: 11px; font-style: normal; font-weight: 400; }
.next-run.idle { color: var(--ally-text-faint); font-weight: 400; }

.instruction, .command { display: -webkit-box; margin-top: 6px; overflow: hidden; color: var(--ally-text-body); font-size: 12px; line-height: 1.5; -webkit-box-orient: vertical; -webkit-line-clamp: 2; }
.command { font-family: ui-monospace, SFMono-Regular, Consolas, monospace; }
.service-command { display: block; overflow: visible; white-space: pre-wrap; overflow-wrap: anywhere; }
/* v-html 注入的 shell 分词 token 不带 scoped 属性，必须 :deep 才能命中。
   配色与工具卡 .tool-command 的暗一档四色同源。 */
.command :deep(.shell-command-word) { color: var(--ally-accent-dim); }
.command :deep(.shell-string) { color: #82b47e; }
.command :deep(.shell-param) { color: #b8818c; }
.command :deep(.shell-operator) { color: #75b5aa; }
.command :deep(.shell-comment) { color: var(--ally-text-faint); font-style: italic; }
html[data-mode="light"] .command :deep(.shell-command-word) { color: var(--ally-accent-strong); }
html[data-mode="light"] .command :deep(.shell-string) { color: #1a7f4b; }
html[data-mode="light"] .command :deep(.shell-param) { color: #b3425e; }
html[data-mode="light"] .command :deep(.shell-operator) { color: #0f766e; }
/* 预览块用代码块底色，与日志弹窗是同一个 mat 层级：它跟卡面（淡色底/淡绿底）
   差着一整个表面层级，两种卡上边界都一眼可见。 */
.buffer-preview { margin-top: 8px; padding: 8px 10px; border-radius: 8px; background: var(--ally-code-bg); }
/* 出错时在同一个 mat 上叠红：直接换成半透明红会让卡片底色透上来，mat 就没了。 */
.buffer-preview.error { background: color-mix(in srgb, var(--ally-danger) 14%, var(--ally-code-bg)); }
.service-error { background: rgba(239,68,68,.08); }
.buffer-title { margin-bottom: 6px; color: var(--ally-text-muted); font-size: 11px; }
.service-buffer-title { display: flex; align-items: center; justify-content: space-between; gap: 8px; }
.buffer-preview pre { display: -webkit-box; margin: 0; overflow: hidden; color: var(--ally-text-body); font: 11px/1.5 ui-monospace,SFMono-Regular,Consolas,monospace; white-space: pre-wrap; word-break: break-word; -webkit-box-orient: vertical; -webkit-line-clamp: 4; }
.service-error { margin-top: 6px; padding: 6px 10px; border-radius: 8px; color: var(--ally-danger-pale); font-size: 11px; }
.empty-hint { color: var(--ally-text-faint); font-size: 12px; }
.log-toolbar { display: flex; align-items: center; flex-wrap: wrap; gap: 4px 12px; margin-bottom: 10px; color: var(--ally-text-muted); font-size: 11px; }
.log-toolbar .n-space { margin-left: auto; }
.log-note { color: var(--ally-warning-pale, var(--ally-text-faint)); }
.full-log { height: min(68vh, 720px); margin: 0; overflow: auto; padding: 14px; border: 1px solid var(--ally-border); border-radius: 8px; background: var(--ally-code-bg); color: var(--ally-text-body); font: 12px/1.55 ui-monospace,SFMono-Regular,Consolas,monospace; tab-size: 2; white-space: pre-wrap; word-break: break-word; }
</style>
