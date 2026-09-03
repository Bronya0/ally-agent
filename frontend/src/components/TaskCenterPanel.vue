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
    :mask-closable="false"
    @update:show="(value) => !value && $emit('close')"
    @after-enter="syncTabs"
  >
    <template #header-extra>
      <n-button size="small" quaternary :loading="loading" @click="$emit('refresh')">{{ $t('common.refresh') }}</n-button>
    </template>

    <div class="task-center-body">
      <n-tabs ref="tabsRef" v-model:value="activeTab" type="line" pane-wrapper-style="padding-top: 4px;">
        <n-tab-pane name="services" :tab="$t('taskCenter.servicesTab', { count: services.length })">
          <div class="task-overview">
            <span>{{ $t('service.runningCount', { count: serviceRunningCount }) }}</span>
            <span>{{ $t('service.bufferPolicy') }}</span>
          </div>
          <n-spin :show="servicesLoading">
            <n-empty v-if="!services.length" :description="$t('service.empty')">
              <template #extra><span class="empty-hint">{{ $t('service.emptyHint') }}</span></template>
            </n-empty>
            <div v-else class="task-list">
              <article v-for="service in services" :key="service.id" class="task-card">
                <div class="card-head">
                  <div class="head-main">
                    <span class="card-title">{{ service.name || service.id }}</span>
                    <span class="mono-muted">{{ service.id }}<template v-if="service.pid"> · PID {{ service.pid }}</template></span>
                  </div>
                  <div class="head-side">
                    <n-tag size="small" round :type="serviceStatusType(service)">{{ serviceStatusLabel(service) }}</n-tag>
                    <n-button size="tiny" quaternary @click="openServiceLog(service)">{{ $t('taskCenter.viewBuffer') }}</n-button>
                    <n-popconfirm
                      v-if="isActiveService(service)"
                      :positive-text="$t('service.stop')"
                      :negative-text="$t('common.cancel')"
                      @positive-click="$emit('stopService', service.id)"
                    >
                      <template #trigger>
                        <n-button size="tiny" type="error" quaternary :loading="stoppingIds.includes(service.id)">{{ $t('service.stop') }}</n-button>
                      </template>
                      {{ $t('service.stopConfirm') }}
                    </n-popconfirm>
                  </div>
                </div>
                <div class="meta-line">
                  <span class="meta-item"><i>{{ $t('service.startedAt') }}</i>{{ formatUnixSeconds(service.startedAt) }}</span>
                  <span class="meta-item"><i>{{ $t('service.stoppedAt') }}</i>{{ formatUnixSeconds(service.stoppedAt) }}</span>
                  <span class="meta-item"><i>{{ $t('service.exitCode') }}</i>{{ isActiveService(service) ? '-' : (service.exitCode ?? 0) }}</span>
                  <span class="meta-item"><i>{{ $t('service.bufferSize') }}</i>{{ formatBytes(service.outputBytes) }}</span>
                  <span class="meta-item"><i>{{ $t('service.retention') }}</i>{{ service.outputTruncated ? $t('common.truncated') : $t('service.completeBuffer') }}</span>
                </div>
                <div class="mono-muted ellipsis" :title="service.cwd">{{ service.cwd }}</div>
                <div class="command" :title="service.command">{{ service.command }}</div>
                <div v-if="service.outputTail" class="buffer-preview">
                  <div class="buffer-title">{{ $t('taskCenter.bufferPreview') }}</div>
                  <pre v-html="previewHtml(service.outputTail)"></pre>
                </div>
                <div v-if="service.error" class="service-error">{{ service.error }}</div>
              </article>
            </div>
          </n-spin>
        </n-tab-pane>

        <n-tab-pane name="scheduled" :tab="$t('taskCenter.scheduledTab', { count: tasks.length })">
          <div class="task-overview">
            <span>{{ $t('scheduled.runningCount', { count: scheduledRunningCount }) }}</span>
            <span>{{ $t('scheduled.sessionOnly') }}</span>
          </div>
          <n-spin :show="scheduledLoading">
            <n-empty v-if="!tasks.length" :description="$t('scheduled.empty')">
              <template #extra><span class="empty-hint">{{ $t('scheduled.emptyHint') }}</span></template>
            </n-empty>
            <div v-else class="task-list">
              <article v-for="task in tasks" :key="task.id" class="task-card">
                <div class="card-head">
                  <div class="head-main">
                    <span class="card-title">{{ task.name || task.id }}</span>
                    <span class="mono-muted">{{ task.id }}</span>
                  </div>
                  <div class="head-side">
                    <n-tag size="small" round :type="scheduledStatusType(task)">{{ scheduledStatusLabel(task) }}</n-tag>
                    <n-tag size="small" round type="warning">YOLO</n-tag>
                    <n-button v-if="task.lastSummary || task.lastError" size="tiny" quaternary @click="openScheduledLog(task)">{{ $t('taskCenter.viewBuffer') }}</n-button>
                    <n-popconfirm
                      :positive-text="$t('common.delete')"
                      :negative-text="$t('common.cancel')"
                      @positive-click="$emit('deleteTask', task.id)"
                    >
                      <template #trigger>
                        <n-button size="tiny" type="error" quaternary :loading="deletingIds.includes(task.id)">{{ $t('scheduled.deleteTask') }}</n-button>
                      </template>
                      {{ $t('scheduled.deleteConfirm') }}
                    </n-popconfirm>
                  </div>
                </div>
                <div class="meta-line">
                  <span class="meta-item"><i>{{ $t('scheduled.schedule') }}</i>{{ scheduleLabel(task.schedule) }}</span>
                  <span class="meta-item"><i>{{ $t('scheduled.nextRun') }}</i>{{ formatTime(task.nextRunAt) }}</span>
                  <span class="meta-item"><i>{{ $t('scheduled.lastRun') }}</i>{{ formatTime(task.lastRunAt) }}</span>
                  <span class="meta-item"><i>{{ $t('scheduled.runCount') }}</i>{{ task.runCount || 0 }}</span>
                  <span class="meta-item"><i>{{ $t('scheduled.limit') }}</i>{{ $t('common.steps', { count: task.maxSteps }) }} · {{ durationLabel(task.timeoutSeconds) }}</span>
                  <span class="meta-item"><i>{{ $t('scheduled.failures') }}</i>{{ task.consecutiveFailures || 0 }}</span>
                </div>
                <div class="mono-muted ellipsis" :title="task.workspace">{{ task.workspace }}</div>
                <div class="instruction">{{ task.instruction }}</div>
                <div v-if="task.lastSummary || task.lastError" class="buffer-preview" :class="{ error: task.lastError && !task.lastSummary }">
                  <div class="buffer-title">{{ $t('taskCenter.bufferPreview') }}</div>
                  <pre v-html="previewHtml(task.lastSummary || task.lastError)"></pre>
                </div>
              </article>
            </div>
          </n-spin>
        </n-tab-pane>
      </n-tabs>
    </div>
  </n-modal>

  <n-modal v-model:show="logVisible" preset="card" :title="logTitle" class="task-log-modal" :style="{ width: 'min(960px, 92vw)' }">
    <div class="log-toolbar">
      <span>{{ logMeta }}</span>
      <span v-if="logRenderNote" class="log-note">{{ logRenderNote }}</span>
      <n-space>
        <n-button v-if="logServiceId" size="small" quaternary :loading="logLoading" @click="refreshServiceLog">{{ $t('common.refresh') }}</n-button>
        <n-button size="small" quaternary :disabled="!logContent" @click="copyLog">{{ $t('taskCenter.copyBuffer') }}</n-button>
      </n-space>
    </div>
    <n-spin :show="logLoading">
      <pre v-if="logContent" ref="logPre" class="full-log" v-html="logRenderHtml"></pre>
      <pre v-else class="full-log">{{ $t('taskCenter.emptyBuffer') }}</pre>
    </n-spin>
  </n-modal>
