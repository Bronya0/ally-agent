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
        <span v-if="msg.durationText" class="tool-duration">{{ msg.durationText }}</span>
      </div>
      <div v-if="canFullscreen" class="tool-header-right">
        <button
          type="button"
          class="html-render-action-btn"
          :title="$t('tools.renderHtml.download')"
          @click="downloadHtml"
        >
          <svg viewBox="0 0 24 24" width="14" height="14" stroke="currentColor" stroke-width="2" fill="none" stroke-linecap="round" stroke-linejoin="round">
            <path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4M7 10l5 5 5-5M12 15V3" />
          </svg>
        </button>
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
    <!-- Never reparent the iframe: DOM moves/Teleport can reload its document. -->
    <div v-else class="html-render-slot" :style="isFullscreen ? { height: `${frameHeight + 6}px` } : undefined">
      <div class="html-render-frame-wrapper" :class="{ 'is-fullscreen': isFullscreen }" :role="isFullscreen ? 'dialog' : undefined" :aria-label="isFullscreen ? msg.title || $t('tools.kind.renderHtml') : undefined">
          <div v-show="isFullscreen" class="html-render-modal-header">
            <div class="html-render-modal-title">
              <span class="html-render-modal-verb">{{ $t('tools.kind.renderHtml') }}</span>
              <span v-if="msg.title" class="html-render-modal-subtitle">{{ msg.title }}</span>
            </div>
            <div class="html-render-modal-actions">
              <button
                type="button"
                class="html-render-modal-close-btn"
                :title="$t('tools.renderHtml.download')"
                @click="downloadHtml"
              >
                <svg viewBox="0 0 24 24" width="16" height="16" stroke="currentColor" stroke-width="2" fill="none" stroke-linecap="round" stroke-linejoin="round">
                  <path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4M7 10l5 5 5-5M12 15V3" />
                </svg>
              </button>
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
        <div v-if="renderedResult.error" class="html-render-error" role="alert">{{ renderedResult.error }}</div>
        <pre v-if="renderedResult.error" class="html-render-error">{{ msg.htmlContent }}</pre>
        <iframe
          v-show="!renderedResult.error"
          ref="frameRef"
          class="html-render-frame"
          sandbox="allow-scripts"
          :srcdoc="renderedDocument"
          :style="{ height: isFullscreen ? '100%' : `${frameHeight}px` }"
          :title="msg.title || $t('tools.kind.renderHtml')"
        ></iframe>
      </div>
    </div>
    <div v-if="exportError" class="html-render-error" role="alert">{{ exportError }}</div>
  </div>
</template>

<script setup>
import { computed, inject, nextTick, onBeforeUnmount, onMounted, ref, toValue, watch } from 'vue';
import { PRESENTATION_ACTIVE, usePresentationActive } from '../composables/presentation.mjs';
import { toolVerbLabel } from '../utils/toolVerb.mjs';
import { buildHtmlRenderDocument, buildStandaloneHtmlDocument, loadHtmlRenderLibrary, normalizeHtmlFrameHeight } from '../utils/htmlRender.mjs';
import { tailLines } from '../utils/toolPreview.mjs';
import ToolStatusIcon from './ToolStatusIcon.vue';

const props = defineProps({
  msg: { type: Object, required: true },
});

const presentationActive = usePresentationActive();
const workspaceActive = inject(PRESENTATION_ACTIVE, true);
const frameRef = ref(null);
let fullscreenTrigger = null;
const frameHeight = ref(200);
const exportError = ref('');
const exporting = ref(false);
const isFullscreen = ref(false);
const frameToken = `ally-html-${Date.now()}-${Math.random().toString(36).slice(2)}`;

const statusLabel = computed(() => toolVerbLabel('render_html', 'render_html', props.msg.status));
const canFullscreen = computed(() => props.msg.status !== 'running' && Boolean(props.msg.htmlContent));

// 流式期间每拍（120/400/900ms）都要算一次，而 html 会累积到几百 KB：为末尾 8 行
// 把整段切一遍是白费的（与命令卡同款修法，见 utils/toolPreview.mjs 的 tailLines）。
const TAIL_PREVIEW_LINES = 8;

const tailPreview = computed(() => {
  const raw = String(props.msg.htmlContent || '');
  // 裸 \r（老式换行）也当换行：旧实现一律 replace(/\r\n?/g, '\n') 归一，这里只在
  // 真的含 \r 时才付那次全文扫描，绝大多数内容直接走尾部截取。
  const text = raw.indexOf('\r') === -1 ? raw : raw.replace(/\r\n?/g, '\n');
  return tailLines(text, TAIL_PREVIEW_LINES).join('\n');
});

