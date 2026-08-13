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
  buildModelConfigExport,
  mergeModelConfigs,
  parseModelConfigImport,
} from './modelConfigIO.mjs';

function model(providerName, modelId, overrides = {}) {
  return {
    providerName,
    apiFormat: 'openai_chat',
    baseUrl: 'https://api.example.com',
    apiKey: 'secret-key',
    model: modelId,
    temperature: 0.2,
    maxTokens: 8192,
    contextWindow: 128000,
    reasoningTag: 'reasoning_content',
    ...overrides,
  };
}

test('model config export includes plaintext credentials and can be parsed', () => {
  const payload = buildModelConfigExport([model('OpenAI', 'gpt-5', { apiKey: 'plain-secret' })]);
  const imported = parseModelConfigImport(JSON.stringify(payload));

  assert.equal(payload.formatVersion, 1);
  assert.equal(imported.length, 1);
  assert.equal(imported[0].apiKey, 'plain-secret');
});

test('incremental import replaces matching provider and model identities', () => {
  const existing = [
    model('OpenAI', 'gpt-5', { apiKey: 'old' }),
    model('Anthropic', 'claude-sonnet', { apiKey: 'keep' }),
  ];
  const imported = [
    model(' openai ', ' GPT-5 ', { apiKey: 'new', maxTokens: 16000 }),
    model('Google', 'gemini-pro', { apiKey: 'added' }),
  ];

  const result = mergeModelConfigs(existing, imported);

  assert.equal(result.added, 1);
  assert.equal(result.updated, 1);
  assert.equal(result.models.length, 3);
  assert.equal(result.models[0].apiKey, 'new');
  assert.equal(result.models[0].maxTokens, 16000);
  assert.equal(result.models[1].apiKey, 'keep');
  assert.equal(result.models[2].model, 'gemini-pro');
});

test('duplicate imported identities use the last entry', () => {
  const result = mergeModelConfigs([], [
    model('OpenAI', 'gpt-5', { apiKey: 'first' }),
    model('openai', 'GPT-5', { apiKey: 'last' }),
  ]);

  assert.equal(result.added, 1);
  assert.equal(result.updated, 0);
  assert.equal(result.models.length, 1);
  assert.equal(result.models[0].apiKey, 'last');
});

test('merge removes duplicate existing identities and keeps the last entry', () => {
  const result = mergeModelConfigs([
    model('OpenAI', 'gpt-5', { apiKey: 'first' }),
    model('openai', 'GPT-5', { apiKey: 'last' }),
  ], []);

  assert.equal(result.models.length, 1);
  assert.equal(result.models[0].apiKey, 'last');
});

test('model config import rejects unsupported or incomplete payloads', () => {
  assert.throws(
    () => parseModelConfigImport('{'),
    (error) => error.code === 'JSON_INVALID',
  );
  assert.throws(
    () => parseModelConfigImport(JSON.stringify({ formatVersion: 2, models: [] })),
    (error) => error.code === 'VERSION_UNSUPPORTED',
  );
  assert.throws(
    () => parseModelConfigImport(JSON.stringify({ formatVersion: 1, models: [{}] })),
    (error) => error.code === 'MODEL_ID_REQUIRED',
  );
});
