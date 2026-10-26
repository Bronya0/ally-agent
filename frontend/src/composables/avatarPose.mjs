// SPDX-License-Identifier: GPL-3.0-only
//
// 位图形象的「换姿势」节奏。纯函数，不碰 DOM，方便单测钉住边界：
// 待机若干秒后做一个动作，动作保持一会儿再回 idle，且不连续重复同一个动作。

// 动作姿势保持多久（毫秒）。跨形象统一，避免节奏漂移。
export const POSE_HOLD_MS = 2600;

export const POSE_GAP_MIN_MS = 6000;
export const POSE_GAP_MAX_MS = 11000;

// 两次动作之间的间隔：闭区间 [min, max]，随机取整。
export function nextPoseDelay(random = Math.random) {
  const value = typeof random === 'function' ? random() : 0;
  // 随机源理论上可以返回 1 或负数，收敛后再取整，避免间隔越界或为负。
  const bounded = Math.min(1, Math.max(0, Number(value) || 0));
  return POSE_GAP_MIN_MS + Math.round(bounded * (POSE_GAP_MAX_MS - POSE_GAP_MIN_MS));
}

// 从候选姿势里挑一个，尽量避开上一次用过的那个；只有一个候选时允许重复。
export function pickPoseName(names, previous, random = Math.random) {
  if (!Array.isArray(names) || names.length === 0) return '';
  const pool = names.length > 1 ? names.filter((name) => name !== previous) : names;
  if (pool.length === 0) return '';
  const value = typeof random === 'function' ? random() : 0;
  const bounded = Math.min(1, Math.max(0, Number(value) || 0));
  // 随机源恰好返回 1 时下标会越界，收敛到最后一个。
  const index = Math.min(pool.length - 1, Math.floor(bounded * pool.length));
  return pool[index];
}

// Own all scheduled work in one bounded controller. The host supplies state
// accessors so the scheduler is independent of Vue and DOM visibility APIs.
export function createPosePlayer({ canPlay, idleNames, hasPose, show, random = Math.random }) {
  let gapTimer = null;
  let holdTimer = null;
  let previous = '';

  function stop() {
    clearTimeout(gapTimer);
    clearTimeout(holdTimer);
    gapTimer = holdTimer = null;
    show('');
  }

  function schedule() {
    clearTimeout(gapTimer);
    gapTimer = null;
    const names = idleNames();
    if (!canPlay() || names.length === 0) return;
    gapTimer = setTimeout(() => {
      gapTimer = null;
      const name = pickPoseName(idleNames(), previous, random);
      if (play(name)) previous = name;
    }, nextPoseDelay(random));
  }

  function play(name, holdMs = POSE_HOLD_MS) {
    if (!name || !hasPose(name) || !canPlay()) return false;
    stop();
    show(name);
    holdTimer = setTimeout(() => {
      holdTimer = null;
      show('');
      schedule();
    }, holdMs);
    return true;
  }

  function reset() {
    stop();
    previous = '';
    schedule();
  }

  return { play, schedule, stop, reset };
}
