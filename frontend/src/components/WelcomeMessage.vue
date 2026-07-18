<template>
  <div class="message-body welcome-body">
    <div class="welcome-top-row">
      <div class="welcome-brand-block">
        <AllyAvatar />
        <div class="build-version welcome-build-version">{{ buildVersion }}</div>
      </div>
      <table class="welcome-info-table">
        <tbody>
          <tr v-for="(row, rowIndex) in tableRows" :key="rowIndex">
            <template v-for="item in row" :key="item.label">
              <th>{{ item.label }}</th>
              <td>
                <ToolsPopover v-if="item.kind === 'tools'" :tools="tools" />
                <McpStatusPopover v-else-if="item.kind === 'mcp'" :summary="item.value" :servers="mcpServers" />
                <template v-else>{{ item.value }}</template>
              </td>
            </template>
            <template v-if="row.length < 2">
              <th class="welcome-info-empty" aria-hidden="true"></th>
              <td class="welcome-info-empty" aria-hidden="true"></td>
            </template>
          </tr>
        </tbody>
      </table>
    </div>
    <div class="welcome-greeting">{{ welcome.greeting }}</div>
  </div>
</template>

<script setup>
import { computed } from 'vue';
import AllyAvatar from './AllyAvatar.vue';
import ToolsPopover from './ToolsPopover.vue';
import McpStatusPopover from './McpStatusPopover.vue';
import { buildVersion } from '../utils/buildVersion';
import { t } from '../i18n.mjs';

const props = defineProps({
  welcome: { type: Object, required: true },
  tools: { type: Array, default: () => [] },
  // Current MCP server statuses. Backed by the mcp:status runtime event in
  // App.vue, so this array updates only when a server's status actually
  // changes — no polling overhead. Passed through to McpStatusPopover so the
  // welcome table MCP cell can be clicked to inspect per-server connection
  // state (initialization can take a few seconds, especially for npx).
  mcpServers: { type: Array, default: () => [] },
});

const tableRows = computed(() => {
  const items = [
    ...(Array.isArray(props.welcome?.rows) ? props.welcome.rows : []),
    { label: t('common.tools'), kind: 'tools' },
  ];
  const rows = [];
  for (let index = 0; index < items.length; index += 2) {
    rows.push(items.slice(index, index + 2));
  }
  return rows;
});
</script>
