<template>
  <span>
    <span class="context-percent" :style="contextUsageStyle">{{ contextPercent }}</span>
    <span class="context-paren">(</span><span class="context-used" :style="contextUsageStyle">{{ contextUsed }}</span><span class="context-max">/{{ contextMax }})</span>
  </span>
</template>

<script setup>
defineProps({
  contextPercent: { type: String, default: '0.0%' },
  contextUsed: { type: String, default: '0' },
  contextMax: { type: String, default: '0' },
  contextUsageStyle: { type: Object, default: () => ({}) },
});
</script>
