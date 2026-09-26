/*
 * SPDX-License-Identifier: GPL-3.0-only
 *
 * Copyright (C) 2026 tangssst <tangssst@qq.com>
 * GitHub: https://github.com/Bronya0/ally-agent
 *
 * This file is part of ally-agent, licensed under the GNU General
 * Public License v3. See the LICENSE file for details.
 */
import { normalizeApiKeysArray, normalizeReasoningEffort } from './modelConfigIO.mjs';

// defaultConfig 只描述持久化配置里**非模型**的部分：模型一律是 models[] 里的预设，
// 界面在用的那个只存一个身份（lastUsedModel），其余模型字段由后端按身份展开
// （见 internal/app ConfigState 与 expandLastUsedModel）。界面上"一条模型都没
// 配置"时的出厂占位见 placeholderModel，它不是配置项。
export function defaultConfig() {
  return {
    workspace: '',
    // Knowledge-base root directory. A session whose workspace resolves to
    // this path runs in KB mode (KB system prompt + read-only sources/).
    kbRoot: '',
    customPrompt: '',
    allowPrivateNetwork: true,
    gitBashPath: '',
    proxyMode: 'off',
    proxyUrl: '',
    proxyNoProxy: '',
    userAgent: '',
    disabledSkills: [],
    models: [],
    // 最近使用模型身份（{providerName, model}，指向 models[] 里的一条）：新 Tab 的
    // 模型种子，也是后端给 HTTP API 会话与计划任务展开模型的依据。
    lastUsedModel: null,
    llmRetries: 6,
    // Post-write auto validation is opt-in: each language stays off until the
    // user enables it in Settings. Backend stores *bool (nil = disabled).
    autoValidationPython: false,
    autoValidationGo: false,
    autoValidationJavaScript: false,
    autoValidationTypeScript: false,
    autoValidationVue: false,
    autoValidationJava: false,
    autoValidationJson: false,
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
    // Remembered user window size (px). Zero means no manual resize yet;
    // the window then opens at 61.8% of the primary screen. The backend
    // saves these after the user drags the window edge.
    windowWidth: 0,
    windowHeight: 0,
    // Auto-compaction threshold as a fraction of the context window
    // (0.6 = 60%). Backend clamps to [0.1, 0.95]; zero (legacy config
    // without the field) is replaced with the default in mergeConfig.
    compactThreshold: 0.6,
    // Manual/auto compaction LLM call timeout in seconds. Backend clamps
    // to [30, 3600]; zero (legacy config) falls back to the default.
    compactTimeoutSeconds: 180,
    // Message body / welcome greeting font size in px. Backend clamps to
    // [12, 24] on save; zero (legacy config) is replaced by the default in
    // assignConfig below.
    messageFontSize: 15.5,
    // Code content / tool card / secondary text / auxiliary text font sizes
    // in px. Zero (legacy config) is replaced by the defaults in assignConfig
    // below.
    codeFontSize: 14,
    toolFontSize: 15,
    subFontSize: 13,
    auxFontSize: 12,
  };
}

export function assignConfig(target, source) {
  const next = {
    ...defaultConfig(),
    ...(source || {}),
  };
  delete next.systemPrompt;
  // lastUsedModel 是 {providerName, model} 身份：缺 model id 视为"没有"（null）。
  const lastUsed = next.lastUsedModel;
  next.lastUsedModel = lastUsed && String(lastUsed.model || '').trim()
    ? { providerName: String(lastUsed.providerName || '').trim(), model: String(lastUsed.model).trim() }
    : null;
  next.models = cloneModelConfigs(next.models);
  // Backend stores autoUpdate as *bool (nil = default on). Normalize null /
  // undefined back to true so the frontend always sees a real boolean.
  next.autoUpdate = next.autoUpdate === false ? false : true;
  // Backend stores the autoValidation* flags as *bool with nil = disabled;
  // only an explicit true keeps a check enabled.
  next.autoValidationPython = next.autoValidationPython === true;
  next.autoValidationGo = next.autoValidationGo === true;
  next.autoValidationJavaScript = next.autoValidationJavaScript === true;
  next.autoValidationTypeScript = next.autoValidationTypeScript === true;
  next.autoValidationVue = next.autoValidationVue === true;
  next.autoValidationJava = next.autoValidationJava === true;
  next.autoValidationJson = next.autoValidationJson === true;
  // compactThreshold: legacy configs without the field (or explicit 0) fall
  // back to the default so the slider shows the effective value, not 0.
  next.compactThreshold = Number(next.compactThreshold) > 0
    ? Math.min(0.95, Math.max(0.2, Number(next.compactThreshold)))
    : 0.6;
  // compactTimeoutSeconds: same fallback; clamp to the backend's [30, 3600]
  // range so the settings input never shows an empty or absurd value.
  next.compactTimeoutSeconds = Number(next.compactTimeoutSeconds) > 0
    ? Math.min(3600, Math.max(30, Math.round(Number(next.compactTimeoutSeconds))))
    : 180;
  // messageFontSize: same fallback; clamp to the same readable range the
  // backend enforces so the UI never shows an empty or absurd value.
  next.messageFontSize = Number(next.messageFontSize) > 0
    ? Math.min(24, Math.max(12, Number(next.messageFontSize)))
    : 15.5;
  // codeFontSize / toolFontSize / subFontSize / auxFontSize: same
  // zero-means-default fallback, clamped to the backend's readable ranges.
  next.codeFontSize = Number(next.codeFontSize) > 0
    ? Math.min(24, Math.max(12, Number(next.codeFontSize)))
    : 14;
  next.toolFontSize = Number(next.toolFontSize) > 0
    ? Math.min(24, Math.max(12, Number(next.toolFontSize)))
    : 15;
  next.subFontSize = Number(next.subFontSize) > 0
    ? Math.min(18, Math.max(11, Number(next.subFontSize)))
    : 13;
  next.auxFontSize = Number(next.auxFontSize) > 0
    ? Math.min(20, Math.max(10, Number(next.auxFontSize)))
    : 12;
  if (!Array.isArray(next.skippedUpdates)) next.skippedUpdates = [];
  delete target.systemPrompt;
  Object.assign(target, next);
}

// placeholderModel 是"一条模型都没配置"时界面上的出厂占位：新模型编辑器的预填、
// composer 与欢迎表格的模型行。它不是配置项：不进 config、不落盘，用户配好第一个
// 模型后就会被真实预设取代。
export function placeholderModel() {
  return {
    providerName: 'OpenAI Compatible',
    apiFormat: 'openai_chat',
    baseUrl: 'https://api.deepseek.com',
    apiKey: '',
    apiKeys: [],
    model: 'deepseek-v4-flash',
    maxTokens: 131072,
    contextWindow: 1000000,
    tokenParam: 'auto',
    reasoningTag: 'reasoning_content',
    reasoningEffort: 'max',
    customHeaders: null,
  };
}

function cloneModelConfigs(models) {
  if (!Array.isArray(models)) return [];
  return models.map((model) => ({
    ...(model || {}),
    reasoningTag: String(model?.reasoningTag || '').trim() || 'reasoning_content',
    reasoningEffort: normalizeReasoningEffort(model?.reasoningEffort),
    tokenParam: String(model?.tokenParam || '').trim() || 'auto',
    customHeaders: model?.customHeaders && Object.keys(model.customHeaders).length
      ? { ...model.customHeaders }
      : null,
    apiKeys: Array.isArray(model?.apiKeys) && model.apiKeys.length
      ? normalizeApiKeysArray(model.apiKeys)
      : (model?.apiKey ? [model.apiKey] : []),
  }));
}
