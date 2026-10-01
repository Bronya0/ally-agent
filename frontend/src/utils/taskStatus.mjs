/*
 * SPDX-License-Identifier: GPL-3.0-only
 *
 * Copyright (C) 2026 tangssst <tangssst@qq.com>
 * GitHub: https://github.com/Bronya0/ally-agent
 *
 * This file is part of ally-agent, licensed under the GNU General
 * Public License v3. See the LICENSE file for details.
 */

// 服务与定时任务的状态词表：一条状态对应哪句文案、哪个语义色调，只在这里判。
// 工具卡（CardGrid 徽标）与任务中心面板（n-tag）都要这份判定，两边各写一张表必然
// 漂移——新状态只加一边，另一边就回落成生英文。
//
// 纯函数、不 import i18n（见 utils 的约定：文案须调用方传）：这里给的是 i18n key，
// 调用方自己 t(key)，key 为空串表示「没有这条状态」，由调用方决定回落（原样显示
// 状态串、或再兜一层文案）。
//
// 色调用的是 CardGrid 的语义词（success / info / warning / danger / neutral），
// 用 naive-ui n-tag 的一方自己映射（见 TaskCenterPanel 的 TAG_TYPES）。

const SERVICE_STATUS_KEYS = {
  starting: 'service.status.starting',
  running: 'common.running',
  stopped: 'service.status.stopped',
  exited: 'service.status.exited',
  interrupted: 'service.status.interrupted',
};

export function serviceStatusKey(status) {
  return SERVICE_STATUS_KEYS[String(status || '').trim().toLowerCase()] || '';
}

// 退出码只对已退出的进程有意义：正常退出（0）是中性，非 0 才是失败。
export function serviceStatusTone(service) {
  const status = String(service?.status || '').trim().toLowerCase();
  if (status === 'exited' && Number(service?.exitCode || 0) !== 0) return 'danger';
  if (status === 'running') return 'success';
  if (status === 'starting') return 'info';
  if (status === 'interrupted') return 'warning';
  return 'neutral';
}

// 「这个服务此刻还活着」——徽标计数、事件合流（不活跃就把卡片移出列表）、列表排序
// （活着的排前面）、任务中心的「停止」按钮与日志轮询都要问这同一个问题。集合只在
// 这里定义：将来多一个活跃状态（比如 restarting）只改这一行，所有调用点自动跟上。
const ACTIVE_SERVICE_STATUSES = ['starting', 'running'];

export function isServiceActive(service) {
  return ACTIVE_SERVICE_STATUSES.includes(String(service?.status || '').trim().toLowerCase());
}

const SCHEDULED_STATUS_KEYS = {
  scheduled: 'scheduled.status.waiting',
  completed: 'scheduled.status.completed',
  failed: 'scheduled.status.failed',
  timed_out: 'scheduled.status.timedOut',
  cancelled: 'scheduled.status.cancelled',
  skipped: 'scheduled.status.skipped',
  missed: 'scheduled.status.missed',
  interrupted: 'scheduled.status.interrupted',
  invalid: 'scheduled.status.invalid',
};

// 正在跑的那些不看上次结果：状态栏显示的就是「运行中」。
export function scheduledStatusKey(task) {
  if (task?.running) return 'common.running';
  return SCHEDULED_STATUS_KEYS[String(task?.lastStatus || '').trim().toLowerCase()] || '';
}

// 正在跑是「进行中」不是「成功」：跑完才知道成败，这里不提前报喜。
export function scheduledStatusTone(task) {
  if (task?.running) return 'info';
  const status = String(task?.lastStatus || '').trim().toLowerCase();
  if (status === 'completed') return 'success';
  if (['failed', 'timed_out', 'invalid'].includes(status)) return 'danger';
  if (['skipped', 'missed', 'cancelled', 'interrupted'].includes(status)) return 'warning';
  return 'neutral';
}
