<template>
  <div v-if="error" class="render-boundary-error">
    <span>{{ $t('render.failed', { label }) }}</span>
    <button @click="reset">{{ $t('common.retry') }}</button>
  </div>
  <slot v-else />
</template>

<script setup>
import { onErrorCaptured, ref } from 'vue';
import { t } from '../i18n.mjs';

const props = defineProps({
  label: { type: String, default: () => t('common.content') },
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
