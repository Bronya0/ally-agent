<template>
  <n-drawer
    :show="show"
    :width="560"
    placement="right"
    :native-scrollbar="true"
    @update:show="(value) => !value && $emit('close')"
  >
    <n-drawer-content title="定时任务" closable>
      <template #header-extra>
        <n-button size="small" quaternary :loading="loading" @click="$emit('refresh')">刷新</n-button>
      </template>

      <div class="scheduled-overview">
        <span>共 {{ tasks.length }} 个任务</span>
        <span>运行中 {{ runningCount }}</span>
        <span>任务仅在 Ally 打开时执行</span>
      </div>

      <n-spin :show="loading">
        <n-empty v-if="!tasks.length" description="暂无定时任务">
          <template #extra>
            <span class="scheduled-empty-hint">可以让模型通过 scheduled_task 工具创建。</span>
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
              <div><span>调度</span><strong>{{ scheduleLabel(task.schedule) }}</strong></div>
              <div><span>下次运行</span><strong>{{ formatTime(task.nextRunAt) }}</strong></div>
              <div><span>上次运行</span><strong>{{ formatTime(task.lastRunAt) }}</strong></div>
              <div><span>运行次数</span><strong>{{ task.runCount || 0 }}</strong></div>
              <div><span>单次限制</span><strong>{{ task.maxSteps }} steps · {{ durationLabel(task.timeoutSeconds) }}</strong></div>
              <div><span>连续失败</span><strong>{{ task.consecutiveFailures || 0 }}</strong></div>
            </div>

            <div class="scheduled-workspace" :title="task.workspace">{{ task.workspace }}</div>
            <div class="scheduled-instruction">{{ task.instruction }}</div>

            <div v-if="task.lastSummary" class="scheduled-output">
              <div class="scheduled-output-title">最近输出</div>
              <pre>{{ task.lastSummary }}</pre>
            </div>
            <div v-if="task.lastError" class="scheduled-output error">
              <div class="scheduled-output-title">最近错误</div>
              <pre>{{ task.lastError }}</pre>
            </div>

            <div class="scheduled-actions">
              <n-popconfirm
                positive-text="删除"
                negative-text="取消"
                @positive-click="$emit('delete', task.id)"
              >
                <template #trigger>
                  <n-button size="small" type="error" ghost :loading="deletingIds.includes(task.id)">删除任务</n-button>
                </template>
                删除后会取消正在运行的任务，且无法恢复。确定继续？
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

const props = defineProps({
  show: { type: Boolean, default: false },
  tasks: { type: Array, default: () => [] },
  loading: { type: Boolean, default: false },
  deletingIds: { type: Array, default: () => [] },
});

defineEmits(['close', 'refresh', 'delete']);

const runningCount = computed(() => props.tasks.filter((task) => task?.running).length);

function statusLabel(task) {
  if (task?.running) return '运行中';
  const labels = {
    scheduled: '等待中', completed: '已完成', failed: '失败', timed_out: '超时',
    cancelled: '已取消', skipped: '已跳过', missed: '已错过', invalid: '无效',
  };
  return labels[task?.lastStatus] || task?.lastStatus || '等待中';
}

function statusType(task) {
  if (task?.running) return 'info';
  if (task?.lastStatus === 'completed') return 'success';
  if (['failed', 'timed_out', 'invalid'].includes(task?.lastStatus)) return 'error';
  if (['skipped', 'missed', 'cancelled'].includes(task?.lastStatus)) return 'warning';
  return 'default';
}

function scheduleLabel(schedule = {}) {
  if (schedule.type === 'once') return `单次 · ${schedule.at || '-'}`;
  if (schedule.type === 'interval') return `每 ${schedule.every || '-'}`;
  if (schedule.type === 'cron') return `Cron ${schedule.cron || '-'} · ${schedule.timezone || '本地时区'}`;
  return '-';
}

function formatTime(value) {
  const timestamp = Number(value || 0);
  if (!timestamp) return '-';
  return new Date(timestamp).toLocaleString();
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
