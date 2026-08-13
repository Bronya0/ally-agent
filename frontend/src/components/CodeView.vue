<!--
SPDX-License-Identifier: GPL-3.0-only

Copyright (C) 2026 tangssst <tangssst@qq.com>
GitHub: https://github.com/Bronya0/ally-agent

This file is part of ally-agent, licensed under the GNU General
Public License v3. See the LICENSE file for details.
-->
<template>
  <div :class="['code-view', { collapsed }]">
    <div class="code-body-scroll">
      <div v-for="(line, li) in displayLines" :key="li" class="code-row">
        <span class="code-gutter">{{ padGutter(previewStartLine + li) }}</span>
        <span class="code-text" v-html="line"></span>
      </div>
    </div>
  </div>
</template>

<script setup>
import { computed } from 'vue'
import hljs from 'highlight.js/lib/core'
import javascript from 'highlight.js/lib/languages/javascript'
import typescript from 'highlight.js/lib/languages/typescript'
import json from 'highlight.js/lib/languages/json'
import bash from 'highlight.js/lib/languages/bash'
import go from 'highlight.js/lib/languages/go'
import xml from 'highlight.js/lib/languages/xml'
import cssLang from 'highlight.js/lib/languages/css'
import markdownLang from 'highlight.js/lib/languages/markdown'
import { codePreviewWindow } from '../utils/toolPreview.mjs'

hljs.registerLanguage('javascript', javascript)
hljs.registerLanguage('js', javascript)
hljs.registerLanguage('typescript', typescript)
hljs.registerLanguage('ts', typescript)
hljs.registerLanguage('json', json)
hljs.registerLanguage('bash', bash)
hljs.registerLanguage('shell', bash)
hljs.registerLanguage('sh', bash)
hljs.registerLanguage('go', go)
hljs.registerLanguage('html', xml)
hljs.registerLanguage('xml', xml)
hljs.registerLanguage('css', cssLang)
hljs.registerLanguage('markdown', markdownLang)
hljs.registerLanguage('md', markdownLang)

const props = defineProps({
  code: { type: String, default: '' },
  filePath: { type: String, default: '' },
  collapsed: { type: Boolean, default: false },
  maxLines: { type: Number, default: 0 },
  previewMode: { type: String, default: 'head' },
})

/** Map file extension to highlight.js language name. */
const EXT_LANG_MAP = {
  ts: 'typescript',
  tsx: 'typescript',
  js: 'javascript',
  jsx: 'javascript',
  py: 'python',
  rb: 'ruby',
  rs: 'rust',
  go: 'go',
  java: 'java',
  sh: 'bash',
  bash: 'bash',
  zsh: 'bash',
  json: 'json',
  yaml: 'yaml',
  yml: 'yaml',
  toml: 'toml',
  md: 'markdown',
  css: 'css',
  html: 'html',
  sql: 'sql',
  c: 'c',
  cpp: 'cpp',
  h: 'c',
  hpp: 'cpp',
}

/**
 * Simple extname equivalent for browser (no Node path dependency).
 * Returns ".ext" or empty string.
 */
function extname(p) {
  if (!p) return ''
  const i = p.lastIndexOf('.')
  if (i <= 0) return ''
  // Check there's no slash after the dot
  if (p.lastIndexOf('/') > i || p.lastIndexOf('\\') > i) return ''
  return p.slice(i).toLowerCase()
}

function detectLang(filePath) {
  const ext = extname(filePath) // e.g. ".ts"
  const base = ext.startsWith('.') ? ext.slice(1) : ext
  if (!base) return null
  return EXT_LANG_MAP[base] || null
}

const preview = computed(() => codePreviewWindow(props.code, {
  collapsed: props.collapsed,
  maxLines: props.maxLines,
  mode: props.previewMode,
}))

const previewStartLine = computed(() => preview.value.startLine || 1)

// Bounded LRU cache for highlighted code HTML. Parent re-renders (e.g. when
// ToolCallCard flips expanded / receives duration updates) used to re-run
// hljs.highlight on the entire file body; for multi-KB files this is a 5-20ms
// hit per re-render. Cache key is (lang, code, maxLines): lang + code identify
// the tokenization output, maxLines handles the collapsed slice.
const CODE_HL_CACHE_MAX = 32;
const codeHlCache = new Map();
function highlightCached(lang, code, maxLines) {
  const key = `${lang || 'plain'}|${maxLines}|${code}`;
  const hit = codeHlCache.get(key);
  if (hit !== undefined) {
    codeHlCache.delete(key);
    codeHlCache.set(key, hit);
    return hit;
  }
  let lines;
  if (!lang || !hljs.getLanguage(lang)) {
    lines = escapeHtml(code).split('\n');
  } else {
    try {
      const result = hljs.highlight(code, { language: lang, ignoreIllegals: true });
      lines = result.value.split('\n');
    } catch {
      lines = escapeHtml(code).split('\n');
    }
  }
  if (maxLines > 0) lines = lines.slice(0, maxLines);
  codeHlCache.set(key, lines);
  if (codeHlCache.size > CODE_HL_CACHE_MAX) {
    const oldest = codeHlCache.keys().next().value;
    if (oldest !== undefined) codeHlCache.delete(oldest);
  }
  return lines;
}

const displayLines = computed(() => {
  const code = preview.value.lines.join('\n')
  if (!code) return []
  const lang = detectLang(props.filePath)
  const maxLines = props.collapsed && props.maxLines > 0 ? props.maxLines : 0;
  return highlightCached(lang, code, maxLines);
})

function padGutter(num) {
  return String(num).padStart(4)
}

function escapeHtml(text) {
  return String(text || '')
    .replaceAll('&', '&amp;')
    .replaceAll('<', '&lt;')
    .replaceAll('>', '&gt;')
    .replaceAll('"', '&quot;')
}
</script>
