<template>
  <div :class="['html-render-card', msg.status]">
    <div class="tool-line html-render-header">
      <span :class="['tool-status-icon', msg.status]">{{ statusIcon }}</span>
      <span class="tool-verb">{{ statusLabel }}</span>
      <span class="tool-name">{{ $t('tools.kind.renderHtml') }}</span>
      <span v-if="msg.title" class="tool-arg" :title="msg.title">({{ msg.title }})</span>
    </div>
    <div v-if="msg.status === 'error'" class="html-render-error">{{ msg.body }}</div>
    <div v-else-if="msg.status === 'running'" class="html-render-writing">
      <pre><code>{{ tailPreview }}</code></pre>
    </div>
    <div v-else class="html-render-frame-wrapper">
      <iframe
        ref="frameRef"
        class="html-render-frame"
        sandbox="allow-scripts"
        :srcdoc="renderedDocument"
        :style="{ height: frameHeight + 'px' }"
        :title="msg.title || $t('tools.kind.renderHtml')"
      ></iframe>
    </div>
  </div>
</template>

<script setup>
import { computed, onBeforeUnmount, onMounted, ref } from 'vue';
import { t } from '../i18n.mjs';

const props = defineProps({
  msg: { type: Object, required: true },
});

const frameRef = ref(null);
const frameHeight = ref(200);
const frameToken = `ally-html-${Date.now()}-${Math.random().toString(36).slice(2)}`;

const statusIcon = computed(() => props.msg.status === 'success' ? '✓' : props.msg.status === 'error' ? '✗' : '');
const statusLabel = computed(() => {
  if (props.msg.status === 'success') return t('tools.status.used');
  if (props.msg.status === 'error') return t('tools.status.failed');
  return t('tools.status.using');
});

const normalizedLines = computed(() => {
  const html = String(props.msg.htmlContent || '').replace(/\r\n?/g, '\n');
  if (!html) return [];
  const lines = html.split('\n');
  if (lines.length > 1 && lines[lines.length - 1] === '') lines.pop();
  return lines;
});

const tailPreview = computed(() => normalizedLines.value.slice(-8).join('\n'));

const renderedDocument = computed(() => {
  const html = String(props.msg.htmlContent || '');
  const token = JSON.stringify(frameToken);
  return `<!DOCTYPE html>
<html>
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'unsafe-inline'; script-src 'unsafe-inline'; img-src data: blob:; font-src data:; media-src data: blob:;">
<style>
  * { box-sizing: border-box; }
  html, body { margin: 0; min-height: 100%; background: transparent; }
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
</head>
<body>
${html}
<script>
(() => {
  const token = ${token};
  let lastHeight = 0;
  const reportHeight = () => {
    const height = Math.max(
      document.documentElement ? document.documentElement.scrollHeight : 0,
      document.body ? document.body.scrollHeight : 0,
      100
    );
    if (height === lastHeight) return;
    lastHeight = height;
    parent.postMessage({ type: 'ally-html-height', token, height }, '*');
  };
  new ResizeObserver(reportHeight).observe(document.documentElement);
  new MutationObserver(reportHeight).observe(document.documentElement, { childList: true, subtree: true, attributes: true });
  window.addEventListener('load', reportHeight);
  requestAnimationFrame(reportHeight);
})();
<\/script>
</body>
</html>`;
});

function handleFrameMessage(event) {
  if (event.source !== frameRef.value?.contentWindow) return;
  if (event.data?.type !== 'ally-html-height' || event.data?.token !== frameToken) return;
  const height = Number(event.data.height || 0);
  if (!Number.isFinite(height) || height <= 0) return;
  frameHeight.value = Math.max(120, Math.min(Math.ceil(height) + 2, 600));
}

onMounted(() => window.addEventListener('message', handleFrameMessage));
onBeforeUnmount(() => window.removeEventListener('message', handleFrameMessage));
</script>

<style scoped>
.html-render-card {
  border: 1px solid rgba(255, 255, 255, 0.08);
  border-radius: 8px;
  overflow: hidden;
  margin: 8px 0;
  background: rgba(255, 255, 255, 0.02);
}

.html-render-header {
  padding: 6px 8px;
  border-bottom: 1px solid rgba(255, 255, 255, 0.06);
}

.html-render-error {
  padding: 10px 12px;
  color: #f2b8b8;
  white-space: pre-wrap;
}

.html-render-writing {
  height: 150px;
  padding: 10px 12px;
  overflow: hidden;
  background: #1e1e1e;
  border-top: 1px solid rgba(255, 255, 255, 0.025);
}

.html-render-writing pre {
  height: 100%;
  margin: 0;
  overflow: hidden;
  display: flex;
  align-items: flex-end;
  color: #d4d4d4;
  font-family: var(--ally-mono-font);
  font-size: 12px;
  line-height: 1.55;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}

.html-render-writing code {
  display: block;
  width: 100%;
}

.html-render-frame-wrapper {
  width: 100%;
}

.html-render-frame {
  width: 100%;
  min-height: 120px;
  max-height: 600px;
  border: none;
  display: block;
  background: transparent;
}
</style>
