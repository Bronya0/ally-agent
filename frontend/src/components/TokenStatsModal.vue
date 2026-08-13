<template>
  <n-modal
    :show="show"
    preset="card"
    :title="$t('stats.title')"
    :style="{ width: 'min(1020px, calc(100vw - 48px))' }"
    :mask-closable="false"
    @update:show="(value) => !value && $emit('close')"
  >
    <template #header-extra>
      <n-button size="small" quaternary :loading="loading" @click="load">{{ $t('common.refresh') }}</n-button>
    </template>

    <div class="stats-body">
      <n-spin :show="loading">
        <template v-if="stats && stats.ok && !isEmpty">
          <!-- 1) 统计表：今日 / 近7日 / 本月 一排 -->
          <div class="stats-overview">
            <div v-for="(range, key) in summaryRanges" :key="key" class="stat-card">
              <div class="stat-label">{{ range.label }}</div>
              <div class="stat-value">{{ fmtTokens(range.summary.totalTokens) }}</div>
              <div class="stat-sub">
                {{ $t('stats.input') }} {{ fmtTokens(range.summary.inputTokens) }}
                · {{ $t('stats.output') }} {{ fmtTokens(range.summary.outputTokens) }}
              </div>
              <div class="stat-sub">
                {{ $t('stats.requests') }} {{ fmtNum(range.summary.requests) }}
                · {{ $t('stats.avgPerRequest') }} {{ fmtTokens(range.summary.avgPerRequest) }}
              </div>
              <div class="stat-sub">
                {{ $t('stats.cacheHitRate') }} {{ (range.summary.cacheHitRate * 100).toFixed(1) }}%
              </div>
            </div>
          </div>

          <!-- 2) 按天 Token 用量柱状图（固定近30天） + 3) 月度热力图 并排一行 -->
          <div class="stats-grid">
          <div class="stats-section chart-card">
            <div class="chart-title">{{ $t('stats.dailyBars') }}</div>
            <div class="chart-legend">
              <span class="legend-item"><i class="legend-dot input"></i>{{ $t('stats.input') }}</span>
              <span class="legend-item"><i class="legend-dot output"></i>{{ $t('stats.output') }}</span>
            </div>
            <div class="chart-host">
              <svg class="stats-chart" :viewBox="`0 0 ${chartW} 200`" role="img" :aria-label="$t('stats.dailyBars')">
                <g v-for="g in barGrid" :key="g.y">
                  <line :x1="padL" :x2="720 - padR" :y1="g.y" :y2="g.y" class="chart-grid" />
                  <text :x="padL + 4" :y="g.y - 5" class="chart-grid-label">{{ fmtTokens(g.value) }}</text>
                </g>
                <g v-for="(bar, i) in dailyBars" :key="i">
                  <rect :x="bar.xIn" :y="bar.yIn" :width="bar.w" :height="bar.hIn" rx="1" class="bar-input" />
                  <rect :x="bar.xOut" :y="bar.yOut" :width="bar.w" :height="bar.hOut" rx="1" class="bar-output" />
                  <rect
                    :x="bar.xHit"
                    :y="12"
                    :width="bar.hitW"
                    :height="200 - 12 - 24"
                    fill="transparent"
                    @mouseenter="showBarTooltip($event, bar)"
                    @mousemove="moveChartTooltip($event)"
                    @mouseleave="hideChartTooltip()"
                  />
                </g>
                <g v-for="t in barTicks" :key="t.i">
                  <text :x="t.x" :y="196" text-anchor="middle" class="chart-axis-label">{{ t.label }}</text>
                </g>
              </svg>
              <div v-if="chartTooltip.visible" class="chart-tooltip" :style="{ left: chartTooltip.x + 'px', top: chartTooltip.y + 'px' }">
                <div class="chart-tooltip-title">{{ chartTooltip.title }}</div>
                <div v-for="(row, ri) in chartTooltip.rows" :key="ri" class="chart-tooltip-row">
                  <i class="chart-tooltip-dot" :style="{ background: row.color }"></i>
                  <span class="chart-tooltip-label">{{ row.label }}</span>
                  <span class="chart-tooltip-value">{{ row.value }}</span>
                </div>
              </div>
            </div>
          </div>
          <div class="stats-section chart-card">
            <div class="chart-title">{{ $t('stats.monthHeatmap') }}</div>
            <div class="heatmap-host">
              <div class="heatmap-weekdays">
                <span v-for="w in 7" :key="w" class="heatmap-weekday">
                  {{ weekdayLabels[(w - 1) % 7] }}
                </span>
              </div>
              <div class="heatmap-cells">
                <div
                  v-for="(cell, i) in heatmapCells"
                  :key="i"
                  class="heatmap-cell"
                  :class="{ dim: !cell.inMonth || cell.isFuture }"
                  :style="{ background: cell.color }"
                  :title="`${cell.date} · ${fmtTokens(cell.total)}`"
                >
                  <span class="heatmap-day">{{ cell.day }}</span>
                </div>
              </div>
            </div>
          </div>
          </div>

          <!-- 4) 模型 周/月 饼图（不考虑 provider） -->
          <div class="stats-grid">
            <div class="stats-section pie-section">
              <div class="chart-title">{{ $t('stats.modelWeek') }}</div>
              <TokenPieChart :items="stats.modelWeek" />
            </div>
            <div class="stats-section pie-section">
              <div class="chart-title">{{ $t('stats.modelMonth') }}</div>
              <TokenPieChart :items="stats.modelMonth" />
            </div>
          </div>

          <!-- 5) 工作区 周/月 饼图（只显示 path 最后一段） -->
          <div class="stats-grid">
            <div class="stats-section pie-section">
              <div class="chart-title">{{ $t('stats.workspaceWeek') }}</div>
              <TokenPieChart :items="stats.workspaceWeek" />
            </div>
            <div class="stats-section pie-section">
              <div class="chart-title">{{ $t('stats.workspaceMonth') }}</div>
              <TokenPieChart :items="stats.workspaceMonth" />
            </div>
          </div>
        </template>

        <n-empty v-else-if="stats && stats.ok && isEmpty" :description="$t('stats.empty')" class="stats-empty">
          <template #extra>
            <span class="stats-empty-hint">{{ $t('stats.emptyHint') }}</span>
          </template>
        </n-empty>
        <n-empty v-else-if="stats && stats.error" :description="stats.error" class="stats-empty" />
      </n-spin>
    </div>
  </n-modal>
