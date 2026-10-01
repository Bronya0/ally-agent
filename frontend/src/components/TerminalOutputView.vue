<!--
SPDX-License-Identifier: GPL-3.0-only

Copyright (C) 2026 tangssst <tangssst@qq.com>
GitHub: https://github.com/Bronya0/ally-agent

This file is part of ally-agent, licensed under the GNU General
Public License v3. See the LICENSE file for details.
-->

<template>
  <!-- collapsed 必须落到根节点的类上：折叠高度上限只写在 .code-view.collapsed
       .code-body-scroll 一条规则里，漏了这个类命令输出就退回 360px 滚动区，
       一条超长 JSON 行足以把卡片撑到满屏。 -->
  <div :class="['code-view', { collapsed }]">
    <div ref="bodyRef" class="code-body-scroll">
      <div v-for="(line, li) in displayLines" :key="li" class="code-row no-gutter">
        <span class="code-text" v-html="line"></span>
      </div>
    </div>
  </div>
</template>

<script setup>
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { codePreviewWindow, normalizedLines } from '../utils/toolPreview.mjs'

// 被裁剪了就要能展开：折叠高度上限按渲染行数卡（6 行），而“有没有更多内容”
// 若按逻辑行数判，单行超长内容（minified JSON、单行日志）永远是 1 行 → 判成
// 无需展开 → 标题不可点，同时内容被裁掉，彻底看不到。裁剪与否只能实测那个
// 真正在裁剪的盒子。
const emit = defineEmits({ overflow: Boolean });

const props = defineProps({
  text: { type: String, default: '' },
  collapsed: { type: Boolean, default: false },
  maxLines: { type: Number, default: 0 },
})

const bodyRef = ref(null);
let observer = null;

function reportOverflow() {
  const el = bodyRef.value;
  if (!el) return;
  emit('overflow', el.scrollHeight - el.clientHeight > 2);
}

onMounted(() => {
  if (typeof ResizeObserver !== 'undefined' && bodyRef.value) {
    observer = new ResizeObserver(reportOverflow);
    observer.observe(bodyRef.value);
  }
  reportOverflow();
});

onBeforeUnmount(() => {
  if (observer) observer.disconnect();
  observer = null;
});

watch(
  () => [props.text, props.collapsed, props.maxLines],
  () => nextTick(reportOverflow),
  { flush: 'post' },
);

// 展开态展示完整原文（含空行）；折叠 tail 预览先剔除空行，保证预览末行
// 总是真实输出，不被 shell 输出常见的尾随空行占位（拆分前 tool-body 路径
// 的 computeToolBodyText 同样逻辑，拆分时丢失，在此恢复）。
const preview = computed(() => {
  if (!props.collapsed) {
    return codePreviewWindow(props.text, { mode: 'tail' })
  }
  const visible = normalizedLines(props.text).filter((line) => line !== '').join('\n')
  return codePreviewWindow(visible, {
    collapsed: true,
    maxLines: props.maxLines,
    mode: 'tail',
  })
})

// 终端输出不做语法高亮：不是源码，高亮只会产生误导（shell 输出混排
// 任意语言片段），纯文本 + mono 字体就是最正确的呈现。
const displayLines = computed(() => {
  const lines = preview.value.lines
  return lines.length ? lines : []
})
</script>
