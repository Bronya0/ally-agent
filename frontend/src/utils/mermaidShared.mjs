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

// 全局默认主题（暗/亮模式共用）：与内置八色语义卡同源的
// “蓝主色 + 橙/绿辅色”清透卡通配色——默认节点浅蓝底、蓝边框、
// 深蓝灰文字，连线中性蓝灰；背景保持纯白。
const MERMAID_DARK_VARIABLES = {
  darkMode: false, // 强制浅亮通透基底，保证文字与高光节点对比度清晰
  background: '#ffffff',
  mainBkg: '#eef4ff',
  secondBkg: '#f4f8ff',
  nodeBorder: '#4c7dff',
  primaryColor: '#eef4ff',
  primaryTextColor: '#1f2d3d',
  primaryBorderColor: '#4c7dff',
  secondaryColor: '#fff7ed',
  secondaryTextColor: '#7a3e00',
  secondaryBorderColor: '#f08c00',
  tertiaryColor: '#e2f7ec',
  tertiaryTextColor: '#145a32',
  tertiaryBorderColor: '#27ae60',
  lineColor: '#7e8ea3',
  textColor: '#1f2d3d',
  nodeTextColor: '#1f2d3d',
  noteBkgColor: '#fefce8',
  noteTextColor: '#713f12',
  noteBorderColor: '#fde047',
  actorBkg: '#eef4ff',
  actorBorder: '#4c7dff',
  actorTextColor: '#1d3f8f',
  actorLineColor: '#7e8ea3',
  signalColor: '#7e8ea3',
  signalTextColor: '#1f2d3d',
  labelBoxBkgColor: '#f4f8ff',
  labelBoxBorderColor: '#d5e1f5',
  labelTextColor: '#1f2d3d',
  loopTextColor: '#713f12',
  activationBorderColor: '#4c7dff',
  activationBkgColor: '#eef4ff',
  sequenceNumberColor: '#ffffff',
  clusterBkg: '#f5f8ff',
  clusterBorder: '#d5e1f5',
  attributeBackgroundColorOdd: '#ffffff',
  attributeBackgroundColorEven: '#e3efff',
  attributeTextColor: '#1f2d3d',
  // Timeline 刻度与分段全套主题色阶
  cScale0: '#4c7dff',
  cScaleLabel0: '#ffffff',
  cScale1: '#f08c00',
  cScaleLabel1: '#ffffff',
  cScale2: '#27ae60',
  cScaleLabel2: '#ffffff',
  cScale3: '#2aa5d2',
  cScaleLabel3: '#ffffff',
  cScale4: '#8b5cf6',
  cScaleLabel4: '#ffffff',
  cScale5: '#e5484d',
  cScaleLabel5: '#ffffff',
  cScale6: '#b45cf6',
  cScaleLabel6: '#ffffff',
  cScale7: '#0d9488',
  cScaleLabel7: '#ffffff',
  timelineEventBkg: '#f4f8ff',
  timelineEventBorder: '#d5e1f5',
  // Journey 旅程图专属清透调色
  journeySectionBkgColor: '#f5f8ff',
  journeySectionBorderColor: '#d5e1f5',
  journeySectionTextColor: '#1f2d3d',
  journeyTaskBkgColor: '#eef4ff',
  journeyTaskTextColor: '#1f2d3d',
  journeyTaskBorderColor: '#d5e1f5',
  faceColor: '#fef08a',
  faceStroke: '#854d0e',
  // 流程图连线标签（线上的节点）底色：theme-base 唯一残留的默认灰
  edgeLabelBackground: '#f4f8ff',
  // 饼图：12 色阶 + 文字统一主题色
  pie1: '#4c7dff',
  pie2: '#f08c00',
  pie3: '#27ae60',
  pie4: '#8b5cf6',
  pie5: '#2aa5d2',
  pie6: '#e5484d',
  pie7: '#b45cf6',
  pie8: '#0d9488',
  pie9: '#eab308',
  pie10: '#ec4899',
  pie11: '#6366f1',
  pie12: '#64748b',
  pieTitleTextColor: '#1f2d3d',
  pieSectionTextColor: '#1f2d3d',
  pieLegendTextColor: '#1f2d3d',
  pieStrokeColor: '#ffffff',
  // 甘特图：任务 / 分区 / 关键路径 / 今日线
  taskBkgColor: '#eef4ff',
  taskBorderColor: '#4c7dff',
  taskTextColor: '#1f2d3d',
  taskTextOutsideColor: '#1f2d3d',
  activeTaskBkgColor: '#fff7ed',
  activeTaskBorderColor: '#f08c00',
  doneTaskBkgColor: '#e2f7ec',
  doneTaskBorderColor: '#27ae60',
  critBkgColor: '#ffecec',
  critBorderColor: '#e5484d',
  sectionBkgColor: '#f5f8ff',
  altSectionBkgColor: '#ffffff',
  sectionBkgColor2: '#eef4ff',
  gridColor: '#d5e1f5',
  todayLineColor: '#f08c00',
  // gitGraph：分支线 / 提交 / 标签
  git0: '#4c7dff',
  git1: '#f08c00',
  git2: '#27ae60',
  git3: '#8b5cf6',
  git4: '#2aa5d2',
  git5: '#e5484d',
  git6: '#b45cf6',
  git7: '#0d9488',
  gitBranchLabel0: '#ffffff',
  gitBranchLabel1: '#ffffff',
  gitBranchLabel2: '#ffffff',
  gitBranchLabel3: '#ffffff',
  gitBranchLabel4: '#ffffff',
  gitBranchLabel5: '#ffffff',
  gitBranchLabel6: '#ffffff',
  gitBranchLabel7: '#ffffff',
  commitLabelColor: '#1f2d3d',
  commitLabelBackground: '#f4f8ff',
  tagLabelColor: '#1f2d3d',
  tagLabelBackground: '#eef4ff',
  tagLabelBorder: '#4c7dff',
};

