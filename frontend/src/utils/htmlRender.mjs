/*
 * SPDX-License-Identifier: GPL-3.0-only
 *
 * Copyright (C) 2026 tangssst <tangssst@qq.com>
 * GitHub: https://github.com/Bronya0/ally-agent
 *
 * This file is part of ally-agent, licensed under the GNU General
 * Public License v3. See the LICENSE file for details.
 */
import echartsRaw from 'echarts/dist/echarts.min.js?raw';

export function normalizeHtmlFrameHeight(height) {
  const value = Number(height || 0);
  if (!Number.isFinite(value) || value <= 0) return 0;
  return Math.max(120, Math.min(Math.ceil(value), 600));
}

export function buildHtmlRenderDocument(html, frameToken) {
  const token = JSON.stringify(String(frameToken || ''));
  return `<!DOCTYPE html>
<html>
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'unsafe-inline'; script-src 'unsafe-inline'; img-src data: blob:; font-src data:; media-src data: blob:;">
<style>
  * { box-sizing: border-box; }
  html, body { margin: 0; background: transparent; }
  body {
    font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, "Noto Sans CJK SC", "Microsoft YaHei", sans-serif;
    color: #e5e5f0;
    padding: 12px;
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
</style>
<script>
${echartsRaw}
</script>
<script>
(() => {
  if (typeof window.echarts !== 'undefined') {
    const origInit = window.echarts.init;
    const charts = [];
    window.echarts.init = function(dom, theme, opts) {
      const chosenTheme = theme || 'dark';
      const inst = origInit.call(this, dom, chosenTheme, opts);
      charts.push(inst);
      return inst;
    };
    window.addEventListener('resize', () => {
      charts.forEach((c) => {
        try {
          if (c && !c.isDisposed()) c.resize();
        } catch (_) {}
      });
    });
  }
})();
</script>
</head>
<body>
${String(html || '')}
<script>
(() => {
  const token = ${token};
  let lastHeight = 0;
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
  new ResizeObserver(reportHeight).observe(document.body);
  new MutationObserver(reportHeight).observe(document.body, { childList: true, subtree: true, attributes: true });
  window.addEventListener('load', reportHeight);
  requestAnimationFrame(reportHeight);
  window.addEventListener('keydown', (e) => {
    if (e.key === 'Escape') {
      parent.postMessage({ type: 'ally-html-escape', token }, '*');
    }
  }, true);
})();
<\/script>
</body>
</html>`;
}
