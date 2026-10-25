<!--
SPDX-License-Identifier: GPL-3.0-only

Copyright (C) 2026 tangssst <tangssst@qq.com>
GitHub: https://github.com/Bronya0/ally-agent

This file is part of ally-agent, licensed under the GNU General
Public License v3. See the LICENSE file for details.
-->
<template>
  <div v-if="attachments.length" class="attachment-grid">
    <div v-for="att in attachments" :key="att.id || att.name" class="attachment-item">
      <n-image v-if="att.kind === 'image' && att.imageRef && att.url" :src="att.previewUrl || att.url" :preview-src="att.url" :alt="att.name" :render-toolbar="renderPreviewToolbar" lazy :img-props="{ style: { maxWidth: '100%', maxHeight: '220px', objectFit: 'contain' } }" />
      <img v-else-if="att.kind === 'image' && attachmentSource(att)" :src="attachmentSource(att)" :alt="att.name" />
      <video v-else-if="att.kind === 'video' && attachmentSource(att)" :src="attachmentSource(att)" controls preload="metadata"></video>
      <audio v-else-if="att.kind === 'audio' && attachmentSource(att)" :src="attachmentSource(att)" controls></audio>
      <div v-else class="attachment-file">{{ attachmentIcon(att) }}</div>
      <div class="attachment-meta">
        <span v-if="!att.generated" class="attachment-name" :title="attachmentTitle(att)">{{ att.name }}</span>
        <span class="attachment-size">{{ attachmentState(att) }}</span>
        <n-button v-if="att.imageRef" size="tiny" text :title="t('imageModels.saveOriginal')" :aria-label="t('imageModels.saveOriginal')" :loading="downloading" :disabled="downloading" @click.stop="saveOriginal(att)">
          <template #icon><DownloadOutlined /></template>
        </n-button>
      </div>
      <pre v-if="att.kind === 'text' && att.text" class="attachment-text-preview">{{ textPreview(att.text) }}</pre>
    </div>
  </div>
</template>

<script setup>
import { ref } from 'vue';
import { t } from '../i18n.mjs';
import { formatAttachmentSize } from '../utils/attachmentSize.mjs';
import { attachmentIcon } from '../utils/attachmentIcon.mjs';
import { useMessage } from 'naive-ui';
import DownloadOutlined from '@vicons/antd/DownloadOutlined';
import { ExportGeneratedImage } from '../../bindings/ally-dev/internal/app/app';

const message = useMessage();
const downloading = ref(false);
async function saveOriginal(attachment) {
  if (downloading.value) return;
  downloading.value = true;
  try { await ExportGeneratedImage(attachment.imageRef); }
  catch (error) { message.error(t('imageModels.saveFailed', { error: String(error) })); }
  finally { downloading.value = false; }
}

function renderPreviewToolbar({ nodes }) {
  return [nodes.rotateCounterclockwise, nodes.rotateClockwise, nodes.resizeToOriginalSize,
    nodes.zoomOut, nodes.zoomIn, nodes.close];
}

defineProps({
  attachments: { type: Array, default: () => [] },
});

function attachmentSource(att) {
  return att.previewUrl || att.url || att.dataUrl || '';
}

function attachmentState(att) {
  const parts = [formatAttachmentSize(att, t('app.attachment.originalTag'))];
  if (att.text) parts.push(t('common.text'));
  if (att.truncated) parts.push(t('common.trimmed'));
  // att.error 是「这张图没发出去」的唯一信号，必须显式出现在卡片上：只塞进 title
  // 的 tooltip 等于把失败藏起来。
  if (att.error) parts.push(att.error);
  return parts.filter(Boolean).join(' · ');
}

function attachmentTitle(att) {
  return [att.name, att.type, att.error].filter(Boolean).join('\n');
}

function textPreview(text) {
  const source = String(text || '').trim();
  return source.length > 600 ? `${source.slice(0, 600)}\n...` : source;
}
</script>
