// SPDX-License-Identifier: GPL-3.0-only
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import { ref } from 'vue';
import { placeholderModel, assignConfig, defaultConfig } from './config.mjs';
import { CHATGPT_PROVIDER_NAME, CHATGPT_CONTEXT_WINDOW, buildModelConfigExport, parseModelConfigImport,
  modelConfigIdentity, normalizeApiFormat, normalizeApiKeysArray, normalizeReasoningEffort, normalizeTokenParam } from './modelConfigIO.mjs';

const source = readFileSync(new URL('../components/ModelsPanel.vue', import.meta.url), 'utf8');
const defaults = source.slice(source.indexOf('function defaultModelDraft('), source.indexOf('// normalizeModelApiKeys'));
const auth = source.slice(source.indexOf('function oauthFields()'), source.indexOf('function onModelDraftSelected('));
const commit = source.slice(source.indexOf('function commitModelDraft()'), source.indexOf('function removeModelDraft('));
const exporter = source.slice(source.indexOf('async function exportModelConfigs()'), source.indexOf('function cloneConfigDraft('));

function fixture() {
  const modelDraft = {};
  const draft = { models: [] };
  const emissions = [], files = [], errors = [];
  const bindings = {
    modelDraft, draft, CHATGPT_PROVIDER_NAME, CHATGPT_CONTEXT_WINDOW,
    placeholderModel, normalizeApiFormat, normalizeReasoningEffort, modelConfigIdentity,
    normalizeDraftTokenParam: normalizeTokenParam, normalizeModelApiKeys: normalizeApiKeysArray,
    normalizedProviderName: (value) => value,
    isOAuth: ref(false), oauthModels: ref([]), modelEditorIndex: ref(0), modelEditorVisible: ref(true),
    collectCustomHeaders: () => modelDraft.customHeaders,
    alignActiveProviderTab() {}, t: (key) => key,
    emit: (...args) => emissions.push(args),
    message: { success() {}, warning(err) { errors.push(err); }, error(err) { errors.push(err); } },
    buildModelConfigExport,
    saveTextFile: async (file) => { files.push(file); return { saved: true }; },
  };
  const api = new Function(...Object.keys(bindings), `${defaults}\n${auth}\n${commit}\n${exporter}
    return { defaultModelDraft, commitModelDraft, exportModelConfigs };
  `)(...Object.values(bindings));
  return { ...api, modelDraft, draft, emissions, files, errors };
}

test('imported model metadata survives actual editor save, config assignment and export', async () => {
  for (const vision of [true, false, undefined]) {
    const f = fixture();
    const [imported] = parseModelConfigImport(JSON.stringify([{ providerName: 'Relay', model: 'model-a',
      displayName: 'Friendly alias', apiKey: 'fake', apiFormat: 'responses', reasoningEffort: 'off',
      ...(vision === undefined ? {} : { visionCapable: vision }) }]));
    f.draft.models = [imported];
    Object.assign(f.modelDraft, f.defaultModelDraft(imported));
    f.commitModelDraft();
    const saved = f.emissions[0][1].models[0];
    assert.equal(saved.visionCapable, vision);
    assert.equal(saved.displayName, 'Friendly alias');
    const config = defaultConfig();
    assignConfig(config, { models: [saved] });
    f.draft.models = config.models;
    await f.exportModelConfigs();
    const [restored] = parseModelConfigImport(f.files[0].content);
    assert.equal(restored.visionCapable, vision);
    assert.equal(restored.displayName, 'Friendly alias');
    assert.equal(restored.apiFormat, 'openai_responses');
    assert.equal(restored.reasoningEffort, 'off');
    assert.deepEqual(f.errors, []);
  }
});

test('editing a model without optional metadata clears the previous editor metadata', () => {
  const f = fixture();
  Object.assign(f.modelDraft, f.defaultModelDraft({ model: 'first', visionCapable: false, displayName: 'First' }));
  Object.assign(f.modelDraft, f.defaultModelDraft({ model: 'second' }));
  assert.equal(f.modelDraft.visionCapable, undefined);
  assert.equal(f.modelDraft.displayName, '');
});

test('invalid model export reports an error without producing an unusable file', async () => {
  const f = fixture();
  f.draft.models = [{}];
  await f.exportModelConfigs();
  assert.equal(f.files.length, 0);
  assert.deepEqual(f.errors, ['settings.modelExportFailed']);
});
