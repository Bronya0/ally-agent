// SPDX-License-Identifier: GPL-3.0-only
import { IMAGE_MODEL_EXPORT_KIND, modelConfigIdentity, normalizeApiKeysArray, normalizeCustomHeaders } from './modelConfigIO.mjs';
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

export const IMAGE_MODEL_EXPORT_VERSION = 1;
const MAX_IMPORTED_IMAGE_MODELS = 200;

function imageImportError(code) {
  const error = new Error(code);
  error.code = code;
  return error;
}

export function parseImageModelImport(text) {
  const data = parseConfigJSON(text);
  // 先认身份再校验结构：对话模型的导出文件也带 models 数组，只说「文件无效」会让
  // 用户反复重试同一个文件，说清是哪种文件才能让他切到对应分类。
  if (data?.kind != null && data.kind !== IMAGE_MODEL_EXPORT_KIND) throw imageImportError('KIND_UNSUPPORTED');
  if (data?.version !== IMAGE_MODEL_EXPORT_VERSION || !Array.isArray(data.models) || data.kind !== IMAGE_MODEL_EXPORT_KIND || data.models.length > MAX_IMPORTED_IMAGE_MODELS) throw imageImportError('INVALID_FILE');
  return data.models.map(normalizeImageModel);
}

// 导出前过一遍归一化：写出去的就是导入时认得的结构，脏字段不会流进文件。
export function buildImageModelExport(models) {
  return {
    version: IMAGE_MODEL_EXPORT_VERSION,
    kind: IMAGE_MODEL_EXPORT_KIND,
    models: (Array.isArray(models) ? models : []).map(normalizeImageModel),
  };
}

// 导入合并：同一供应商下的同名模型覆盖，其余追加；返回计数供界面反馈（导入成功
// 不再一声不吭，与对话模型那条「新增 X 个，更新 Y 个」对齐）。
export function mergeImageModels(config, models) {
  let added = 0;
  let updated = 0;
  for (const model of models) {
    const existing = (config.models || []).find((item) => imageModelKey(item) === imageModelKey(model));
    if (existing) updated += 1;
    else added += 1;
    replaceImageModel(config, existing, model);
  }
  return { added, updated };
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
