import test from 'node:test';
import assert from 'node:assert/strict';
import { normalizeRailWidth, RAIL_DEFAULT_WIDTH, RAIL_MIN_WIDTH, RAIL_MAX_WIDTH } from './railWidth.mjs';

test('rail width defaults safely for missing or invalid preferences', () => {
  for (const value of [undefined, null, '', ' ', 'broken', NaN, Infinity, {}, true]) {
    assert.equal(normalizeRailWidth(value), RAIL_DEFAULT_WIDTH);
  }
});

test('rail width restores numeric preferences and clamps resize bounds', () => {
  for (const [value, expected] of [
    ['220', 220], [210.6, 211], [-20, RAIL_MIN_WIDTH], [900, RAIL_MAX_WIDTH],
    [RAIL_MIN_WIDTH, RAIL_MIN_WIDTH], [RAIL_MAX_WIDTH, RAIL_MAX_WIDTH],
  ]) {
    assert.equal(normalizeRailWidth(value), expected);
  }
});