</template>

<script setup>
import { computed, nextTick, onUnmounted, ref, watch } from 'vue';
import { GetServiceOutput } from '../../bindings/ally-dev/internal/app/app';
import { formatDateTime, t } from '../i18n.mjs';
import { renderAnsiToHtml } from '../utils/ansi.mjs';

const props = defineProps({
  show: { type: Boolean, default: false },
  tasks: { type: Array, default: () => [] },
  services: { type: Array, default: () => [] },
  scheduledLoading: { type: Boolean, default: false },
  servicesLoading: { type: Boolean, default: false },
  deletingIds: { type: Array, default: () => [] },
  stoppingIds: { type: Array, default: () => [] },
});

defineEmits(['close', 'refresh', 'deleteTask', 'stopService']);

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
let logRefreshTimer = 0;

const scheduledRunningCount = computed(() => props.tasks.filter((task) => task?.running).length);
const serviceRunningCount = computed(() => props.services.filter(isActiveService).length);
const loading = computed(() => props.scheduledLoading || props.servicesLoading);

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

function previewHtml(text) {
  return renderAnsiToHtml(text).html;
}

function openScheduledLog(task) {
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
  logServiceId.value = service.id;
  logServiceActive.value = isActiveService(service);
  logTitle.value = service.name || service.id;
  logContent.value = service.outputTail || '';
  logVisible.value = true;
  // Scroll to bottom as soon as the modal opens so the user sees the latest
  // output (matching openScheduledTaskLog). Without this, the initial render
  // shows the top of the buffer even though refreshServiceLog will scroll
  // later — the gap is visible and confusing for long-running services.
  scrollLogToBottom();
  await refreshServiceLog();
}

