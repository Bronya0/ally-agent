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

 色调只落在标题前那颗状态点与徽标上，不再给整块卡片铺一层色调底：单卡铺满一行时，
9% 的色块铺满整个宽度，看着是一大块绿/红底，比状态本身更抢眼（service 卡整块泛绿
最刺眼）。底色统一走中性面，状态靠小圆点说，卡片这才像“条目”而不是“色块”。

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
          <span class="card-grid-dot" />
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
/* 左内边距为 0：卡片与 caption/空态行都要跟 .tool-body 的正文左沿（即 tool name
   文字起点）对齐。给 6px 会让整块卡片比 tool name 右移 6px，看上去像卡片自带缩进。 */
.card-grid {
  padding: 2px 8px 4px 0;
}

.card-grid-row {
  display: grid;
  /* min(190px, 100%) 是防止窄容器下轨道撑出横向滚动条的标准写法 */
  grid-template-columns: repeat(auto-fill, minmax(min(190px, 100%), 1fr));
  gap: 8px;
}

/* 只有一张卡时铺满一行（create / start / 授权的单条结果就是这样）。auto-fill 会把
   它按 190px 塞进第一列，右边一片空白，而卡里的命令与目录宽过一列就被省略号切光
   ——单条卡本身就是这条消息的全部内容，没有并列对象，没必要分栏。 */
.card-grid-row-single {
  grid-template-columns: minmax(min(190px, 100%), 1fr);
}

/* 底色统一中性：状态由 .card-grid-dot 与徽标承担，卡片本身不再被色调染色（见文件
   头说明）。四边内边距一致，标题与工具名正文左沿对齐。 */
.card-grid-item {
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 3px;
  padding: 8px 10px 9px;
  border-radius: 8px;
  border: 1px solid var(--ally-border-subtle);
  background: var(--ally-hover-faint);
}

/* 状态点：6px 实心圆，取色与 TaskCenterPanel 的 .card-kind 同源。放在标题前而不是
   做成左侧竖条，是为了不占左内边距——竖条会把标题右推，单卡铺满时那一推格外显眼。 */
.card-grid-dot {
  flex-shrink: 0;
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: var(--ally-info);
}

.card-grid-item.tone-danger .card-grid-dot {
  background: var(--ally-danger);
}

.card-grid-item.tone-warning .card-grid-dot {
  background: var(--ally-warning);
}

.card-grid-item.tone-success .card-grid-dot {
  background: var(--ally-success);
}

.card-grid-item.tone-neutral .card-grid-dot {
  background: var(--ally-text-faint);
}

.card-grid-head {
  display: flex;
  align-items: center;
  gap: 7px;
}

/* 标题走 UI 字体（不是等宽）：卡标题通常是人给的名字（“临时心跳”“前端构建”），
   等宽字体渲染中文会掉字距，字重也压不下去，一眼就像调试输出。 */
.card-grid-title {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: var(--ally-text-primary);
  font-size: var(--ally-sub-font-size);
  font-weight: 600;
}

/* 徽标吃掉行尾的空白：标题被省略号截断时，徽标也不会被挤出行外。 */
.card-grid-badge {
  margin-left: auto;
  flex-shrink: 0;
  padding: 0 6px;
  border-radius: 999px;
  color: var(--ally-info-soft);
  background: color-mix(in srgb, var(--ally-info) 16%, transparent);
  font-size: var(--ally-aux-font-size);
  line-height: 1.65;
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

/* 命令 / 调度：等宽是对的（这是原文），但它是卡片的第二级信息，压暗一档并给两行，
   单卡铺满时一行省略号会把命令砍掉一半。 */
.card-grid-subtitle {
  padding-left: 13px;
  color: var(--ally-text-soft);
  font-family: var(--ally-mono-font);
  font-size: var(--ally-aux-font-size);
  line-height: 1.5;
  display: -webkit-box;
  overflow: hidden;
  overflow-wrap: anywhere;
  -webkit-box-orient: vertical;
  -webkit-line-clamp: 2;
}

/* 事实行（id · pid · 目录 · 时间）：最次要信息，压到最淡一档，长 id 不该比标题显眼。 */
.card-grid-desc {
  padding-left: 13px;
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
