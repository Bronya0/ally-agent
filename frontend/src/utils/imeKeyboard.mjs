// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 tangssst <tangssst@qq.com>

// 在组件挂载前装到 window 捕获阶段。组词按键只交给浏览器/输入法，
// 不进入业务快捷键、弹窗或第三方菜单；不 preventDefault，保留原生选字行为。
// 每个事件独立判断，没有需要 compositionend/blur 配对清理的应用状态。
export function isolateImeKeydown(event) {
  // WebKit 的确认键可能晚于 compositionend，此时只剩 229 标记。
  if (event.isComposing || event.keyCode === 229) event.stopImmediatePropagation();
}