watch(logVisible, (visible) => {
  if (logRefreshTimer) window.clearInterval(logRefreshTimer);
  logRefreshTimer = 0;
  if (visible && logServiceId.value && logServiceActive.value) {
    logRefreshTimer = window.setInterval(() => void refreshServiceLog(), 1500);
  }
});

// Stop the polling loop if the service stops while the log viewer is still
// open — previously the 1.5s GetServiceOutput calls kept firing until the
// user closed the modal, even though the data had gone static.
watch(logServiceActive, (active) => {
  if (!active && logRefreshTimer) {
    window.clearInterval(logRefreshTimer);
    logRefreshTimer = 0;
  }
});

// Sync logServiceActive with the services prop so the watch above fires when
// the service backing the open log modal stops running. Without this,
// logServiceActive only updates on openServiceLog() and would stay true until
// the modal closes — even though the underlying service had stopped.
watch(() => props.services, () => {
  if (!logServiceId.value) return;
  const svc = props.services.find((s) => s?.id === logServiceId.value);
  const stillActive = svc ? isActiveService(svc) : false;
  if (logServiceActive.value !== stillActive) {
    logServiceActive.value = stillActive;
  }
}, { deep: false });

onUnmounted(() => {
  if (logRefreshTimer) window.clearInterval(logRefreshTimer);
});

async function refreshServiceLog() {
  if (!logServiceId.value) return;
  logLoading.value = true;
  try {
    const result = await GetServiceOutput(logServiceId.value);
    logContent.value = result?.output || '';
    logMeta.value = t('taskCenter.serviceBufferMeta', {
      size: formatBytes(result?.bytes),
      retained: result?.truncated ? t('taskCenter.latestRetained') : t('taskCenter.completeRetained'),
    });
    scrollLogToBottom();
  } catch (err) {
    logMeta.value = t('service.outputLoadFailed', { error: err });
  } finally {
    logLoading.value = false;
  }
}

async function copyLog() {
  if (!logContent.value || !navigator?.clipboard) return;
  await navigator.clipboard.writeText(logContent.value);
}

function scrollLogToBottom() {
  // The log modal may still be mounting (n-modal transition) or the <pre>
  // may still be laying out a large buffer (up to 512 KiB) when nextTick
  // fires. Use requestAnimationFrame after nextTick so the browser has
  // flushed layout, and retry once more on the next frame to catch slow
  // renders. This matches how dev-tools consoles auto-stick to bottom.
  nextTick(() => {
    requestAnimationFrame(() => {
      const pre = logPre.value;
      if (!pre) return;
      pre.scrollTop = pre.scrollHeight;
      // Retry once more on the next frame for large buffers that finish
      // laying out after the first paint.
      requestAnimationFrame(() => {
        if (logPre.value) logPre.value.scrollTop = logPre.value.scrollHeight;
      });
    });
  });
}

function isActiveService(service) {
  return ['starting', 'running'].includes(service?.status);
}

function scheduledStatusLabel(task) {
  if (task?.running) return t('common.running');
  const labels = {
    scheduled: t('scheduled.status.waiting'), completed: t('scheduled.status.completed'), failed: t('scheduled.status.failed'), timed_out: t('scheduled.status.timedOut'),
    cancelled: t('scheduled.status.cancelled'), skipped: t('scheduled.status.skipped'), missed: t('scheduled.status.missed'), invalid: t('scheduled.status.invalid'),
  };
  return labels[task?.lastStatus] || task?.lastStatus || t('scheduled.status.waiting');
}

function scheduledStatusType(task) {
  if (task?.running) return 'info';
  if (task?.lastStatus === 'completed') return 'success';
  if (['failed', 'timed_out', 'invalid'].includes(task?.lastStatus)) return 'error';
  if (['skipped', 'missed', 'cancelled'].includes(task?.lastStatus)) return 'warning';
  return 'default';
}

