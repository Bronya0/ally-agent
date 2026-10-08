<!--
SPDX-License-Identifier: GPL-3.0-only

Copyright (C) 2026 tangssst <tangssst@qq.com>
GitHub: https://github.com/Bronya0/ally-agent

This file is part of ally-agent, licensed under the GNU General
Public License v3. See the LICENSE file for details.
-->
<template>
  <!-- 插件页容器：宿主只提供一块带留白的区域，页面内部完全由插件自己实现。
       挂载失败必须留在页面上（只有控制台的话用户永远看不到），所以这里有一个
       错误态 + 重试。 -->
  <div class="plugin-page">
    <div v-if="error" class="plugin-page-error">
      <div class="plugin-page-error-title">{{ t('plugins.mountError') }}</div>
      <pre class="plugin-page-error-body">{{ error }}</pre>
      <n-button size="small" secondary @click="start">{{ t('common.retry') }}</n-button>
    </div>
    <div v-show="!error" ref="mountPoint" class="plugin-page-body"></div>
    <div v-if="loading && !error" class="plugin-page-loading">{{ t('plugins.loadingPage') }}</div>
  </div>
</template>

<script setup>
import { onBeforeUnmount, onMounted, ref, watch } from 'vue';
import { useMessage } from 'naive-ui';
import { locale, t } from '../i18n.mjs';
import { mountPlugin } from '../utils/pluginHost.mjs';
import { buildVersion } from '../utils/buildVersion.js';

const props = defineProps({
  // 一条 PluginInfo（来自 ListPlugins）。
  plugin: { type: Object, required: true },
  // 当前工作区路径：插件读文件时带上它，避免落到"全局工作区"而与页面看到的目录不一致。
  workspace: { type: String, default: '' },
  // 当前是否正显示着这个插件页。页面用 v-show 常驻，所以“被藏起来”必须显式告诉插件。
  active: { type: Boolean, default: false },
});
const emit = defineEmits(['mounted', 'failed', 'denied']);
const message = useMessage();

const mountPoint = ref(null);
const error = ref('');
const loading = ref(false);
let handle = null;
// container 是当前会话**专属**的挂载容器（每次 start 新建一个）。挂载是异步的，过期
// 的那次回来时必须只能清掉自己的 DOM——共用 mountPoint 会把它清成空白页。
let container = null;

function notify(text, type) {
  const target = message[String(type || 'info')];
  if (typeof target === 'function') target.call(message, String(text || ''));
  else message.info(String(text || ''));
}

// runToken：每次 start 自增，用来丢弃“过期”的那次挂载。入口加载是异步的，加载期间
// 用户可能切走、把它禁用、甚至覆盖升级；那时刚拿到的句柄必须立刻释放，否则它会挂在
// 一个已经废弃的页面上，订阅与定时器再没人能清。
let runToken = 0;

async function dispose() {
  const current = handle;
  const node = container;
  handle = null;
  container = null;
  if (current) {
    try {
      current.dispose();
    } catch (err) {
      console.error('[plugin] 卸载出错', err);
    }
  }
  if (node && node.parentNode) node.parentNode.removeChild(node);
}

function teardown() {
  runToken += 1;
  return dispose();
}

async function start() {
  const token = ++runToken;
  await dispose();
  error.value = '';
  const element = mountPoint.value;
  if (!element || !props.plugin) return;
  // 每次挂载都用一个新容器：过期的那次只清得掉自己的容器，碰不到后来者已经渲染的
  // DOM（共享容器时正好相反：它一清，新页面就空白了）。
  const node = document.createElement('div');
  node.className = 'plugin-page-mount';
  element.appendChild(node);
  container = node;
  loading.value = true;
  try {
    const session = await mountPlugin(
      props.plugin,
      node,
      { version: buildVersion, locale, workspace: props.workspace },
      { notify, onDenied: (info) => emit('denied', info) },
    );
    if (token !== runToken) {
      // 这次挂载已被后来者（重挂载或卸载）取代：释放句柄并摘掉自己那个容器，不许挂上去。
      session.dispose();
      if (node.parentNode) node.parentNode.removeChild(node);
      return;
    }
    handle = session;
    session.setActive(props.active);
    emit('mounted', props.plugin.id);
  } catch (err) {
    if (token !== runToken) return;
    error.value = String((err && err.message) || err);
    emit('failed', { id: props.plugin.id, message: error.value });
    console.error('[plugin] 挂载失败', err);
  } finally {
    if (token === runToken) loading.value = false;
  }
}

onMounted(start);
// 卸载要连 runToken 一起推进：在飞的那次挂载回来时会发现自己过期，从而释放句柄。
onBeforeUnmount(teardown);
// 重新导入会换掉 updatedAt（模块 URL 上的 stamp 随之改变），所以这里要重新挂载，
// 否则页面会一直跑旧代码。watch 源刻意用字符串：插件对象在每次列表刷新后都会换身份
// （内容相同），数组字面量每次比较都算“变了”，于是去管理页看一眼就会把已打开的插件页
// 重挂一遍、把用户填的东西清掉。
watch(
  () => `${props.plugin && props.plugin.id}|${props.plugin && props.plugin.updatedAt}`,
  () => {
    start();
  },
);
// 页面显隐：v-show 常驻，所以由这里把可见性转成插件的 shown/hidden 事件。
watch(
  () => props.active,
  (value) => {
    if (handle) handle.setActive(value);
  },
);
</script>

<style scoped>
.plugin-page {
  position: relative;
  display: flex;
  flex-direction: column;
  width: 100%;
  height: 100%;
  min-width: 0;
  min-height: 0;
  background: var(--ally-surface-content);
  overflow: hidden;
}

/* 每次挂载的专属容器。display:contents 让它自己不产生盒子：插件的顶层子元素仍然直接
   参与 .plugin-page-body 的布局，与「没有这层容器」时完全一致。 */
.plugin-page-mount {
  display: contents;
}

/* 插件自己的页面在这里渲染：只给基础留白与滚动，其余全部交给插件。 */
.plugin-page-body {
  flex: 1;
  min-height: 0;
  overflow: auto;
  padding: 16px 22px 24px;
  color: var(--ally-text-body);
  font-size: var(--ally-message-font-size);
}

.plugin-page-loading {
  position: absolute;
  inset: 0;
  display: flex;
  align-items: center;
  justify-content: center;
  color: var(--ally-text-faint);
  font-size: var(--ally-sub-font-size);
  background: var(--ally-surface-content);
}

.plugin-page-error {
  display: flex;
  flex-direction: column;
  gap: 10px;
  align-items: flex-start;
  padding: 28px 24px;
}

.plugin-page-error-title {
  font-size: var(--ally-message-font-size);
  font-weight: 600;
  color: var(--ally-danger-text);
}

.plugin-page-error-body {
  max-width: 100%;
  margin: 0;
  padding: 10px 12px;
  overflow: auto;
  white-space: pre-wrap;
  word-break: break-word;
  font-family: var(--ally-mono-font), monospace;
  font-size: var(--ally-code-font-size);
  color: var(--ally-text-soft);
  background: var(--ally-surface-raised);
  border-radius: 6px;
}
</style>