</template>

<script setup>
import { computed, ref, watch } from 'vue';
import { GetTokenStats } from '../../bindings/ally-dev/internal/app/app';
import { t } from '../i18n.mjs';
import { fmtNum, fmtTokens } from '../utils/format.mjs';
import TokenPieChart from './TokenPieChart.vue';

const props = defineProps({
  show: { type: Boolean, default: false },
});
defineEmits(['close']);

const stats = ref(null);
const loading = ref(false);

let loadGeneration = 0;

async function load() {
  const generation = ++loadGeneration;
  loading.value = true;
  try {
    const result = await GetTokenStats();
    if (generation !== loadGeneration) return;
    stats.value = result;
  } catch (err) {
    if (generation !== loadGeneration) return;
    stats.value = { ok: false, error: String(err?.message || err || t('stats.loadFailed')) };
  } finally {
    if (generation === loadGeneration) loading.value = false;
  }
}

watch(
  () => props.show,
  (visible) => {
    if (visible) {
      load();
    } else {
      loadGeneration += 1;
      loading.value = false;
      stats.value = null;
    }
  },
  { immediate: true }
);

// ── 统计表：今日 / 近7日 / 本月 ──
const isEmpty = computed(() => {
  const s = stats.value;
  if (!s) return true;
  return !(s.summaryToday?.totalTokens || s.summary7Days?.totalTokens || s.summaryMonth?.totalTokens);
});
const summaryRanges = computed(() => {
  const s = stats.value;
  if (!s) return [];
  return [
    { key: 'today', label: t('stats.today'), summary: s.summaryToday || {} },
    { key: 'week', label: t('stats.last7Days'), summary: s.summary7Days || {} },
    { key: 'month', label: t('stats.thisMonth'), summary: s.summaryMonth || {} },
  ];
});
// ── 月度热力图（固定当月，周一起始，最多 6 行 x 7 列）──
const weekdayLabels = [
  t('stats.mon'), t('stats.tue'), t('stats.wed'), t('stats.thu'),
  t('stats.fri'), t('stats.sat'), t('stats.sun'),
];

const HEAT_COLORS = [
  'rgba(255,255,255,0.04)',
  'rgba(74,222,128,0.18)',
  'rgba(74,222,128,0.38)',
  'rgba(74,222,128,0.62)',
  'rgba(74,222,128,0.9)',
];

