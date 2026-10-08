/*
 * SPDX-License-Identifier: GPL-3.0-only
 *
 * Copyright (C) 2026 tangssst <tangssst@qq.com>
 * GitHub: https://github.com/Bronya0/ally-agent
 *
 * This file is part of ally-agent, licensed under the GNU General Public
 * License v3. See the LICENSE file for details.
 */
// host.files 的路径守门。刻意单独成文件、不碰生成的 bindings：node 测试要能直接把它钉住
// （pluginHost.mjs 顶层 import bindings，node 里加载不了，见同目录的 pluginPath.test.mjs）。

// pluginWorkspacePath 是 host.files 唯一接受的路径形态：工作区内的相对路径。空串、绝对
// 路径（含 Windows 盘符与 UNC）与任何一段 `..` 一律拒。
//
// 后端那几个绑定对绝对路径是**放行**的（它们服务的是编辑器语义，见 LESSONS 的
// resolve-read-no-fence），所以「工作区: 只读」这句权限摘要就靠这里兜住。这不是安全边界
// ——插件能绕开 host 直接调绑定（见 docs/plugin-system.md 第 8 节）——但 host 给出去的
// 能力必须与声明一致。
export function pluginWorkspacePath(raw) {
  const value = String(raw ?? '').trim();
  if (!value) throw new Error('host.files 需要路径');
  if (value.startsWith('/') || value.startsWith('\\') || /^[A-Za-z]:/.test(value)) {
    throw new Error(`host.files 只接受工作区内的相对路径：${value}`);
  }
  if (value.split(/[\\/]+/).includes('..')) {
    throw new Error(`host.files 不接受上级目录引用：${value}`);
  }
  return value;
}
