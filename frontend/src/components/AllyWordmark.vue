<template>
  <span class="ally-wordmark" :aria-label="text">{{ text }}</span>
</template>

<script setup>
defineProps({
  text: { type: String, default: 'Ally' },
});
</script>

<style scoped>
/* 斜向渐变:微暖灰起步,过渡到暖白,右下角橙色收口;避免第一个字母偏灰 */
.ally-wordmark {
  background: linear-gradient(135deg, #b8a090 0%, #d0b890 40%, #e0c890 60%, #e09a5a 100%);
  -webkit-background-clip: text;
  background-clip: text;
  -webkit-text-fill-color: transparent;
  letter-spacing: -0.5px;
}
</style>
