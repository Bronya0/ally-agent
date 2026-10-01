/*
 * SPDX-License-Identifier: GPL-3.0-only
 *
 * Copyright (C) 2026 tangssst <tangssst@qq.com>
 * GitHub: https://github.com/Bronya0/ally-agent
 *
 * This file is part of ally-agent, licensed under the GNU General
 * Public License v3. See the LICENSE file for details.
 */

// showsSandboxAlert 判断一张工具卡要不要显示“沙箱已拦截”那一行。
//
// 标记本身来自结果（后端 sandboxDenied），可它说的是「这次调用被内核拦下了写入」。
// service 的标记记在服务记录上是 sticky 的：read / stop 的结果会把它连同输出尾巴一起
// 带回来，而读日志、停服务本身不写盘——显示在那两张卡上等于说“这次操作被拦”，可被拦的
// 是当初那条启动命令。所以 service 只认 start 动作，其余工具照旧。
//
// 动作取卡片自己的 toolAction（与卡片动词同源：入参流到了就一定有；没流到则是空串，
// 这时不显示——与动词退化成裸工具名是同一个取舍，见 toolVerb.mjs 的说明）。
export function showsSandboxAlert(toolName, action) {
  if (toolName !== 'service') return true;
  return action === 'start';
}
