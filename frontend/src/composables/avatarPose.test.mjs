// SPDX-License-Identifier: GPL-3.0-only
import test from 'node:test';
import assert from 'node:assert/strict';

import {
  POSE_GAP_MAX_MS,
  POSE_GAP_MIN_MS,
  nextPoseDelay,
  pickPoseName,
} from './avatarPose.mjs';

test('pose gap stays inside the configured window for any random source', () => {
  assert.equal(nextPoseDelay(() => 0), POSE_GAP_MIN_MS);
  assert.equal(nextPoseDelay(() => 1), POSE_GAP_MAX_MS);
  assert.equal(nextPoseDelay(() => 0.5), POSE_GAP_MIN_MS + (POSE_GAP_MAX_MS - POSE_GAP_MIN_MS) / 2);
  // 随机源越界或返回垃圾值时不能给出负数或超上限的间隔。
  for (const random of [() => -3, () => 42, () => Number.NaN, () => 'x', null, undefined]) {
    const gap = nextPoseDelay(random);
    assert.ok(Number.isFinite(gap), `gap must be finite for ${String(random)}`);
    assert.ok(gap >= POSE_GAP_MIN_MS && gap <= POSE_GAP_MAX_MS, `gap out of range: ${gap}`);
  }
});

test('pose picker never repeats the previous pose when alternatives exist', () => {
  const names = ['proud', 'stretch'];
  for (let i = 0; i < 50; i++) {
    const picked = pickPoseName(names, 'proud', () => i / 50);
    assert.equal(picked, 'stretch');
  }
  assert.equal(pickPoseName(names, 'stretch', () => 0), 'proud');
});

test('pose picker degrades safely on empty or single-entry sets', () => {
  assert.equal(pickPoseName([], 'proud'), '');
  assert.equal(pickPoseName(null, 'proud'), '');
  assert.equal(pickPoseName(['proud'], 'proud', () => 0), 'proud');
  // 随机源返回 1 时下标不能越界成 undefined。
  assert.equal(pickPoseName(['proud', 'stretch'], '', () => 1), 'stretch');
  assert.equal(pickPoseName(['proud', 'stretch'], '', () => 42), 'stretch');
});
