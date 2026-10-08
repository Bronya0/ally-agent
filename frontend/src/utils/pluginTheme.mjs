/*
 * SPDX-License-Identifier: GPL-3.0-only
 *
 * Copyright (C) 2026 tangssst <tangssst@qq.com>
 * GitHub: https://github.com/Bronya0/ally-agent
 *
 * This file is part of ally-agent, licensed under the GNU General
 * Public License v3. See the LICENSE file for details.
 */
// 插件主题层。宿主的两轴主题（data-theme 主题族 + data-mode 明暗）都挂在 <html>
// 上，而插件页渲染在同一个文档里，所以插件直接用 var(--ally-*) 就会跟着切换——
// 这一层只为「JS 里需要具体色值」的场景服务（图表、canvas、内联 SVG）。
//
// 观察只盯着那两个属性：属性变更回调而不是轮询，且只在真的变化时才重新取快照。

// PLUGIN_THEME_TOKENS 是给插件的公开 token 契约：只允许依赖清单里的变量，其余
// --ally-* 属于内部实现，随时可能改名。这份清单必须与 docs/plugin-system.md 第 6 节
// 一致（那份文档是唯一对外契约；`plugin-dev` 内置技能还没做，见其第 9 / 13 节）。
export const PLUGIN_THEME_TOKENS = [
  '--ally-surface-content',
  '--ally-surface-panel',
  '--ally-surface-chrome',
  '--ally-surface-raised',
  '--ally-surface-deep',
  '--ally-text-primary',
  '--ally-text-high',
  '--ally-text-body',
  '--ally-text-secondary',
  '--ally-text-soft',
  '--ally-text-tertiary',
  '--ally-text-muted',
  '--ally-text-faint',
  '--ally-text-ghost',
  '--ally-success',
  '--ally-success-text',
  '--ally-danger',
  '--ally-danger-text',
  '--ally-warning',
  '--ally-warning-text',
  '--ally-info',
  '--ally-accent',
  '--ally-accent-ink',
  '--ally-accent-bright',
  '--ally-accent-strong',
  '--ally-hover-faint',
  '--ally-hover-strong',
  '--ally-border-input',
  '--ally-ui-font',
  '--ally-mono-font',
  '--ally-message-font-size',
  '--ally-sub-font-size',
  '--ally-aux-font-size',
  '--ally-composer-blur',
  '--ally-panel-glass',
];

// 主题族的默认值不带属性（见 utils/theme.mjs）：缺属性即 amber / dark。
const DEFAULT_THEME = 'amber';
const DEFAULT_MODE = 'dark';

function readThemeIdentity() {
  if (typeof document === 'undefined') {
    return { name: DEFAULT_THEME, mode: DEFAULT_MODE };
  }
  const root = document.documentElement;
  return {
    name: root.getAttribute('data-theme') || DEFAULT_THEME,
    mode: root.getAttribute('data-mode') === 'light' ? 'light' : DEFAULT_MODE,
  };
}

function readTokens() {
  const tokens = {};
  if (typeof window === 'undefined' || typeof window.getComputedStyle !== 'function') {
    return tokens;
  }
  const styles = window.getComputedStyle(document.documentElement);
  for (const token of PLUGIN_THEME_TOKENS) {
    tokens[token] = String(styles.getPropertyValue(token) || '').trim();
  }
  return tokens;
}

// themeSnapshot 返回当前主题身份与公开 token 的色值快照。
export function themeSnapshot() {
  const identity = readThemeIdentity();
  return { ...identity, tokens: readTokens() };
}

// onThemeChange 订阅「主题族或明暗变化」。返回取消订阅函数。
export function onThemeChange(callback) {
  if (typeof callback !== 'function' || typeof MutationObserver === 'undefined') {
    return () => {};
  }
  let last = readThemeIdentity();
  const observer = new MutationObserver(() => {
    const next = readThemeIdentity();
    if (next.name === last.name && next.mode === last.mode) return;
    last = next;
    callback(themeSnapshot());
  });
  observer.observe(document.documentElement, {
    attributes: true,
    attributeFilter: ['data-theme', 'data-mode'],
  });
  return () => observer.disconnect();
}
