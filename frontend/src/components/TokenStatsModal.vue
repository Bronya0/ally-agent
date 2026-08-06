<template>
  <n-modal
    :show="show"
    preset="card"
    :title="$t('stats.title')"
    :style="{ width: 'min(980px, calc(100vw - 48px))' }"
    :mask-closable="false"
    @update:show="(value) => !value && $emit('close')"
  >
    <template #header-extra>
      <n-select
        v-model:value="rangeDays"
        size="small"
        class="stats-range-select"
        :options="rangeOptions"
        @update:value="load"
      />
      <n-button size="small" quaternary :loading="loading" @click="load">{{ $t('common.refresh') }}</n-button>
    </template>

    <div class="stats-body">
      <n-spin :show="loading">
        <template v-if="stats && stats.ok && stats.totalRequests > 0">
          <div class="stats-overview">
            <div class="stat-card">
              <div class="stat-label">{{ $t('stats.totalTokens') }}</div>
              <div class="stat-value">{{ fmtTokens(totalTokens) }}</div>
              <div class="stat-sub">
                {{ $t('stats.input') }} {{ fmtTokens(stats.totalInputTokens) }}
                · {{ $t('stats.output') }} {{ fmtTokens(stats.totalOutputTokens) }}
              </div>
            </div>
            <div class="stat-card">
              <div class="stat-label">{{ $t('stats.requests') }}</div>
              <div class="stat-value">{{ fmtNum(stats.totalRequests) }}</div>
              <div class="stat-sub">{{ $t('stats.range', { days: stats.rangeDays }) }}</div>
            </div>
            <div class="stat-card">
              <div class="stat-label">{{ $t('stats.cacheHitRate') }}</div>
              <div class="stat-value">{{ (stats.cacheHitRate * 100).toFixed(1) }}%</div>
              <div class="stat-sub">
                {{ $t('stats.cacheHit') }} {{ fmtTokens(stats.totalCacheHitTokens) }}
                · {{ $t('stats.cacheMiss') }} {{ fmtTokens(stats.totalCacheMissTokens) }}
              </div>
            </div>
            <div class="stat-card compact-stat">
              <div class="stat-label">{{ $t('stats.avgPerRequest') }}</div>
              <div class="stat-value">{{ fmtTokens(avgPerRequest) }}</div>
              <div class="stat-sub">{{ $t('stats.modelsUsed', { count: stats.byModel.length }) }}</div>
            </div>
            <div class="stat-card compact-stat">
              <div class="stat-label">{{ $t('stats.activeDays') }}</div>
              <div class="stat-value">{{ fmtNum(stats.activeDays) }}</div>
              <div class="stat-sub">{{ $t('stats.range', { days: stats.rangeDays }) }}</div>
            </div>
            <div class="stat-card compact-stat">
              <div class="stat-label">{{ $t('stats.sessions') }}</div>
              <div class="stat-value">{{ fmtNum(stats.uniqueSessions) }}</div>
              <div class="stat-sub">{{ $t('stats.modelsUsed', { count: stats.byModel.length }) }}</div>
            </div>
          </div>

          <n-tabs type="line" animated pane-wrapper-style="padding-top: 6px;">
            <n-tab-pane name="model" :tab="$t('stats.tabModel')">
              <div class="stats-section">
                <TokenCategoryTrendChart :series="stats.byModelDay" :days="stats.byDay" kind="model" :ranks="stats.byModel" />
              </div>
            </n-tab-pane>

            <n-tab-pane name="provider" :tab="$t('stats.tabProvider')">
              <div class="stats-section">
                <TokenCategoryTrendChart :series="stats.byProviderDay" :days="stats.byDay" kind="provider" :ranks="stats.byProvider" />
              </div>
            </n-tab-pane>

            <n-tab-pane name="source" :tab="$t('stats.tabSource')">
              <div class="stats-section">
                <TokenCategoryTrendChart :series="stats.bySourceDay" :days="stats.byDay" kind="source" :ranks="stats.bySource" />
              </div>
            </n-tab-pane>

            <n-tab-pane name="day" :tab="$t('stats.tabDay')">
              <div class="stats-section">
                <div class="chart-legend">
                  <span class="legend-item"><i class="legend-dot input"></i>{{ $t('stats.input') }}</span>
                  <span class="legend-item"><i class="legend-dot output"></i>{{ $t('stats.output') }}</span>
                </div>
                <div class="chart-host">
                  <svg class="stats-chart" viewBox="0 0 720 240" role="img" :aria-label="$t('stats.dailyTrend')">
                    <defs>
                      <linearGradient id="stats-area-fill" x1="0" y1="0" x2="0" y2="1">
                        <stop offset="0%" stop-color="var(--ally-accent)" stop-opacity="0.32" />
                        <stop offset="100%" stop-color="var(--ally-accent)" stop-opacity="0.02" />
                      </linearGradient>
                    </defs>
                    <g v-for="g in gridLines" :key="g.y">
                      <line :x1="padL" :x2="chartW - padR" :y1="g.y" :y2="g.y" class="chart-grid" />
                      <text :x="padL + 4" :y="g.y - 5" class="chart-grid-label">{{ fmtTokens(g.value) }}</text>
                    </g>
                    <polygon :points="areaPoints" fill="url(#stats-area-fill)" />
                    <polyline :points="inputPoints" fill="none" class="chart-line input" />
                    <polyline :points="outputPoints" fill="none" class="chart-line output" />
                    <g v-for="(pt, i) in dayLabelPoints" :key="i">
                      <text :x="pt.x" :y="chartH - 6" text-anchor="middle" class="chart-axis-label">{{ pt.label }}</text>
                    </g>
                    <rect
                      v-for="(r, i) in dayHoverRects"
                      :key="'h'+i"
                      :x="r.x" :y="padT" :width="r.width" :height="chartH - padT - padB"
                      fill="transparent"
                      @mouseenter="showDayTooltip($event, r)"
                      @mousemove="moveChartTooltip($event)"
                      @mouseleave="hideChartTooltip()"
                    />
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
            </n-tab-pane>

            <n-tab-pane name="hour" :tab="$t('stats.tabHour')">
              <div class="stats-section">
                <div class="chart-legend">
                  <span class="legend-item"><i class="legend-dot input"></i>{{ $t('stats.input') }} + {{ $t('stats.output') }}</span>
                </div>
                <div class="chart-host">
                  <svg class="stats-chart" viewBox="0 0 720 200" role="img" :aria-label="$t('stats.hourlyDistribution')">
                    <g v-for="(h, i) in hourBars" :key="i">
                      <rect
                        :x="h.x"
                        :y="h.y"
                        :width="h.w"
                        :height="h.h"
                        rx="2"
                        :class="['hour-bar', { hot: h.hot }]"
                        @mouseenter="showHourTooltip($event, h)"
                        @mousemove="moveChartTooltip($event)"
                        @mouseleave="hideChartTooltip()"
                      />
                    </g>
                    <g v-for="t in hourTicks" :key="t.hour">
                      <text :x="t.x" :y="190" text-anchor="middle" class="chart-axis-label">{{ String(t.hour).padStart(2, '0') }}</text>
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
            </n-tab-pane>

            <n-tab-pane name="cache" :tab="$t('stats.tabCache')">
              <div class="stats-section">
                <div class="chart-title">{{ $t('stats.cacheDailyRate') }}</div>
                <div class="chart-legend">
                  <span class="legend-item"><i class="legend-dot input"></i>{{ $t('stats.cacheHitRate') }}</span>
                </div>
                <div class="chart-host">
                  <svg class="stats-chart" viewBox="0 0 720 200" role="img" :aria-label="$t('stats.cacheDailyRate')">
                    <g v-for="g in cacheRateChart.grid" :key="g.y">
                      <line :x1="padL" :x2="chartW - padR" :y1="g.y" :y2="g.y" class="chart-grid" />
                      <text :x="padL + 4" :y="g.y - 5" class="chart-grid-label">{{ g.value.toFixed(0) }}%</text>
                    </g>
                    <polyline v-if="cacheRateChart.pts" :points="cacheRateChart.pts" fill="none" class="chart-line input" />
                    <g v-for="(pt, i) in cacheRateChart.labelPoints" :key="i">
                      <text :x="pt.x" :y="200 - 6" text-anchor="middle" class="chart-axis-label">{{ pt.label }}</text>
                    </g>
                    <rect
                      v-for="(r, i) in cacheRateChart.hoverRects"
                      :key="'ch'+i"
                      :x="r.x" :y="12" :width="r.width" :height="200 - 12 - 28"
                      fill="transparent"
                      @mouseenter="showCacheTooltip($event, r)"
                      @mousemove="moveChartTooltip($event)"
                      @mouseleave="hideChartTooltip()"
                    />
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
              <div class="stats-section cache-section">
                <div class="donut-wrap">
                  <svg viewBox="0 0 180 180" width="180" height="180" role="img" :aria-label="$t('stats.cacheHitRate')">
                    <circle cx="90" cy="90" r="70" class="donut-track" />
                    <circle
                      cx="90" cy="90" r="70"
                      class="donut-fill"
                      :stroke-dasharray="`${donutCircumference * stats.cacheHitRate} ${donutCircumference}`"
                      transform="rotate(-90 90 90)"
                    />
                    <text x="90" y="86" text-anchor="middle" class="donut-value">{{ (stats.cacheHitRate * 100).toFixed(1) }}%</text>
                    <text x="90" y="106" text-anchor="middle" class="donut-label">{{ $t('stats.cacheHitRate') }}</text>
                  </svg>
                </div>
                <div class="cache-detail">
                  <div class="cache-row">
                    <span class="cache-name">{{ $t('stats.cacheHit') }}</span>
                    <span class="cache-value">{{ fmtTokens(stats.totalCacheHitTokens) }}</span>
                  </div>
                  <div class="cache-row">
                    <span class="cache-name">{{ $t('stats.cacheMiss') }}</span>
                    <span class="cache-value">{{ fmtTokens(stats.totalCacheMissTokens) }}</span>
                  </div>
                  <div class="cache-tip">{{ $t('stats.cacheTip') }}</div>
                </div>
              </div>
            </n-tab-pane>

            <n-tab-pane name="workspace" :tab="$t('stats.tabWorkspace')">
              <div class="stats-section">
                <TokenCategoryTrendChart :series="stats.byWorkspaceDay" :days="stats.byDay" kind="workspace" :ranks="stats.byWorkspace" />
              </div>
            </n-tab-pane>
          </n-tabs>
        </template>

        <n-empty v-else-if="stats && stats.ok" :description="$t('stats.empty')" class="stats-empty">
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
import TokenCategoryTrendChart from './TokenCategoryTrendChart.vue';

