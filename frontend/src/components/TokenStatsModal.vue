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
                <div class="model-rows">
                  <div v-for="(m, i) in stats.byModel" :key="m.name" class="rank-row">
                    <span class="rank-index">{{ i + 1 }}</span>
                    <div class="rank-main">
                      <div class="rank-line">
                        <span class="rank-name" :title="m.name">{{ m.name }}</span>
                        <span class="rank-tokens">
                          {{ fmtTokens(m.inputTokens + m.outputTokens) }}
                          <em>{{ fmtTokens(m.inputTokens) }} / {{ fmtTokens(m.outputTokens) }}</em>
                        </span>
                      </div>
                      <div class="share-track">
                        <div class="share-fill" :style="{ width: (m.share * 100).toFixed(1) + '%' }"></div>
                      </div>
                      <div class="rank-meta">
                        <span>{{ $t('stats.requests') }} {{ fmtNum(m.requests) }}</span>
                        <span>{{ $t('stats.share') }} {{ (m.share * 100).toFixed(1) }}%</span>
                        <span v-if="m.cacheHitTokens + m.cacheMissTokens > 0">
                          {{ $t('stats.cacheHitRate') }} {{ cacheRate(m).toFixed(1) }}%
                        </span>
                      </div>
                    </div>
                  </div>
                </div>
              </div>
            </n-tab-pane>

            <n-tab-pane name="provider" :tab="$t('stats.tabProvider')">
              <div class="stats-section">
                <div class="model-rows">
                  <div v-for="(item, i) in stats.byProvider" :key="item.name" class="rank-row">
                    <span class="rank-index">{{ i + 1 }}</span>
                    <div class="rank-main">
                      <div class="rank-line">
                        <span class="rank-name" :title="item.name">{{ item.name }}</span>
                        <span class="rank-tokens">{{ fmtTokens(item.inputTokens + item.outputTokens) }}</span>
                      </div>
                      <div class="share-track"><div class="share-fill" :style="{ width: (item.share * 100).toFixed(1) + '%' }"></div></div>
                      <div class="rank-meta">
                        <span>{{ $t('stats.requests') }} {{ fmtNum(item.requests) }}</span>
                        <span>{{ $t('stats.share') }} {{ (item.share * 100).toFixed(1) }}%</span>
                      </div>
                    </div>
                  </div>
                </div>
              </div>
            </n-tab-pane>

            <n-tab-pane name="source" :tab="$t('stats.tabSource')">
              <div class="stats-section">
                <div class="model-rows">
                  <div v-for="(item, i) in stats.bySource" :key="item.name" class="rank-row">
                    <span class="rank-index">{{ i + 1 }}</span>
                    <div class="rank-main">
                      <div class="rank-line">
                        <span class="rank-name">{{ sourceLabel(item.name) }}</span>
                        <span class="rank-tokens">{{ fmtTokens(item.inputTokens + item.outputTokens) }}</span>
                      </div>
                      <div class="share-track"><div class="share-fill" :style="{ width: (item.share * 100).toFixed(1) + '%' }"></div></div>
                      <div class="rank-meta">
                        <span>{{ $t('stats.requests') }} {{ fmtNum(item.requests) }}</span>
                        <span>{{ $t('stats.share') }} {{ (item.share * 100).toFixed(1) }}%</span>
                      </div>
                    </div>
                  </div>
                </div>
              </div>
            </n-tab-pane>

            <n-tab-pane name="day" :tab="$t('stats.tabDay')">
              <div class="stats-section">
                <div class="chart-legend">
                  <span class="legend-item"><i class="legend-dot input"></i>{{ $t('stats.input') }}</span>
                  <span class="legend-item"><i class="legend-dot output"></i>{{ $t('stats.output') }}</span>
                </div>
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
                </svg>
              </div>
            </n-tab-pane>

            <n-tab-pane name="hour" :tab="$t('stats.tabHour')">
              <div class="stats-section">
                <svg class="stats-chart" viewBox="0 0 720 200" role="img" :aria-label="$t('stats.hourlyDistribution')">
                  <g v-for="(h, i) in hourBars" :key="i">
                    <rect
                      :x="h.x"
                      :y="h.y"
                      :width="h.w"
                      :height="h.h"
                      rx="2"
                      :class="['hour-bar', { hot: h.hot }]"
                    >
                      <title>{{ hourTooltip(i, h) }}</title>
                    </rect>
                  </g>
                  <g v-for="t in hourTicks" :key="t.hour">
                    <text :x="t.x" :y="190" text-anchor="middle" class="chart-axis-label">{{ String(t.hour).padStart(2, '0') }}</text>
                  </g>
                </svg>
              </div>
            </n-tab-pane>

            <n-tab-pane name="cache" :tab="$t('stats.tabCache')">
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
                <div class="model-rows">
                  <div v-for="(w, i) in stats.byWorkspace" :key="w.name" class="rank-row">
                    <span class="rank-index">{{ i + 1 }}</span>
                    <div class="rank-main">
                      <div class="rank-line">
                        <span class="rank-name workspace" :title="w.name">{{ w.name || $t('stats.defaultWorkspace') }}</span>
                        <span class="rank-tokens">{{ fmtTokens(w.inputTokens + w.outputTokens) }}</span>
                      </div>
                      <div class="share-track">
                        <div class="share-fill" :style="{ width: (w.share * 100).toFixed(1) + '%' }"></div>
                      </div>
                      <div class="rank-meta">
                        <span>{{ $t('stats.requests') }} {{ fmtNum(w.requests) }}</span>
                        <span>{{ $t('stats.share') }} {{ (w.share * 100).toFixed(1) }}%</span>
                      </div>
                    </div>
                  </div>
                </div>
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
import { GetTokenStats } from '../../wailsjs/go/app/App';
import { t } from '../i18n.mjs';

