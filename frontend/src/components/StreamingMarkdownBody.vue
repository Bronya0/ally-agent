<!--
SPDX-License-Identifier: GPL-3.0-only

Copyright (C) 2026 tangssst <tangssst@qq.com>
GitHub: https://github.com/Bronya0/ally-agent

This file is part of ally-agent, licensed under the GNU General
Public License v3. See the LICENSE file for details.
-->
<template>
  <div class="message-body markdown-body">
    <!-- 已完成的块：只在块边界推进时重解析一次 -->
    <div v-if="committedHtml" class="stream-committed" v-html="committedHtml"></div>
    <!-- 当前未完成的那一块：逐帧推进，长块（长代码块/公式/图片）自动降频 -->
    <div v-if="tailHtml" class="stream-tail" v-html="tailHtml"></div>
  </div>
</template>

<script setup>
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue';
import {
  STREAM_MAX_LAG_MS,
  advanceShown,
  createStreamRenderState,
} from '../utils/streamEase.mjs';

const props = defineProps({
  msg: { type: Object, required: true },
  renderFn: { type: Function, required: true },
});

// 关闭动画偏好下不做缓动：内容到达即上屏，行为与旧实现一致。
const REDUCED_MOTION =
  typeof window !== 'undefined' &&
  window.matchMedia &&
  window.matchMedia('(prefers-reduced-motion: reduce)').matches;

// 推进逻辑全在 utils/streamEase.mjs（纯函数 + 可单测的状态机），这里只做两件事：
// ① 用 rAF 把"已显示长度"朝"已接收长度"逼近；② 把状态机产出的两段 HTML 写进模板。
// 独立渲染作用域：父级 ChatMessages 的 render 函数不订阅流式增量，所以缓冲帧
// （最多 60FPS）只会重解析本组件的一小段尾部，不会拖累整个消息列表。
const committedHtml = ref('');
const tailHtml = ref('');

const content = computed(() => String(props.msg?.content ?? ''));
const streaming = computed(() => props.msg?.streaming === true);

const renderState = createStreamRenderState((text, isStreaming) => props.renderFn(text, isStreaming));

let rafId = 0;
let shown = 0;
let lagSince = 0;

function flush(force) {
  const out = renderState.render(content.value, shown, streaming.value, force === true);
  committedHtml.value = out.committedHtml;
  tailHtml.value = out.tailHtml;
}

function scheduleFrame() {
  if (rafId) return;
  rafId = requestAnimationFrame(onFrame);
}

function cancelFrame() {
  if (!rafId) return;
  cancelAnimationFrame(rafId);
  rafId = 0;
}

// 到达即显示：挂载、流式结束、内容被截断（重试丢弃）时对齐，不做回退动画。
function snapToReceived() {
  cancelFrame();
  shown = content.value.length;
  lagSince = 0;
  flush(true);
}

function onFrame(timestamp) {
  rafId = 0;
  const target = content.value.length;
  if (!streaming.value || REDUCED_MOTION) {
    snapToReceived();
    return;
  }
  if (shown >= target) {
    lagSince = 0; // 已追平：停帧，等下一次 content 增长再起，不空转 rAF
    return;
  }
  if (!lagSince) lagSince = timestamp;
  if (timestamp - lagSince > STREAM_MAX_LAG_MS) {
    shown = target; // 落后太久（网络卡顿/长回答）：直接跳齐，不做长尾追赶
    lagSince = 0;
  } else {
    shown = advanceShown(shown, target);
  }
  flush(false);
  if (shown < target) scheduleFrame();
}

onMounted(() => {
  // 挂载即对齐当前进度（切回后台 Tab/恢复会话时不重放已经输出过的文字）。
  snapToReceived();
  if (streaming.value && !REDUCED_MOTION) scheduleFrame();
});

watch(content, (value) => {
  if (value.length < shown) {
    snapToReceived(); // 内容被截断：直接对齐，不回退动画
    return;
  }
  scheduleFrame();
});

watch(streaming, (isStreaming) => {
  if (isStreaming) {
    scheduleFrame();
    return;
  }
  // 收尾：整篇重渲染一次，恢复代码高亮与 mermaid 图。
  snapToReceived();
});

onBeforeUnmount(cancelFrame);
</script>
