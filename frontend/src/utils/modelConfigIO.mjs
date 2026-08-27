/*
 * SPDX-License-Identifier: GPL-3.0-only
 *
 * Copyright (C) 2026 tangssst <tangssst@qq.com>
 * GitHub: https://github.com/Bronya0/ally-agent
 *
 * This file is part of ally-agent, licensed under the GNU General
 * Public License v3. See the LICENSE file for details.
 */
export const MODEL_CONFIG_EXPORT_VERSION = 1;

const MODEL_FIELDS = [
  'providerName',
  'apiFormat',
  'baseUrl',
  'apiKey',
  'apiKeys',
  'model',
  'temperature',
  'maxTokens',
  'contextWindow',
  'reasoningTag',
  'tokenParam',
  'reasoningEffort',
];

function importError(code) {
  const error = new Error(code);
  error.code = code;
  return error;
}

function normalizeProviderName(value) {
  return String(value || '').trim() || 'OpenAI Compatible';
}

function normalizeModelId(value) {
  return String(value || '').trim();
}

// normalizeTokenParam mirrors the Go backend: empty/unknown -> 'auto' (legacy
// max_tokens), only 'max_completion_tokens' opts into the newer field.
export function normalizeTokenParam(value) {
  const v = String(value || '').trim().toLowerCase().replace(/[-\s]+/g, '_');
  if (['max_completion_tokens', 'max_completion_token', 'completion_tokens', 'completion'].includes(v)) {
    return 'max_completion_tokens';
  }
  if (['max_tokens', 'max_token', 'tokens', 'legacy'].includes(v)) return 'max_tokens';
  return 'auto';
}

// normalizeReasoningEffort mirrors the Go backend normalizeReasoningEffort so
// a value resolves identically regardless of which layer reads it. Recognized
// aliases (case/dash/space/underscore variants, "default"/"off"/"unset")
// collapse to the canonical level; anything else falls back to "auto".
export function normalizeReasoningEffort(value) {
  const v = String(value || '').trim().toLowerCase().replace(/[-_\s]+/g, '');
  switch (v) {
    case 'auto': case 'default': case 'unset': case 'off': case '':
      return 'auto';
    case 'low': return 'low';
    case 'medium': case 'med': return 'medium';
    case 'high': return 'high';
    case 'xhigh': case 'extrahigh': case 'extremehigh': return 'xhigh';
    case 'max': case 'maximum': case 'maximal': return 'max';
    default: return 'auto';
  }
}

// reasoningEffortLevels is the canonical ordered set of levels exposed in the
// UI (auto = send nothing). Keep in sync with the backend constants.
export const reasoningEffortLevels = ['auto', 'low', 'medium', 'high', 'xhigh', 'max'];

export function modelConfigIdentity(model) {
  return `${normalizeProviderName(model?.providerName).toLocaleLowerCase('en-US')}\u0000${normalizeModelId(model?.model).toLocaleLowerCase('en-US')}`;
}

function copyModelFields(model) {
  const result = {};
  for (const field of MODEL_FIELDS) {
    if (Object.hasOwn(model || {}, field)) result[field] = model[field];
  }
  return result;
}

// normalizeApiKeys 归一化 key 池:去空白、空项并按出现顺序去重;没有数组时
// 回退到旧的单 apiKey 字段,保证老版本导出文件兼容。
function normalizeApiKeys(keys, fallbackKey) {
  const out = [];
  const seen = new Set();
  const source = Array.isArray(keys) && keys.length ? keys : (fallbackKey ? [fallbackKey] : []);
  for (const k of source) {
    const v = String(k || '').trim();
    if (v && !seen.has(v)) {
      seen.add(v);
      out.push(v);
    }
  }
  return out;
}

// normalizeApiKeysArray 与 normalizeApiKeys 相同,但仅接受数组输入,供
// assignConfig/cloneModelConfigs 统一去重语义(与后端 normalizeAPIKeys 一致)。
export function normalizeApiKeysArray(keys) {
  return normalizeApiKeys(keys, null);
}

function normalizeImportedModel(model) {
  if (!model || typeof model !== 'object' || Array.isArray(model)) {
    throw importError('MODEL_INVALID');
  }

  const modelId = normalizeModelId(model.model);
  if (!modelId) throw importError('MODEL_ID_REQUIRED');

  const apiKeys = normalizeApiKeys(model.apiKeys, model.apiKey);
  const normalized = {
    providerName: normalizeProviderName(model.providerName),
    apiFormat: String(model.apiFormat || 'openai_chat').trim() || 'openai_chat',
    baseUrl: String(model.baseUrl || '').trim(),
    apiKey: apiKeys[0] || '',
    apiKeys,
    model: modelId,
    temperature: Number.isFinite(Number(model.temperature)) ? Number(model.temperature) : 0.2,
    maxTokens: Number.isFinite(Number(model.maxTokens)) && Number(model.maxTokens) > 0 ? Math.trunc(Number(model.maxTokens)) : 131072,
    contextWindow: Number.isFinite(Number(model.contextWindow)) && Number(model.contextWindow) > 0 ? Math.trunc(Number(model.contextWindow)) : 1000000,
    reasoningTag: String(model.reasoningTag || 'reasoning_content').trim() || 'reasoning_content',
    tokenParam: normalizeTokenParam(model.tokenParam),
    reasoningEffort: normalizeReasoningEffort(model.reasoningEffort),
  };

  return normalized;
}

export function buildModelConfigExport(models) {
  return {
    formatVersion: MODEL_CONFIG_EXPORT_VERSION,
    models: Array.isArray(models) ? models.map(copyModelFields).map(normalizeImportedModel) : [],
  };
}

export function parseModelConfigImport(text) {
  let parsed;
  try {
    parsed = JSON.parse(String(text || ''));
  } catch {
    throw importError('JSON_INVALID');
  }

  if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) {
    throw importError('ROOT_INVALID');
  }
  if (parsed.formatVersion !== MODEL_CONFIG_EXPORT_VERSION) {
    throw importError('VERSION_UNSUPPORTED');
  }
  if (!Array.isArray(parsed.models)) throw importError('MODELS_REQUIRED');

  return parsed.models.map(normalizeImportedModel);
}

export function mergeModelConfigs(existingModels, importedModels) {
  const merged = [];
  const indexByIdentity = new Map();
  const importedByIdentity = new Map();

  for (const model of existingModels || []) {
    const copy = { ...(model || {}) };
    const modelId = normalizeModelId(copy.model);
    if (!modelId) {
      merged.push(copy);
      continue;
    }
    const identity = modelConfigIdentity(copy);
    const existingIndex = indexByIdentity.get(identity);
    if (existingIndex === undefined) {
      indexByIdentity.set(identity, merged.length);
      merged.push(copy);
    } else {
      merged.splice(existingIndex, 1, copy);
    }
  }
  for (const imported of importedModels || []) {
    const normalized = normalizeImportedModel(imported);
    importedByIdentity.set(modelConfigIdentity(normalized), normalized);
  }

  let added = 0;
  let updated = 0;
  for (const [identity, normalized] of importedByIdentity) {
    const existingIndex = indexByIdentity.get(identity);
    if (existingIndex === undefined) {
      indexByIdentity.set(identity, merged.length);
      merged.push(normalized);
      added += 1;
    } else {
      merged.splice(existingIndex, 1, normalized);
      updated += 1;
    }
  }

  return { models: merged, added, updated };
}
