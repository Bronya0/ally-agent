// SPDX-License-Identifier: GPL-3.0-only
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import { computed, ref } from 'vue';
import { generatedImageAttachment } from './generatedImages.mjs';
import { imageModelIdentity, imageTestLimits, supportsImageEdit, validateImageTestFiles } from './imageModels.mjs';

const component = readFileSync(new URL('../components/ImageModelTestModal.vue', import.meta.url), 'utf8');
const script = /<script setup>([\s\S]*?)<\/script>/.exec(component)[1].replace(/^import .*;$/gm, '');
function tester({ protocol = 'openai-images', generate, FileReader, beforeGenerate } = {}) {
  const events = [];
  const successes = [];
  const model = { providerName: 'test', model: 'image', protocol };
  const api = new Function('computed', 'ref', 'TestImageModel', 't', 'generatedImageAttachment', 'imageModelIdentity', 'imageTestLimits', 'supportsImageEdit', 'validateImageTestFiles', 'defineProps', 'defineEmits', 'FileReader', 'useMessage',
    `${script}; return { prompt, aspectRatio, images, results, error, generating, busy, generate, addImages, pasteImages, close };`)(
    computed, ref, generate, key => key, generatedImageAttachment,
    imageModelIdentity, imageTestLimits, supportsImageEdit, validateImageTestFiles, () => ({ model, beforeGenerate }),
    () => (...args) => events.push(args), FileReader, () => ({ success: text => successes.push(text) }),
  );
  return { ...api, events, successes };
}

test('manual generation prevents duplicates, recovers after failure, and starts empty when reopened', async () => {
  const artifact = { imageRef: `image:${'a'.repeat(64)}/${'b'.repeat(32)}`, mime: 'image/png' };
  let finishGeneration;
  let calls = 0;
  const api = tester({
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
  assert.deepEqual(api.results.value, []);
  const pending = api.generate();
  await api.generate();
  api.close(false);
  assert.equal(calls, 1);
  assert.deepEqual(api.events, [], 'keep pending request visible');
  finishGeneration(artifact);
  await pending;
  assert.deepEqual(api.results.value.map(item => item.imageRef), [artifact.imageRef]);
  assert.ok(generatedImageAttachment(api.results.value[0]));
  assert.deepEqual(api.successes, ['imageModels.testSuccess']);
  await api.generate();
  assert.match(api.error.value, /provider failed/);
  assert.equal(api.generating.value, false);
  assert.equal(api.results.value.length, 1);
  assert.equal(api.successes.length, 1, 'failure must not show a success message');
  const retry = api.generate();
  assert.equal(calls, 3, 'a failed call must not leave the tester busy');
  const next = { ...artifact, imageRef: artifact.imageRef.replace(/b/g, 'd') };
  finishGeneration(next);
  await retry;
  assert.equal(api.results.value[0].imageRef, next.imageRef);
  assert.equal(api.error.value, '');
  assert.equal(api.successes.length, 2);
  api.close(false);
  assert.deepEqual(api.events, [['close']]);
  const reopened = tester();
  assert.deepEqual(reopened.results.value, []);
  assert.deepEqual(reopened.images.value, []);
  assert.equal(reopened.prompt.value, '');
  assert.equal(reopened.error.value, '');
  assert.equal(reopened.busy.value, false);
});

test('manual image requests wait for saved model configuration and stop on save failure', async () => {
  let saved;
  let calls = 0;
  const api = tester({
    beforeGenerate: () => new Promise(resolve => { saved = resolve; }),
    generate: async () => { calls++; return { imageRef: 'output' }; },
  });
  api.prompt.value = 'draw';
  const pending = api.generate();
  await api.generate();
  assert.equal(calls, 0, 'never call the backend with an unsaved preset');
  assert.equal(api.busy.value, true);
  saved();
  await pending;
  assert.equal(calls, 1);
  const failed = tester({
    beforeGenerate: async () => { throw Error('config save failed'); },
    generate: async () => { calls++; },
  });
  failed.prompt.value = 'draw';
  await failed.generate();
  assert.equal(calls, 1, 'save failure must block a potentially billable call');
  assert.equal(failed.busy.value, false);
  assert.match(failed.error.value, /config save failed/);
});

test('reference selection is atomic, prevents overlapping reads, and rejects unsupported editing', async () => {
  const readers = [];
  class Reader {
    constructor() { readers.push(this); }
    readAsDataURL(file) { this.file = file; }
  }
  const api = tester({ FileReader: Reader });
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
  await minimax.addImages([file]);
  assert.equal(minimax.images.value.length, 0);
  assert.equal(readers.length, 3);
});
