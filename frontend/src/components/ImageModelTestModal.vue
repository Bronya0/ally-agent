<!-- SPDX-License-Identifier: GPL-3.0-only -->
<template>
  <n-modal :show="true" preset="card" :title="t('imageModels.test')"
    :style="{ width: 'min(760px, calc(100vw - 48px))' }"
    :mask-closable="!busy" :close-on-esc="!busy" :closable="!busy" @close="emit('close')" @update:show="close">
    <div class="image-test-body">
      <div class="image-test-model">{{ model.providerName }} / {{ model.model }}</div>
      <n-input v-model:value="prompt" type="textarea" :placeholder="t('imageModels.testPrompt')"
        :autosize="{ minRows: 3, maxRows: 8 }" :maxlength="imageTestLimits.promptChars" :disabled="busy" @paste="pasteImages" />
      <div class="image-test-controls">
        <n-button size="small" :disabled="busy || !canEdit" @click="fileInput?.click()">{{ t('imageModels.testAddImages') }}</n-button>
        <n-select v-model:value="aspectRatio" :options="ratioOptions" :disabled="busy" size="small" class="image-test-ratio" />
        <span class="image-test-note">{{ t(canEdit ? 'imageModels.testImageHint' : 'imageModels.testNoEdit') }}</span>
      </div>
      <input ref="fileInput" type="file" accept="image/png,image/jpeg,image/webp,.png,.jpg,.jpeg,.webp" multiple hidden @change="chooseImages" />
      <div v-if="images.length" class="image-test-inputs">
        <div v-for="(item, index) in images" :key="item.id" class="image-test-input">
          <img :src="item.dataUrl" :alt="item.name" />
          <n-button size="tiny" :disabled="busy" @click="images.splice(index, 1)">{{ t('common.delete') }}</n-button>
        </div>
      </div>
      <n-alert v-if="error" type="error" :show-icon="false">{{ error }}</n-alert>
      <div class="image-test-actions">
        <span class="image-test-note">{{ t('imageModels.testResultHint') }}</span>
        <n-button type="primary" :loading="generating" :disabled="busy || !prompt.trim()" @click="generate">
          {{ t(images.length ? 'imageModels.testEdit' : 'settings.tool.image_generate') }}
        </n-button>
      </div>
      <div v-for="result in results" :key="result.imageRef" class="image-test-result">
        <div class="image-test-result-prompt">{{ result.prompt }}</div>
        <div class="image-test-note">{{ new Date(result.createdAt).toLocaleString() }} · {{ result.width }} × {{ result.height }}</div>
        <MessageAttachments :attachments="[generatedImageAttachment(result)].filter(Boolean)" />
      </div>
    </div>
  </n-modal>
</template>

<script setup>
import { computed, ref } from 'vue';
import { useMessage } from 'naive-ui';
import { TestImageModel } from '../../bindings/ally-dev/internal/app/app';
import { t } from '../i18n.mjs';
import { generatedImageAttachment } from '../utils/generatedImages.mjs';
import { imageModelIdentity, imageTestLimits, supportsImageEdit, validateImageTestFiles } from '../utils/imageModels.mjs';
import MessageAttachments from './MessageAttachments.vue';

const props = defineProps({ model: { type: Object, required: true }, beforeGenerate: { type: Function, default: null } });
const emit = defineEmits(['close']);
const message = useMessage();
const identity = imageModelIdentity(props.model);
const canEdit = supportsImageEdit(props.model);
const prompt = ref('');
const aspectRatio = ref('');
const ratioOptions = computed(() => [{ label: t('imageModels.testRatio'), value: '' }, ...['1:1', '3:2', '2:3'].map(value => ({ label: value, value }))]);
const images = ref([]);
const results = ref([]);
const error = ref('');
const generating = ref(false);
const readingImages = ref(false);
const busy = computed(() => generating.value || readingImages.value);
const fileInput = ref(null);
function close(show) { if (!show && !busy.value) emit('close'); }

async function addImages(files) {
  if (busy.value || !canEdit || !files.length) return;
  readingImages.value = true;
  error.value = '';
  try {
    validateImageTestFiles([...images.value, ...files]);
    const additions = [];
    for (const file of files) {
      const dataUrl = await new Promise((resolve, reject) => {
        const reader = new FileReader();
        reader.onload = () => resolve(reader.result);
        reader.onerror = () => reject(reader.error || new Error(t('imageModels.testImageInvalid')));
        reader.onabort = () => reject(new Error(t('imageModels.testImageInvalid')));
        reader.readAsDataURL(file);
      });
      additions.push({ id: crypto.randomUUID(), name: file.name, type: file.type, size: file.size, dataUrl });
    }
    images.value.push(...additions);
  } catch { error.value = t('imageModels.testImageInvalid'); }
  finally { readingImages.value = false; }
}
async function chooseImages(event) {
  const files = Array.from(event.target.files || []);
  event.target.value = '';
  await addImages(files);
}
function pasteImages(event) {
  const files = Array.from(event.clipboardData?.files || []);
  if (!files.length) return;
  event.preventDefault();
  void addImages(files);
}
async function generate() {
  if (busy.value || !prompt.value.trim()) return;
  generating.value = true;
  error.value = '';
  try {
    // Presets update optimistically; the backend must have the saved keys and
    // endpoint before this potentially billable request starts.
    if (props.beforeGenerate) await props.beforeGenerate();
    const result = await TestImageModel(identity, {
      prompt: prompt.value, aspectRatio: aspectRatio.value, images: images.value.map(item => item.dataUrl),
    });
    results.value = [result, ...results.value].slice(0, imageTestLimits.maxResults);
    message.success(t('imageModels.testSuccess'));
  } catch (cause) { error.value = String(cause); }
  finally { generating.value = false; }
}
</script>

<style scoped>
.image-test-body { display: flex; flex-direction: column; gap: 12px; max-height: calc(100vh - 210px); overflow-y: auto; }
.image-test-model { color: var(--ally-text-muted); overflow-wrap: anywhere; }
.image-test-controls, .image-test-actions { display: flex; align-items: center; flex-wrap: wrap; gap: 8px; }
.image-test-actions { justify-content: space-between; }
.image-test-ratio { width: 130px; }
.image-test-note { color: var(--ally-text-faint); font-size: 12px; }
.image-test-inputs { display: flex; flex-wrap: wrap; gap: 8px; }
.image-test-input { display: flex; flex-direction: column; gap: 4px; width: 88px; }
.image-test-input img { width: 88px; height: 88px; object-fit: contain; border-radius: 6px; }
.image-test-result { display: flex; flex-direction: column; gap: 6px; border-top: 1px solid var(--ally-border); padding-top: 12px; }
.image-test-result-prompt { white-space: pre-wrap; overflow-wrap: anywhere; }
</style>
