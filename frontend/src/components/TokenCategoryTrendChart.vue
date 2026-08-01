<template>
  <div class="token-category-chart">
    <div class="chart-wrapper">
      <!-- Chart area with hover interaction -->
      <div class="chart-area" @mouseleave="hoverIndex = -1">
        <svg
          class="stats-chart"
          viewBox="0 0 720 240"
          role="img"
          :aria-label="t('stats.categoryTrend')"
        >
          <g v-for="g in chart.grid" :key="g.y">
            <line :x1="padL" :x2="chartW - padR" :y1="g.y" :y2="g.y" class="chart-grid" />
            <text :x="padL + 4" :y="g.y - 5" class="chart-grid-label">{{ fmtTokens(g.value) }}</text>
          </g>
          <polygon
            v-for="layer in chart.layers"
            :key="layer.name"
            :points="layer.pts"
            :style="{ fill: layer.color, fillOpacity: 0.5 }"
          />
          <!-- Hover guide line -->
          <line
            v-if="hoverIndex >= 0"
            :x1="hoverX" :x2="hoverX"
            :y1="padT" :y2="chartH - padB"
            class="hover-guide"
          />
          <!-- Hover dots on each layer -->
          <template v-if="hoverIndex >= 0">
            <circle
              v-for="(layer, si) in chart.layers"
              :key="'dot-' + si"
              :cx="hoverX"
              :cy="layer.points[hoverIndex]?.y ?? 0"
              r="3.5"
              :fill="layer.color"
              stroke="rgba(0,0,0,0.3)"
              stroke-width="1"
            />
          </template>
          <g v-for="(pt, i) in chart.labelPoints" :key="i">
            <text :x="pt.x" :y="chartH - 6" text-anchor="middle" class="chart-axis-label">{{ pt.label }}</text>
          </g>
          <!-- Invisible hover zones -->
          <rect
            v-for="(r, i) in hoverRects"
            :key="'zone-' + i"
            :x="r.x" :y="padT"
            :width="r.width" :height="chartH - padT - padB"
            fill="transparent"
            @mouseenter="hoverIndex = i"
          />
        </svg>
        <!-- Floating tooltip -->
        <div v-if="hoverIndex >= 0 && tooltipData" class="chart-tooltip" :style="tooltipStyle">
          <div class="tooltip-date">{{ tooltipData.date }}</div>
          <div v-for="(item, si) in tooltipData.items" :key="item.name" class="tooltip-row">
            <i class="tooltip-dot" :style="{ background: chart.layers[si]?.color }"></i>
            <span class="tooltip-name">{{ item.label }}</span>
            <span class="tooltip-value">{{ fmtTokens(item.value) }}</span>
          </div>
        </div>
      </div>

      <!-- Right floating panel: merged legend + ranking -->
      <div class="chart-legend-panel">
        <div v-for="(item, i) in rankItems" :key="item.name" class="legend-rank-item">
          <div class="legend-rank-header">
            <i class="legend-dot" :style="{ background: item.color }"></i>
            <span class="legend-name" :title="item.name">{{ item.label }}</span>
            <span class="legend-value">{{ fmtTokens(item.total) }}</span>
          </div>
          <div class="legend-share-track">
            <div class="legend-share-fill" :style="{ width: (item.share * 100).toFixed(1) + '%', background: item.color }"></div>
          </div>
          <div class="legend-meta">
            <span>{{ t('stats.requests') }} {{ fmtNum(item.requests) }}</span>
            <span>{{ (item.share * 100).toFixed(1) }}%</span>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup>
import { computed, ref } from 'vue';
import { t } from '../i18n.mjs';

const props = defineProps({
  series: { type: Array, default: () => [] },
  days: { type: Array, default: () => [] },
  kind: { type: String, default: 'model' },
  ranks: { type: Array, default: () => [] },
});

const chartW = 720;
const chartH = 240;
const padL = 46;
const padR = 10;
const padT = 12;
const padB = 28;

