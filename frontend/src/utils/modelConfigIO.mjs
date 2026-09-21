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
  'customHeaders',
  'visionCapable',
];

function importError(code) {
  const error = new Error(code);
  error.code = code;
  return error;
}

// normalizeCustomHeaders mirrors the Go backend normalizeCustomHeaders:
// trim key/value, drop empty entries and transport-managed header names,
// canonicalize keys (x-api-version -> X-Api-Version) with deterministic
// dedup (lexicographically first key wins), cap at 32 entries. Returns null
// for an effectively empty set so no empty maps linger in configs/exports.
const MAX_CUSTOM_HEADERS = 32;
const MANAGED_HEADER_NAMES = new Set([
  'Host', 'Content-Length', 'Connection', 'Transfer-Encoding', 'Keep-Alive',
  'Proxy-Authenticate', 'Proxy-Authorization', 'Te', 'Trailer', 'Upgrade',
]);

function canonicalHeaderKey(key) {
  return String(key || '')
    .trim()
    .split('-')
    .map((part) => (part ? part[0].toUpperCase() + part.slice(1).toLowerCase() : ''))
    .join('-');
}

function isValidHeaderKey(key) {
  return /^[!#$%&'*+.^_|~0-9A-Za-z-]+$/.test(key);
}

export function normalizeCustomHeaders(headers) {
  const source = headers && typeof headers === 'object' && !Array.isArray(headers) ? headers : {};
  const keys = Object.keys(source).sort();
  const out = {};
  for (const raw of keys) {
    const key = canonicalHeaderKey(raw);
    const value = String(source[raw] ?? '').trim();
    if (!key || !value) continue;
    if (MANAGED_HEADER_NAMES.has(key)) continue;
    if (!isValidHeaderKey(key)) continue;
    if (Object.hasOwn(out, key)) continue;
    if (Object.keys(out).length >= MAX_CUSTOM_HEADERS) break;
    out[key] = value;
  }
  return Object.keys(out).length ? out : null;
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

// normalizeApiFormat mirrors the Go backend: the canonical wire formats are
// 'openai_chat', 'openai_responses' and 'anthropic_messages'. Recognized aliases
// (case/dash/space variants, 'responses', 'claude', 'messages', ...) fold into
// one of them; anything unknown — including every Chat spelling the backend
// accepts, such as 'openai' or 'chat_completions' — falls back to 'openai_chat',
// which is the backend's default bucket too. It lives here rather than inside a
// component so the alias table has one reusable home: a component-local copy is
// how the reasoning-effort tables drifted apart in the first place.
export function normalizeApiFormat(value) {
  const v = String(value || '').trim().toLowerCase().replace(/[-\s]+/g, '_');
  if (['openai_responses', 'responses', 'response'].includes(v)) return 'openai_responses';
  if (['anthropic', 'anthropic_messages', 'claude', 'claude_messages', 'messages'].includes(v)) return 'anthropic_messages';
  return 'openai_chat';
}

// normalizeReasoningEffort collapses the spellings of a level into the canonical
// value. Levels are picked from a dropdown (reasoningEffortLevels), so nothing in
// the UI produces any other spelling — this is a safety net for values that
// arrive from elsewhere, e.g. a hand-edited config. Recognized aliases (case/
// dash/space/underscore variants, "default"/"unset") collapse to the canonical
// level; "off" is a level of its own (explicitly stop thinking, unlike "auto"
// which leaves the decision to the provider); anything else falls back to
// "auto". The alias set mirrors the Go normalizer (infra_bridges.go) exactly.
// Tolerance has to match, not merely exist: a spelling only one side folds is
// not "cleaned up", it is silently rewritten — an "off" that reads as "auto"
// turns thinking back on behind the user's back, and this side runs on config
// load and can persist the rewrite. Both alias tables are pinned by tests.
export function normalizeReasoningEffort(value) {
  const v = String(value || '').trim().toLowerCase().replace(/[-_\s]+/g, '');
  switch (v) {
    case 'auto': case 'default': case 'unset': case '':
      return 'auto';
    case 'off': case 'none': case 'disabled': case 'nothinking': case 'nothink':
      return 'off';
    case 'low': return 'low';
    case 'medium': case 'med': return 'medium';
    case 'high': return 'high';
    case 'xhigh': case 'extrahigh': case 'extremehigh': return 'xhigh';
    case 'max': case 'maximum': case 'maximal': return 'max';
    default: return 'auto';
  }
}

// reasoningEffortLevels is the canonical ordered set of levels exposed in the
// UI. auto = send nothing (the provider decides), off = ask the provider to stop
// thinking — each protocol spells that in its own way (reasoning_effort: "none",
// reasoning: {effort: "none"}, Anthropic thinking.type: "disabled").
// Keep in sync with the backend constants.
export const reasoningEffortLevels = ['auto', 'off', 'low', 'medium', 'high', 'xhigh', 'max'];

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

// 从 config 形状的源（config.models 里的预设，或顶层默认字段）提取归一化模型快照。
// 曾内联在 App.vue；游戏区等独立面板也需要同一归一化，下沉至此避免各处自抄一份。
export function modelSnapshotFrom(source) {
  const keys = normalizeApiKeysArray(
    Array.isArray(source?.apiKeys) && source.apiKeys.length
      ? source.apiKeys
      : (source?.apiKey ? [source.apiKey] : [])
  );
  return {
    providerName: source?.providerName || 'OpenAI Compatible',
    apiFormat: source?.apiFormat || 'openai_chat',
    baseUrl: source?.baseUrl || '',
    model: source?.model || '',
    temperature: source?.temperature ?? 0.2,
    maxTokens: source?.maxTokens || 131072,
    contextWindow: source?.contextWindow || 1000000,
    tokenParam: source?.tokenParam || 'auto',
    reasoningTag: String(source?.reasoningTag || '').trim() || 'reasoning_content',
    // 视觉能力随快照下发（目录给的三态值）：undefined = 未知，Go 侧按未知处理并
    // 原样发送图片；只有明确 false 才会在请求构造时把图片换成文字占位。
    visionCapable: typeof source?.visionCapable === 'boolean' ? source.visionCapable : undefined,
    reasoningEffort: normalizeReasoningEffort(source?.reasoningEffort),
    // 空 key 池置 null 而不是 []，与下方 customHeaders 同款契约：null = 该
    // 模型未配 key，overlay 不携带该字段，后端保留顶层默认模型的 key 池。
    // [] 会被 mergeConfig 判为非 nil 而"显式清空"，把已保存的 key 一起抹
    // 掉——首次配置完直接发送报 "API key is required" 就是这条路径。
    apiKeys: keys.length ? keys : null,
    apiKey: keys[0] || '',
    // Per-model custom headers must ride the snapshot: chat requests send
    // {...config, ...snapshot}, and the StartChat overlay replaces the whole
    // map (null = this model has none; backend keeps its top-level mirror).
    customHeaders: source?.customHeaders && Object.keys(source.customHeaders).length
      ? { ...source.customHeaders }
      : null,
  };
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
    customHeaders: normalizeCustomHeaders(model.customHeaders) || undefined,
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
