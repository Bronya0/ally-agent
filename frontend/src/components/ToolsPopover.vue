<template>
  <n-popover
    :show="visible"
    trigger="manual"
    placement="top"
    :show-arrow="false"
    @clickoutside="visible = false"
  >
    <template #trigger>
      <button type="button" class="tools-trigger" title="查看模型可调用工具" @click.stop="visible = !visible">
        {{ toolCount }} tools
      </button>
    </template>
    <div class="tools-popover" @click.stop>
      <div class="tools-overview">模型可调用 {{ toolCount }} 个工具 · 内置 {{ builtinTools.length }} · MCP {{ mcpTools.length }}</div>
      <div v-if="builtinToolGroups.length" class="tools-section">
        <div class="tools-section-title">内置能力</div>
        <div v-for="group in builtinToolGroups" :key="group.key" class="tool-list-item">
          <div class="tool-list-name">{{ group.label }}<span class="tool-list-server"> · {{ group.count }} tools</span></div>
          <div class="tool-list-desc">{{ group.description }}</div>
        </div>
      </div>
      <div v-if="mcpTools.length" class="tools-section">
        <div class="tools-section-title">MCP</div>
        <div v-for="tool in mcpTools" :key="tool.name" class="tool-list-item">
          <div class="tool-list-name">{{ tool.name }}<span v-if="tool.server" class="tool-list-server"> · {{ tool.server }}</span></div>
          <div class="tool-list-desc">{{ tool.description || 'No description' }}</div>
        </div>
      </div>
    </div>
  </n-popover>
</template>

<script setup>
import { computed, ref } from 'vue';

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
    label: '文件浏览/读取',
    names: ['list_files', 'batch_read'],
    description: '浏览目录、批量读取文件内容；运行时统一显示为读取类卡片。',
  },
  {
    key: 'search',
    label: '搜索',
    names: ['grep_files'],
    description: '在工作区里按正则搜索文件内容。',
  },
  {
    key: 'write',
    label: '文件修改',
    names: ['edit', 'create_file', 'delete_path'],
    description: '编辑、创建和删除工作区文件。',
  },
  {
    key: 'command',
    label: '命令/后台进程',
    names: ['run_command', 'background_process'],
    description: '运行短时命令，或启动和停止不阻塞 Agent 的长驻进程。',
  },
  {
    key: 'network',
    label: '网络读取',
    names: ['http_request', 'web_fetch'],
    description: '读取 HTTP API 或网页文本。',
  },
  {
    key: 'remote',
    label: '远程 SSH',
    names: ['remote_list_files', 'remote_read_file', 'remote_edit', 'remote_create_file', 'remote_delete_path', 'remote_run_command'],
    description: '在远程 SSH 工作区执行读取、编辑和命令。',
  },
  {
    key: 'state',
    label: '任务状态',
    names: ['todo_write', 'create_goal', 'update_goal', 'get_goal', 'scheduled_task'],
    description: '维护 todo、目标模式和持久化定时任务。',
  },
  {
    key: 'memory',
    label: '记忆',
    names: ['memory_read', 'memory_write'],
    description: '读取和写入全局长期记忆。',
  },
  {
    key: 'agent',
    label: '代理/技能',
    names: ['agent_delegate', 'Skill'],
    description: '加载技能或启动子代理执行子任务。',
  },
  {
    key: 'utility',
    label: '实用工具',
    names: ['calculate', 'wait', 'ask'],
    description: '执行本地确定性计算、短时等待或向用户提问。',
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
      label: '其他',
      count: otherCount,
      description: '尚未归类的内置模型工具。',
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
