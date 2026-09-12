<!--
SPDX-License-Identifier: GPL-3.0-only

Copyright (C) 2026 tangssst <tangssst@qq.com>
GitHub: https://github.com/Bronya0/ally-agent

This file is part of ally-agent, licensed under the GNU General
Public License v3. See the LICENSE file for details.
-->
<template>
  <n-popover
    :show="visible"
    trigger="manual"
    placement="top"
    :show-arrow="false"
    @clickoutside="visible = false"
  >
    <template #trigger>
      <button type="button" class="skills-trigger" :title="$t('app.skills.viewList')" @click.stop="visible = !visible">
        {{ summary }}
      </button>
    </template>
    <div class="skills-popover" @click.stop>
      <!-- 只列名称：可用技能来自后端实时状态，逗号分隔。 -->
      <div v-if="names.length === 0" class="skills-empty">{{ $t('app.skills.noneAvailable') }}</div>
      <div v-else class="skills-names">{{ names.join(', ') }}</div>
    </div>
  </n-popover>
</template>

<script setup>
import { ref } from 'vue';

defineProps({
  summary: { type: String, default: '' },
  names: { type: Array, default: () => [] },
});

const visible = ref(false);
</script>

<style scoped>
.skills-trigger {
  padding: 0;
  color: var(--ally-info-soft);
  border: 0;
  background: transparent;
  font: inherit;
  cursor: pointer;
  text-align: left;
}

.skills-trigger:hover {
  color: var(--ally-info-soft);
  text-decoration: underline;
  text-underline-offset: 2px;
}

.skills-popover {
  width: min(460px, calc(100vw - 36px));
  max-height: min(420px, calc(100vh - 120px));
  overflow: auto;
  padding: 6px;
  color: var(--ally-text-body);
  background: var(--ally-surface-raised);
  backdrop-filter: var(--ally-glass-blur, none);
  -webkit-backdrop-filter: var(--ally-glass-blur, none);
  border: 1px solid var(--ally-border);
  border-radius: 8px;
}

.skills-names {
  padding: 4px 6px;
  color: var(--ally-text-body);
  font-family: var(--ally-mono-font);
  font-size: 12px;
  line-height: 1.6;
  word-break: break-word;
}

.skills-empty {
  padding: 8px 6px;
  color: var(--ally-text-muted);
  font-size: 12px;
}
</style>
