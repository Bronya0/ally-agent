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
import { assignConfig, defaultConfig } from './config.mjs';

test('reasoning tag defaults to reasoning_content', () => {
  assert.equal(defaultConfig().reasoningTag, 'reasoning_content');

  const draft = defaultConfig();
  assignConfig(draft, { reasoningTag: '', models: [{ model: 'test', reasoningTag: '' }] });
  assert.equal(draft.reasoningTag, 'reasoning_content');
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

test('assignConfig keeps apiKeys pool on models and top level', () => {
  const config = defaultConfig();
  config.models = [{ model: 'saved-model', apiKey: 'k1', apiKeys: ['k1', 'k2'] }];
  const draft = defaultConfig();

  assignConfig(draft, config);

  assert.deepEqual(draft.models[0].apiKeys, ['k1', 'k2']);
  draft.models[0].apiKeys.push('k3');
  assert.deepEqual(config.models[0].apiKeys, ['k1', 'k2']);
});

test('assignConfig falls back to apiKey when apiKeys absent', () => {
  const draft = defaultConfig();

  assignConfig(draft, { ...defaultConfig(), apiKey: 'legacy-key' });

  assert.deepEqual(draft.apiKeys, ['legacy-key']);
});
