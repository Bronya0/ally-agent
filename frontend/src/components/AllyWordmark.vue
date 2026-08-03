<template>
  <span class="ally-wordmark" :aria-label="text">{{ text }}</span>
</template>

<script setup>
defineProps({
  text: { type: String, default: 'Ally' },
});
</script>

<style scoped>
/* 使用固定的银白→暖金→浅灰蓝→银白渐变，避免不同 WebView 的字体合成造成色带漂移。 */
.ally-wordmark {
  background: var(--ally-wordmark-gradient);
  background-repeat: no-repeat;
  background-size: 100% 100%;
  -webkit-background-clip: text;
  background-clip: text;
  color: transparent;
  -webkit-text-fill-color: transparent;
  font-family: 'Inter', sans-serif;
  font-weight: 700;
  font-synthesis: none;
  letter-spacing: -0.5px;
}
</style>
