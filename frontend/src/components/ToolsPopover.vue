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
      <div v-if="builtinToolGroups.length" class="tools-section">
        <div class="tools-section-title">{{ $t('tools.builtin') }}</div>
        <div v-for="group in builtinToolGroups" :key="group.key" class="tool-list-item">
          <div class="tool-list-name">{{ group.label }}<span class="tool-list-server"> · {{ $t('tools.count', { count: group.count }) }}</span></div>
          <div class="tool-list-desc">{{ group.description }}</div>
        </div>
      </div>
      <div v-if="mcpTools.length" class="tools-section">
        <div class="tools-section-title">MCP</div>
        <div v-for="tool in mcpTools" :key="tool.name" class="tool-list-item">
          <div class="tool-list-name">{{ tool.name }}<span v-if="tool.server" class="tool-list-server"> · {{ tool.server }}</span></div>
          <div class="tool-list-desc">{{ tool.description || $t('common.noDescription') }}</div>
        </div>
      </div>
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
const toolCount = computed(() => props.tools.length);
const builtinTools = computed(() => props.tools.filter((tool) => tool?.source !== 'mcp'));
const mcpTools = computed(() => props.tools.filter((tool) => tool?.source === 'mcp'));
const builtinToolGroups = computed(() => groupBuiltinTools(builtinTools.value));

const BUILTIN_TOOL_GROUPS = [
  {
    key: 'read',
    label: t('tools.group.read'),
    names: ['list_files', 'read', 'batch_read'],
    description: t('tools.group.readDescription'),
  },
  {
    key: 'search',
    label: t('tools.group.search'),
    names: ['grep'],
    description: t('tools.group.searchDescription'),
  },
  {
    key: 'write',
    label: t('tools.group.write'),
    names: ['edit', 'create', 'delete'],
    description: t('tools.group.writeDescription'),
  },
  {
    key: 'command',
    label: t('tools.group.command'),
    names: ['command', 'service'],
    description: t('tools.group.commandDescription'),
  },
  {
    key: 'network',
    label: t('tools.group.network'),
    names: ['http_request', 'web_fetch'],
    description: t('tools.group.networkDescription'),
  },
  {
    key: 'remote',
    label: t('tools.group.remote'),
    names: ['remote_list_files', 'remote_read_file', 'remote_edit', 'remote_create_file', 'remote_delete_path', 'remote_run_command'],
    description: t('tools.group.remoteDescription'),
  },
  {
    key: 'state',
    label: t('tools.group.state'),
    names: ['plan', 'scheduled_task'],
    description: t('tools.group.stateDescription'),
  },
  {
    key: 'agent',
    label: t('tools.group.agent'),
    names: ['subagent', 'skill', 'Skill'],
    description: t('tools.group.agentDescription'),
  },
  {
    key: 'utility',
    label: t('tools.group.utility'),
    names: ['calculate', 'wait', 'ask'],
    description: t('tools.group.utilityDescription'),
  },
];

function groupBuiltinTools(tools) {
  const byName = new Map(tools.map((tool) => [tool?.name, tool]));
  const used = new Set();
  const groups = [];
  for (const group of BUILTIN_TOOL_GROUPS) {
    const count = group.names.filter((name) => byName.has(name)).length;
    if (!count) continue;
    group.names.forEach((name) => used.add(name));
    groups.push({ ...group, count });
  }
  const otherCount = tools.filter((tool) => tool?.name && !used.has(tool.name)).length;
  if (otherCount) {
    groups.push({
      key: 'other',
      label: t('tools.group.other'),
      count: otherCount,
      description: t('tools.group.otherDescription'),
    });
  }
  return groups;
}
</script>

<style scoped>
.tools-trigger {
  padding: 0;
  color: #9fc3ec;
  border: 0;
  background: transparent;
  font: inherit;
  cursor: pointer;
  text-align: left;
}

.tools-trigger:hover {
  color: #c4dcf8;
  text-decoration: underline;
  text-underline-offset: 2px;
}

.tools-popover {
  width: min(520px, calc(100vw - 36px));
  max-height: min(520px, calc(100vh - 120px));
  overflow: auto;
  padding: 6px;
  color: #bdbdbd;
  background: #1a1a1a;
  border-radius: 8px;
}

.tools-overview {
  padding: 4px 6px 8px;
  color: #a3a3a3;
  font-size: 12px;
}

.tools-section + .tools-section {
  margin-top: 10px;
}

.tools-section-title {
  padding: 4px 6px;
  color: #e5e5e5;
  font-size: 12px;
  font-weight: 650;
}

.tool-list-item {
  padding: 7px 6px;
  border-top: 1px solid rgba(255, 255, 255, 0.06);
}

.tool-list-name {
  color: #d4d4d4;
  font-family: var(--ally-mono-font);
  font-size: 12px;
}

.tool-list-server,
.tool-list-desc {
  color: #8a8a8a;
  font-family: var(--ally-ui-font);
}

.tool-list-desc {
  margin-top: 3px;
  font-size: 12px;
  line-height: 1.35;
  white-space: normal;
}
</style>