const PALETTE = [
  'var(--ally-accent)',
  '#6db3f2',
  '#c58af9',
  '#f2b36d',
  '#6ee7b7',
  '#f28db2',
  '#8bd3f2',
  '#e7d36d',
  '#9aa7f2',
  '#7ed6a5',
  '#f27d9c',
  '#a5c9f2',
];

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

function displayName(name) {
  if (props.kind === 'source') {
    const key = `stats.source.${name}`;
    const translated = t(key);
    return translated === key ? name : translated;
  }
  if (props.kind === 'workspace') return name || t('stats.defaultWorkspace');
  return name;
}

function valueAt(s, i) {
  return (s.inputTokens?.[i] || 0) + (s.outputTokens?.[i] || 0);
}

const chart = computed(() => {
  const list = props.series || [];
  const n = props.days?.length || 0;
  const innerW = chartW - padL - padR;
  const innerH = chartH - padT - padB;

  const totals = new Array(n).fill(0);
  let max = 1;
  for (const s of list) {
    for (let i = 0; i < n; i++) {
      totals[i] += valueAt(s, i);
      if (totals[i] > max) max = totals[i];
    }
  }

  const x = (i) => (n <= 1 ? padL + innerW / 2 : padL + (i * innerW) / (n - 1));
  const y = (v) => padT + innerH - (v / max) * innerH;

  const stack = new Array(n).fill(0);
  const layers = list.map((s, si) => {
    const topPts = [];
    const bottomPts = [];
    const points = [];
    for (let i = 0; i < n; i++) {
      const v = valueAt(s, i);
      stack[i] += v;
      const px = x(i);
      const py = y(stack[i]);
      topPts.push(`${px.toFixed(1)},${py.toFixed(1)}`);
      points.push({ x: px, y: py, value: v });
    }
    for (let i = n - 1; i >= 0; i--) {
      bottomPts.push(`${x(i).toFixed(1)},${y(stack[i] - valueAt(s, i)).toFixed(1)}`);
    }
    return {
      name: s.name,
      label: displayName(s.name),
      color: PALETTE[si % PALETTE.length],
      pts: [...topPts, ...bottomPts].join(' '),
      points,
    };
  });

  const grid = [0.25, 0.5, 0.75, 1].map((ratio) => ({
    y: padT + innerH - ratio * innerH,
    value: Math.round(max * ratio),
  }));

  const step = Math.max(1, Math.floor(n / 6));
  const labelPoints = (props.days || [])
    .map((d, i) => ({ x: x(i), label: d.date ? d.date.slice(5) : '' }))
    .filter((_, i) => i % step === 0 || i === n - 1);

  return { layers, grid, labelPoints, x, n };
});

const rankItems = computed(() => {
  const list = props.ranks || [];
  return list.map((item, i) => ({
    name: item.name,
    label: displayName(item.name),
    total: (item.inputTokens || 0) + (item.outputTokens || 0),
    share: item.share || 0,
    requests: item.requests || 0,
    color: PALETTE[i % PALETTE.length],
  }));
});

// ── hover interaction ──
const hoverIndex = ref(-1);

const hoverX = computed(() => {
  if (hoverIndex.value < 0) return 0;
  return chart.value.x(hoverIndex.value);
});

const hoverRects = computed(() => {
  const n = chart.value.n;
  if (n === 0) return [];
  const innerW = chartW - padL - padR;
  const rectW = n <= 1 ? innerW : innerW / (n - 1);
  const rects = [];
  for (let i = 0; i < n; i++) {
    rects.push({
      x: chart.value.x(i) - rectW / 2,
      width: rectW,
    });
  }
  return rects;
});

const tooltipData = computed(() => {
  if (hoverIndex.value < 0) return null;
  const days = props.days || [];
  const day = days[hoverIndex.value];
  if (!day) return null;
  const items = chart.value.layers.map((layer) => ({
    name: layer.name,
    label: layer.label,
    value: layer.points[hoverIndex.value]?.value || 0,
  }));
  return { date: day.date || '', items };
});

