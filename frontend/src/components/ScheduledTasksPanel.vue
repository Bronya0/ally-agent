<template>
  <n-drawer
    :show="show"
    :width="560"
    placement="right"
    :native-scrollbar="true"
    @update:show="(value) => !value && $emit('close')"
  >
    <n-drawer-content :title="$t('scheduled.title')" closable>
      <template #header-extra>
        <n-button size="small" quaternary :loading="loading" @click="$emit('refresh')">{{ $t('common.refresh') }}</n-button>
      </template>

      <div class="scheduled-overview">
        <span>{{ $t('scheduled.total', { count: tasks.length }) }}</span>
        <span>{{ $t('scheduled.runningCount', { count: runningCount }) }}</span>
        <span>{{ $t('scheduled.openOnly') }}</span>
      </div>

      <n-spin :show="loading">
        <n-empty v-if="!tasks.length" :description="$t('scheduled.empty')">
          <template #extra>
            <span class="scheduled-empty-hint">{{ $t('scheduled.emptyHint') }}</span>
          </template>
        </n-empty>

        <div v-else class="scheduled-list">
          <article v-for="task in tasks" :key="task.id" class="scheduled-card">
            <div class="scheduled-card-header">
              <div class="scheduled-title-wrap">
                <div class="scheduled-title">{{ task.name || task.id }}</div>
                <div class="scheduled-id">{{ task.id }}</div>
              </div>
              <div class="scheduled-tags">
                <n-tag size="small" round :type="statusType(task)">{{ statusLabel(task) }}</n-tag>
                <n-tag size="small" round type="warning">YOLO</n-tag>
              </div>
            </div>

            <div class="scheduled-grid">
              <div><span>{{ $t('scheduled.schedule') }}</span><strong>{{ scheduleLabel(task.schedule) }}</strong></div>
              <div><span>{{ $t('scheduled.nextRun') }}</span><strong>{{ formatTime(task.nextRunAt) }}</strong></div>
              <div><span>{{ $t('scheduled.lastRun') }}</span><strong>{{ formatTime(task.lastRunAt) }}</strong></div>
              <div><span>{{ $t('scheduled.runCount') }}</span><strong>{{ task.runCount || 0 }}</strong></div>
              <div><span>{{ $t('scheduled.limit') }}</span><strong>{{ $t('common.steps', { count: task.maxSteps }) }} · {{ durationLabel(task.timeoutSeconds) }}</strong></div>
              <div><span>{{ $t('scheduled.failures') }}</span><strong>{{ task.consecutiveFailures || 0 }}</strong></div>
            </div>

            <div class="scheduled-workspace" :title="task.workspace">{{ task.workspace }}</div>
            <div class="scheduled-instruction">{{ task.instruction }}</div>

            <div v-if="task.lastSummary" class="scheduled-output">
              <div class="scheduled-output-title">{{ $t('scheduled.latestOutput') }}</div>
              <pre>{{ task.lastSummary }}</pre>
            </div>
            <div v-if="task.lastError" class="scheduled-output error">
              <div class="scheduled-output-title">{{ $t('scheduled.latestError') }}</div>
              <pre>{{ task.lastError }}</pre>
            </div>

            <div class="scheduled-actions">
              <n-popconfirm
                :positive-text="$t('common.delete')"
                :negative-text="$t('common.cancel')"
                @positive-click="$emit('delete', task.id)"
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
    </n-drawer-content>
  </n-drawer>
</template>

<script setup>
import { computed } from 'vue';
import { formatDateTime, t } from '../i18n.mjs';

const props = defineProps({
  show: { type: Boolean, default: false },
  tasks: { type: Array, default: () => [] },
  loading: { type: Boolean, default: false },
  deletingIds: { type: Array, default: () => [] },
});

defineEmits(['close', 'refresh', 'delete']);

const runningCount = computed(() => props.tasks.filter((task) => task?.running).length);

