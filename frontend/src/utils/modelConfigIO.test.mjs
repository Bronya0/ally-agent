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
  normalizeApiFormat,
  normalizeCustomHeaders,
  normalizeReasoningEffort,
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

test('normalizeCustomHeaders trims, canonicalizes, and drops managed names', () => {
  const got = normalizeCustomHeaders({
    ' x-api-version ': ' 2023-06-01 ',
    Host: 'evil.example',
    'content-length': '999',
    '': 'value',
    'Empty': '   ',
  });
  assert.deepEqual(got, { 'X-Api-Version': '2023-06-01' });

  assert.equal(normalizeCustomHeaders({ A: '', B: ' ' }), null);
  assert.equal(normalizeCustomHeaders(null), null);
});

// Mirrors TestNormalizeReasoningEffort in internal/app/infra_bridges_test.go.
// Every spelling the backend accepts must fold the same way here: a spelling
// only one side folds is not "tolerated", it is silently rewritten, and an
// "off" read as "auto" turns thinking back on (and this side persists it).
test('normalizeReasoningEffort folds exactly the spellings the backend accepts', () => {
  const cases = {
    '': 'auto',
    auto: 'auto',
    Auto: 'auto',
    default: 'auto',
    unset: 'auto',
    off: 'off',
    OFF: 'off',
    none: 'off',
    disabled: 'off',
    nothinking: 'off',
    nothink: 'off',
    'no-think': 'off',
    NO_THINK: 'off',
    low: 'low',
    LOW: 'low',
    medium: 'medium',
    med: 'medium',
    high: 'high',
    xhigh: 'xhigh',
    'X-HIGH': 'xhigh',
    extra_high: 'xhigh',
    extremehigh: 'xhigh',
    max: 'max',
    maximum: 'max',
    maximal: 'max',
    bogus: 'auto',
    'high effort': 'auto',
  };
  for (const [input, want] of Object.entries(cases)) {
    assert.equal(normalizeReasoningEffort(input), want, `normalizeReasoningEffort(${JSON.stringify(input)})`);
  }
});

// Mirrors the alias buckets of the Go normalizeAPIFormat (infra_bridges.go).
test('normalizeApiFormat folds exactly the spellings the backend accepts', () => {
  const cases = {
    '': 'openai_chat',
    openai: 'openai_chat',
    openai_compatible: 'openai_chat',
    openai_chat: 'openai_chat',
    chat: 'openai_chat',
    chat_completions: 'openai_chat',
    chat_completion: 'openai_chat',
    'OpenAI-Chat': 'openai_chat',
    bogus: 'openai_chat',
    openai_responses: 'openai_responses',
    responses: 'openai_responses',
    response: 'openai_responses',
    anthropic: 'anthropic_messages',
    anthropic_messages: 'anthropic_messages',
    claude: 'anthropic_messages',
    claude_messages: 'anthropic_messages',
    messages: 'anthropic_messages',
  };
  for (const [input, want] of Object.entries(cases)) {
    assert.equal(normalizeApiFormat(input), want, `normalizeApiFormat(${JSON.stringify(input)})`);
  }
});

test('custom headers survive model config export and import', () => {
  const payload = buildModelConfigExport([
    model('Relay', 'relay-model', { customHeaders: { 'X-Relay': 'token', host: 'drop' } }),
  ]);
  const imported = parseModelConfigImport(JSON.stringify(payload));

  assert.deepEqual(imported[0].customHeaders, { 'X-Relay': 'token' });
});
