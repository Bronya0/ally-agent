// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.

// 数码波浪的全局入口（模块级单例）。
// 全屏特效层全局只挂一份（App.vue 根部的 <DigitalWave>），它挂载时把自己的
// 播放函数注册到这里；任何触发点都通过 burstDigitalWave 打到同一个实例上，
// 不会出现两块画布，也不会因为触发点分散而漏挂一层。
// 与樱花风 useSakuraBreeze 同一思路：单例特效层 + 模块级共享入口。
let runner = null;

export function registerDigitalWave(play) {
  runner = play;
  return () => {
    if (runner === play) runner = null;
  };
}

/* 从 (x, y) 起波。调用方拿不到坐标时兜底到窗口底部中间——
   触发它的输入条就在那儿，观感仍然是"从触发点炸开"。 */
export function burstDigitalWave(x, y) {
  if (!runner) return;
  const px = Number.isFinite(x) ? x : window.innerWidth / 2;
  const py = Number.isFinite(y) ? y : window.innerHeight - 48;
  runner(px, py);
}