const props = defineProps({
  show: { type: Boolean, default: false },
});
defineEmits(['close']);

const stats = ref(null);
const loading = ref(false);
const rangeDays = ref(1);
const rangeOptions = [
  { label: '1 ' + t('stats.days'), value: 1 },
  { label: '7 ' + t('stats.days'), value: 7 },
  { label: '30 ' + t('stats.days'), value: 30 },
  { label: '90 ' + t('stats.days'), value: 90 },
];

let loadGeneration = 0;

async function load() {
  const generation = ++loadGeneration;
  const requestedRange = rangeDays.value;
  loading.value = true;
  try {
    const result = await GetTokenStats(requestedRange);
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

function fmtTokens(n) {
  const value = Number(n || 0);
  if (value >= 1e9) return (value / 1e9).toFixed(2) + 'B';
  if (value >= 1e6) return (value / 1e6).toFixed(2) + 'M';
  if (value >= 1e3) return (value / 1e3).toFixed(1) + 'K';
  return String(value);
}
function fmtNum(n) {
  return Number(n || 0).toLocaleString();
}

const totalTokens = computed(() => (stats.value?.totalInputTokens || 0) + (stats.value?.totalOutputTokens || 0));
const avgPerRequest = computed(() => {
  const req = stats.value?.totalRequests || 0;
  return req > 0 ? Math.round(totalTokens.value / req) : 0;
});

// ── shared chart tooltip ──
// One tooltip instance is shared across all SVG charts in this modal. Each
// chart renders transparent <rect> hover zones; on mouseenter the caller
// invokes showChartTooltip with the host container, mouse event, title and
// rows. The tooltip div is absolutely positioned within .chart-host.
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

// ── day chart geometry ──
const chartW = 720;
const chartH = 240;
const padL = 46;
const padR = 10;
const padT = 12;
const padB = 28;

const dayChart = computed(() => {
  const days = stats.value?.byDay || [];
  const max = Math.max(1, ...days.map((d) => (d.inputTokens || 0) + (d.outputTokens || 0)));
  const innerW = chartW - padL - padR;
  const innerH = chartH - padT - padB;
  const n = days.length;
  const x = (i) => (n <= 1 ? padL + innerW / 2 : padL + (i * innerW) / (n - 1));
  const y = (v) => padT + innerH - (v / max) * innerH;
  const inputPts = days.map((d, i) => `${x(i).toFixed(1)},${y(d.inputTokens || 0).toFixed(1)}`).join(' ');
  const outputPts = days.map((d, i) => `${x(i).toFixed(1)},${y(d.outputTokens || 0).toFixed(1)}`).join(' ');
  const areaPts = `${padL},${padT + innerH} ${inputPts} ${x(n - 1).toFixed(1)},${padT + innerH}`;
  const grid = [0.25, 0.5, 0.75, 1].map((ratio) => ({
    y: padT + innerH - ratio * innerH,
    value: Math.round(max * ratio),
  }));
  const step = Math.max(1, Math.floor(n / 6));
  const labelPoints = days
    .map((d, i) => ({ x: x(i), label: d.date ? d.date.slice(5) : '' }))
    .filter((_, i) => i % step === 0 || i === n - 1);
  // Hover zones: vertical bands centered on each data point.
  const bandW = n > 1 ? innerW / (n - 1) : innerW;
  const hoverRects = days.map((d, i) => ({
    x: Math.max(padL, x(i) - bandW / 2),
    width: bandW,
    date: d.date || '',
    input: d.inputTokens || 0,
    output: d.outputTokens || 0,
  }));
  return { inputPts, outputPts, areaPts, grid, labelPoints, hoverRects };
});

const gridLines = computed(() => dayChart.value.grid);
const inputPoints = computed(() => dayChart.value.inputPts);
const outputPoints = computed(() => dayChart.value.outputPts);
const areaPoints = computed(() => dayChart.value.areaPts);
const dayLabelPoints = computed(() => dayChart.value.labelPoints);
const dayHoverRects = computed(() => dayChart.value.hoverRects);

function showDayTooltip(event, r) {
  showChartTooltip(event, r.date, [
    { label: t('stats.input'), value: fmtTokens(r.input), color: 'var(--ally-accent)' },
    { label: t('stats.output'), value: fmtTokens(r.output), color: '#6db3f2' },
  ]);
}

// ── hour chart geometry ──
const hourBars = computed(() => {
  const hours = stats.value?.byHour || [];
  const max = Math.max(1, ...hours.map((h) => (h.inputTokens || 0) + (h.outputTokens || 0)));
  const innerW = 720 - 12 - 12;
  const innerH = 200 - 20 - 30;
  const slot = innerW / 24;
  const barW = slot * 0.62;
  return hours.map((h, i) => {
    const value = (h.inputTokens || 0) + (h.outputTokens || 0);
    const height = (value / max) * innerH;
    return {
      x: 12 + i * slot + (slot - barW) / 2,
      y: 20 + innerH - height,
      w: barW,
      h: Math.max(value > 0 ? 2 : 0, height),
      value,
      hot: value > 0 && value >= max * 0.6,
      hour: i,
      input: h.inputTokens || 0,
      output: h.outputTokens || 0,
    };
  });
});
const hourTicks = computed(() => {
  const hours = stats.value?.byHour || [];
  const innerW = 720 - 24;
  const slot = innerW / 24;
  return hours
    .map((_, hour) => ({ hour, x: 12 + hour * slot + slot / 2 }))
    .filter((tick) => tick.hour % 6 === 0);
});

function showHourTooltip(event, h) {
  const label = String(h.hour).padStart(2, '0') + ':00';
  showChartTooltip(event, label, [
    { label: t('stats.input'), value: fmtTokens(h.input), color: 'var(--ally-accent)' },
    { label: t('stats.output'), value: fmtTokens(h.output), color: '#6db3f2' },
  ]);
}

// ── cache daily rate chart geometry ──
const cacheRateChart = computed(() => {
  const days = stats.value?.byDay || [];
  const n = days.length;
  const innerW = chartW - padL - padR;
  const innerH = 200 - 12 - 28;
  const x = (i) => (n <= 1 ? padL + innerW / 2 : padL + (i * innerW) / (n - 1));
  const y = (rate) => 12 + innerH - rate * innerH;
  const pts = [];
  for (let i = 0; i < n; i++) {
    const d = days[i];
    const total = (d.cacheHitTokens || 0) + (d.cacheMissTokens || 0);
    if (total > 0) pts.push(`${x(i).toFixed(1)},${y((d.cacheHitTokens || 0) / total).toFixed(1)}`);
  }
  const grid = [0, 0.25, 0.5, 0.75, 1].map((ratio) => ({ y: y(ratio), value: ratio * 100 }));
  const step = Math.max(1, Math.floor(n / 6));
  const labelPoints = days
    .map((d, i) => ({ x: x(i), label: d.date ? d.date.slice(5) : '' }))
    .filter((_, i) => i % step === 0 || i === n - 1);
  const bandW = n > 1 ? innerW / (n - 1) : innerW;
  const hoverRects = days.map((d, i) => {
    const total = (d.cacheHitTokens || 0) + (d.cacheMissTokens || 0);
    return {
      x: Math.max(padL, x(i) - bandW / 2),
      width: bandW,
      date: d.date || '',
      rate: total > 0 ? (d.cacheHitTokens || 0) / total : 0,
      hit: d.cacheHitTokens || 0,
      miss: d.cacheMissTokens || 0,
    };
  });
  return { pts: pts.join(' '), grid, labelPoints, hoverRects };
});

function showCacheTooltip(event, r) {
  showChartTooltip(event, r.date, [
    { label: t('stats.cacheHitRate'), value: (r.rate * 100).toFixed(1) + '%', color: 'var(--ally-accent)' },
    { label: t('stats.cacheHit'), value: fmtTokens(r.hit), color: 'var(--ally-accent)' },
    { label: t('stats.cacheMiss'), value: fmtTokens(r.miss), color: '#6f6f6f' },
  ]);
}

const donutCircumference = 2 * Math.PI * 70;
</script>

<style scoped>
.stats-body {
  min-height: 260px;
  max-height: min(76vh, 760px);
  overflow-y: auto;
  padding-right: 2px;
}
.stats-range-select {
  width: 118px;
  margin-right: 8px;
}
.stats-overview {
  display: grid;
  grid-template-columns: repeat(6, 1fr);
  gap: 8px;
  margin-bottom: 16px;
}
.stat-card {
  padding: 10px 10px;
  border: 1px solid rgba(255, 255, 255, 0.08);
  border-radius: 10px;
  background: rgba(255, 255, 255, 0.03);
  min-width: 0;
}
.stat-label {
  color: #8f8f8f;
  font-size: 11px;
}
.stat-value {
  margin-top: 4px;
  color: #f1f1f1;
  font-size: 18px;
  font-weight: 650;
  letter-spacing: 0.2px;
}
.stat-sub {
  margin-top: 4px;
  color: #777;
  font-size: 11px;
  line-height: 1.5;
}
.stats-section {
  padding: 6px 2px;
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
.chart-line.input {
  stroke: var(--ally-accent);
  stroke-width: 2;
  stroke-linejoin: round;
  stroke-linecap: round;
}
.chart-line.output {
  stroke: #6db3f2;
  stroke-width: 2;
  stroke-linejoin: round;
  stroke-linecap: round;
}
.hour-bar {
  fill: color-mix(in srgb, var(--ally-accent) 55%, #6f6f6f);
}
.hour-bar.hot {
  fill: var(--ally-accent);
}
.cache-section {
  display: flex;
  align-items: center;
  gap: 28px;
  margin-top: 14px;
  padding: 10px 6px;
}
.donut-track {
  fill: none;
  stroke: rgba(255, 255, 255, 0.08);
  stroke-width: 16;
}
.donut-fill {
  fill: none;
  stroke: var(--ally-accent);
  stroke-width: 16;
  stroke-linecap: round;
}
.donut-value {
  fill: #f1f1f1;
  font-size: 26px;
  font-weight: 650;
}
.donut-label {
  fill: #8f8f8f;
  font-size: 11px;
}
.cache-detail {
  flex: 1;
}
.cache-row {
  display: flex;
  justify-content: space-between;
  padding: 8px 0;
  border-bottom: 1px solid rgba(255, 255, 255, 0.06);
  font-size: 13px;
}
.cache-name {
  color: #9a9a9a;
}
.cache-value {
  color: #e8e8e8;
  font-family: ui-monospace, SFMono-Regular, Consolas, monospace;
}
.cache-tip {
  margin-top: 10px;
  color: #777;
  font-size: 11px;
  line-height: 1.6;
}
.stats-empty {
  padding: 60px 0;
}
.stats-empty-hint {
  color: #777;
  font-size: 12px;
}
</style>