function statusLabel(task) {
  if (task?.running) return t('common.running');
  const labels = {
    scheduled: t('scheduled.status.waiting'), completed: t('scheduled.status.completed'), failed: t('scheduled.status.failed'), timed_out: t('scheduled.status.timedOut'),
    cancelled: t('scheduled.status.cancelled'), skipped: t('scheduled.status.skipped'), missed: t('scheduled.status.missed'), invalid: t('scheduled.status.invalid'),
  };
  return labels[task?.lastStatus] || task?.lastStatus || t('scheduled.status.waiting');
}

function statusType(task) {
  if (task?.running) return 'info';
  if (task?.lastStatus === 'completed') return 'success';
  if (['failed', 'timed_out', 'invalid'].includes(task?.lastStatus)) return 'error';
  if (['skipped', 'missed', 'cancelled'].includes(task?.lastStatus)) return 'warning';
  return 'default';
}

function scheduleLabel(schedule = {}) {
  if (schedule.type === 'once') return t('scheduled.once', { at: schedule.at || '-' });
  if (schedule.type === 'interval') return t('scheduled.interval', { interval: schedule.every || '-' });
  if (schedule.type === 'cron') return t('scheduled.cron', { cron: schedule.cron || '-', timezone: schedule.timezone || t('common.localTimezone') });
  return '-';
}

function formatTime(value) {
  const timestamp = Number(value || 0);
  if (!timestamp) return '-';
  return formatDateTime(timestamp);
}

function durationLabel(seconds) {
  const value = Number(seconds || 0);
  if (value >= 3600 && value % 3600 === 0) return `${value / 3600}h`;
  if (value >= 60 && value % 60 === 0) return `${value / 60}m`;
  return `${value}s`;
}
</script>

<style scoped>
.scheduled-overview {
  display: flex;
  flex-wrap: wrap;
  gap: 8px 16px;
  margin-bottom: 16px;
  color: #8f8f8f;
  font-size: 12px;
}

.scheduled-list {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.scheduled-card {
  padding: 14px;
  border: 1px solid rgba(255, 255, 255, 0.08);
  border-radius: 10px;
  background: rgba(255, 255, 255, 0.025);
}

.scheduled-card-header,
.scheduled-actions {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 12px;
}

.scheduled-title-wrap {
  min-width: 0;
}

.scheduled-title {
  color: #f1f1f1;
  font-size: 14px;
  font-weight: 650;
}

.scheduled-id,
.scheduled-workspace {
  overflow: hidden;
  color: #707070;
  font-family: ui-monospace, SFMono-Regular, Consolas, monospace;
  font-size: 11px;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.scheduled-tags {
  display: flex;
  gap: 6px;
  flex-shrink: 0;
}

.scheduled-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 8px 16px;
  margin: 14px 0 10px;
}

.scheduled-grid div {
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.scheduled-grid span {
  color: #777;
  font-size: 11px;
}

.scheduled-grid strong {
  overflow: hidden;
  color: #c9c9c9;
  font-size: 12px;
  font-weight: 500;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.scheduled-instruction {
  display: -webkit-box;
  margin-top: 10px;
  overflow: hidden;
  color: #bdbdbd;
  font-size: 12px;
  line-height: 1.55;
  -webkit-box-orient: vertical;
  -webkit-line-clamp: 4;
}

.scheduled-output {
  margin-top: 12px;
  padding: 10px;
  border-radius: 7px;
  background: rgba(255, 255, 255, 0.035);
}

.scheduled-output.error {
  background: rgba(239, 68, 68, 0.08);
}

.scheduled-output-title {
  margin-bottom: 6px;
  color: #8f8f8f;
  font-size: 11px;
}

.scheduled-output pre {
  max-height: 220px;
  margin: 0;
  overflow: auto;
  color: #d4d4d4;
  font-family: ui-monospace, SFMono-Regular, Consolas, monospace;
  font-size: 11px;
  line-height: 1.5;
  white-space: pre-wrap;
  word-break: break-word;
}

.scheduled-actions {
  justify-content: flex-end;
  margin-top: 12px;
}

.scheduled-empty-hint {
  color: #777;
  font-size: 12px;
}
</style>
