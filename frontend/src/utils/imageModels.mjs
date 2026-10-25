// SPDX-License-Identifier: GPL-3.0-only
import { modelConfigIdentity, normalizeApiKeysArray, normalizeCustomHeaders } from './modelConfigIO.mjs';
import { isConfigObject, parseConfigJSON } from './configJSON.mjs';

export const imageProtocols = [
  { value: 'openai-images', label: 'OpenAI', baseUrl: 'https://api.openai.com/v1' },
  { value: 'xai-images', label: 'xAI', baseUrl: 'https://api.x.ai/v1' },
  { value: 'minimax-images', label: 'MiniMax', baseUrl: 'https://api.minimax.io/v1' },
];

export const imageTestLimits = { maxInputs: 5, maxBytes: 20 * 1024 * 1024, promptChars: 16000, maxResults: 20 };

export function validateImageTestFiles(files) {
  if (files.length > imageTestLimits.maxInputs || files.some(file => !['image/png', 'image/jpeg', 'image/webp'].includes(file.type) || !Number.isFinite(file.size) || file.size <= 0) || files.reduce((total, file) => total + file.size, 0) > imageTestLimits.maxBytes) {
    throw new Error('Expected up to five PNG, JPEG, or WebP images within 20 MiB combined');
  }
}

export function cloneImageGeneration(value) {
  return JSON.parse(JSON.stringify(value || { enabled: false, models: [], generateModel: null, editModel: null }));
}

export function inferImageProtocol(model) {
  const name = String(model || '').trim().toLowerCase();
  if (name.startsWith('gpt-image-') || name === 'chatgpt-image-latest') return 'openai-images';
  if (name.startsWith('grok-imagine-image')) return 'xai-images';
  if (name === 'image-01' || name === 'image-01-live') return 'minimax-images';
  return '';
}

export function imageModelIdentity(model) {
  return model ? { providerName: model.providerName, model: model.model } : null;
}

export function imageModelKey(model) { return model ? modelConfigIdentity(model) : ''; }
export function supportsImageEdit(model) { return ['openai-images', 'xai-images'].includes(model?.protocol); }

export function normalizeImageModel(model) {
  if (!isConfigObject(model)) throw new Error('Invalid image model');
  const protocol = String(model.protocol || inferImageProtocol(model.model));
  if (!imageProtocols.some((item) => item.value === protocol)) throw new Error('Unsupported image protocol');
  const apiKeys = normalizeApiKeysArray(Array.isArray(model.apiKeys) && model.apiKeys.length ? model.apiKeys : [model.apiKey]);
  const result = {
    providerName: String(model.providerName || '').trim(),
    model: String(model.model || '').trim(), protocol,
    baseUrl: String(model.baseUrl || '').trim().replace(/\/+$/, ''),
    apiKey: apiKeys[0] || '', apiKeys,
    customHeaders: normalizeCustomHeaders(model.customHeaders),
  };
  const url = new URL(result.baseUrl);
  if (!result.providerName || !result.model || !['https:', 'http:'].includes(url.protocol) || url.username || url.password || url.search || url.hash) throw new Error('Invalid image model');
  return result;
}

export function parseImageModelImport(text) {
  const data = parseConfigJSON(text);
  if (data?.version !== 1 || !Array.isArray(data.models) || data.kind !== 'ally-image-models' || data.models.length > 200) throw new Error('Invalid image model file');
  return data.models.map(normalizeImageModel);
}

// Editing or removing a preset also repairs its default references.
export function replaceImageModel(config, previous, next) {
  const key = imageModelKey(previous);
  const models = config.models || [];
  const nextKey = imageModelKey(next);
  if (next && models.some((item) => imageModelKey(item) === nextKey && imageModelKey(item) !== key)) throw new Error('Duplicate image model');
  config.models = previous ? models.flatMap((item) => imageModelKey(item) === key ? (next ? [next] : []) : [item]) : [...models, next];
  for (const field of ['generateModel', 'editModel']) {
    if (key && imageModelKey(config[field]) === key) config[field] = next && (field !== 'editModel' || supportsImageEdit(next)) ? imageModelIdentity(next) : null;
  }
}
