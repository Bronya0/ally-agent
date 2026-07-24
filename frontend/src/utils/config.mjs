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
    tokenParam: 'auto',
    customPrompt: '',
    allowPrivateNetwork: true,
    gitBashPath: '',
    proxyMode: 'off',
    proxyUrl: '',
    proxyNoProxy: '',
    userAgent: '',
    reasoningTag: 'reasoning_content',
    disabledSkills: [],
    models: [],
    llmRetries: 2,
  };
}

export function assignConfig(target, source) {
  const next = {
    ...defaultConfig(),
    ...(source || {}),
  };
  delete next.systemPrompt;
  next.reasoningTag = String(next.reasoningTag || '').trim() || 'reasoning_content';
  next.models = cloneModelConfigs(next.models);
  delete target.systemPrompt;
  Object.assign(target, next);
}

export function cloneModelConfigs(models) {
  if (!Array.isArray(models)) return [];
  return models.map((model) => ({
    ...(model || {}),
    reasoningTag: String(model?.reasoningTag || '').trim() || 'reasoning_content',
    tokenParam: String(model?.tokenParam || '').trim() || 'auto',
  }));
}
