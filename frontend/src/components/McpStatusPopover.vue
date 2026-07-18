<template>
  <n-popover
    :show="visible"
    trigger="manual"
    placement="top"
    :show-arrow="false"
    @clickoutside="visible = false"
  >
    <template #trigger>
      <button type="button" class="mcp-trigger" :title="$t('app.mcp.viewStatus')" @click.stop="visible = !visible">
        {{ summary }}
      </button>
    </template>
    <div class="mcp-popover" @click.stop>
      <div class="mcp-overview">{{ $t('app.mcp.overview', { total: total, connected: connectedCount, tools: toolCount }) }}</div>
      <div v-if="servers.length === 0" class="mcp-empty">{{ $t('app.mcp.noServices') }}</div>
      <div v-else class="mcp-section">
        <div v-for="srv in servers" :key="srv.name" class="mcp-list-item">
          <div class="mcp-list-head">
            <span :class="['mcp-status-dot', statusClass(srv)]" aria-hidden="true"></span>
            <span class="mcp-list-name">{{ srv.name }}</span>
            <span class="mcp-list-meta">{{ $t(`app.mcp.status.${statusKey(srv)}`) }}</span>
            <span v-if="srv.transport" class="mcp-list-transport">· {{ srv.transport }}</span>
            <span v-if="Number(srv.toolCount) > 0" class="mcp-list-tools">· {{ $t('tools.count', { count: srv.toolCount }) }}</span>
          </div>
          <div v-if="srv.error" class="mcp-list-error">{{ srv.error }}</div>
        </div>
      </div>
    </div>
  </n-popover>
</template>

<script setup>
import { computed, ref } from 'vue';
import { t } from '../i18n.mjs';

const props = defineProps({
  summary: { type: String, default: '' },
  servers: { type: Array, default: () => [] },
});

const visible = ref(false);

const total = computed(() => props.servers.length);
const connectedCount = computed(() => props.servers.filter((s) => s?.status === 'connected').length);
const toolCount = computed(() => props.servers.reduce((sum, s) => sum + (Number(s?.toolCount) || 0), 0));

// Map MCP status bucket to a translation key + dot color. The backend emits
// one of: connected / connecting / failed / disconnected (missing). Unknown
// values fall back to "unknown" so the UI never shows a raw English string.
function statusKey(srv) {
  const v = String(srv?.status || '').toLowerCase();
  if (v === 'connected') return 'connected';
  if (v === 'connecting') return 'connecting';
  if (v === 'failed') return 'failed';
  if (v === 'disconnected') return 'disconnected';
  return 'unknown';
}
function statusClass(srv) {
  return statusKey(srv);
}
</script>

<style scoped>
.mcp-trigger {
  padding: 0;
  color: #9fc3ec;
  border: 0;
  background: transparent;
  font: inherit;
  cursor: pointer;
  text-align: left;
}

.mcp-trigger:hover {
  color: #c4dcf8;
  text-decoration: underline;
  text-underline-offset: 2px;
}

.mcp-popover {
  width: min(460px, calc(100vw - 36px));
  max-height: min(420px, calc(100vh - 120px));
  overflow: auto;
  padding: 6px;
  color: #bdbdbd;
  background: #1a1a1a;
  border-radius: 8px;
}

.mcp-overview {
  padding: 4px 6px 8px;
  color: #a3a3a3;
  font-size: 12px;
}

.mcp-empty {
  padding: 8px 6px;
  color: #8a8a8a;
  font-size: 12px;
}

.mcp-list-item {
  padding: 7px 6px;
  border-top: 1px solid rgba(255, 255, 255, 0.06);
}

.mcp-list-head {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 12px;
}

.mcp-list-name {
  color: #d4d4d4;
  font-family: var(--ally-mono-font);
}

.mcp-list-meta,
.mcp-list-transport,
.mcp-list-tools {
  color: #8a8a8a;
  font-family: var(--ally-ui-font);
}

.mcp-list-error {
  margin-top: 4px;
  color: #ff9b9b;
  font-family: var(--ally-mono-font);
  font-size: 12px;
  line-height: 1.35;
  white-space: pre-wrap;
  word-break: break-word;
}

.mcp-status-dot {
  display: inline-block;
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: #6a6a6a;
  flex: none;
}

.mcp-status-dot.connected {
  background: #4ade80;
}

.mcp-status-dot.connecting {
  background: #fbbf24;
  animation: mcp-pulse 1.2s ease-in-out infinite;
}

.mcp-status-dot.failed {
  background: #f87171;
}

.mcp-status-dot.disconnected {
  background: #6a6a6a;
}

.mcp-status-dot.unknown {
  background: #6a6a6a;
}

@keyframes mcp-pulse {
  0%, 100% { opacity: 1; }
  50% { opacity: 0.4; }
}
</style>
