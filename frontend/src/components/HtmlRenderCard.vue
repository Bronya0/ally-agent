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
      <div class="tool-header-left">
        <ToolStatusIcon :status="msg.status" />
        <span class="tool-verb">{{ statusLabel }}</span>
        <span v-if="msg.title" class="tool-arg" :title="msg.title">({{ msg.title }})</span>
      </div>
      <div v-if="canFullscreen" class="tool-header-right">
        <button
          type="button"
          class="html-render-action-btn"
          :title="$t('tools.renderHtml.fullscreen')"
          @click="openFullscreen"
        >
          <svg viewBox="0 0 24 24" width="14" height="14" stroke="currentColor" stroke-width="2" fill="none" stroke-linecap="round" stroke-linejoin="round">
            <path d="M15 3h6v6M9 21H3v-6M21 3l-7 7M3 21l7-7" />
          </svg>
        </button>
      </div>
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

    <!-- 全屏模态弹窗 -->
    <Teleport to="body">
      <div
        v-if="isFullscreen"
        class="html-render-modal-overlay"
        @click.self="closeFullscreen"
      >
        <div class="html-render-modal-window">
          <div class="html-render-modal-header">
            <div class="html-render-modal-title">
              <span class="html-render-modal-verb">{{ $t('tools.kind.renderHtml') }}</span>
              <span v-if="msg.title" class="html-render-modal-subtitle">{{ msg.title }}</span>
            </div>
            <div class="html-render-modal-actions">
              <span class="html-render-modal-hint">Esc</span>
              <button
                type="button"
                class="html-render-modal-close-btn"
                :title="$t('tools.renderHtml.closeFullscreen')"
                @click="closeFullscreen"
              >
                <svg viewBox="0 0 24 24" width="16" height="16" stroke="currentColor" stroke-width="2" fill="none" stroke-linecap="round" stroke-linejoin="round">
                  <line x1="18" y1="6" x2="6" y2="18" />
                  <line x1="6" y1="6" x2="18" y2="18" />
                </svg>
              </button>
            </div>
          </div>
          <div class="html-render-modal-body">
            <iframe
              class="html-render-modal-frame"
              sandbox="allow-scripts"
              :srcdoc="renderedDocument"
              :title="msg.title || $t('tools.kind.renderHtml')"
            ></iframe>
          </div>
        </div>
      </div>
    </Teleport>
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
const isFullscreen = ref(false);
const frameToken = `ally-html-${Date.now()}-${Math.random().toString(36).slice(2)}`;

const statusLabel = computed(() => toolVerbLabel('render_html', 'render_html', props.msg.status));
const canFullscreen = computed(() => props.msg.status !== 'running' && Boolean(props.msg.htmlContent));

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
  if (event.data?.token !== frameToken) return;
  if (event.data?.type === 'ally-html-escape') {
    if (isFullscreen.value) closeFullscreen();
    return;
  }
  if (event.source !== frameRef.value?.contentWindow) return;
  if (event.data?.type !== 'ally-html-height') return;
  const height = normalizeHtmlFrameHeight(event.data.height);
  if (!height || height === frameHeight.value) return;
  frameHeight.value = height;
}

function openFullscreen() {
  isFullscreen.value = true;
  window.addEventListener('keydown', handleKeydown, true);
}

function closeFullscreen() {
  isFullscreen.value = false;
  window.removeEventListener('keydown', handleKeydown, true);
}

function handleKeydown(event) {
  if (event.key === 'Escape') {
    event.stopPropagation();
    closeFullscreen();
  }
}

onMounted(() => window.addEventListener('message', handleFrameMessage));
onBeforeUnmount(() => {
  window.removeEventListener('message', handleFrameMessage);
  window.removeEventListener('keydown', handleKeydown, true);
});
</script>

<style scoped>
.tool-line {
  display: flex;
  align-items: center;
  justify-content: space-between;
  width: 100%;
}

.tool-header-left {
  display: flex;
  align-items: center;
  gap: 6px;
  min-width: 0;
  overflow: hidden;
}

.tool-header-right {
  display: flex;
  align-items: center;
  margin-left: auto;
}

.html-render-action-btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 24px;
  height: 24px;
  padding: 0;
  border: 1px solid transparent;
  border-radius: 4px;
  background: transparent;
  color: var(--ally-text-muted, #8b949e);
  cursor: pointer;
  transition: all 0.15s ease;
}

.html-render-action-btn:hover {
  background: var(--ally-surface-hover, rgba(255, 255, 255, 0.08));
  color: var(--ally-text, #e5e5f0);
  border-color: var(--ally-border, rgba(255, 255, 255, 0.1));
}

.html-render-error {
  padding: 10px 12px;
  color: var(--ally-danger-pale);
  white-space: pre-wrap;
}

.html-render-writing {
  height: 150px;
  padding: 10px 12px;
  overflow: hidden;
  background: var(--ally-surface-deep);
  border-top: 1px solid var(--ally-border-subtle);
}

.html-render-writing pre {
  height: 100%;
  margin: 0;
  overflow: hidden;
  display: flex;
  align-items: flex-end;
  color: var(--ally-text-body);
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
  border: 1px solid var(--ally-border);
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

/* 铺满侧栏和 header 之外的区域（参考资源树编辑器 has-file 模式）：
   从 header 下方 (top: 44px)、侧边栏右侧 (left: var(--ally-mode-sider-width, 44px))
   铺满右下全部区域，不留空白 */
.html-render-modal-overlay {
  position: fixed;
  top: 44px;
  left: var(--ally-mode-sider-width, 44px);
  right: 0;
  bottom: 0;
  z-index: 60;
  background: var(--ally-surface-content, #1a1a1a);
  display: flex;
  flex-direction: column;
  box-sizing: border-box;
}

.html-render-modal-window {
  width: 100%;
  height: 100%;
  max-width: none;
  display: flex;
  flex-direction: column;
  background: var(--ally-surface-content, #1a1a1a);
  border: none;
  border-radius: 0;
  box-shadow: none;
  overflow: hidden;
}

.html-render-modal-header {
  height: 38px;
  min-height: 38px;
  padding: 0 12px;
  display: flex;
  align-items: center;
  justify-content: space-between;
  border-bottom: 1px solid var(--ally-border);
  background: var(--ally-surface-panel, #1f1f1f);
  user-select: none;
}

.html-render-modal-title {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 13px;
  min-width: 0;
}

.html-render-modal-verb {
  font-weight: 600;
  color: var(--ally-accent, #6366f1);
}

.html-render-modal-subtitle {
  color: var(--ally-text-muted, #8b949e);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.html-render-modal-actions {
  display: flex;
  align-items: center;
  gap: 8px;
}

.html-render-modal-hint {
  font-size: 11px;
  padding: 2px 6px;
  border-radius: 4px;
  background: rgba(255, 255, 255, 0.08);
  color: var(--ally-text-muted, #8b949e);
  font-family: var(--ally-mono-font, monospace);
}

.html-render-modal-close-btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 28px;
  height: 28px;
  border: none;
  border-radius: 6px;
  background: transparent;
  color: var(--ally-text-muted, #8b949e);
  cursor: pointer;
  transition: all 0.15s ease;
}

.html-render-modal-close-btn:hover {
  background: var(--ally-surface-hover, rgba(255, 255, 255, 0.1));
  color: var(--ally-text, #e5e5f0);
}

.html-render-modal-body {
  flex: 1;
  min-height: 0;
  position: relative;
  background: transparent;
}

.html-render-modal-frame {
  width: 100%;
  height: 100%;
  border: none;
  display: block;
  background: transparent;
}
</style>
