<!--
SPDX-License-Identifier: GPL-3.0-only

Copyright (C) 2026 tangssst <tangssst@qq.com>
GitHub: https://github.com/Bronya0/ally-agent

This file is part of ally-agent, licensed under the GNU General
Public License v3. See the LICENSE file for details.
-->
<template>
  <n-modal v-model:show="visible" preset="card" :title="$t('git.title')" class="git-diff-modal" @after-leave="cleanup">
    <div class="git-diff-toolbar">
      <div class="git-diff-summary">
        <span class="git-diff-branch">{{ diffResult?.branch || gitStatus.branch || '-' }}</span>
        <span v-if="gitStatus.ahead > 0" class="git-stat ahead" :title="$t('composer.git.ahead')">↑{{ gitStatus.ahead }}</span>
        <span v-if="gitStatus.behind > 0" class="git-stat behind" :title="$t('composer.git.behind')">↓{{ gitStatus.behind }}</span>
        <span v-if="gitStatus.added > 0" class="git-stat added">+{{ gitStatus.added }}</span>
        <span v-if="gitStatus.modified > 0" class="git-stat modified">~{{ gitStatus.modified }}</span>
        <span v-if="gitStatus.deleted > 0" class="git-stat deleted">-{{ gitStatus.deleted }}</span>
        <span v-if="diffResult?.truncated" class="git-diff-truncated">{{ $t('common.truncated') }}</span>
      </div>
      <n-button size="small" secondary :loading="loading" @click="loadDiff(true)">{{ $t('common.refresh') }}</n-button>
    </div>

    <div v-if="loading" class="git-diff-loading">{{ $t('git.loading') }}</div>
    <div v-else-if="diffResult?.error" class="git-diff-error">{{ diffResult.error }}</div>
    <div v-else-if="!files.length" class="git-diff-empty">{{ $t('git.empty') }}</div>
    <div v-else class="git-diff-layout">
      <div class="git-diff-tree">
        <button
          v-for="row in treeRows"
          :key="`${row.type}:${row.path}`"
          :class="['git-diff-tree-row', row.type, { active: row.type === 'file' && row.path === selectedFile?.path }]"
          :style="{ '--git-depth': row.depth }"
          @click="row.type === 'dir' ? toggleDir(row.path) : selectFile(row.file)"
        >
          <span class="git-diff-disclosure">{{ row.type === 'dir' ? (treeExpanded[row.path] === false ? '▸' : '▾') : '' }}</span>
          <span v-if="row.type === 'file'" :class="['git-diff-status', row.file.status]">{{ statusLabel(row.file.status) }}</span>
          <span v-else class="git-diff-dir-icon">{{ $t('common.directory') }}</span>
          <span class="git-diff-tree-name" :title="row.path">{{ row.name }}</span>
          <span v-if="row.added > 0" class="diff-stat-added">+{{ row.added }}</span>
          <span v-if="row.deleted > 0" class="diff-stat-removed">-{{ row.deleted }}</span>
        </button>
      </div>

      <div class="git-diff-preview">
        <template v-if="selectedFile">
          <div class="git-diff-preview-header">
            <span :class="['git-diff-status', selectedFile.status]">{{ statusLabel(selectedFile.status) }}</span>
            <span class="git-diff-preview-path" :title="selectedFile.path">{{ selectedFile.path }}</span>
            <span v-if="selectedFile.added > 0" class="diff-stat-added">+{{ selectedFile.added }}</span>
            <span v-if="selectedFile.deleted > 0" class="diff-stat-removed">-{{ selectedFile.deleted }}</span>
            <span v-if="selectedFile.binary" class="git-diff-chip">{{ $t('common.binary') }}</span>
            <span v-if="selectedFile.truncated" class="git-diff-chip">{{ $t('common.truncated') }}</span>
          </div>
          <DiffView
            v-if="selectedFile.diff && !selectedFile.binary"
            :diff-text="selectedFile.diff"
            :file-path="selectedFile.path"
            :added-count="selectedFile.added || 0"
            :removed-count="selectedFile.deleted || 0"
            :collapsed="false"
            :show-header="false"
          />
          <pre v-else class="git-diff-placeholder">{{ selectedFile.diff || selectedFile.error || $t('git.noText') }}</pre>
          <div v-if="selectedFile.error" class="git-diff-error compact">{{ selectedFile.error }}</div>
        </template>
        <div v-else class="git-diff-empty">{{ $t('git.selectFile') }}</div>
      </div>
    </div>
  </n-modal>
</template>