const props = defineProps({
  show: { type: Boolean, default: false },
});
defineEmits(['close']);

const stats = ref(null);
const loading = ref(false);
const rangeDays = ref(30);
const rangeOptions = [
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
    }
  }
);

function fmtTokens(n) {
  const value = Number(n || 0);
  if (value >= 1e9) return (value / 1e9).toFixed(2) + 'B';
  if (value >= 1e6) return (value / 1e6).toFixed(2) + 'M';
  if (value >= 1e3) return (value / 1e3).toFixed(1) + 'K';
  return String(value);
}
function hourTooltip(i, h) {
  return String(i).padStart(2, '0') + ':00 · ' + fmtTokens(h.value);
}

function fmtNum(n) {
  return Number(n || 0).toLocaleString();
}
function sourceLabel(source) {
  const key = `stats.source.${source}`;
  const translated = t(key);
  return translated === key ? source : translated;
}
function cacheRate(item) {
  const total = (item.cacheHitTokens || 0) + (item.cacheMissTokens || 0);
  return total > 0 ? (item.cacheHitTokens / total) * 100 : 0;
}

const totalTokens = computed(() => (stats.value?.totalInputTokens || 0) + (stats.value?.totalOutputTokens || 0));
const avgPerRequest = computed(() => {
  const req = stats.value?.totalRequests || 0;
  return req > 0 ? Math.round(totalTokens.value / req) : 0;
});

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
  return { inputPts, outputPts, areaPts, grid, labelPoints };
});

const gridLines = computed(() => dayChart.value.grid);
const inputPoints = computed(() => dayChart.value.inputPts);
const outputPoints = computed(() => dayChart.value.outputPts);
const areaPoints = computed(() => dayChart.value.areaPts);
const dayLabelPoints = computed(() => dayChart.value.labelPoints);

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
  grid-template-columns: repeat(auto-fit, minmax(150px, 1fr));
  gap: 12px;
  margin-bottom: 16px;
}
.stat-card {
  padding: 12px 14px;
  border: 1px solid rgba(255, 255, 255, 0.08);
  border-radius: 10px;
  background: rgba(255, 255, 255, 0.03);
}
.stat-label {
  color: #8f8f8f;
  font-size: 11px;
}
.stat-value {
  margin-top: 4px;
  color: #f1f1f1;
  font-size: 22px;
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
.model-rows {
  display: flex;
  flex-direction: column;
  gap: 12px;
}
.rank-row {
  display: flex;
  align-items: flex-start;
  gap: 10px;
}
.rank-index {
  flex: 0 0 22px;
  margin-top: 2px;
  color: #707070;
  font-size: 12px;
  text-align: center;
  font-family: ui-monospace, SFMono-Regular, Consolas, monospace;
}
.rank-main {
  flex: 1;
  min-width: 0;
}
.rank-line {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: 12px;
}
.rank-name {
  overflow: hidden;
  color: #ececec;
  font-size: 13px;
  font-weight: 550;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.rank-name.workspace {
  font-family: ui-monospace, SFMono-Regular, Consolas, monospace;
  font-size: 12px;
  font-weight: 450;
}
.rank-tokens {
  flex-shrink: 0;
  color: #d6d6d6;
  font-size: 12px;
  font-family: ui-monospace, SFMono-Regular, Consolas, monospace;
}
.rank-tokens em {
  margin-left: 6px;
  color: #8a8a8a;
  font-style: normal;
  font-size: 11px;
}
.share-track {
  height: 5px;
  margin-top: 6px;
  overflow: hidden;
  border-radius: 3px;
  background: rgba(255, 255, 255, 0.07);
}
.share-fill {
  height: 100%;
  border-radius: 3px;
  background: linear-gradient(90deg, var(--ally-accent-dim), var(--ally-accent));
}
.rank-meta {
  display: flex;
  gap: 14px;
  margin-top: 5px;
  color: #777;
  font-size: 11px;
}
.stats-empty {
  padding: 60px 0;
}
.stats-empty-hint {
  color: #777;
  font-size: 12px;
}
</style>
