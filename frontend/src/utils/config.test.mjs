import assert from 'node:assert/strict';
import test from 'node:test';
import { assignConfig, defaultConfig } from './config.mjs';

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