const heatmapCells = computed(() => {
  const days = stats.value?.daily || [];
  const byDate = new Map(days.map((d) => [d.date, d]));
  const now = new Date();
  const year = now.getFullYear();
  const month = now.getMonth();
  const todayNum = now.getDate();
  const first = new Date(year, month, 1);
  // getDay(): 0=周日..6=周六，转成周一起始的列偏移
  const startCol = (first.getDay() + 6) % 7;
  const daysInMonth = new Date(year, month + 1, 0).getDate();
  const totals = [];
  for (let dayNum = 1; dayNum <= daysInMonth; dayNum++) {
    const dateStr = `${year}-${String(month + 1).padStart(2, '0')}-${String(dayNum).padStart(2, '0')}`;
    const record = byDate.get(dateStr);
    totals.push(record ? (record.inputTokens || 0) + (record.outputTokens || 0) : 0);
  }
  const max = Math.max(1, ...totals);
  const cells = [];
  for (let i = 0; i < 42; i++) {
    const dayNum = i - startCol + 1;
    const inMonth = dayNum >= 1 && dayNum <= daysInMonth;
    const isFuture = inMonth && dayNum > todayNum;
    const dateStr = inMonth
      ? `${year}-${String(month + 1).padStart(2, '0')}-${String(dayNum).padStart(2, '0')}`
      : '';
    const total = inMonth && !isFuture ? totals[dayNum - 1] : 0;
    let color = HEAT_COLORS[0];
    if (inMonth && !isFuture && total > 0) {
      const ratio = total / max;
      const level = ratio >= 0.75 ? 4 : ratio >= 0.5 ? 3 : ratio >= 0.25 ? 2 : 1;
      color = HEAT_COLORS[level];
    }
    cells.push({ day: inMonth ? dayNum : '', date: dateStr, total, inMonth, isFuture, color });
  }
  return cells;
});

// ── 按天柱状图（固定近30天，输入/输出堆叠）──
const chartW = 460;
const padL = 46;
const padR = 10;
const padT = 12;
const padB = 24;

const dailyBars = computed(() => {
  const days = stats.value?.daily || [];
  const n = days.length;
  const innerW = chartW - padL - padR;
  const innerH = 200 - padT - padB;
  const max = Math.max(1, ...days.map((d) => (d.inputTokens || 0) + (d.outputTokens || 0)));
  const slot = innerW / Math.max(1, n);
  const barW = Math.max(1, slot * 0.6);
  return days.map((d, i) => {
    const input = d.inputTokens || 0;
    const output = d.outputTokens || 0;
    const x = padL + i * slot + (slot - barW) / 2;
    const hIn = (input / max) * innerH;
    const hOut = (output / max) * innerH;
    return {
      xIn: x,
      yIn: padT + innerH - hIn - hOut,
      hIn: Math.max(hIn > 0 ? 1 : 0, hIn),
      xOut: x,
      yOut: padT + innerH - hOut,
      hOut: Math.max(hOut > 0 ? 1 : 0, hOut),
      w: barW,
      xHit: padL + i * slot,
      hitW: slot,
      date: d.date || '',
      input,
      output,
      total: input + output,
    };
  });
});

const barGrid = computed(() => {
  const days = stats.value?.daily || [];
  const max = Math.max(1, ...days.map((d) => (d.inputTokens || 0) + (d.outputTokens || 0)));
  const innerH = 200 - padT - padB;
  return [0.25, 0.5, 0.75, 1].map((ratio) => ({
    y: padT + innerH - ratio * innerH,
    value: Math.round(max * ratio),
  }));
});

const barTicks = computed(() => {
  const days = stats.value?.daily || [];
  const n = days.length;
  const innerW = chartW - padL - padR;
  const slot = innerW / Math.max(1, n);
  const step = Math.max(1, Math.floor(n / 6));
  return days
    .map((d, i) => ({ i, x: padL + i * slot + slot / 2, label: d.date ? d.date.slice(5) : '' }))
    .filter((tick, i) => i % step === 0 || i === n - 1);
});

// ── shared chart tooltip ──
const chartTooltip = ref({ visible: false, x: 0, y: 0, title: '', rows: [] });

function showChartTooltip(event, title, rows) {
  const host = event.currentTarget.closest('.chart-host');
  if (!host) return;
  const rect = host.getBoundingClientRect();
  chartTooltip.value = {
    visible: true,
    x: event.clientX - rect.left,
    y: event.clientY - rect.top,
    title,
    rows,
  };
}

function moveChartTooltip(event) {
  if (!chartTooltip.value.visible) return;
  const host = event.currentTarget.closest('.chart-host');
  if (!host) return;
  const rect = host.getBoundingClientRect();
  chartTooltip.value.x = event.clientX - rect.left;
  chartTooltip.value.y = event.clientY - rect.top;
}

function hideChartTooltip() {
  chartTooltip.value = { ...chartTooltip.value, visible: false };
}

function showBarTooltip(event, bar) {
  showChartTooltip(event, bar.date, [
    { label: t('stats.input'), value: fmtTokens(bar.input), color: 'var(--ally-accent)' },
    { label: t('stats.output'), value: fmtTokens(bar.output), color: '#6db3f2' },
  ]);
}
</script>

