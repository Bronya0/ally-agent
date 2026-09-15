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
import test from 'node:test';
import {
  CUSTOM_PROVIDER_ID,
  applyCatalogPreset,
  findCatalogModel,
  findCatalogProvider,
  providerCatalogOptions,
  providerModelOptions,
} from './modelProviderCatalog.mjs';

const catalog = {
  providers: [{
    id: 'known',
    name: 'Known Provider',
    apiFormat: 'openai_chat',
    baseUrl: 'https://api.example.com/v1',
    models: [{ id: 'model-2', name: 'Model 2', contextWindow: 128000, maxTokens: 16000, reasoningTag: 'reasoning_content' }],
  }],
};

test('catalog options keep known providers and append custom configuration', () => {
  assert.deepEqual(providerCatalogOptions(catalog, 'Custom'), [
    { label: 'Known Provider', value: 'known' },
    { label: 'Custom', value: CUSTOM_PROVIDER_ID },
  ]);
});

test('catalog provider and model lookup return null for unknown values', () => {
  const provider = findCatalogProvider(catalog, 'known');
  assert.equal(provider?.name, 'Known Provider');
  assert.equal(findCatalogProvider(catalog, 'missing'), null);
  assert.equal(findCatalogModel(provider, 'model-2')?.name, 'Model 2');
  assert.equal(findCatalogModel(provider, 'missing'), null);
  assert.deepEqual(providerModelOptions(provider), [{ label: 'Model 2 · model-2', value: 'model-2' }]);
});

test('catalog preset fills connection metadata and preserves credentials', () => {
  const provider = findCatalogProvider(catalog, 'known');
  const model = findCatalogModel(provider, 'model-2');
  assert.deepEqual(applyCatalogPreset(provider, model, { apiKey: 'secret', temperature: 0.3 }), {
    apiKey: 'secret',
    temperature: 0.3,
    providerName: 'Known Provider',
    apiFormat: 'openai_chat',
    baseUrl: 'https://api.example.com/v1',
    model: 'model-2',
    maxTokens: 16000,
    contextWindow: 128000,
    reasoningTag: 'reasoning_content',
    // 目录未声明视觉能力时必须显式给出 undefined（三态里的"未知"），而不是省掉
    // 这个键：ModelsPanel 用 Object.assign(modelDraft, preset) 套用结果，键缺失会
    // 把上一个模型的 visionCapable 静默继承下来。
    visionCapable: undefined,
  });
});

test('catalog preset carries the tri-state vision capability without inheriting it', () => {
  const provider = findCatalogProvider(catalog, 'known');
  const undeclared = findCatalogModel(provider, 'model-2');

  const cleared = applyCatalogPreset(provider, undeclared, { apiKey: 'secret', visionCapable: true });
  assert.equal(Object.hasOwn(cleared, 'visionCapable'), true);
  assert.equal(cleared.visionCapable, undefined);

  const textOnly = { ...undeclared, visionCapable: false };
  assert.equal(applyCatalogPreset(provider, textOnly, { visionCapable: true }).visionCapable, false);
});
