// SPDX-License-Identifier: GPL-3.0-only
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import { computed, ref } from 'vue';
import { generatedImageAttachment } from './generatedImages.mjs';
import { imageModelIdentity, imageTestLimits, supportsImageEdit, validateImageTestFiles } from './imageModels.mjs';

const component = readFileSync(new URL('../components/ImageModelTestModal.vue', import.meta.url), 'utf8');
const script = /<script setup>([\s\S]*?)<\/script>/.exec(component)[1].replace(/^import .*;$/gm, '');
function tester({ protocol = 'openai-images', history = async () => [], generate, FileReader } = {}) {
  let mount;
  const events = [];
  const model = { providerName: 'test', model: 'image', protocol };
  const api = new Function('computed', 'ref', 'onMounted', 'TestImageModel', 'ListImageModelTests', 't', 'generatedImageAttachment', 'imageModelIdentity', 'imageTestLimits', 'supportsImageEdit', 'validateImageTestFiles', 'defineProps', 'defineEmits', 'FileReader',
    `${script}; return { prompt, aspectRatio, images, results, error, generating, busy, generate, addImages, pasteImages, close };`)(
    computed, ref, fn => { mount = fn; }, generate, history, key => key, generatedImageAttachment,
    imageModelIdentity, imageTestLimits, supportsImageEdit, validateImageTestFiles, () => ({ model }),
    () => (...args) => events.push(args), FileReader,
  );
  return { ...api, mount, events };
}

test('manual generation waits for history, prevents duplicate requests, and preserves prior results on failure', async () => {
  const artifact = { imageRef: `image:${'a'.repeat(64)}/${'b'.repeat(32)}`, mime: 'image/png' };
  let finishHistory;
  let finishGeneration;
  let calls = 0;
  const api = tester({
    history: () => new Promise(resolve => { finishHistory = resolve; }),
    generate: (id, request) => {
      assert.deepEqual(id, { providerName: 'test', model: 'image' });
      assert.deepEqual(request, { prompt: 'make it blue', aspectRatio: '2:3', images: ['data:image/png;base64,original'] });
      calls++;
      if (calls === 2) throw new Error('provider failed');
      return new Promise(resolve => { finishGeneration = resolve; });
    },
  });
  api.prompt.value = 'make it blue';
  api.aspectRatio.value = '2:3';
  api.images.value = [{ dataUrl: 'data:image/png;base64,original' }];
  const mounted = api.mount();
  await api.generate();
  assert.equal(calls, 0, 'history response must not overwrite a newer result');
  const previous = { ...artifact, imageRef: artifact.imageRef.replace(/b/g, 'c') };
  finishHistory([previous]);
  await mounted;
  const pending = api.generate();
  await api.generate();
  api.close(false);
  assert.equal(calls, 1);
  assert.deepEqual(api.events, [], 'keep pending request visible');
  finishGeneration(artifact);
  await pending;
  assert.deepEqual(api.results.value.map(item => item.imageRef), [artifact.imageRef, previous.imageRef]);
  assert.ok(generatedImageAttachment(api.results.value[0]));
  await api.generate();
  assert.match(api.error.value, /provider failed/);
  assert.equal(api.generating.value, false);
  assert.equal(api.results.value.length, 2);
  api.close(false);
  assert.deepEqual(api.events, [['close']]);
});

test('reference selection is atomic, prevents overlapping reads, and rejects unsupported editing', async () => {
  const readers = [];
  class Reader {
    constructor() { readers.push(this); }
    readAsDataURL(file) { this.file = file; }
  }
  const api = tester({ FileReader: Reader });
  await api.mount();
  const file = { name: 'cat.png', type: 'image/png', size: 12 };
  const pending = api.addImages([file]);
  await api.addImages([file]);
  assert.equal(readers.length, 1);
  readers[0].result = 'data:image/png;base64,original';
  readers[0].onload();
  await pending;
  assert.equal(api.images.value.length, 1);
  assert.equal(api.images.value[0].dataUrl, readers[0].result);
  await api.addImages([{ ...file, size: imageTestLimits.maxBytes }]);
  assert.equal(readers.length, 1, 'validate the combined limit before reading');
  assert.equal(api.images.value.length, 1);
  assert.ok(api.error.value);
  const failed = api.addImages([file, file]);
  readers[1].result = 'data:image/png;base64,other';
  readers[1].onload();
  await Promise.resolve();
  readers[2].onerror();
  await failed;
  assert.equal(api.images.value.length, 1, 'failed selection cannot leave a partial batch');
  const minimax = tester({ protocol: 'minimax-images', FileReader: Reader });
  await minimax.mount();
  await minimax.addImages([file]);
  assert.equal(minimax.images.value.length, 0);
  assert.equal(readers.length, 3);
});
