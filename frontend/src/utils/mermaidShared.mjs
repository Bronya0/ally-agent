// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.

// Mermaid 渲染的共享基座：围栏语言识别、源码归一化与懒加载初始化。
// 聊天流（App.vue）与工作区编辑器的 Markdown 预览共用同一份
// 主题配置，避免两处初始化漂移。

// 围栏语言 → 缺省图型指令。裸 mermaid/mmd 假定源码自带指令行，
// 其余别名补上对应的图型声明，让 ```flowchart 这类写法也能渲染。
const MERMAID_FENCE_DIRECTIVES = new Map([
  ['mermaid', ''],
  ['mmd', ''],
  ['flowchart', 'flowchart TD'],
  ['graph', 'graph TD'],
  ['sequencediagram', 'sequenceDiagram'],
  ['sequence', 'sequenceDiagram'],
  ['classdiagram', 'classDiagram'],
  ['statediagram', 'stateDiagram'],
  ['statediagram-v2', 'stateDiagram-v2'],
  ['erdiagram', 'erDiagram'],
  ['journey', 'journey'],
  ['gantt', 'gantt'],
  ['pie', 'pie'],
  ['gitgraph', 'gitGraph'],
  ['mindmap', 'mindmap'],
  ['timeline', 'timeline'],
  ['quadrantchart', 'quadrantChart'],
  ['requirementdiagram', 'requirementDiagram'],
  ['c4diagram', 'c4Diagram'],
  ['sankey-beta', 'sankey-beta'],
  ['xychart-beta', 'xychart-beta'],
  ['block-beta', 'block-beta'],
  ['architecture-beta', 'architecture-beta'],
  ['packet-beta', 'packet-beta'],
]);

// 源码已经以图型指令开头的判定（可带 frontmatter）。
const MERMAID_SOURCE_START_RE = /^(?:---[\s\S]*?---\s*)?(?:flowchart|graph|sequenceDiagram|classDiagram|stateDiagram(?:-v2)?|erDiagram|journey|gantt|pie|gitGraph|mindmap|timeline|quadrantChart|requirementDiagram|c4Diagram|sankey-beta|xychart-beta|block-beta|architecture-beta|packet-beta)\b/i;

export function mermaidFenceSpec(lang) {
  const raw = String(lang || '').trim();
  if (!raw) return null;
  const first = raw.split(/\s+/)[0].toLowerCase();
  if (!MERMAID_FENCE_DIRECTIVES.has(first)) return null;
  return {
    raw,
    first,
    directive: MERMAID_FENCE_DIRECTIVES.get(first),
  };
}

export function normalizeMermaidSource(code, spec) {
  const source = String(code || '').trim();
  if (!source || !spec?.directive) return source;
  if (MERMAID_SOURCE_START_RE.test(source)) return source;
  if (spec.first === 'flowchart' || spec.first === 'graph') {
    const directive = /\s/.test(spec.raw) ? spec.raw : spec.directive;
    return `${directive}\n${source}`;
  }
  return `${spec.directive}\n${source}`;
}

let mermaidModulePromise = null;
let mermaidInitialized = false;
let mermaidThemeMode = null;

// 当前颜色模式：与 utils/theme.mjs 写在 <html data-mode> 上的值保持一致。
function currentColorMode() {
  try {
    return document.documentElement.getAttribute('data-mode') === 'light' ? 'light' : 'dark';
  } catch {
    return 'dark';
  }
}

// 暗色主题（Darcula 派生）。
const MERMAID_DARK_VARIABLES = {
  darkMode: true,
  background: '#2b2b2b',
  mainBkg: '#323232',
  secondBkg: '#383838',
  primaryColor: '#323232',
  primaryTextColor: '#a9b7c6',
  primaryBorderColor: '#cc7832',
  secondaryColor: '#353535',
  secondaryTextColor: '#a9b7c6',
  secondaryBorderColor: '#6897bb',
  tertiaryColor: '#303330',
  tertiaryTextColor: '#a9b7c6',
  tertiaryBorderColor: '#6a8759',
  lineColor: '#808080',
  textColor: '#a9b7c6',
  nodeTextColor: '#a9b7c6',
  noteBkgColor: '#3b352b',
  noteTextColor: '#d7ba7d',
  noteBorderColor: '#bbb529',
  actorBkg: '#323232',
  actorBorder: '#6897bb',
  actorTextColor: '#a9b7c6',
  actorLineColor: '#666666',
  signalColor: '#a9b7c6',
  signalTextColor: '#a9b7c6',
  labelBoxBkgColor: '#323232',
  labelBoxBorderColor: '#6a8759',
  labelTextColor: '#a9b7c6',
  loopTextColor: '#d7ba7d',
  activationBorderColor: '#cc7832',
  activationBkgColor: '#3b332b',
  sequenceNumberColor: '#2b2b2b',
  clusterBkg: '#2f2f2f',
  clusterBorder: '#5d5d5d',
};

