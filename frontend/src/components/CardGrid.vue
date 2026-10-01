<!--
SPDX-License-Identifier: GPL-3.0-only

Copyright (C) 2026 tangssst <tangssst@qq.com>
GitHub: https://github.com/Bronya0/ally-agent

This file is part of ally-agent, licensed under the GNU General
Public License v3. See the LICENSE file for details.
-->
<!--
一张卡片一条记录的纯展示网格：一行多个，容器窄了就自动换行（grid auto-fill）。
数据形状与业务无关——标题、副标题、描述、右上角徽标、色调——所以任何工具结果都能
把自己的行映射成卡片塞进来（现在用于 ssh_cluster 服务器清单 / service 进程清单 /
scheduled_task 任务清单，以及 create / start / 授权这类单条结果）。

条目形状（全部可选，只有 title 值得给）：
  { title, subtitle, description, badge, tone, key }
  tone: 'info'（默认）| 'danger' | 'warning' | 'success' | 'neutral'

上限落在 maxItems（默认 40）：清单条数不该由数据决定渲染量，超出的部分只报一行
「还有 N 项未展示」，收口在这一处。空清单由 emptyText 报，需要一行说明（比如
服务卡的「2/5 运行中」）用 caption。
-->
<template>
  <div class="card-grid">
    <p v-if="caption" class="card-grid-caption">{{ caption }}</p>
    <p v-if="!cards.length && emptyText" class="card-grid-empty">{{ emptyText }}</p>
    <div v-else-if="cards.length" :class="['card-grid-row', { 'card-grid-row-single': cards.length === 1 }]">
      <div
        v-for="(card, index) in cards"
        :key="card.key || index"
        :class="['card-grid-item', `tone-${card.tone}`]"
      >
        <div class="card-grid-head">
          <span class="card-grid-title" :title="card.title">{{ card.title }}</span>
          <span v-if="card.badge" class="card-grid-badge">{{ card.badge }}</span>
        </div>
        <div v-if="card.subtitle" class="card-grid-subtitle" :title="card.subtitle">{{ card.subtitle }}</div>
        <div v-if="card.description" class="card-grid-desc" :title="card.description">{{ card.description }}</div>
      </div>
    </div>
    <p v-if="hiddenCount" class="card-grid-more">{{ $t('tools.cardGrid.more', { count: hiddenCount }) }}</p>
  </div>
</template>

<script setup>
import { computed } from 'vue';

// 色调只有这张表里的值有效，别的一律回落 info：不认识的色调不该渲染成一个没有
// 颜色规则的裸卡片。
const CARD_TONES = new Set(['info', 'danger', 'warning', 'success', 'neutral']);
const DEFAULT_MAX_ITEMS = 40;

const props = defineProps({
  items: { type: Array, default: () => [] },
  maxItems: { type: Number, default: DEFAULT_MAX_ITEMS },
  emptyText: { type: String, default: '' },
  caption: { type: String, default: '' },
});

const normalized = computed(() => (Array.isArray(props.items) ? props.items : []).map((item) => ({
  key: String(item?.key || ''),
  title: String(item?.title || ''),
  subtitle: String(item?.subtitle || ''),
  description: String(item?.description || ''),
  badge: String(item?.badge || ''),
  tone: CARD_TONES.has(item?.tone) ? item.tone : 'info',
})));

const limit = computed(() => {
  const value = Math.floor(Number(props.maxItems));
  return Number.isFinite(value) && value > 0 ? value : DEFAULT_MAX_ITEMS;
});

const cards = computed(() => normalized.value.slice(0, limit.value));
const hiddenCount = computed(() => Math.max(0, normalized.value.length - cards.value.length));
</script>

<style scoped>
.card-grid {
  padding: 2px 8px 4px 6px;
}

.card-grid-row {
  display: grid;
  /* min(190px, 100%) 是防止窄容器下轨道撑出横向滚动条的标准写法 */
  grid-template-columns: repeat(auto-fill, minmax(min(190px, 100%), 1fr));
  gap: 6px;
}

/* 只有一张卡时铺满一行（create / start / 授权的单条结果就是这样）。auto-fill 会把
   它按 190px 塞进第一列，右边一片空白，而卡里的命令与目录宽过一列就被省略号切光
   ——单条卡本身就是这条消息的全部内容，没有并列对象，没必要分栏。 */
.card-grid-row-single {
  grid-template-columns: minmax(min(190px, 100%), 1fr);
}

.card-grid-item {
  min-width: 0;
  padding: 5px 8px 6px;
  border-radius: 6px;
  border: 1px solid var(--ally-border-subtle);
  border-left: 3px solid var(--ally-info);
  background: color-mix(in srgb, var(--ally-info) 9%, transparent);
}

.card-grid-item.tone-danger {
  border-left-color: var(--ally-danger);
  background: color-mix(in srgb, var(--ally-danger) 9%, transparent);
}

.card-grid-item.tone-warning {
  border-left-color: var(--ally-warning);
  background: color-mix(in srgb, var(--ally-warning) 9%, transparent);
}

.card-grid-item.tone-success {
  border-left-color: var(--ally-success);
  background: color-mix(in srgb, var(--ally-success) 9%, transparent);
}

.card-grid-item.tone-neutral {
  border-left-color: var(--ally-border-strong);
  background: var(--ally-hover-faint);
}

.card-grid-head {
  display: flex;
  align-items: center;
  gap: 6px;
}

.card-grid-title {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: var(--ally-text-primary);
  font-family: var(--ally-mono-font);
  font-size: var(--ally-sub-font-size);
}

/* 徽标吃掉行尾的空白：标题被省略号截断时，徽标也不会被挤出行外。 */
.card-grid-badge {
  margin-left: auto;
  flex-shrink: 0;
  padding: 0 5px;
  border-radius: 999px;
  color: var(--ally-info-soft);
  background: color-mix(in srgb, var(--ally-info) 18%, transparent);
  font-size: var(--ally-aux-font-size);
  line-height: 1.6;
}

.card-grid-item.tone-danger .card-grid-badge {
  color: var(--ally-danger-pale);
  background: color-mix(in srgb, var(--ally-danger) 20%, transparent);
}

.card-grid-item.tone-warning .card-grid-badge {
  color: var(--ally-warning-text);
  background: color-mix(in srgb, var(--ally-warning) 20%, transparent);
}

.card-grid-item.tone-success .card-grid-badge {
  color: var(--ally-success-pale);
  background: color-mix(in srgb, var(--ally-success) 20%, transparent);
}

.card-grid-item.tone-neutral .card-grid-badge {
  color: var(--ally-text-soft);
  background: var(--ally-state-selected);
}

.card-grid-subtitle {
  margin-top: 2px;
  color: var(--ally-text-soft);
  font-family: var(--ally-mono-font);
  font-size: var(--ally-aux-font-size);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.card-grid-desc {
  margin-top: 2px;
  color: var(--ally-text-faint);
  font-size: var(--ally-aux-font-size);
  line-height: 1.5;
  overflow-wrap: anywhere;
}

.card-grid-caption {
  margin: 0 0 4px;
  color: var(--ally-text-muted);
  font-size: var(--ally-sub-font-size);
}

.card-grid-empty,
.card-grid-more {
  margin: 0;
  color: var(--ally-text-faint);
  font-size: var(--ally-sub-font-size);
}
</style>
