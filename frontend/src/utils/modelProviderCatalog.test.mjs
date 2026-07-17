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
  });
});
