// SPDX-License-Identifier: GPL-3.0-only
import assert from 'node:assert/strict';
import test from 'node:test';
import { buildModelConfigExport, parseModelConfigImport, modelSnapshotFrom, modelConfigIdentity, mergeModelConfigs } from './modelConfigIO.mjs';
import { assignConfig, defaultConfig } from './config.mjs';
import { formatModelLabel } from './modelLabel.mjs';

test('ChatGPT model survives tab snapshots and export/import with its account and slug', () => {
  const model = { authType: 'chatgpt_oauth', oauthAccountId: 'oaiapp_one', providerName: 'ChatGPT · one', model: 'account-model-slug', displayName: 'Friendly Model', apiFormat: 'openai_responses', baseUrl: 'https://api.openai.com/v1' };
  const restored = parseModelConfigImport(JSON.stringify(buildModelConfigExport([model])))[0];
  const tab = modelSnapshotFrom(restored);
  assert.equal(tab.authType, 'chatgpt_oauth');
  assert.equal(tab.oauthAccountId, 'oaiapp_one');
  assert.equal(tab.model, 'account-model-slug');
  assert.equal(tab.providerName, 'ChatGPT Official');
  assert.equal(tab.contextWindow, 512000);
  assert.equal(formatModelLabel(tab), 'ChatGPT Official · Friendly Model');
});

test('OAuth imports remove key pools and header overrides and pin Responses', () => {
  const payload = buildModelConfigExport([{ authType: 'chatgpt_oauth', oauthAccountId: 'account', providerName: 'ChatGPT', model: 'slug', apiKey: 'paid-key', apiKeys: ['paid-key'], customHeaders: { Authorization: 'override' }, baseUrl: 'https://elsewhere.test', apiFormat: 'openai_chat' }]);
  assert.equal(payload.models[0].providerName, 'ChatGPT Official');
  assert.equal(payload.models[0].contextWindow, 512000);
  assert.equal(payload.models[0].apiKey, '');
  assert.deepEqual(payload.models[0].apiKeys, []);
  assert.equal(payload.models[0].customHeaders, undefined);
  assert.equal(payload.models[0].baseUrl, 'https://api.openai.com/v1');
  assert.equal(payload.models[0].apiFormat, 'openai_responses');
});

test('legacy API key models keep their billing path', () => {
  const tab = modelSnapshotFrom({ providerName: 'Existing', model: 'model', apiKey: 'existing-key' });
  assert.equal(tab.authType, 'api_key');
  assert.equal(tab.oauthAccountId, '');
  assert.deepEqual(tab.apiKeys, ['existing-key']);
});

test('same ChatGPT model on two accounts stays distinct after import and config assignment', () => {
  const first = { authType: 'chatgpt_oauth', oauthAccountId: 'one', providerName: 'ChatGPT Official', model: 'slug', contextWindow: 256000 };
  const second = { ...first, oauthAccountId: 'two' };
  assert.notEqual(modelConfigIdentity(first), modelConfigIdentity(second));
  const imported = parseModelConfigImport(JSON.stringify(buildModelConfigExport([first, second])));
  const { models: merged } = mergeModelConfigs([], imported);
  assert.equal(merged.length, 2);
  assert.equal(modelSnapshotFrom(merged[0]).contextWindow, 256000);
  const config = defaultConfig();
  assignConfig(config, { models: merged, lastUsedModel: { providerName: 'ChatGPT Official', model: 'slug', oauthAccountId: 'two' } });
  assert.equal(config.lastUsedModel.oauthAccountId, 'two');
});
