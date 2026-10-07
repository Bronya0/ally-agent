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

// 自动压缩阈值的默认值与 clamp 范围：与后端同一组数（Go 侧 defaultCompactThreshold /
// clampCompactThreshold）。跨语言没法共用常量，所以由 config.test.mjs 读 Go 源码把两边
// 钉在一起。以前这里只靠一句注释说“Backend clamps to [0.1, 0.95]”，而实际范围是
// [0.2, 0.95]——没有任何守护的副本就是这么漂的。
export const COMPACT_THRESHOLD_DEFAULT = 0.6;
export const COMPACT_THRESHOLD_MIN = 0.2;
export const COMPACT_THRESHOLD_MAX = 0.95;

// 可关闭的页面键：Agent（chat）与设置（settings）永不隐藏。与后端
// sanitizeHiddenModes 同一组键，两边漂移会导致“界面关了、后端又放回来”。
export const HIDEABLE_MODES = ['kb', 'skills', 'mcp', 'models', 'ssh', 'stats', 'games'];

// 可关闭页面的显示名（i18n 键）：设置页的开关列表由 HIDEABLE_MODES 派生，键集合
// 必须与它完全一致——漏一个键就少一个开关（点了没反应），多一个键是死配置。
// config.test.mjs 钉住两边集合相等。
export const PAGE_VISIBILITY_LABELS = {
  kb: 'app.mode.kb',
  skills: 'app.mode.skills',
  mcp: 'app.mode.mcp',
  models: 'app.mode.models',
  ssh: 'app.mode.sshCluster',
  stats: 'header.tokenStats',
  games: 'header.games',
};

export function normalizeHiddenModes(modes) {
  if (!Array.isArray(modes)) return [];
  const out = [];
  const seen = new Set();
  for (const item of modes) {
    const key = String(item || '').trim().toLowerCase();
    if (!key || seen.has(key) || !HIDEABLE_MODES.includes(key)) continue;
    seen.add(key);
    out.push(key);
  }
  return out;
}

// cloneConfigDraft 归一 hiddenModes：SettingsModal 与 ModelsPanel 共用这一处，
// 免得两份草稿拷贝漂移（只留可关闭的页面键，chat/settings 永不隐藏）。
export function normalizeDraftHiddenModes(draft) {
  if (draft && typeof draft === 'object') {
    draft.hiddenModes = normalizeHiddenModes(draft.hiddenModes);
  }
  return draft;
}

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
    // 左侧模式栏的隐藏页面名单（mode key 小写）：知识库（kb）与设置（settings）
    // 永不隐藏，后端 sanitizeHiddenModes 同一组键。空数组 = 全部可见。
    hiddenModes: [],
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
    // Auto-compaction threshold as a fraction of the context window (see the
    // constants above: 0.6 = 60%). The backend clamps it to the same
    // [COMPACT_THRESHOLD_MIN, COMPACT_THRESHOLD_MAX] range; zero (legacy config
    // without the field) is replaced with the default in mergeConfig.
    compactThreshold: COMPACT_THRESHOLD_DEFAULT,
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
    ? Math.min(COMPACT_THRESHOLD_MAX, Math.max(COMPACT_THRESHOLD_MIN, Number(next.compactThreshold)))
    : COMPACT_THRESHOLD_DEFAULT;
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
  // hiddenModes：只留可关闭的页面键（chat/settings 永不隐藏，未知键丢弃），与后端
  // sanitizeHiddenModes 同一组键；非数组输入回到全部可见。
  next.hiddenModes = normalizeHiddenModes(next.hiddenModes);
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
