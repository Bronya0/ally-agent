<!--
SPDX-License-Identifier: GPL-3.0-only

Copyright (C) 2026 tangssst <tangssst@qq.com>
GitHub: https://github.com/Bronya0/ally-agent

This file is part of ally-agent, licensed under the GNU General
Public License v3. See the LICENSE file for details.
-->
<template>
  <div v-if="visible" class="command-menu">
    <div class="command-title">{{ $t('commands.title', { commands: builtinCount, skills: skillsCount }) }}</div>
    <div ref="commandScrollRef" class="command-scroll">
      <button
        v-for="(cmd, index) in commands"
        :key="cmd.key"
        :class="['command-item', { active: index === selectedIndex }]"
        @mousedown.prevent="$emit('select', index)"
      >
        <span class="command-label">{{ cmd.label }}</span>
        <span class="command-desc">{{ cmd.description }}</span>
      </button>
    </div>
    <div v-if="commands.length === 0" class="command-empty">{{ $t('commands.empty') }}</div>
  </div>
</template>

<script setup>
import { ref, watch, nextTick } from 'vue';

const props = defineProps({
  visible: Boolean,
  commands: { type: Array, default: () => [] },
  builtinCount: { type: Number, default: 0 },
  skillsCount: { type: Number, default: 0 },
  selectedIndex: { type: Number, default: 0 },
});

defineEmits(['select']);

const commandScrollRef = ref(null);

watch(() => props.selectedIndex, () => {
  nextTick(() => {
    const el = commandScrollRef.value?.querySelector('.command-item.active');
    el?.scrollIntoView({ block: 'nearest' });
  });
});
</script>

<style scoped>
.command-menu {
  position: absolute;
  left: 12px;
  right: 12px;
  bottom: calc(100% - 2px);
  z-index: 20;
  overflow: hidden;
  border-radius: 10px;
  background: var(--ally-surface-raised);
  border: 1px solid var(--ally-border-strong);
  box-shadow: var(--ally-overlay-shadow);
}

.command-scroll {
  max-height: 240px;
  overflow-y: auto;
  scrollbar-width: thin;
  scrollbar-color: var(--ally-scrollbar) transparent;
}

.command-title {
  padding: 9px 12px;
  color: var(--ally-text-faint);
  font-size: 11px;
  text-transform: uppercase;
  letter-spacing: 0.12em;
  border-bottom: 1px solid var(--ally-border);
}

.command-item {
  display: grid;
  grid-template-columns: 96px minmax(0, 1fr);
  align-items: center;
  gap: 10px;
  width: 100%;
  padding: 10px 12px;
  color: var(--ally-text-body);
  text-align: left;
  border: 0;
  background: transparent;
  cursor: pointer;
}

.command-item:hover,
.command-item.active {
  background: var(--ally-state-hover);
}

.command-label {
  color: var(--ally-text-primary);
  font-family: var(--ally-ui-font);
  font-size: 12px;
}

.command-desc {
  overflow: hidden;
  color: var(--ally-text-soft);
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 12px;
}

.command-empty {
  padding: 12px;
  color: var(--ally-text-faint);
  font-size: 12px;
  text-align: center;
}
</style>
