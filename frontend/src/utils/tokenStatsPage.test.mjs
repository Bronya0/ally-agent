import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';

const source = readFileSync(new URL('../components/TokenStatsModal.vue', import.meta.url), 'utf8');
const computed = (get) => ({ get value() { return get(); } });

test('daily usage outside the current month and last week still shows the charts', () => {
  const start = source.indexOf('const isEmpty = computed(');
  const end = source.indexOf('const summaryRanges', start);
  assert.ok(start >= 0 && end > start);
  const stats = { value: { summaryToday: {}, summary7Days: {}, summaryMonth: {}, daily: [{ inputTokens: 100, outputTokens: 0 }] } };
  const empty = new Function('stats', 'computed', `${source.slice(start, end)}; return isEmpty;`)(stats, computed);
  assert.equal(empty.value, false);
  stats.value.daily = [];
  assert.equal(empty.value, true);
});

test('month heatmap uses month data even when the 30-day bars omit day one', () => {
  const start = source.indexOf('const HEAT_COLORS');
  const end = source.indexOf('// ── 按天柱状图', start);
  assert.ok(start >= 0 && end > start);
  class FixedDate extends Date {
    constructor(...args) { super(...(args.length ? args : [2026, 7, 31, 12])); }
  }
  const stats = { value: {
    monthDaily: [{ date: '2026-08-01', inputTokens: 100, outputTokens: 20 }],
    daily: [{ date: '2026-08-02', inputTokens: 0, outputTokens: 0 }],
  } };
  const cells = new Function('stats', 'computed', 'Date', `${source.slice(start, end)}; return heatmapCells;`)(stats, computed, FixedDate);
  assert.equal(cells.value.find((cell) => cell.date === '2026-08-01').total, 120);
});
