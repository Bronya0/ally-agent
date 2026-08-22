<!--
SPDX-License-Identifier: GPL-3.0-only

Copyright (C) 2026 tangssst <tangssst@qq.com>
GitHub: https://github.com/Bronya0/ally-agent

This file is part of ally-agent, licensed under the GNU General
Public License v3. See the LICENSE file for details.
-->
<template>
  <n-modal
    :show="visible"
    preset="card"
    class="file-info-modal"
    :title="$t('app.fileInfo.title')"
    style="width: min(640px, 92vw)"
    @update:show="(v) => emit('update:show', v)"
    @after-leave="cleanup"
  >
    <div v-if="loading" class="file-info-loading">{{ $t('app.fileInfo.loading') }}</div>
    <div v-else-if="errorText" class="file-info-error">{{ errorText }}</div>
    <div v-else-if="info" class="file-info-body">
      <div class="file-info-head">
        <span class="file-info-path" :title="info.path">{{ info.path }}</span>
      </div>
      <div v-for="section in sections" :key="section.title" class="file-info-section">
        <div class="file-info-section-title">{{ section.title }}</div>
        <div class="file-info-grid">
          <div v-for="row in section.rows" :key="row.label" class="file-info-row">
            <span class="file-info-label">{{ row.label }}</span>
            <span class="file-info-value" :title="row.value">{{ row.value }}</span>
            <button
              v-if="row.copyable"
              type="button"
              class="file-info-copy"
              :title="$t('common.copy')"
              @click="copyValue(row.value)"
            >{{ $t('common.copy') }}</button>
          </div>
        </div>
      </div>
    </div>
  </n-modal>
</template>

<script setup>
import { computed, ref, watch } from 'vue';
import { useMessage } from 'naive-ui';
import { GetWorkspaceFileInfoAt } from '../../bindings/ally-dev/internal/app/app';
import { buildFileInfoSections } from '../utils/fileInfo.mjs';
import { t } from '../i18n.mjs';

const props = defineProps({
  show: { type: Boolean, default: false },
  // 相对路径（树节点 path），与后端 resolveReadPath 对齐
  path: { type: String, default: '' },
  workspace: { type: String, default: '' },
});

const emit = defineEmits(['update:show']);
const message = useMessage();

const visible = computed(() => props.show);
const loading = ref(false);
const info = ref(null);
const errorText = ref('');
let loadSeq = 0;

watch(() => [props.show, props.path, props.workspace], ([show]) => {
  if (show && props.path && props.workspace) load();
});

async function load() {
  const seq = ++loadSeq;
  loading.value = true;
  errorText.value = '';
  info.value = null;
  try {
    const result = await GetWorkspaceFileInfoAt({ workspace: props.workspace, path: props.path });
    if (seq !== loadSeq) return;
    info.value = result || null;
  } catch (err) {
    if (seq !== loadSeq) return;
    errorText.value = String(err?.message || err || t('app.fileInfo.loadFailed'));
  } finally {
    if (seq === loadSeq) loading.value = false;
  }
}

function cleanup() {
  loadSeq++;
  loading.value = false;
  info.value = null;
  errorText.value = '';
}

// formatTime/formatSize 逻辑已抽到 utils/fileInfo.mjs（与内容区信息面板共用）

const sections = computed(() => buildFileInfoSections(info.value));

function copyValue(value) {
  const text = String(value || '');
  if (!text) return;
  if (navigator.clipboard?.writeText) {
    navigator.clipboard.writeText(text).then(
      () => message.success(t('app.copy.done')),
      () => fallbackCopy(text),
    );
  } else {
    fallbackCopy(text);
  }
}

function fallbackCopy(text) {
  try {
    const ta = document.createElement('textarea');
    ta.value = text;
    ta.style.position = 'fixed';
    ta.style.opacity = '0';
    document.body.appendChild(ta);
    ta.select();
    document.execCommand('copy');
    document.body.removeChild(ta);
    message.success(t('app.copy.done'));
  } catch {
    message.error(t('app.copy.failed'));
  }
}
</script>

<style scoped>
.file-info-body { max-height: 62vh; overflow-y: auto; padding-right: 4px; }
.file-info-head { display: flex; flex-direction: column; gap: 2px; margin-bottom: 10px; }
.file-info-path { font-family: var(--ally-mono-font); font-size: 12px; color: #858b98; word-break: break-all; }
.file-info-section { margin-bottom: 12px; }
.file-info-section-title {
  font-size: 11px;
  text-transform: uppercase;
  letter-spacing: 0.06em;
  color: #6f7887;
  margin-bottom: 4px;
  padding-bottom: 3px;
  border-bottom: 1px solid rgba(255, 255, 255, 0.06);
}
.file-info-grid { display: grid; grid-template-columns: 1fr; gap: 2px; }
.file-info-row {
  display: grid;
  grid-template-columns: minmax(96px, auto) 1fr auto;
  align-items: baseline;
  gap: 8px;
  font-size: 12px;
  line-height: 1.6;
}
.file-info-label { color: #8b93a3; white-space: nowrap; }
.file-info-value {
  font-family: var(--ally-mono-font);
  color: #c5cfdb;
  word-break: break-all;
  min-width: 0;
}
.file-info-copy {
  border: 0;
  background: transparent;
  color: #6f7887;
  font-size: 11px;
  cursor: pointer;
  padding: 0 2px;
  white-space: nowrap;
}
.file-info-copy:hover { color: #a9d8ff; }
.file-info-loading, .file-info-error { padding: 24px; text-align: center; color: #858b98; font-size: 13px; }
.file-info-error { color: #e06c75; }
</style>
