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
      <button type="button" class="tools-trigger" :title="$t('tools.open')" @click.stop="visible = !visible">
        {{ $t('tools.count', { count: toolCount }) }}
      </button>
    </template>
    <div class="tools-popover" @click.stop>
      <div class="tools-overview">{{ $t('tools.overview', { total: toolCount, builtin: builtinTools.length, mcp: mcpTools.length }) }}</div>
      <!-- 只列名称：工具清单来自后端 ListTools 实时状态，不写死分组。 -->
      <div class="tools-names">{{ allNames.join(', ') }}</div>
    </div>
  </n-popover>
</template>

<script setup>
import { computed, ref } from 'vue';
import { t } from '../i18n.mjs';

const props = defineProps({
  tools: { type: Array, default: () => [] },
});

const visible = ref(false);
// 与 MCP 计数同口径：只统计/展示实际注入的工具（ListTools 返回的 enabled
// 标记 = 未被 per-server 黑名单勾掉）；内置工具恒为 enabled。
const enabledTools = computed(() => props.tools.filter((tool) => tool?.enabled !== false));
const toolCount = computed(() => enabledTools.value.length);
const builtinTools = computed(() => enabledTools.value.filter((tool) => tool?.source !== 'mcp'));
const mcpTools = computed(() => enabledTools.value.filter((tool) => tool?.source === 'mcp'));
const allNames = computed(() => enabledTools.value.map((tool) => tool?.name).filter(Boolean));
</script>

<style scoped>
.tools-trigger {
  padding: 0;
  color: var(--ally-info-soft);
  border: 0;
  background: transparent;
  font: inherit;
  cursor: pointer;
  text-align: left;
}

.tools-trigger:hover {
  color: var(--ally-info-soft);
  text-decoration: underline;
  text-underline-offset: 2px;
}

.tools-popover {
  width: min(520px, calc(100vw - 36px));
  max-height: min(520px, calc(100vh - 120px));
  overflow: auto;
  padding: 6px;
  color: var(--ally-text-body);
  background: var(--ally-surface-raised);
  backdrop-filter: var(--ally-glass-blur, none);
  -webkit-backdrop-filter: var(--ally-glass-blur, none);
  border: 1px solid var(--ally-border);
  border-radius: 8px;
}

.tools-overview {
  padding: 4px 6px 8px;
  color: var(--ally-text-soft);
  font-size: 12px;
}

.tools-names {
  padding: 4px 6px;
  color: var(--ally-text-body);
  font-family: var(--ally-mono-font);
  font-size: 12px;
  line-height: 1.6;
  word-break: break-word;
}
</style>
