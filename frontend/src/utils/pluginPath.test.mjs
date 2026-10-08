/*
 * SPDX-License-Identifier: GPL-3.0-only
 *
 * Copyright (C) 2026 tangssst <tangssst@qq.com>
 * GitHub: https://github.com/Bronya0/ally-agent
 *
 * This file is part of ally-agent, licensed under the GNU General Public
 * License v3. See the LICENSE file for details.
 */
import assert from 'node:assert/strict';
import test from 'node:test';
import { pluginWorkspacePath } from './pluginPath.mjs';

// host.files 的路径守门：只放行工作区内的相对路径。后端绑定对绝对路径是放行的（编辑器
// 语义），所以「工作区: 只读」这句权限摘要唯一就靠这里兜住——被测到的是这条边界本身。
test('pluginWorkspacePath 只放行工作区内的相对路径', () => {
  for (const ok of ['index.js', './src/app.vue', 'src\\app.vue', 'a/b/c.txt', '.']) {
    assert.equal(pluginWorkspacePath(ok), ok);
  }
  for (const bad of [
    '/etc/passwd',
    'C:/Users/x/.ally_agent/config.json',
    'c:\\tmp\\a.txt',
    '\\\\server\\share\\x',
    '../outside.js',
    'a/../../b.js',
    '..\\b.js',
    '  ',
    '',
  ]) {
    assert.throws(() => pluginWorkspacePath(bad), /host\.files/, `${JSON.stringify(bad)} 必须被拒`);
  }
});