<script setup>
import { computed, ref, shallowRef, watch } from 'vue';
import DiffView from './DiffView.vue';
import { CancelGitDiff, GetGitDiff } from '../../bindings/ally-dev/internal/app/app';
import { t } from '../i18n.mjs';

const props = defineProps({
  show: { type: Boolean, default: false },
  gitStatus: { type: Object, default: () => ({ isRepo: false }) },
  workspace: { type: String, default: '' },
});

const emit = defineEmits(['update:show']);

const visible = computed({
  get: () => props.show,
  set: (value) => emit('update:show', value),
});

const loading = ref(false);
const diffResult = shallowRef(null);
const loadedKey = ref('');
const treeExpanded = ref({});
const selectedPath = ref('');
const loadSeq = ref(0);

const files = computed(() => Array.isArray(diffResult.value?.files) ? diffResult.value.files : []);
const selectedFile = computed(() => {
  return files.value.find((file) => file.path === selectedPath.value) || files.value[0] || null;
});
const treeRows = computed(() => buildTreeRows(files.value, treeExpanded.value));

watch(() => props.show, (show) => {
  if (show) loadDiff(false);
});

function cacheKey() {
  const st = props.gitStatus || {};
  return [props.workspace || '', st.branch || '', st.added || 0, st.modified || 0, st.deleted || 0].join('|');
}

async function loadDiff(force = false) {
  if (loading.value) return;
  const key = cacheKey();
  if (!force && loadedKey.value === key && diffResult.value) return;
  const seq = loadSeq.value + 1;
  loadSeq.value = seq;
  loading.value = true;
  try {
    const result = await GetGitDiff();
    if (seq !== loadSeq.value) return;
    diffResult.value = result || { isRepo: false, files: [] };
    loadedKey.value = key;
    if (!files.value.some((file) => file.path === selectedPath.value)) {
      selectedPath.value = files.value[0]?.path || '';
    }
  } catch (err) {
    if (seq !== loadSeq.value) return;
    diffResult.value = { isRepo: false, files: [], error: String(err || t('git.loadFailed')) };
    loadedKey.value = key;
    selectedPath.value = '';
  } finally {
    if (seq === loadSeq.value) loading.value = false;
  }
}

function selectFile(file) {
  if (!file?.path) return;
  selectedPath.value = file.path;
}

function cleanup() {
  loadSeq.value++;
  CancelGitDiff().catch(() => {});
  diffResult.value = null;
  loadedKey.value = '';
  treeExpanded.value = {};
  selectedPath.value = '';
  loading.value = false;
}

function toggleDir(path) {
  treeExpanded.value = { ...treeExpanded.value, [path]: treeExpanded.value[path] === false };
}

function statusLabel(status) {
  const labels = {
    added: 'A',
    copied: 'C',
    deleted: 'D',
    modified: 'M',
    renamed: 'R',
    untracked: '?',
  };
  return labels[status] || 'M';
}

function buildTreeRows(files, expanded) {
  const root = { type: 'dir', name: '', path: '', depth: -1, added: 0, deleted: 0, children: new Map() };
  const sorted = [...files].sort((a, b) => String(a.path || '').localeCompare(String(b.path || '')));
  for (const file of sorted) {
    const parts = String(file.path || '').split(/[\\/]/).filter(Boolean);
    if (!parts.length) continue;
    let node = root;
    node.added += file.added || 0;
    node.deleted += file.deleted || 0;
    for (let i = 0; i < parts.length - 1; i++) {
      const name = parts[i];
      const dirPath = parts.slice(0, i + 1).join('/');
      if (!node.children.has(name)) {
        node.children.set(name, { type: 'dir', name, path: dirPath, depth: i, added: 0, deleted: 0, children: new Map() });
      }
      node = node.children.get(name);
      node.added += file.added || 0;
      node.deleted += file.deleted || 0;
    }
    const name = parts[parts.length - 1];
    node.children.set(`file:${file.path}`, {
      type: 'file',
      name,
      path: file.path,
      depth: parts.length - 1,
      added: file.added || 0,
      deleted: file.deleted || 0,
      file,
    });
  }

  const rows = [];
  function flatten(node) {
    const children = [...node.children.values()].sort((a, b) => {
      if (a.type !== b.type) return a.type === 'dir' ? -1 : 1;
      return a.name.localeCompare(b.name);
    });
    for (const child of children) {
      rows.push(child);
      if (child.type === 'dir' && expanded[child.path] !== false) {
        flatten(child);
      }
    }
  }
  flatten(root);
  return rows;
}
</script>
