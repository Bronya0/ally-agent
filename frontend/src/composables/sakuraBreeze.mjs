// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.

import { ref } from 'vue';

// 樱花青草风特效的全局共享开关（模块级单例状态）。
// 每个 AllyAvatar 实例（各工作区 Tab / 启动页的眼球）都调用本入口
// 读写同一份 ref；唯一的 <SakuraBreeze> 特效层挂在 App.vue 根部并
// 绑定同一个状态。效果：任意 Tab 点眼球 = 切换同一开关，
// A Tab 开启后切到 B Tab 再点一下即为关闭。
const sakuraOn = ref(false);

export function useSakuraBreeze() {
  function toggleSakura() {
    sakuraOn.value = !sakuraOn.value;
  }
  return { sakuraOn, toggleSakura };
}
