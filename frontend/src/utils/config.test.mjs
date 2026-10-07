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
import { COMPACT_THRESHOLD_DEFAULT, COMPACT_THRESHOLD_MAX, COMPACT_THRESHOLD_MIN, HIDEABLE_MODES, PAGE_VISIBILITY_LABELS, assignConfig, defaultConfig, normalizeHiddenModes, placeholderModel } from './config.mjs';

// 顶层不再有任何模型字段：模型只有 models[] 预设 + lastUsedModel 身份（其余字段
// 由后端按身份展开）。这条不变式是整个重构的地基，钉住它。
test('defaultConfig carries no top-level model fields', () => {
  const cfg = defaultConfig();
  for (const key of ['providerName', 'apiFormat', 'baseUrl', 'apiKey', 'apiKeys', 'model', 'maxTokens', 'contextWindow', 'tokenParam', 'reasoningTag', 'reasoningEffort', 'customHeaders']) {
    assert.equal(Object.hasOwn(cfg, key), false, `${key} must not live on the config`);
  }
  assert.equal(cfg.lastUsedModel, null);
  // 出厂占位是界面的事，不进配置。
  assert.equal(placeholderModel().model, 'deepseek-v4-flash');
});

test('assignConfig normalizes the last-used model identity', () => {
  const draft = defaultConfig();
  assignConfig(draft, { lastUsedModel: { providerName: '  Relay ', model: ' relay-model ' } });
  assert.deepEqual(draft.lastUsedModel, { providerName: 'Relay', model: 'relay-model' });

  // 缺 model id 的身份等于"没有"：不能留一个永远展开不出东西的悬空身份。
  assignConfig(draft, { lastUsedModel: { providerName: 'Relay', model: '' } });
  assert.equal(draft.lastUsedModel, null);
});

test('model entries default their reasoning tag to reasoning_content', () => {
  const draft = defaultConfig();
  assignConfig(draft, { models: [{ model: 'test', reasoningTag: '' }] });
  assert.equal(draft.models[0].reasoningTag, 'reasoning_content');
});

test('assignConfig does not share the model list with a draft config', () => {
  const config = defaultConfig();
  const draft = defaultConfig();
  config.models = [{ name: 'Saved', model: 'saved-model' }];

  assignConfig(draft, config);
  draft.models.push({ name: 'Draft only', model: 'draft-model' });

  assert.equal(config.models.length, 1);
  assert.notEqual(draft.models, config.models);
});

test('assignConfig does not share model entries with a draft config', () => {
  const config = defaultConfig();
  const draft = defaultConfig();
  config.models = [{ name: 'Saved', model: 'saved-model' }];

  assignConfig(draft, config);
  draft.models[0].model = 'mutated-model';

  assert.equal(config.models[0].model, 'saved-model');
});

test('assignConfig drops legacy systemPrompt field', () => {
  const draft = defaultConfig();
  draft.systemPrompt = 'old target value';

  assignConfig(draft, { ...defaultConfig(), systemPrompt: 'legacy override' });

  assert.equal(Object.hasOwn(draft, 'systemPrompt'), false);
});

test('assignConfig keeps the apiKeys pool on model entries', () => {
  const config = defaultConfig();
  config.models = [{ model: 'saved-model', apiKey: 'k1', apiKeys: ['k1', 'k2'] }];
  const draft = defaultConfig();

  assignConfig(draft, config);

  assert.deepEqual(draft.models[0].apiKeys, ['k1', 'k2']);
  draft.models[0].apiKeys.push('k3');
  assert.deepEqual(config.models[0].apiKeys, ['k1', 'k2']);
});

// 可关闭页面键前后端必须是同一组：后端 hideableModes 切片与前端
// HIDEABLE_MODES 漂移会导致“界面关了、后端又放回来”（或反之）。切片按字面量
// 钉住顺序——map 块的旧正则到第一个 } 就停，块里加注释即误匹配。
test('可关闭页面键与后端一致', () => {
  const bizSource = readFileSync(new URL('../../../internal/app/biz_config.go', import.meta.url), 'utf8');
  const body = /var hideableModes = \[\]string\{([^}]*)\}/.exec(bizSource);
  assert.ok(body, 'Go 侧找不到 hideableModes 切片');
  const goKeys = [...body[1].matchAll(/"([a-z]+)"/g)].map((match) => match[1]);
  assert.deepEqual([...HIDEABLE_MODES], goKeys);
  // assignConfig 归一：大小写/空白归一，chat、settings 与未知键丢弃。
  const draft = defaultConfig();
  assignConfig(draft, { hiddenModes: [' Skills ', 'Chat', 'KB', 'settings', 'bogus', 'skills'] });
  assert.deepEqual(draft.hiddenModes, ['skills', 'kb']);
  assert.deepEqual(normalizeHiddenModes('not-an-array'), []);
  // 设置页的开关列表由 HIDEABLE_MODES 派生，label 映射必须覆盖全部键：少一个键就少
  // 一个开关（点了没反应），多一个键则是没人用的死配置。
  assert.deepEqual(Object.keys(PAGE_VISIBILITY_LABELS).sort(), [...HIDEABLE_MODES].sort());
});

// 前后端的压缩阈值必须是同一组数（后端 defaultCompactThreshold / clampCompactThreshold）。
// 跨语言没法共用常量，所以读 Go 源码钉住：谁只改了一边，这里就红。不一致的后果是
// “界面显示的阈值”和“实际触发压缩的阈值”不是同一个，而两边都不报错。
function goFloat(source, name) {
  const match = new RegExp(`\\b${name}\\b\\s*=\\s*([0-9.]+)`).exec(source);
  assert.ok(match, `Go 侧找不到 ${name}（改名了？两边要一起改）`);
  return Number(match[1]);
}

test('压缩阈值的默认值与范围与后端一致', () => {
  const appSource = readFileSync(new URL('../../../internal/app/app.go', import.meta.url), 'utf8');
  const compactSource = readFileSync(new URL('../../../internal/app/biz_compact.go', import.meta.url), 'utf8');

  assert.equal(COMPACT_THRESHOLD_DEFAULT, goFloat(appSource, 'defaultCompactThreshold'));

  // clampCompactThreshold 里的两个边界值：取函数体内出现的所有小数，最小与最大即两个
  // 边界——比逐行匹配稳，改动顺序不会误伤。
  const body = /func clampCompactThreshold\(v float64\) float64 \{([\s\S]*?)\n\}/.exec(compactSource);
  assert.ok(body, 'Go 侧找不到 clampCompactThreshold');
  const bounds = [...body[1].matchAll(/(\d+\.\d+)/g)].map((match) => Number(match[1]));
  assert.ok(bounds.length >= 2, 'clampCompactThreshold 里没读到边界值');
  assert.equal(COMPACT_THRESHOLD_MIN, Math.min(...bounds));
  assert.equal(COMPACT_THRESHOLD_MAX, Math.max(...bounds));
});
