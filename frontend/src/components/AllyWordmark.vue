<template>
  <span class="ally-wordmark" :aria-label="text">{{ text }}</span>
</template>

<script setup>
defineProps({
  text: { type: String, default: 'Ally' },
});
</script>

<style scoped>
/* 使用加载动画中 Ally 文字的渐变色: 银白→暖金→浅灰蓝→银白 */
.ally-wordmark {
  background: linear-gradient(120deg, #f8fafc 0%, #e0a458 36%, #d7dde8 68%, #f8fafc 100%);
  -webkit-background-clip: text;
  background-clip: text;
  -webkit-text-fill-color: transparent;
  letter-spacing: -0.5px;
}
</style>
