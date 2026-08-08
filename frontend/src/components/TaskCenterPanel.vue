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
      <n-tabs ref="tabsRef" v-model:value="activeTab" type="line" default-value="scheduled" pane-wrapper-style="padding-top: 4px;">
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
                <div class="card-header">
                  <div class="title-wrap">
                    <div class="card-title">{{ task.name || task.id }}</div>
                    <div class="mono-muted">{{ task.id }}</div>
                  </div>
                  <div class="tags">
                    <n-tag size="small" round :type="scheduledStatusType(task)">{{ scheduledStatusLabel(task) }}</n-tag>
                    <n-tag size="small" round type="warning">YOLO</n-tag>
                  </div>
                </div>
                <div class="meta-grid">
                  <div><span>{{ $t('scheduled.schedule') }}</span><strong>{{ scheduleLabel(task.schedule) }}</strong></div>
                  <div><span>{{ $t('scheduled.nextRun') }}</span><strong>{{ formatTime(task.nextRunAt) }}</strong></div>
                  <div><span>{{ $t('scheduled.lastRun') }}</span><strong>{{ formatTime(task.lastRunAt) }}</strong></div>
                  <div><span>{{ $t('scheduled.runCount') }}</span><strong>{{ task.runCount || 0 }}</strong></div>
                  <div><span>{{ $t('scheduled.limit') }}</span><strong>{{ $t('common.steps', { count: task.maxSteps }) }} · {{ durationLabel(task.timeoutSeconds) }}</strong></div>
                  <div><span>{{ $t('scheduled.failures') }}</span><strong>{{ task.consecutiveFailures || 0 }}</strong></div>
                </div>
                <div class="mono-muted ellipsis" :title="task.workspace">{{ task.workspace }}</div>
                <div class="instruction">{{ task.instruction }}</div>
                <div v-if="task.lastSummary || task.lastError" class="buffer-preview" :class="{ error: task.lastError && !task.lastSummary }">
                  <div class="buffer-title">{{ $t('taskCenter.bufferPreview') }}</div>
                  <pre>{{ task.lastSummary || task.lastError }}</pre>
                </div>
                <div class="actions">
                  <n-button v-if="task.lastSummary || task.lastError" size="small" ghost @click="openScheduledLog(task)">{{ $t('taskCenter.viewBuffer') }}</n-button>
                  <n-popconfirm
                    :positive-text="$t('common.delete')"
                    :negative-text="$t('common.cancel')"
                    @positive-click="$emit('deleteTask', task.id)"
                  >
                    <template #trigger>
                      <n-button size="small" type="error" ghost :loading="deletingIds.includes(task.id)">{{ $t('scheduled.deleteTask') }}</n-button>
                    </template>
                    {{ $t('scheduled.deleteConfirm') }}
                  </n-popconfirm>
                </div>
              </article>
            </div>
          </n-spin>
        </n-tab-pane>

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
                <div class="card-header">
                  <div class="title-wrap">
                    <div class="card-title">{{ service.name || service.id }}</div>
                    <div class="mono-muted">{{ service.id }} · PID {{ service.pid || '-' }}</div>
                  </div>
                  <n-tag size="small" round :type="serviceStatusType(service)">{{ serviceStatusLabel(service) }}</n-tag>
                </div>
                <div class="meta-grid">
                  <div><span>{{ $t('service.startedAt') }}</span><strong>{{ formatUnixSeconds(service.startedAt) }}</strong></div>
                  <div><span>{{ $t('service.stoppedAt') }}</span><strong>{{ formatUnixSeconds(service.stoppedAt) }}</strong></div>
                  <div><span>{{ $t('service.exitCode') }}</span><strong>{{ isActiveService(service) ? '-' : (service.exitCode ?? 0) }}</strong></div>
                  <div><span>{{ $t('service.bufferSize') }}</span><strong>{{ formatBytes(service.outputBytes) }}</strong></div>
                  <div><span>{{ $t('service.retention') }}</span><strong>{{ service.outputTruncated ? $t('common.truncated') : $t('service.completeBuffer') }}</strong></div>
                </div>
                <div class="mono-muted ellipsis" :title="service.cwd">{{ service.cwd }}</div>
                <div class="command" :title="service.command">{{ service.command }}</div>
                <div v-if="service.outputTail" class="buffer-preview">
                  <div class="buffer-title">{{ $t('taskCenter.bufferPreview') }}</div>
                  <pre>{{ service.outputTail }}</pre>
                </div>
                <div v-if="service.error" class="service-error">{{ service.error }}</div>
                <div class="actions">
                  <n-button size="small" ghost @click="openServiceLog(service)">{{ $t('taskCenter.viewBuffer') }}</n-button>
                  <n-popconfirm
                    v-if="isActiveService(service)"
                    :positive-text="$t('service.stop')"
                    :negative-text="$t('common.cancel')"
                    @positive-click="$emit('stopService', service.id)"
                  >
                    <template #trigger>
                      <n-button size="small" type="warning" ghost :loading="stoppingIds.includes(service.id)">{{ $t('service.stop') }}</n-button>
                    </template>
                    {{ $t('service.stopConfirm') }}
                  </n-popconfirm>
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
      <n-space>
        <n-button v-if="logServiceId" size="small" quaternary :loading="logLoading" @click="refreshServiceLog">{{ $t('common.refresh') }}</n-button>
        <n-button size="small" quaternary :disabled="!logContent" @click="copyLog">{{ $t('taskCenter.copyBuffer') }}</n-button>
      </n-space>
    </div>
    <n-spin :show="logLoading">
      <pre ref="logPre" class="full-log">{{ logContent || $t('taskCenter.emptyBuffer') }}</pre>
    </n-spin>
  </n-modal>
