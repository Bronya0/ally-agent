export const MODEL_CONFIG_EXPORT_VERSION = 1;

const MODEL_FIELDS = [
  'providerName',
  'apiFormat',
  'baseUrl',
  'apiKey',
  'model',
  'temperature',
  'maxTokens',
  'contextWindow',
  'reasoningTag',
  'tokenParam',
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

function normalizeImportedModel(model) {
  if (!model || typeof model !== 'object' || Array.isArray(model)) {
    throw importError('MODEL_INVALID');
  }

  const modelId = normalizeModelId(model.model);
  if (!modelId) throw importError('MODEL_ID_REQUIRED');

  const normalized = {
    providerName: normalizeProviderName(model.providerName),
    apiFormat: String(model.apiFormat || 'openai_chat').trim() || 'openai_chat',
    baseUrl: String(model.baseUrl || '').trim(),
    apiKey: String(model.apiKey || ''),
    model: modelId,
    temperature: Number.isFinite(Number(model.temperature)) ? Number(model.temperature) : 0.2,
    maxTokens: Number.isFinite(Number(model.maxTokens)) && Number(model.maxTokens) > 0 ? Math.trunc(Number(model.maxTokens)) : 128000,
    contextWindow: Number.isFinite(Number(model.contextWindow)) && Number(model.contextWindow) > 0 ? Math.trunc(Number(model.contextWindow)) : 1048576,
    reasoningTag: String(model.reasoningTag || 'reasoning_content').trim() || 'reasoning_content',
    tokenParam: normalizeTokenParam(model.tokenParam),
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
