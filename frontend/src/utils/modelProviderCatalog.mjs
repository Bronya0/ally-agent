/*
 * SPDX-License-Identifier: GPL-3.0-only
 *
 * Copyright (C) 2026 tangssst <tangssst@qq.com>
 * GitHub: https://github.com/Bronya0/ally-agent
 *
 * This file is part of ally-agent, licensed under the GNU General
 * Public License v3. See the LICENSE file for details.
 */
export const CUSTOM_PROVIDER_ID = '__custom__';

export function naturalCompare(left, right) {
  return String(left || '').localeCompare(String(right || ''), undefined, {
    numeric: true,
    sensitivity: 'base',
  });
}

export function providerCatalogOptions(catalog, customLabel) {
  const providers = Array.isArray(catalog?.providers) ? catalog.providers : [];
  return [
    ...providers.map((provider) => ({
      label: provider.name || provider.id,
      value: provider.id,
    })),
    { label: customLabel, value: CUSTOM_PROVIDER_ID },
  ];
}

export function providerModelOptions(provider) {
  return (Array.isArray(provider?.models) ? provider.models : []).map((model) => ({
    label: model.name && model.name !== model.id ? `${model.name} · ${model.id}` : model.id,
    value: model.id,
  }));
}

export function findCatalogProvider(catalog, providerId) {
  return (Array.isArray(catalog?.providers) ? catalog.providers : [])
    .find((provider) => provider.id === providerId) || null;
}

export function findCatalogModel(provider, modelId) {
  return (Array.isArray(provider?.models) ? provider.models : [])
    .find((model) => model.id === modelId) || null;
}

export function applyCatalogPreset(provider, model, current = {}) {
  if (!provider || !model) return { ...current };
  return {
    ...current,
    providerName: String(provider.name || provider.id || '').trim(),
    apiFormat: String(provider.apiFormat || 'openai_chat').trim(),
    baseUrl: String(provider.baseUrl || '').trim(),
    model: String(model.id || '').trim(),
    maxTokens: Number(model.maxTokens) || Number(current.maxTokens) || 131072,
    contextWindow: Number(model.contextWindow) || Number(current.contextWindow) || 1000000,
    reasoningTag: String(model.reasoningTag || current.reasoningTag || 'reasoning_content').trim() || 'reasoning_content',
  };
}
