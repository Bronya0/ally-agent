/*
 * SPDX-License-Identifier: GPL-3.0-only
 *
 * Copyright (C) 2026 tangssst <tangssst@qq.com>
 * GitHub: https://github.com/Bronya0/ally-agent
 *
 * This file is part of ally-agent, licensed under the GNU General
 * Public License v3. See the LICENSE file for details.
 */
import test from 'node:test';
import assert from 'node:assert/strict';
import { runInNewContext } from 'node:vm';
import { readFileSync } from 'node:fs';
import { computed, ref } from 'vue';

import {
  buildHtmlRenderDocument,
  normalizeHtmlFrameHeight,
  resolveHtmlRenderAssetUrl,
  loadHtmlRenderLibrary,
  buildStandaloneHtmlDocument,
} from './htmlRender.mjs';

test('render document height measurement does not grow with iframe viewport height', () => {
  const document = buildHtmlRenderDocument('<div>content</div>', 'frame-token');

  assert.doesNotMatch(document, /html, body\s*\{[^}]*min-height:\s*100%/s);
  assert.match(document, /document\.body\.getBoundingClientRect\(\)\.height/);
  assert.match(document, /echarts/);
  assert.match(document, /window\.echarts\.init/);
  assert.equal(normalizeHtmlFrameHeight(201.2), 202);
  assert.equal(normalizeHtmlFrameHeight(600), 600);
  assert.equal(normalizeHtmlFrameHeight(601), 600);
});

test('chart assets resolve on Windows HTTP and macOS custom-scheme hosts', () => {
  for (const base of ['http://wails.localhost/', 'https://wails.localhost/', 'wails://wails.localhost/']) {
    assert.equal(resolveHtmlRenderAssetUrl(base, '/assets/chart.js'), `${base}assets/chart.js`);
  }
  assert.equal(resolveHtmlRenderAssetUrl('http://localhost:5177/workbench', './chart.js'), 'http://localhost:5177/chart.js');
  for (const asset of ['https://cdn.example/chart.js', 'wails://different-host/chart.js', 'javascript:alert(1)', 'data:text/javascript,alert(1)']) {
    assert.throws(() => resolveHtmlRenderAssetUrl('wails://wails.localhost/', asset), /same-origin/);
  }
});

test('embedded documents reference the shared chart asset without widening image or connect CSP', () => {
  const document = buildHtmlRenderDocument('<p>plain HTML</p>', 'frame', 'wails://wails.localhost/');
  assert.match(document, /<script src="wails:\/\/wails\.localhost\//);
  assert.match(document, /default-src 'none'/);
  const scriptUrl = document.match(/<script src="([^"]+)"/)[1];
  assert.ok(document.includes(`script-src 'unsafe-inline' ${scriptUrl};`), 'CSP must allow the exact embedded script URL');
  assert.match(document, /img-src data: blob:/);
  assert.doesNotMatch(document, /connect-src|allow-same-origin|https:\/\/cdn/);
});

test('export reads one shared asset, retries failure, and embeds its source offline', async () => {
  let requests = 0;
  const fetchAsset = async () => {
    requests++;
    return { ok: true, text: async () => 'window.echarts = { offline: true };' };
  };
  const [a, b] = await Promise.all([
    loadHtmlRenderLibrary('http://export-fixture.local/', fetchAsset),
    loadHtmlRenderLibrary('http://export-fixture.local/', fetchAsset),
  ]);
  assert.equal(requests, 1);
  assert.equal(a, b);
  const document = buildStandaloneHtmlDocument('<p>export</p>', a);
  assert.match(document, /window\.echarts = \{ offline: true \}/);
  assert.doesNotMatch(document, /<script[^>]+src=/);
  await assert.rejects(loadHtmlRenderLibrary('http://retry-fixture.local/', async () => ({ ok: false, status: 503 })), /503/);
  assert.equal(await loadHtmlRenderLibrary('http://retry-fixture.local/', fetchAsset), a);
  assert.throws(() => buildStandaloneHtmlDocument('<p>export</p>'), /source is required/);
});

test('HTML card contains document-construction failures and can recover', () => {
  const source = readFileSync(new URL('../components/HtmlRenderCard.vue', import.meta.url), 'utf8');
  const start = source.indexOf('const renderedResult = computed(');
  const end = source.indexOf('\nfunction handleFrameMessage', start);
  const fail = ref(true);
  const { renderedResult, renderedDocument } = new Function('computed', 'props', 'frameToken', 'buildHtmlRenderDocument',
    `${source.slice(start, end)}; return { renderedResult, renderedDocument };`)(computed, { msg: { htmlContent: 'original content' } }, 'token', () => {
    if (fail.value) throw new Error('unsupported scheme');
    return 'recovered document';
  });
  assert.equal(renderedDocument.value, '');
  assert.equal(renderedResult.value.error, 'unsupported scheme');
  fail.value = false;
  assert.equal(renderedDocument.value, 'recovered document');
  assert.equal(renderedResult.value.error, '');
});

test('chart resize tracking deduplicates init and releases disposed instances', () => {
  const document = buildHtmlRenderDocument('', 'frame');
  const bootstrap = [...document.matchAll(/<script>([\s\S]*?)<\/script>/g)]
    .map((match) => match[1]).find((script) => script.includes('const charts = new Set()'));
  assert.ok(bootstrap);
  const instances = new Map();
  const listeners = {};
  const window = {
    echarts: { init(dom) {
      if (!instances.has(dom)) instances.set(dom, {
        probes: 0, resizes: 0,
        isDisposed() { this.probes++; return false; },
        resize() { this.resizes++; },
        dispose() { instances.delete(dom); },
      });
      return instances.get(dom);
    } },
    addEventListener: (name, callback) => { listeners[name] = callback; },
  };
  runInNewContext(bootstrap, { window });
  const first = window.echarts.init('chart');
  assert.equal(window.echarts.init('chart'), first);
  listeners.resize();
  assert.equal(first.resizes, 1);
  first.dispose();
  const probes = first.probes;
  const next = window.echarts.init('chart');
  listeners.resize();
  assert.equal(first.probes, probes, 'disposed instance must no longer be retained by resize tracking');
  assert.equal(next.resizes, 1);
});