const tooltipStyle = computed(() => {
  if (hoverIndex.value < 0) return { display: 'none' };
  const x = chart.value.x(hoverIndex.value);
  const leftPercent = (x / chartW) * 100;
  if (leftPercent < 22) {
    return { left: `${leftPercent}%`, transform: 'translateX(0)' };
  }
  if (leftPercent > 78) {
    return { left: `${leftPercent}%`, transform: 'translateX(-100%)' };
  }
  return { left: `${leftPercent}%`, transform: 'translateX(-50%)' };
});
</script>

<style scoped>
.token-category-chart {
  margin-bottom: 8px;
}
.chart-wrapper {
  display: flex;
  gap: 14px;
  align-items: flex-start;
}
.chart-area {
  flex: 1;
  min-width: 0;
  position: relative;
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
.hover-guide {
  stroke: rgba(255, 255, 255, 0.35);
  stroke-width: 1;
  stroke-dasharray: 3 3;
  pointer-events: none;
}

/* ── floating tooltip ── */
.chart-tooltip {
  position: absolute;
  top: 4px;
  z-index: 10;
  padding: 8px 10px;
  background: rgba(30, 30, 32, 0.96);
  border: 1px solid rgba(255, 255, 255, 0.12);
  border-radius: 8px;
  box-shadow: 0 4px 16px rgba(0, 0, 0, 0.4);
  pointer-events: none;
  white-space: nowrap;
  backdrop-filter: blur(6px);
}
.tooltip-date {
  margin-bottom: 5px;
  color: #ccc;
  font-size: 11px;
  font-weight: 600;
  border-bottom: 1px solid rgba(255, 255, 255, 0.08);
  padding-bottom: 4px;
}
.tooltip-row {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 11px;
  line-height: 1.7;
}
.tooltip-dot {
  width: 8px;
  height: 8px;
  border-radius: 2px;
  flex-shrink: 0;
}
.tooltip-name {
  color: #aaa;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  max-width: 120px;
}
.tooltip-value {
  margin-left: auto;
  color: #f0f0f0;
  font-family: ui-monospace, SFMono-Regular, Consolas, monospace;
  font-weight: 550;
}

/* ── right floating legend + ranking panel ── */
.chart-legend-panel {
  width: 190px;
  flex-shrink: 0;
  display: flex;
  flex-direction: column;
  gap: 8px;
  max-height: 240px;
  overflow-y: auto;
  padding-right: 2px;
}
.legend-rank-item {
  padding: 5px 7px;
  border-radius: 6px;
  background: rgba(255, 255, 255, 0.025);
  border: 1px solid rgba(255, 255, 255, 0.05);
}
.legend-rank-header {
  display: flex;
  align-items: center;
  gap: 6px;
}
.legend-dot {
  width: 9px;
  height: 9px;
  border-radius: 2px;
  flex-shrink: 0;
}
.legend-name {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  color: #dcdcdc;
  font-size: 11px;
  font-weight: 500;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.legend-value {
  flex-shrink: 0;
  color: #c8c8c8;
  font-size: 11px;
  font-family: ui-monospace, SFMono-Regular, Consolas, monospace;
}
.legend-share-track {
  height: 4px;
  margin-top: 5px;
  overflow: hidden;
  border-radius: 2px;
  background: rgba(255, 255, 255, 0.06);
}
.legend-share-fill {
  height: 100%;
  border-radius: 2px;
  opacity: 0.7;
}
.legend-meta {
  display: flex;
  justify-content: space-between;
  margin-top: 4px;
  color: #777;
  font-size: 10px;
}

@media (max-width: 600px) {
  .chart-wrapper {
    flex-direction: column;
  }
  .chart-legend-panel {
    width: 100%;
    max-height: none;
    flex-direction: row;
    flex-wrap: wrap;
    gap: 6px;
  }
  .legend-rank-item {
    flex: 1 1 140px;
  }
}
</style>