<style scoped>
.stats-body {
  min-height: 260px;
  max-height: min(76vh, 760px);
  overflow-y: auto;
  padding-right: 2px;
}
.stats-overview {
  display: grid;
  grid-template-columns: repeat(3, 1fr);
  gap: 8px;
  margin-bottom: 16px;
}
.stat-card {
  padding: 10px 12px;
  border: 1px solid rgba(255, 255, 255, 0.08);
  border-radius: 10px;
  background: rgba(255, 255, 255, 0.03);
  min-width: 0;
}
.stat-label {
  color: #b8b8b8;
  font-size: 11px;
}
.stat-value {
  margin-top: 4px;
  color: #f1f1f1;
  font-size: 20px;
  font-weight: 650;
  letter-spacing: 0.2px;
}
.stat-sub {
  margin-top: 4px;
  color: #a3a3a3;
  font-size: 11px;
  line-height: 1.5;
}
.stats-section {
  padding: 6px 2px;
  margin-bottom: 6px;
}
.chart-title {
  margin: 2px 0 10px;
  color: #9a9a9a;
  font-size: 12px;
}
.chart-legend {
  display: flex;
  gap: 16px;
  margin-bottom: 8px;
  color: #9a9a9a;
  font-size: 12px;
}
.legend-item {
  display: inline-flex;
  align-items: center;
  gap: 6px;
}
.legend-dot {
  width: 10px;
  height: 10px;
  border-radius: 3px;
}
.legend-dot.input {
  background: var(--ally-accent);
}
.legend-dot.output {
  background: #6db3f2;
}
.stats-chart {
  width: 100%;
  height: auto;
  overflow: visible;
}
.chart-host {
  position: relative;
}
.chart-tooltip {
  position: absolute;
  pointer-events: none;
  z-index: 10;
  padding: 8px 10px;
  background: rgba(30, 30, 30, 0.95);
  border: 1px solid rgba(255, 255, 255, 0.12);
  border-radius: 8px;
  font-size: 12px;
  white-space: nowrap;
  transform: translate(-50%, calc(-100% - 10px));
  box-shadow: 0 4px 16px rgba(0, 0, 0, 0.4);
}
.chart-tooltip-title {
  color: #f1f1f1;
  font-weight: 600;
  margin-bottom: 4px;
}
.chart-tooltip-row {
  display: flex;
  align-items: center;
  gap: 6px;
  color: #c0c0c0;
  line-height: 1.6;
}
.chart-tooltip-dot {
  width: 8px;
  height: 8px;
  border-radius: 2px;
  flex-shrink: 0;
}
.chart-tooltip-label {
  flex: 1;
}
.chart-tooltip-value {
  color: #e8e8e8;
  font-family: ui-monospace, SFMono-Regular, Consolas, monospace;
}
.chart-grid {
  stroke: rgba(255, 255, 255, 0.07);
  stroke-width: 1;
}
.chart-grid-label {
  fill: #6f6f6f;
  font-size: 10px;
}
.chart-axis-label {
  fill: #6f6f6f;
  font-size: 10px;
}
.bar-input {
  fill: var(--ally-accent);
}
.bar-output {
  fill: #6db3f2;
}

/* ── 月度热力图 ── */
.heatmap-host {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 6px;
}
.heatmap-weekdays {
  display: grid;
  grid-template-columns: repeat(7, 38px);
  gap: 4px;
}
.heatmap-weekday {
  display: flex;
  align-items: center;
  justify-content: center;
  color: #6f6f6f;
  font-size: 10px;
  height: 20px;
}
.heatmap-cells {
  display: grid;
  grid-template-columns: repeat(7, 38px);
  grid-auto-flow: row;
  grid-template-rows: repeat(6, 38px);
  gap: 4px;
}
.heatmap-cell {
  display: flex;
  align-items: center;
  justify-content: center;
  border-radius: 5px;
  font-size: 11px;
  color: #a8a8a8;
  cursor: default;
}
.heatmap-cell.dim {
  opacity: 0;
}
.heatmap-day {
  pointer-events: none;
}

/* ── 饼图网格 ── */
.stats-grid {
  display: grid;
  grid-template-columns: repeat(2, 1fr);
  gap: 12px;
  margin-bottom: 6px;
}
.pie-section,
.chart-card {
  border: 1px solid rgba(255, 255, 255, 0.07);
  border-radius: 10px;
  padding: 12px;
  background: rgba(255, 255, 255, 0.02);
  min-width: 0;
}

.stats-empty {
  padding: 60px 0;
}
.stats-empty-hint {
  color: #777;
  font-size: 12px;
}
</style>
