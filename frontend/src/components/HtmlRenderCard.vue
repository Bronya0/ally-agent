<!--
SPDX-License-Identifier: GPL-3.0-only

Copyright (C) 2026 tangssst <tangssst@qq.com>
GitHub: https://github.com/Bronya0/ally-agent

This file is part of ally-agent, licensed under the GNU General
Public License v3. See the LICENSE file for details.
-->
<template>
  <div :class="['rich-tool-card', 'render_html', msg.status]">
    <div class="tool-line">
      <ToolStatusIcon :status="msg.status" />
      <span class="tool-verb">{{ statusLabel }}</span>
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
import { toolVerbLabel } from '../utils/toolVerb.mjs';
import { buildHtmlRenderDocument, normalizeHtmlFrameHeight } from '../utils/htmlRender.mjs';
import ToolStatusIcon from './ToolStatusIcon.vue';

const props = defineProps({
  msg: { type: Object, required: true },
});

const frameRef = ref(null);
const frameHeight = ref(200);
const frameToken = `ally-html-${Date.now()}-${Math.random().toString(36).slice(2)}`;

const statusLabel = computed(() => toolVerbLabel('render_html', 'render_html', props.msg.status));

const normalizedLines = computed(() => {
  const html = String(props.msg.htmlContent || '').replace(/\r\n?/g, '\n');
  if (!html) return [];
  const lines = html.split('\n');
  if (lines.length > 1 && lines[lines.length - 1] === '') lines.pop();
  return lines;
});

const tailPreview = computed(() => normalizedLines.value.slice(-8).join('\n'));

const renderedDocument = computed(() => {
  return buildHtmlRenderDocument(props.msg.htmlContent, frameToken);
});

function handleFrameMessage(event) {
  if (event.source !== frameRef.value?.contentWindow) return;
  if (event.data?.type !== 'ally-html-height' || event.data?.token !== frameToken) return;
  const height = normalizeHtmlFrameHeight(event.data.height);
  if (!height || height === frameHeight.value) return;
  frameHeight.value = height;
}

onMounted(() => window.addEventListener('message', handleFrameMessage));
onBeforeUnmount(() => window.removeEventListener('message', handleFrameMessage));
</script>

<style scoped>
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
  margin-top: 4px;
  border: 1px solid rgba(255, 255, 255, 0.08);
  border-radius: 8px;
  overflow: hidden;
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
