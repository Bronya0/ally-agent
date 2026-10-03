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
      <!-- 命令输出是不可信文本，必须走文本插值转义：用 v-html 时输出里的 style
           标签会被当场解析成真样式表注入全局（curl 一个页面、shell 把命令原文
           回显到报错里都会带出完整 style 元素），html/body 一旦被改写就整窗变形
           ——正文被 30em 居中卡窄、底色刷成白，只有重启才恢复。这里也不需要
           高亮，纯文本就是正确呈现。 -->
      <div v-for="(line, li) in displayLines" :key="li" class="code-row no-gutter">
        <span class="code-text">{{ line }}</span>
      </div>
    </div>
  </div>
</template>

<script setup>
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { codePreviewWindow, tailLines } from '../utils/toolPreview.mjs'

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
  // 展开态不裁剪（上限已撤掉、不再溢出），没必要量；折叠态只有几行，量一次很便宜。
  // 这条测量每拍都会被内容变化触发，展开态下读 scrollHeight 等于每拍把整棵正文子树重排。
  if (!props.collapsed) return;
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
//
// 折叠分支只从尾部取需要的行：旧写法把整段输出切一遍、filter 后 join 回字符串，
// 再交给 codePreviewWindow 切第二遍——同一段文本每帧被完整切两次。命令输出在
// 流式期间有几 MB，而这张卡每拍都要重渲（见 utils/toolUpdateFlush.mjs）。
//
// 终端输出不做语法高亮：不是源码，高亮只会产生误导（shell 输出混排
// 任意语言片段），纯文本 + mono 字体就是最正确的呈现。
const displayLines = computed(() => {
  if (!props.collapsed) return codePreviewWindow(props.text, { mode: 'tail' }).lines
  // maxLines<=0 是不裁剪（codePreviewWindow 在这种情形返回全部行）。
  const limit = props.maxLines > 0 ? props.maxLines : Number.POSITIVE_INFINITY
  return tailLines(props.text, limit, { skipBlank: true })
})
</script>
