import { normalizeApiKeysArray, normalizeReasoningEffort } from './modelConfigIO.mjs';

export function defaultConfig() {
  return {
    providerName: 'OpenAI Compatible',
    apiFormat: 'openai_chat',
    baseUrl: 'https://api.deepseek.com',
    apiKey: '',
    apiKeys: [],
    model: 'deepseek-v4-flash',
    workspace: '',
    temperature: 0.2,
    maxTokens: 128000,
    contextWindow: 1048576,
    tokenParam: 'auto',
    customPrompt: '',
    allowPrivateNetwork: true,
    gitBashPath: '',
    proxyMode: 'off',
    proxyUrl: '',
    proxyNoProxy: '',
    userAgent: '',
    reasoningTag: 'reasoning_content',
    reasoningEffort: 'auto',
    disabledSkills: [],
    models: [],
    llmRetries: 2,
    // Auto-update defaults to on. Loaded config may store explicit false to
    // opt out; legacy config without the field is treated as enabled by the
    // backend (*bool pointer).
    autoUpdate: true,
    skippedUpdates: [],
    // Custom chat background. Backend stores BackgroundImage as a filename
    // under ~/.ally_agent and BackgroundOpacity in [0, 1]. Frontend keeps a
    // local data URL cache (not persisted here) plus the opacity slider value.
    backgroundImage: '',
    backgroundOpacity: 0.15,
  };
}

export function assignConfig(target, source) {
  const next = {
    ...defaultConfig(),
    ...(source || {}),
  };
  delete next.systemPrompt;
  next.reasoningTag = String(next.reasoningTag || '').trim() || 'reasoning_content';
  next.reasoningEffort = normalizeReasoningEffort(next.reasoningEffort);
  next.apiKeys = normalizeApiKeysArray(next.apiKeys).length
    ? normalizeApiKeysArray(next.apiKeys)
    : (next.apiKey ? [next.apiKey] : []);
  next.models = cloneModelConfigs(next.models);
  // Backend stores autoUpdate as *bool (nil = default on). Normalize null /
  // undefined back to true so the frontend always sees a real boolean.
  next.autoUpdate = next.autoUpdate === false ? false : true;
  if (!Array.isArray(next.skippedUpdates)) next.skippedUpdates = [];
  delete target.systemPrompt;
  Object.assign(target, next);
}

function cloneModelConfigs(models) {
  if (!Array.isArray(models)) return [];
  return models.map((model) => ({
    ...(model || {}),
    reasoningTag: String(model?.reasoningTag || '').trim() || 'reasoning_content',
    reasoningEffort: normalizeReasoningEffort(model?.reasoningEffort),
    tokenParam: String(model?.tokenParam || '').trim() || 'auto',
    apiKeys: Array.isArray(model?.apiKeys) && model.apiKeys.length
      ? normalizeApiKeysArray(model.apiKeys)
      : (model?.apiKey ? [model.apiKey] : []),
  }));
}
