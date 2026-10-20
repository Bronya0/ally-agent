/*
 * SPDX-License-Identifier: GPL-3.0-only
 *
 * Copyright (C) 2026 tangssst <tangssst@qq.com>
 * GitHub: https://github.com/Bronya0/ally-agent
 *
 * This file is part of ally-agent, licensed under the GNU General
 * Public License v3. See the LICENSE file for details.
 */
import echartsAssetUrl from 'echarts/dist/echarts.min.js?url';

export function normalizeHtmlFrameHeight(height) {
  const value = Number(height || 0);
  if (!Number.isFinite(value) || value <= 0) return 0;
  return Math.max(120, Math.min(Math.ceil(value), 600));
}

// Frames share a build-addressed local ECharts asset; standalone downloads
// inline it so they remain fully functional outside Ally and offline.
const DOCUMENT_HEAD_STYLES = `
  * { box-sizing: border-box; }
  html, body { margin: 0; background: transparent; }
  body {
    font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, "Noto Sans CJK SC", "Microsoft YaHei", sans-serif;
    color: #e5e5f0;
    font-size: 14px;
    line-height: 1.5;
    overflow-x: hidden;
  }
  a { color: #a78bfa; text-decoration: none; }
  a:hover { text-decoration: underline; }
  table { border-collapse: collapse; width: 100%; margin: 8px 0; }
  th, td { border: 1px solid rgba(255,255,255,0.1); padding: 6px 10px; text-align: left; }
  th { background: rgba(255,255,255,0.05); font-weight: 500; }
  svg { max-width: 100%; height: auto; }
  pre { background: rgba(255,255,255,0.05); padding: 8px; border-radius: 4px; overflow-x: auto; }
  code { font-family: "SF Mono", "Fira Code", Consolas, monospace; font-size: 13px; }
  img { max-width: 100%; }
`;

const ECHARTS_BOOTSTRAP = `
(() => {
  if (typeof window.echarts !== 'undefined') {
    const origInit = window.echarts.init;
    const charts = new Set();
    window.echarts.init = function(dom, theme, opts) {
      const chosenTheme = theme || 'dark';
      const inst = origInit.call(this, dom, chosenTheme, opts);
      if (!charts.has(inst)) {
        charts.add(inst);
        const dispose = inst.dispose;
        inst.dispose = function(...args) {
          charts.delete(inst);
          return dispose.apply(this, args);
        };
      }
      return inst;
    };
    window.addEventListener('resize', () => {
      charts.forEach((c) => {
        try {
          if (c && !c.isDisposed()) c.resize();
          else charts.delete(c);
        } catch (_) {}
      });
    });
  }
})();
`;

const CSP_META = `<meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'unsafe-inline'; script-src 'unsafe-inline'; img-src data: blob:; font-src data:; media-src data: blob:;">`;

function buildBody(html) {
  return `${String(html || '')}`;
}

export function resolveHtmlRenderAssetUrl(baseUrl, asset = echartsAssetUrl) {
  const base = new URL(baseUrl);
  const url = new URL(String(asset), base);
  // macOS Wails uses its registered wails:// scheme, whose URL.origin is
  // "null". Compare protocol/host rather than treating null as an origin.
  if (!['http:', 'https:', 'wails:'].includes(url.protocol) || url.protocol !== base.protocol || url.host !== base.host) {
    throw new Error('HTML renderer requires a same-origin ECharts asset');
  }
  return url.href;
}

let exportSourceUrl = '';
let exportSourcePromise;

// Reuse the very same asset as the iframe; fetch source text only on export.
// Cache one successful load, and allow retry after a transient failure.
export function loadHtmlRenderLibrary(baseUrl = globalThis.location?.href || 'http://wails.localhost/', fetchAsset = globalThis.fetch) {
  const url = resolveHtmlRenderAssetUrl(baseUrl);
  if (url === exportSourceUrl && exportSourcePromise) return exportSourcePromise;
  exportSourceUrl = url;
  const request = Promise.resolve().then(() => fetchAsset(url)).then(async (response) => {
    if (!response.ok) throw new Error(`ECharts asset load failed: ${response.status}`);
    const source = await response.text();
    if (!source.trim()) throw new Error('ECharts asset is empty');
    return source;
  }).catch((error) => {
    if (exportSourcePromise === request) {
      exportSourcePromise = undefined;
      exportSourceUrl = '';
    }
    throw error;
  });
  exportSourcePromise = request;
  return request;
}

export function buildHtmlRenderDocument(html, frameToken, baseUrl = globalThis.location?.href || 'http://wails.localhost/') {
  // A classic script works in an opaque sandbox origin without granting
  // allow-same-origin. CSP permits only this exact local asset, not a CDN.
  const scriptUrl = resolveHtmlRenderAssetUrl(baseUrl).replaceAll('&', '&amp;').replaceAll('"', '&quot;').replaceAll('<', '&lt;');
  const frameCsp = CSP_META.replace("script-src 'unsafe-inline';", `script-src 'unsafe-inline' ${scriptUrl};`);
  const token = JSON.stringify(String(frameToken || ''));
  return `<!DOCTYPE html>
<html>
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
${frameCsp}
<style>${DOCUMENT_HEAD_STYLES}</style>
<script src="${scriptUrl}"></script>
<script>${ECHARTS_BOOTSTRAP}</script>
</head>
<body>
${buildBody(html)}
<script>
(() => {
  const token = ${token};
  window.addEventListener('keydown', (event) => {
    if (event.key === 'Escape') parent.postMessage({ type: 'ally-html-escape', token }, '*');
  });
  let lastHeight = 0;
  let heightFrame = 0;
  const reportHeight = () => {
    const bodyRectHeight = document.body ? document.body.getBoundingClientRect().height : 0;
    const height = Math.max(
      Math.ceil(bodyRectHeight),
      document.body ? document.body.scrollHeight : 0,
      100
    );
    if (height === lastHeight) return;
    lastHeight = height;
    parent.postMessage({ type: 'ally-html-height', token, height }, '*');
  };
  const scheduleHeight = () => {
    if (heightFrame) return;
    heightFrame = requestAnimationFrame(() => {
      heightFrame = 0;
      reportHeight();
    });
  };
  new ResizeObserver(scheduleHeight).observe(document.body);
  new MutationObserver(scheduleHeight).observe(document.body, { childList: true, subtree: true, attributes: true });
  window.addEventListener('load', scheduleHeight);
  scheduleHeight();
})();
<\/script>
</body>
</html>`;
}

// 下载用的自包含文档：与 iframe 同源的外壳（样式 + 内联 echarts + 暗色默认），
// 去掉 iframe 专用的向父窗口上报高度脚本——独立文件里没有 parent 通道。
export function buildStandaloneHtmlDocument(html, echartsSource) {
  if (typeof echartsSource !== 'string' || !echartsSource.trim()) throw new Error('ECharts source is required for offline export');
  return `<!DOCTYPE html>
<html>
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
${CSP_META}
<title>Ally Render</title>
<style>${DOCUMENT_HEAD_STYLES}</style>
<script>
${echartsSource}
</script>
<script>${ECHARTS_BOOTSTRAP}</script>
</head>
<body>
${buildBody(html)}
</body>
</html>`;
}