function serviceStatusLabel(service) {
  const labels = {
    starting: t('service.status.starting'), running: t('common.running'), stopped: t('service.status.stopped'),
    exited: t('service.status.exited'), interrupted: t('service.status.interrupted'),
  };
  return labels[service?.status] || service?.status || '-';
}

function serviceStatusType(service) {
  if (service?.status === 'running') return 'success';
  if (service?.status === 'starting') return 'info';
  if (service?.status === 'interrupted') return 'warning';
  if (service?.status === 'exited' && service?.exitCode) return 'error';
  return 'default';
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

function formatBytes(value) {
  const bytes = Number(value || 0);
  if (bytes >= 1024 * 1024) return `${(bytes / (1024 * 1024)).toFixed(1)} MiB`;
  if (bytes >= 1024) return `${(bytes / 1024).toFixed(1)} KiB`;
  return `${bytes} B`;
}
</script>

<style scoped>
.task-center-body { height: min(72vh, 760px); min-height: 360px; overflow: auto; padding-right: 4px; }
.task-center-body :deep(.n-tabs) { min-height: 100%; }
.task-overview { display: flex; flex-wrap: wrap; gap: 4px 16px; margin-bottom: 10px; color: var(--ally-text-muted); font-size: 12px; }
.task-list { display: flex; flex-direction: column; gap: 8px; padding-bottom: 8px; }
.task-card { padding: 8px 10px; border: 1px solid var(--ally-border); border-radius: 8px; background: var(--ally-hover-faint); }
.card-head { display: flex; align-items: center; flex-wrap: wrap; gap: 4px 12px; justify-content: space-between; }
.head-main { display: flex; align-items: baseline; flex-wrap: wrap; min-width: 0; gap: 4px 8px; }
.head-side { display: flex; align-items: center; flex-shrink: 0; gap: 4px; }
.card-title { color: var(--ally-text-primary); font-size: 13px; font-weight: 650; }
.mono-muted { color: var(--ally-text-faint); font-family: ui-monospace, SFMono-Regular, Consolas, monospace; font-size: 11px; }
.ellipsis { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }

/* Dense label/value pairs on one wrapping line instead of a two-column grid. */
.meta-line { display: flex; flex-wrap: wrap; gap: 2px 0; margin: 6px 0 4px; color: var(--ally-text-body); font-size: 11px; }
.meta-item { display: inline-flex; align-items: baseline; gap: 4px; }
.meta-item + .meta-item::before { margin: 0 8px; color: var(--ally-border-strong, var(--ally-text-faint)); content: '\00B7'; }
.meta-item i { color: var(--ally-text-faint); font-size: 11px; font-style: normal; }

.instruction, .command { display: -webkit-box; margin-top: 4px; overflow: hidden; color: var(--ally-text-body); font-size: 12px; line-height: 1.5; -webkit-box-orient: vertical; -webkit-line-clamp: 2; }
.command { font-family: ui-monospace, SFMono-Regular, Consolas, monospace; }
.buffer-preview { margin-top: 8px; padding: 8px 10px; border-radius: 8px; background: var(--ally-hover-faint); }
.buffer-preview.error, .service-error { background: rgba(239,68,68,.08); }
.buffer-title { margin-bottom: 6px; color: var(--ally-text-muted); font-size: 11px; }
.buffer-preview pre { display: -webkit-box; margin: 0; overflow: hidden; color: var(--ally-text-body); font: 11px/1.5 ui-monospace,SFMono-Regular,Consolas,monospace; white-space: pre-wrap; word-break: break-word; -webkit-box-orient: vertical; -webkit-line-clamp: 6; }
.service-error { margin-top: 6px; padding: 6px 10px; border-radius: 8px; color: var(--ally-danger-pale); font-size: 11px; }
.empty-hint { color: var(--ally-text-faint); font-size: 12px; }
.log-toolbar { display: flex; align-items: center; flex-wrap: wrap; gap: 4px 12px; margin-bottom: 10px; color: var(--ally-text-muted); font-size: 11px; }
.log-toolbar .n-space { margin-left: auto; }
.log-note { color: var(--ally-warning-pale, var(--ally-text-faint)); }
.full-log { height: min(68vh, 720px); margin: 0; overflow: auto; padding: 14px; border: 1px solid var(--ally-border); border-radius: 8px; background: var(--ally-code-bg); color: var(--ally-text-body); font: 12px/1.55 ui-monospace,SFMono-Regular,Consolas,monospace; tab-size: 2; white-space: pre-wrap; word-break: break-word; }
</style>
