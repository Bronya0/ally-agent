<!--
SPDX-License-Identifier: GPL-3.0-only

Copyright (C) 2026 tangssst <tangssst@qq.com>
GitHub: https://github.com/Bronya0/ally-agent

This file is part of ally-agent, licensed under the GNU General
Public License v3. See the LICENSE file for details.
-->
<template>
  <n-modal
    :show="show"
    preset="card"
    closable
    class="release-notes-modal"
    :title="$t('app.releaseNotes.title')"
    @update:show="onUpdateShow"
    @close="emit('close')"
  >
    <div class="release-notes-head">
      <span class="release-notes-version">{{ $t('app.releaseNotes.version', { version }) }}</span>
      <span class="release-notes-hint">{{ $t('app.releaseNotes.hint') }}</span>
    </div>
    <!-- 正文 markdown 由 App.vue 渲染成 html 传入（与消息正文同一套管线），这里只负责展示。 -->
    <div class="release-notes-body message-body markdown-body" v-html="html"></div>
    <div class="release-notes-actions">
      <n-button size="small" type="primary" @click="emit('close')">{{ $t('common.close') }}</n-button>
    </div>
  </n-modal>
</template>

<script setup>
defineProps({
  show: { type: Boolean, default: false },
  version: { type: String, default: '' },
  html: { type: String, default: '' },
});

const emit = defineEmits(['close']);

// 所有关闭路径（遮罩、ESC、右上角关闭）都收敛成一个 close，由父级统一落"已看过"标记
// ——那个标记只允许有一个写入点。
function onUpdateShow(value) {
  if (!value) emit('close');
}
</script>