</template>

<script setup>
import { computed, nextTick, onUnmounted, ref, watch } from 'vue';
import { GetServiceOutput } from '../../bindings/ally-dev/internal/app/app';
import { formatDateTime, t } from '../i18n.mjs';

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

const activeTab = ref('scheduled');
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

function syncTabs() {
  nextTick(() => tabsRef.value?.syncBarPosition?.());
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
.task-overview { display: flex; flex-wrap: wrap; gap: 8px 16px; margin-bottom: 14px; color: #8f8f8f; font-size: 12px; }
.task-list { display: flex; flex-direction: column; gap: 12px; padding-bottom: 12px; }
.task-card { padding: 14px; border: 1px solid rgba(255,255,255,.08); border-radius: 10px; background: rgba(255,255,255,.025); }
.card-header, .actions, .log-toolbar { display: flex; align-items: flex-start; justify-content: space-between; gap: 12px; }
.title-wrap { min-width: 0; }
.card-title { color: #f1f1f1; font-size: 14px; font-weight: 650; }
.mono-muted { color: #707070; font-family: ui-monospace, SFMono-Regular, Consolas, monospace; font-size: 11px; }
.ellipsis { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.tags { display: flex; flex-shrink: 0; gap: 6px; }
.meta-grid { display: grid; grid-template-columns: repeat(2,minmax(0,1fr)); gap: 8px 16px; margin: 14px 0 10px; }
.meta-grid div { display: flex; flex-direction: column; gap: 2px; }
.meta-grid span { color: #777; font-size: 11px; }
.meta-grid strong { overflow: hidden; color: #c9c9c9; font-size: 12px; font-weight: 500; text-overflow: ellipsis; white-space: nowrap; }
.instruction, .command { display: -webkit-box; margin-top: 10px; overflow: hidden; color: #bdbdbd; font-size: 12px; line-height: 1.55; -webkit-box-orient: vertical; -webkit-line-clamp: 3; }
.command { font-family: ui-monospace, SFMono-Regular, Consolas, monospace; -webkit-line-clamp: 2; }
.buffer-preview { margin-top: 12px; padding: 10px; border-radius: 8px; background: rgba(255,255,255,.035); }
.buffer-preview.error, .service-error { background: rgba(239,68,68,.08); }
.buffer-title { margin-bottom: 6px; color: #8f8f8f; font-size: 11px; }
.buffer-preview pre { display: -webkit-box; margin: 0; overflow: hidden; color: #d4d4d4; font: 11px/1.5 ui-monospace,SFMono-Regular,Consolas,monospace; white-space: pre-wrap; word-break: break-word; -webkit-box-orient: vertical; -webkit-line-clamp: 6; }
.service-error { margin-top: 10px; padding: 8px 10px; border-radius: 8px; color: #f0a5a5; font-size: 11px; }
.actions { justify-content: flex-end; margin-top: 12px; }
.empty-hint { color: #777; font-size: 12px; }
.log-toolbar { align-items: center; margin-bottom: 10px; color: #858585; font-size: 11px; }
.full-log { height: min(68vh, 720px); margin: 0; overflow: auto; padding: 14px; border: 1px solid rgba(255,255,255,.08); border-radius: 8px; background: #161616; color: #d7d7d7; font: 12px/1.55 ui-monospace,SFMono-Regular,Consolas,monospace; tab-size: 2; white-space: pre-wrap; word-break: break-word; }
@media (max-width: 720px) { .meta-grid { grid-template-columns: 1fr; } }
</style>
