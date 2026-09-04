<!--
SPDX-License-Identifier: GPL-3.0-only

Copyright (C) 2026 tangssst <tangssst@qq.com>
GitHub: https://github.com/Bronya0/ally-agent

This file is part of ally-agent, licensed under the GNU General
Public License v3. See the LICENSE file for details.
-->
<template>
  <div :class="['rich-tool-card', 'read-grep-group', msg.status, { expanded: isExpanded, 'non-interactive': true }]">
    <div class="tool-line read-grep-toggle" @click="toggleExpanded">
      <!-- 折叠行不放状态图标（比圆点/对勾更干净）：组内还有 running 调用时
           统计文字带 shimmer 流光即进行中信号，出错时统计文字转红 -->
      <!-- 折叠行：取消整行 shimmer 流光，文字保持固定，数字递增时播放微弹跳入动画 -->
      <span class="read-grep-stats">
        <template v-for="(part, idx) in statsParts" :key="part.key">
          <span v-if="idx > 0" class="read-grep-stats-sep">{{ $t('common.commaSep') }}</span>
          <span>{{ part.prefix }}</span><span class="read-grep-num-slot"><span :key="part.count" class="read-grep-num-bump">{{ part.count }}</span></span><span>{{ part.suffix }}</span>
        </template>
      </span>
      <!-- 折叠指示紧跟文字（不靠最右）；展开时旋转 90°。RightOutlined SVG
           与工作区文件树的展开箭头同款，不用 Unicode 字符避免 WebView2
           系统字体回退导致的粗细不一致 -->
      <span class="read-grep-caret" :title="isExpanded ? $t('common.collapse') : $t('common.expand')">
        <RightOutlined />
      </span>
    </div>
    <div v-if="isExpanded" class="read-grep-body">
      <div
        v-for="(entry, index) in allEntries"
        :key="`${entry.verb}-${entry.title || index}`"
        :class="['read-group-entry', entry.extraClass, entry.status || msg.status]"
      >
        <span class="read-group-tree">{{ treePrefix(index, allEntries.length) }}</span>
        <span class="tool-verb read-grep-entry-verb">{{ entry.verb }}</span>
        <span class="read-group-path" :title="entry.title">{{ entry.title || $t('common.untitled') }}</span>
        <span v-if="entry.displayChip" class="read-group-chip">{{ entry.displayChip }}</span>
      </div>
    </div>
  </div>
</template>

<script setup>
import { computed, inject, ref } from 'vue';
import { t } from '../i18n.mjs';
import RightOutlined from '@vicons/antd/RightOutlined';

const props = defineProps({
  msg: { type: Object, required: true },
  focused: { type: Boolean, default: false },
});

defineEmits(['focus', 'toggle']);

// 展开状态存组件级 Map（由 ChatMessages 注入），按组首条 eventId 索引。
// 组对象在流式累加时每次重建，状态放 Map 里不丢失。
const groupExpanded = inject('readGrepGroupExpanded', null);
const localExpanded = ref(false);

const isExpanded = computed(() => {
  if (groupExpanded) {
    const key = groupKey(props.msg);
    if (key) return groupExpanded.get(key) === true;
    return false;
  }
  return localExpanded.value;
});

function groupKey(msg) {
  return String(msg?.eventId || '');
}

function toggleExpanded() {
  if (groupExpanded) {
    const key = groupKey(props.msg);
    if (!key) return;
    groupExpanded.set(key, !(groupExpanded.get(key) === true));
    return;
  }
  localExpanded.value = !localExpanded.value;
}

const isRunning = computed(() => props.msg.status === 'running');

function parseCountTemplate(tpl, count) {
  const s = String(tpl || '');
  const placeholder = '{count}';
  const idx = s.indexOf(placeholder);
  if (idx === -1) return { prefix: s, count, suffix: '' };
  return {
    prefix: s.slice(0, idx),
    count,
    suffix: s.slice(idx + placeholder.length),
  };
}

// 统计项分片：将 "已读取 N 次" 拆分为 prefix, count, suffix
// 使得 count 变化时仅数字部分触发微向上弹跳并柔和回落的动画，整行文字静止固定
const statsParts = computed(() => {
  const parts = [];
  if (props.msg.readCount > 0) {
    parts.push({ key: 'read', ...parseCountTemplate(t('tools.readGrep.readCount', { count: '{count}' }), props.msg.readCount) });
  }
  if (props.msg.grepCount > 0) {
    parts.push({ key: 'grep', ...parseCountTemplate(t('tools.readGrep.grepCount', { count: '{count}' }), props.msg.grepCount) });
  }
  if (props.msg.listCount > 0) {
    parts.push({ key: 'list', ...parseCountTemplate(t('tools.readGrep.listCount', { count: '{count}' }), props.msg.listCount) });
  }
  if (!parts.length) {
    parts.push({ key: 'read', ...parseCountTemplate(t('tools.readGrep.readCount', { count: '{count}' }), 1) });
  }
  return parts;
});

// 展开体的统一条目列表：按调用先后顺序排列（seq 由 App.vue 折叠时按源消息顺序
// 递增编号），树形前缀按全局序计算，避免分段计算 total 导致的 '└─' 提前出现在中间行。
// 兼容旧数据：没有 seq 的历史组保持原 read→grep→list 分组顺序。
const allEntries = computed(() => {
  const entries = [];
  for (const e of props.msg.readEntries || []) {
    entries.push({ verb: 'Read', title: e.title, displayChip: childChip(e), status: e.status, seq: e.seq });
  }
  for (const e of props.msg.grepItems || []) {
    entries.push({ verb: 'Grep', title: e.title, displayChip: e.chip, status: e.status, extraClass: 'grep-entry', seq: e.seq });
  }
  for (const e of props.msg.listItems || []) {
    entries.push({ verb: 'List', title: e.title, displayChip: e.chip, status: e.status, extraClass: 'list-entry', seq: e.seq });
  }
  if (entries.some((e) => typeof e.seq === 'number')) {
    entries.sort((a, b) => (a.seq ?? 0) - (b.seq ?? 0));
  }
  return entries;
});

function treePrefix(index, total) {
  return index === total - 1 ? '└─' : '├─';
}

// 子 read 的 chip：只显示行数；失败条目保留错误信息（与 ReadGroupCard 一致）
function childChip(entry) {
  if (entry.chip && String(entry.chip).startsWith('failed:')) return entry.chip;
  const lineCount = Number(entry.lineCount) || 0;
  if (lineCount <= 0) return '';
  const parts = [`${lineCount} lines`];
  if (entry.truncated) parts.push(`(${t('common.truncated')})`);
  return `· ${parts.join(' · ')}`;
}
</script>
