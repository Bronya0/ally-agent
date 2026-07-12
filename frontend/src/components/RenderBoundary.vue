<template>
  <div v-if="error" class="render-boundary-error">
    <span>{{ label }} 渲染失败</span>
    <button @click="reset">重试</button>
  </div>
  <slot v-else />
</template>

<script setup>
import { onErrorCaptured, ref } from 'vue';

const props = defineProps({
  label: { type: String, default: '内容' },
});

const error = ref(null);

onErrorCaptured((err, _instance, info) => {
  error.value = err;
  console.error(`[render:error] ${props.label}`, info, err);
  return false;
});

function reset() {
  error.value = null;
}
</script>