// 浅色主题：白纸底 + 墨色文字 + 同族强调色（与 light 模式的
// --ally-* token 色阶保持一致的浅色系）。
const MERMAID_LIGHT_VARIABLES = {
  darkMode: false,
  background: '#ffffff',
  mainBkg: '#f2f4f7',
  secondBkg: '#e9edf2',
  primaryColor: '#f2f4f7',
  primaryTextColor: '#2c333c',
  primaryBorderColor: '#d08c3c',
  secondaryColor: '#eef1f5',
  secondaryTextColor: '#2c333c',
  secondaryBorderColor: '#5b8db8',
  tertiaryColor: '#f5f3ef',
  tertiaryTextColor: '#2c333c',
  tertiaryBorderColor: '#6a8759',
  lineColor: '#8b93a0',
  textColor: '#2c333c',
  nodeTextColor: '#2c333c',
  noteBkgColor: '#faf3e2',
  noteTextColor: '#7a5f1e',
  noteBorderColor: '#b8a44a',
  actorBkg: '#f2f4f7',
  actorBorder: '#5b8db8',
  actorTextColor: '#2c333c',
  actorLineColor: '#98a0ab',
  signalColor: '#2c333c',
  signalTextColor: '#2c333c',
  labelBoxBkgColor: '#f2f4f7',
  labelBoxBorderColor: '#6a8759',
  labelTextColor: '#2c333c',
  loopTextColor: '#7a5f1e',
  activationBorderColor: '#d08c3c',
  activationBkgColor: '#f7efe0',
  sequenceNumberColor: '#ffffff',
  clusterBkg: '#eef1f5',
  clusterBorder: '#c9d1da',
};

// 懒加载并初始化 mermaid（幂等；主题跟随当前颜色模式，模式切换后
// 再次调用会以新主题重新 initialize）。聊天流与编辑器预览共用。
export function loadMermaid() {
  const mode = currentColorMode();
  if (!mermaidModulePromise) {
    mermaidModulePromise = import('mermaid').then((mod) => {
      return mod.default || mod;
    });
  }
  return mermaidModulePromise.then((mermaid) => {
    if (!mermaidInitialized || mermaidThemeMode !== mode) {
      const variables = mode === 'light' ? MERMAID_LIGHT_VARIABLES : MERMAID_DARK_VARIABLES;
      mermaid.initialize({
        startOnLoad: false,
        securityLevel: 'strict',
        theme: 'base',
        darkMode: mode === 'light',
        htmlLabels: false,
        flowchart: {
          curve: 'linear',
          useMaxWidth: true,
          nodeSpacing: 36,
          rankSpacing: 48,
        },
        sequence: {
          useMaxWidth: true,
          wrap: true,
          diagramMarginX: 24,
          diagramMarginY: 18,
          actorMargin: 48,
        },
        themeVariables: {
          ...variables,
          fontFamily: 'Inter, "Microsoft YaHei", sans-serif',
          fontSize: '14px',
        },
        themeCSS: '.node rect,.node polygon,.node circle,.node ellipse{stroke-width:1.4px}.edgePath .path,.flowchart-link{stroke-width:1.5px}.nodeLabel,.label text{font-weight:500}',
      });
      mermaidInitialized = true;
      mermaidThemeMode = mode;
    }
    return mermaid;
  });
}

export function escapeHtmlText(value) {
  return String(value ?? '')
    .replaceAll('&', '&amp;')
    .replaceAll('<', '&lt;')
    .replaceAll('>', '&gt;')
    .replaceAll('"', '&quot;')
    .replaceAll("'", '&#39;');
}
