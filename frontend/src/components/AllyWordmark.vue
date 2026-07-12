<template>
  <span class="ally-wordmark" :aria-label="text">{{ text }}</span>
</template>

<script setup>
defineProps({
  text: { type: String, default: 'Ally' },
});
</script>
