<!--
SPDX-License-Identifier: GPL-3.0-only

Copyright (C) 2026 tangssst <tangssst@qq.com>
GitHub: https://github.com/Bronya0/ally-agent

This file is part of ally-agent, licensed under the GNU General
Public License v3. See the LICENSE file for details.
-->
<template>
  <div v-if="visible" class="file-mention-menu">
    <div class="file-mention-title">
      <span>{{ $t('fileMention.title') }}</span>
      <span v-if="meta" class="file-mention-meta">{{ meta }}</span>
    </div>
    <div ref="scrollRef" class="file-mention-scroll">
      <button
        v-for="(item, index) in entries"
        :key="`${item.dir ? 'd' : 'f'}:${item.path}`"
        :class="['file-mention-item', { active: index === selectedIndex }]"
        type="button"
        @mousedown.prevent="$emit('select', index)"
      >
        <span class="file-mention-icon" aria-hidden="true">{{ item.dir ? '/' : '.' }}</span>
        <span class="file-mention-path" :title="item.path">{{ item.path }}</span>
      </button>
    </div>
    <div v-if="loading" class="file-mention-empty">{{ $t('fileMention.loading') }}</div>
    <div v-else-if="entries.length === 0" class="file-mention-empty">{{ $t('fileMention.empty') }}</div>
  </div>
</template>

<script setup>
import { nextTick, ref, watch } from 'vue';

const props = defineProps({
  visible: Boolean,
  entries: { type: Array, default: () => [] },
  selectedIndex: { type: Number, default: 0 },
  loading: Boolean,
  meta: { type: String, default: '' },
});

defineEmits(['select']);

const scrollRef = ref(null);

watch(() => props.selectedIndex, () => {
  nextTick(() => {
    const el = scrollRef.value?.querySelector('.file-mention-item.active');
    el?.scrollIntoView({ block: 'nearest' });
  });
});
</script>

<style scoped>
.file-mention-menu {
  position: absolute;
  left: 12px;
  right: 12px;
  bottom: calc(100% - 2px);
  z-index: 21;
  overflow: hidden;
  border-radius: 8px;
  background: var(--ally-surface-raised);
  border: 1px solid var(--ally-border-strong);
  box-shadow: 0 22px 60px rgba(0, 0, 0, 0.55);
}

.file-mention-title {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 8px 12px;
  color: #737373;
  font-size: 11px;
  text-transform: uppercase;
  letter-spacing: 0;
  border-bottom: 1px solid var(--ally-border);
}

.file-mention-meta {
  overflow: hidden;
  max-width: 55%;
  text-overflow: ellipsis;
  white-space: nowrap;
  text-transform: none;
  letter-spacing: 0;
}

.file-mention-scroll {
  max-height: 260px;
  overflow-y: auto;
  scrollbar-width: thin;
  scrollbar-color: #404040 transparent;
}

.file-mention-item {
  display: grid;
  grid-template-columns: 20px minmax(0, 1fr);
  align-items: center;
  width: 100%;
  padding: 8px 12px;
  color: #d4d4d4;
  text-align: left;
  border: 0;
  background: transparent;
  cursor: pointer;
}

.file-mention-item:hover,
.file-mention-item.active {
  background: var(--ally-state-hover);
}

.file-mention-icon {
  color: var(--ally-accent-bright);
  font-family: var(--ally-mono-font);
  font-size: var(--ally-sub-font-size);
}

.file-mention-path {
  overflow: hidden;
  color: #f5f5f5;
  font-family: var(--ally-mono-font);
  font-size: 12px;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.file-mention-empty {
  padding: 12px;
  color: #737373;
  font-size: 12px;
  text-align: center;
}
</style>
