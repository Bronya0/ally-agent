// SPDX-License-Identifier: GPL-3.0-only
import assert from 'node:assert/strict';
import test from 'node:test';
import { readFileSync } from 'node:fs';
import { assignConfig, defaultConfig } from './config.mjs';
import { cloneImageGeneration, imageModelIdentity, imageTestLimits, inferImageProtocol, normalizeImageModel, parseImageModelImport, replaceImageModel, supportsImageEdit, validateImageTestFiles } from './imageModels.mjs';

test('manual image uploads enforce the same limits as backend before reading files', () => {
  const go = readFileSync(new URL('../../../internal/tools/imagegen/imagegen.go', import.meta.url), 'utf8');
  const goConst = name => new RegExp(`${name}\\s*=\\s*([0-9_ *]+)`).exec(go)[1].split('*').reduce((n, part) => n * Number(part.trim().replaceAll('_', '')), 1);
  assert.equal(imageTestLimits.maxInputs, goConst('MaxInputs'));
  assert.equal(imageTestLimits.maxBytes, goConst('MaxImageBytes'));
  assert.equal(imageTestLimits.promptChars, goConst('MaxPromptChars'));
  const png = { type: 'image/png', size: imageTestLimits.maxBytes };
  assert.doesNotThrow(() => validateImageTestFiles([png]));
  assert.throws(() => validateImageTestFiles([png, { ...png, size: 1 }]));
  assert.throws(() => validateImageTestFiles(Array(6).fill({ ...png, size: 1 })));
  for (const file of [{ ...png, type: 'image/svg+xml' }, { ...png, size: 0 }, { ...png, size: NaN }]) {
    assert.throws(() => validateImageTestFiles([file]));
  }
});

const model = { providerName: 'xAI', model: 'grok-imagine-image', protocol: 'xai-images', baseUrl: 'https://api.x.ai/v1', apiKeys: ['key'], customHeaders: { 'X-Image': 'value' } };

test('image protocol prediction recognizes image models without classifying chat models', () => {
  assert.equal(inferImageProtocol('gpt-image-2'), 'openai-images');
  assert.equal(inferImageProtocol('GROK-IMAGINE-IMAGE-2.0'), 'xai-images');
  assert.equal(inferImageProtocol('image-01'), 'minimax-images');
  assert.equal(inferImageProtocol('grok-4'), '');
  assert.equal(inferImageProtocol('gpt-5'), '');
  assert.equal(supportsImageEdit({ protocol: 'minimax-images' }), false);
});

test('image defaults follow renamed presets and clear on removal or lost editing capability', () => {
  const config = { models: [model], generateModel: imageModelIdentity(model), editModel: imageModelIdentity(model) };
  const changed = { ...model, model: 'image-01', protocol: 'minimax-images' };
  replaceImageModel(config, model, changed);
  assert.deepEqual(config.generateModel, imageModelIdentity(changed));
  assert.equal(config.editModel, null);
  replaceImageModel(config, changed, null);
  assert.equal(config.generateModel, null);
  assert.deepEqual(config.models, []);
});

test('image preset normalization and imports preserve credentials and reject unrelated exports', () => {
  const normalized = normalizeImageModel({ ...model, apiKeys: [' key ', 'key', 'second'] });
  assert.deepEqual(normalized.apiKeys, ['key', 'second']);
  assert.deepEqual(parseImageModelImport(JSON.stringify({ version: 1, kind: 'ally-image-models', models: [model] }))[0], normalizeImageModel(model));
  assert.throws(() => parseImageModelImport(JSON.stringify({ version: 1, models: [model] })));
  assert.throws(() => normalizeImageModel({ ...model, baseUrl: 'https://user:password@api.x.ai/v1' }));
  assert.throws(() => normalizeImageModel({ ...model, protocol: 'guess' }));
});

test('chat config assignment owns image models and defaults without mixing chat presets', () => {
  const config = defaultConfig();
  const source = { ...defaultConfig(), imageGeneration: { enabled: true, models: [model], generateModel: imageModelIdentity(model), editModel: null } };
  assignConfig(config, source);
  const clone = cloneImageGeneration(config.imageGeneration);
  clone.models[0].apiKeys[0] = 'changed';
  config.imageGeneration.models[0].customHeaders['X-Image'] = 'changed';
  config.imageGeneration.generateModel.model = 'changed';
  assert.equal(source.imageGeneration.models[0].apiKeys[0], 'key');
  assert.equal(source.imageGeneration.models[0].customHeaders['X-Image'], 'value');
  assert.equal(source.imageGeneration.generateModel.model, model.model);
  assert.deepEqual(config.models, []);
});
