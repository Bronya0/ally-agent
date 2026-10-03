<!--
SPDX-License-Identifier: GPL-3.0-only

Copyright (C) 2026 tangssst <tangssst@qq.com>
GitHub: https://github.com/Bronya0/ally-agent

This file is part of ally-agent, licensed under the GNU General
Public License v3. See the LICENSE file for details.
-->
<template>
  <div :class="['code-view', { collapsed }]">
    <div ref="bodyRef" class="code-body-scroll">
      <div v-for="(line, li) in displayLines" :key="li" class="code-row">
        <span class="code-gutter">{{ padGutter(previewStartLine + li) }}</span>
        <span class="code-text" v-html="line"></span>
      </div>
    </div>
  </div>
</template>

<script setup>
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import hljs from 'highlight.js/lib/core'
import javascript from 'highlight.js/lib/languages/javascript'
import typescript from 'highlight.js/lib/languages/typescript'
import json from 'highlight.js/lib/languages/json'
import bash from 'highlight.js/lib/languages/bash'
import powershell from 'highlight.js/lib/languages/powershell'
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
hljs.registerLanguage('powershell', powershell)
hljs.registerLanguage('ps1', powershell)
hljs.registerLanguage('go', go)
hljs.registerLanguage('html', xml)
hljs.registerLanguage('xml', xml)
hljs.registerLanguage('css', cssLang)
hljs.registerLanguage('markdown', markdownLang)
hljs.registerLanguage('md', markdownLang)

const props = defineProps({
  code: { type: String, default: '' },
  filePath: { type: String, default: '' },
  language: { type: String, default: '' },
  collapsed: { type: Boolean, default: false },
  maxLines: { type: Number, default: 0 },
  previewMode: { type: String, default: 'head' },
  // 工具仍在跑（消息 status === 'running'）：正文每拍都在变长，视图退化成
  // 最便宜的纯文本（见 displayLines）。
  live: { type: Boolean, default: false },
})

// 被裁剪了就要能展开：折叠高度上限按渲染行数卡，而“有没有更多内容”若按逻辑
// 行数判，minified 文件/单行 JSON 永远只有 1 行 → 判成无需展开 → 标题不
// 可点，同时内容被裁掉。裁剪与否只能实测那个真正在裁剪的盒子。
const emit = defineEmits({ overflow: Boolean });

const bodyRef = ref(null);
let observer = null;

function reportOverflow() {
  // 展开态不裁剪（上限已撤掉、不再溢出），没必要量；折叠态只有几行，量一次很便宜。
  // 这条测量每拍都会被内容变化触发，展开态下读 scrollHeight 等于每拍把整棵正文子树重排。
  if (!props.collapsed) return;
  const el = bodyRef.value;
  if (!el) return;
  emit('overflow', el.scrollHeight - el.clientHeight > 2);
}

onMounted(() => {
  if (typeof ResizeObserver !== 'undefined' && bodyRef.value) {
    observer = new ResizeObserver(reportOverflow);
    observer.observe(bodyRef.value);
  }
  reportOverflow();
});

onBeforeUnmount(() => {
  if (observer) observer.disconnect();
  observer = null;
});

watch(
  () => [props.code, props.collapsed, props.maxLines, props.previewMode],
  () => nextTick(reportOverflow),
  { flush: 'post' },
);

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

function detectLang(filePath, explicitLang) {
  if (explicitLang) return explicitLang
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
  if (!lang || lang === 'none' || !hljs.getLanguage(lang)) {
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
  const maxLines = props.collapsed && props.maxLines > 0 ? props.maxLines : 0;
  // 流式期间不做语法高亮、也不走高亮缓存：每拍的内容都是新的，缓存永远命不中
  // （还会把有用条目挤出去），而 hljs 的词法分析要按整段内容全量跑一遍。颜色在
  // 完成那一帧随 .tool-body-swap 的淡入一起出现。
  // 只转义预览窗口内的那几行，而不是把 code 整段转义后再切行——省掉每拍对整份内容
  // 的重复处理。行来源必须是 preview.value.lines：gutter 行号取自 previewStartLine，
  // 只有窗口内的行才与它对得上（create 用的是 tail 窗口）。
  if (props.live) {
    return preview.value.lines.map((line) => escapeHtml(line));
  }
  const lang = detectLang(props.filePath, props.language)
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
