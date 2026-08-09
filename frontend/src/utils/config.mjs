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
    maxTokens: 384000,
    contextWindow: 1000000,
    tokenParam: 'auto',
    customPrompt: '',
    allowPrivateNetwork: true,
    gitBashPath: '',
    proxyMode: 'off',
    proxyUrl: '',
    proxyNoProxy: '',
    userAgent: '',
    reasoningTag: 'reasoning_content',
    reasoningEffort: 'max',
    disabledSkills: [],
    models: [],
    llmRetries: 6,
    // Auto-update defaults to on. Loaded config may store explicit false to
    // opt out; legacy config without the field is treated as enabled by the
    // backend (*bool pointer).
    autoUpdate: true,
    skippedUpdates: [],
    // Close-to-tray defaults on: closing the window hides to the system tray
    // instead of quitting. Backend stores it as *bool; an explicit false
    // means closing quits the app.
    closeToTray: true,
    // Custom chat background. Backend stores BackgroundImage as a filename
    // under ~/.ally_agent and BackgroundOpacity in [0, 1]. Frontend keeps a
    // local data URL cache (not persisted here) plus the opacity slider value.
    backgroundImage: '',
    backgroundOpacity: 0.15,
    // Remembered user window size (px). Zero means no manual resize yet;
    // the window then opens at 61.8% of the primary screen. The backend
    // saves these after the user drags the window edge.
    windowWidth: 0,
    windowHeight: 0,
    // Auto-compaction threshold as a fraction of the context window
    // (0.6 = 60%). Backend clamps to [0.1, 0.95]; zero (legacy config
    // without the field) is replaced with the default in mergeConfig.
    compactThreshold: 0.6,
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
  next.closeToTray = next.closeToTray === false ? false : true;
  // compactThreshold: legacy configs without the field (or explicit 0) fall
  // back to the default so the slider shows the effective value, not 0.
  next.compactThreshold = Number(next.compactThreshold) > 0
    ? Math.min(0.95, Math.max(0.2, Number(next.compactThreshold)))
    : 0.6;
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