const renderedResult = computed(() => {
  try {
    return { document: buildHtmlRenderDocument(props.msg.htmlContent, frameToken), error: '' };
  } catch (error) {
    return { document: '', error: String(error?.message || error) };
  }
});
const renderedDocument = computed(() => renderedResult.value.document);

function handleFrameMessage(event) {
  if (event.source !== frameRef.value?.contentWindow) return;
  if (event.data?.token !== frameToken) return;
  if (event.data.type === 'ally-html-escape') {
    if (isFullscreen.value) closeFullscreen();
    return;
  }
  if (event.data.type !== 'ally-html-height' || isFullscreen.value) return;
  const height = normalizeHtmlFrameHeight(event.data.height);
  if (!height || height === frameHeight.value) return;
  frameHeight.value = height;
}

function openFullscreen() {
  window.dispatchEvent(new Event('ally-html-close-fullscreen'));
  fullscreenTrigger = document.activeElement;
  isFullscreen.value = true;
  nextTick(() => {
    if (isFullscreen.value) frameRef.value?.parentElement?.querySelector('.html-render-modal-close-btn:last-child')?.focus();
  });
}

function closeFullscreen() {
  isFullscreen.value = false;
  if (presentationActive.value) fullscreenTrigger?.focus?.({ preventScroll: true });
  fullscreenTrigger = null;
}

function handleEscape(event) {
  if (!isFullscreen.value || event.key !== 'Escape') return;
  event.preventDefault();
  event.stopImmediatePropagation();
  closeFullscreen();
}
watch(() => toValue(workspaceActive), (active) => { if (!active) closeFullscreen(); });
watch(isFullscreen, (open, _previous, onCleanup) => {
  if (!open) return;
  window.addEventListener('keydown', handleEscape, true);
  window.addEventListener('ally-html-close-fullscreen', closeFullscreen);
  onCleanup(() => {
    window.removeEventListener('keydown', handleEscape, true);
    window.removeEventListener('ally-html-close-fullscreen', closeFullscreen);
  });
}, { flush: 'sync' });

// 导出自包含 HTML：echarts 与基础样式全部内联（与 iframe 同一外壳），
// 断网双击打开图表照常渲染。文件名取卡片 title，剥掉非法字符。
function exportBaseName() {
  const raw = String(props.msg.title || '').trim();
  const safe = raw.replace(/[\\/:*?"<>|\u0000-\u001f]/g, '_').slice(0, 80).trim();
  return safe || 'ally-render';
}

async function downloadHtml() {
  if (!props.msg.htmlContent || exporting.value) return;
  exporting.value = true;
  exportError.value = '';
  try {
    const source = await loadHtmlRenderLibrary();
    const blob = new Blob([buildStandaloneHtmlDocument(props.msg.htmlContent, source)], { type: 'text/html;charset=utf-8' });
    const url = URL.createObjectURL(blob);
    const anchor = document.createElement('a');
    anchor.href = url;
    anchor.download = `${exportBaseName()}.html`;
    document.body.appendChild(anchor);
    anchor.click();
    anchor.remove();
    setTimeout(() => URL.revokeObjectURL(url), 1000);
  } catch (error) {
    exportError.value = String(error?.message || error);
  } finally {
    exporting.value = false;
  }
}

onMounted(() => {
  window.addEventListener('message', handleFrameMessage);
});
onBeforeUnmount(() => {
  window.removeEventListener('message', handleFrameMessage);
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
  transition: none;
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

.html-render-frame-wrapper.is-fullscreen {
  position: fixed;
  top: 44px;
  left: var(--ally-mode-sider-width, 44px);
  right: 0;
  bottom: 0;
  z-index: 60;
  width: auto;
  margin: 0;
  display: flex;
  flex-direction: column;
  border: none;
  border-radius: 0;
  background: var(--ally-surface-content);
}
.html-render-frame-wrapper.is-fullscreen .html-render-frame {
  flex: 1;
  min-height: 0;
  max-height: none;
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
  transition: none;
}

.html-render-modal-close-btn:hover {
  background: var(--ally-surface-hover, rgba(255, 255, 255, 0.1));
  color: var(--ally-text, #e5e5f0);
}

</style>
