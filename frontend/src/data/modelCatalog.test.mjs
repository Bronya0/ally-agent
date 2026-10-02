/*
 * SPDX-License-Identifier: GPL-3.0-only
 *
 * Copyright (C) 2026 tangssst <tangssst@qq.com>
 * GitHub: https://github.com/Bronya0/ally-agent
 *
 * This file is part of ally-agent, licensed under the GNU General
 * Public License v3. See the LICENSE file for details.
 */
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';

import { buildModelCatalog } from '../../../scripts/generate-model-catalog.mjs';

// 入库的 frontend/src/data/modelCatalog.json 是生成物（源是 docs/model_api.json，约
// 4.6MB）。以前没有任何东西守着它：改了源、或者改了生成逻辑，忘了跑
// `npm run generate:model-catalog`，界面就会静默用旧数据——上下文窗口、输出上限、
// 接口格式都可能过期，而报错的地方离原因很远（模型设置里数值不对）。
test('入库的模型目录与生成器当前的输出一致', () => {
  const built = buildModelCatalog(JSON.parse(readFileSync(new URL('../../../docs/model_api.json', import.meta.url), 'utf8')));
  assert.equal(
    built,
    readFileSync(new URL('./modelCatalog.json', import.meta.url), 'utf8'),
    '模型目录过期：跑 npm run generate:model-catalog 重新生成后再提交',
  );
});
