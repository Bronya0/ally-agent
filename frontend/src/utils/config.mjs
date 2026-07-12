export function defaultConfig() {
  return {
    providerName: 'OpenAI Compatible',
    apiFormat: 'openai_chat',
    baseUrl: 'https://api.deepseek.com',
    apiKey: '',
    model: 'deepseek-v4-flash',
    workspace: '',
    temperature: 0.2,
    maxTokens: 128000,
    contextWindow: 1048576,
    customPrompt: '',
    allowPrivateNetwork: true,
    disabledSkills: [],
    models: [],
  };
}

export function assignConfig(target, source) {
  const next = {
    ...defaultConfig(),
    ...(source || {}),
  };
  delete next.systemPrompt;
  next.models = cloneModelConfigs(next.models);
  delete target.systemPrompt;
  Object.assign(target, next);
}

export function cloneModelConfigs(models) {
  if (!Array.isArray(models)) return [];
  return models.map((model) => ({ ...(model || {}) }));
}