// 浅色主题：与深色统一共享这套蓝主色调色板。
const MERMAID_LIGHT_VARIABLES = {
  ...MERMAID_DARK_VARIABLES,
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
          curve: 'basis',
          useMaxWidth: true,
          nodeSpacing: 40,
          rankSpacing: 52,
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
        themeCSS: `
          /* 全局基础节点：精致细边框、大圆角、清透浅蓝卡片 */
          .node rect, .node polygon, .node circle, .node ellipse {
            stroke-width: 1.5px;
            rx: 8px;
            ry: 8px;
            filter: drop-shadow(0 1px 2px rgba(0, 0, 0, 0.04));
          }
          .edgePath .path, .flowchart-link {
            stroke-width: 1.6px;
            stroke: #7e8ea3;
          }
          .edgeLabel {
            background-color: #f4f8ff !important;
            padding: 3px 6px;
            border-radius: 6px;
            color: #1f2d3d !important;
            fill: #1f2d3d !important;
            font-size: 12px;
            border: 1px solid #d5e1f5;
          }
          /* htmlLabels:false 时连线标签的背景矩形，双保险兜底 */
          .edgeLabel rect, .edgeLabel .labelBkg {
            fill: #f4f8ff !important;
            stroke: none !important;
          }
          .nodeLabel, .label text {
            font-weight: 500;
            letter-spacing: 0.2px;
            fill: #1f2d3d !important;
            color: #1f2d3d !important;
          }
          .cluster rect {
            rx: 10px;
            ry: 10px;
            stroke-width: 1.2px;
            fill: #f5f8ff !important;
            stroke: #d5e1f5 !important;
          }
          .er.entityBox {
            stroke-width: 1.5px;
            rx: 8px;
          }

          /* =========================================================================
             内置 Pastel / Soft Tint（马卡龙 / 冰淇淋）组件专属类名（直接 :::xxx 可用）
             ========================================================================= */
          /* 1. 五大清透卡片色阶 (蓝 / 橙 / 粉 / 绿 / 紫) */
          .node.blue rect, .node.blue polygon, .node.cardDesign rect, .node.cardV1 rect, .node.nodeBlue rect {
            fill: #eff6ff !important;
            stroke: #bfdbfe !important;
          }
          .node.orange rect, .node.orange polygon, .node.cardBuild rect, .node.nodeOrange rect {
            fill: #fff7ed !important;
            stroke: #fed7aa !important;
          }
          .node.pink rect, .node.pink polygon, .node.cardTest rect, .node.nodePink rect {
            fill: #fdf2f8 !important;
            stroke: #fbcfe8 !important;
          }
          .node.green rect, .node.green polygon, .node.cardLaunch rect, .node.nodeGreen rect {
            fill: #f0fdf4 !important;
            stroke: #bbf7d0 !important;
          }
          .node.purple rect, .node.purple polygon, .node.nodePurple rect {
            fill: #f5f3ff !important;
            stroke: #ddd6fe !important;
          }

          /* 2. 鲜明小圆点 (轴线端点 / 状态指示) */
          .node.dot-blue circle, .node.dotBlue circle {
            fill: #2563eb !important;
            stroke: #93c5fd !important;
            stroke-width: 2px !important;
          }
          .node.dot-orange circle, .node.dotOrange circle {
            fill: #f97316 !important;
            stroke: #fed7aa !important;
            stroke-width: 2px !important;
          }
          .node.dot-pink circle, .node.dotPink circle {
            fill: #ec4899 !important;
            stroke: #fbcfe8 !important;
            stroke-width: 2px !important;
          }
          .node.dot-green circle, .node.dotGreen circle {
            fill: #16a34a !important;
            stroke: #bbf7d0 !important;
            stroke-width: 2px !important;
          }
          .node.dot-purple circle, .node.dotPurple circle {
            fill: #8b5cf6 !important;
            stroke: #c4b5fd !important;
            stroke-width: 2px !important;
          }

          /* 3. 强调 Badge 徽标与起止药丸端点 */
          .node.badge polygon, .node.badge rect, .node.v1Badge polygon, .node.v1Badge rect {
            fill: #2563eb !important;
            stroke: #1d4ed8 !important;
          }
          .node.badge text, .node.v1Badge text {
            fill: #ffffff !important;
            color: #ffffff !important;
            font-weight: 600 !important;
          }
          .node.endpoint rect, .node.endPoint rect {
            fill: #ffffff !important;
            stroke: #18181b !important;
            stroke-width: 1.5px !important;
          }

          /* 4. 聊天循环语义八色卡（与上方五色卡并存）：图中 :::user 等直接引用，
             无需本地 classDef。注意：这 8 个类名成为保留名，图内同名 classDef
             会被这里的 !important 规则覆盖（与 blue/orange 等既有类一致）。 */
          .node.user rect, .node.user polygon, .node.user circle, .node.user ellipse {
            fill: #ffe8cc !important;
            stroke: #f08c00 !important;
            stroke-width: 1.5px !important;
          }
          .node.user .nodeLabel, .node.user .label text {
            fill: #7a3e00 !important;
            color: #7a3e00 !important;
          }
          .node.core rect, .node.core polygon, .node.core circle, .node.core ellipse {
            fill: #e3efff !important;
            stroke: #4c7dff !important;
          }
          .node.core .nodeLabel, .node.core .label text {
            fill: #1d3f8f !important;
            color: #1d3f8f !important;
          }
          .node.llm rect, .node.llm polygon, .node.llm circle, .node.llm ellipse {
            fill: #ede7ff !important;
            stroke: #8b5cf6 !important;
          }
          .node.llm .nodeLabel, .node.llm .label text {
            fill: #4b1d9e !important;
            color: #4b1d9e !important;
          }
          .node.prov rect, .node.prov polygon, .node.prov circle, .node.prov ellipse {
            fill: #f6e8ff !important;
            stroke: #b45cf6 !important;
          }
          .node.prov .nodeLabel, .node.prov .label text {
            fill: #6b1d9e !important;
            color: #6b1d9e !important;
          }
          .node.ev rect, .node.ev polygon, .node.ev circle, .node.ev ellipse {
            fill: #dff5ff !important;
            stroke: #2aa5d2 !important;
          }
          .node.ev .nodeLabel, .node.ev .label text {
            fill: #0b556b !important;
            color: #0b556b !important;
          }
          .node.exec rect, .node.exec polygon, .node.exec circle, .node.exec ellipse {
            fill: #e2f7ec !important;
            stroke: #27ae60 !important;
          }
          .node.exec .nodeLabel, .node.exec .label text {
            fill: #145a32 !important;
            color: #145a32 !important;
          }
          .node.fin rect, .node.fin polygon, .node.fin circle, .node.fin ellipse {
            fill: #f1f3f5 !important;
            stroke: #8492a6 !important;
          }
          .node.fin .nodeLabel, .node.fin .label text {
            fill: #333333 !important;
            color: #333333 !important;
          }
          .node.risk rect, .node.risk polygon, .node.risk circle, .node.risk ellipse {
            fill: #ffecec !important;
            stroke: #e5484d !important;
            stroke-dasharray: 5 4 !important;
          }
          .node.risk .nodeLabel, .node.risk .label text {
            fill: #7a1f22 !important;
            color: #7a1f22 !important;
          }
        `,
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
