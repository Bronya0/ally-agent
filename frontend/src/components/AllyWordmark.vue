<template>
  <span class="ally-wordmark" :aria-label="text">{{ text }}</span>
</template>

<script setup>
defineProps({
  text: { type: String, default: 'Ally' },
});
</script>

<style scoped>
/* 斜向渐变:中灰到浅灰白,右下角暖橙收口;静态 background-clip 仅首次光栅化 */
.ally-wordmark {
  background: linear-gradient(135deg, #9a9a92 0%, #c4c4bc 45%, #d8d8d0 65%, #e09a5a 100%);
  -webkit-background-clip: text;
  background-clip: text;
  -webkit-text-fill-color: transparent;
  letter-spacing: -0.5px;
}
</style>
