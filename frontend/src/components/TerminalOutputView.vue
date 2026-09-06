<!--
SPDX-License-Identifier: GPL-3.0-only

Copyright (C) 2026 tangssst <tangssst@qq.com>
GitHub: https://github.com/Bronya0/ally-agent

This file is part of ally-agent, licensed under the GNU General
Public License v3. See the LICENSE file for details.
-->

<template>
  <div class="code-view">
    <div class="code-body-scroll">
      <div v-for="(line, li) in displayLines" :key="li" class="code-row no-gutter">
        <span class="code-text" v-html="line"></span>
      </div>
    </div>
  </div>
</template>

<script setup>
import { computed } from 'vue'
import { codePreviewWindow, normalizedLines } from '../utils/toolPreview.mjs'

const props = defineProps({
  text: { type: String, default: '' },
  collapsed: { type: Boolean, default: false },
  maxLines: { type: Number, default: 0 },
})

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
