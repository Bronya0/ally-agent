<!--
SPDX-License-Identifier: GPL-3.0-only

Copyright (C) 2026 tangssst <tangssst@qq.com>
GitHub: https://github.com/Bronya0/ally-agent

This file is part of ally-agent, licensed under the GNU General
Public License v3. See the LICENSE file for details.
-->
<template>
  <div class="message-body markdown-body" v-html="html"></div>
</template>

<script setup>
import { computed } from 'vue';

const props = defineProps({
  msg: { type: Object, required: true },
  renderFn: { type: Function, required: true },
});

// 独立渲染作用域：只有本组件的渲染 effect 订阅 msg.content。
// 流式期间每帧的内容增量只触发这一行的 Markdown 重解析，不再把整个
// 消息列表的 render 函数（含数百次 v-memo 数组比较）一起拖下水。
const html = computed(() => props.renderFn(props.msg.content, props.msg.streaming));
</script>
