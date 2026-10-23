// SPDX-License-Identifier: GPL-3.0-only
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';

const source = readFileSync(new URL('../App.vue', import.meta.url), 'utf8');
const start = source.indexOf('function queueConfigSave(snapshot)');
const end = source.indexOf('\nasync function refreshFooterStats', start);
assert.ok(start >= 0 && end > start);
const deferred = () => { let resolve, reject; const promise = new Promise((a, b) => { resolve = a; reject = b; }); return { promise, resolve, reject }; };
function harness(SaveConfig, GetConfig = async () => ({ userAgent: 'committed' })) {
  const config = {}, configDraft = {};
  const queue = new Function('SaveConfig', 'GetConfig', 'fetchPlugins', 'plugins', 'pluginsLoaded', 'config', 'configDraft', 'assignConfig', 'resyncTabModelsFromPresets', 'adoptPristineTabModels', 'applyFontSizes', `
    let configSaveQueue = Promise.resolve();
    let configSaveVersion = 0;
    ${source.slice(start, end)}
    return queueConfigSave;
  `)(SaveConfig, GetConfig, async () => [], { value: [] }, { value: false }, config, configDraft, Object.assign, () => {}, () => {}, () => {});
  return { queue, config, configDraft };
}

test('queued config owns nested model credentials and headers', async () => {
  const received = [];
  const { queue } = harness(async (payload) => received.push(payload));
  const snapshot = { models: [{ apiKeys: ['original'], customHeaders: { 'X-Test': 'original' } }] };
  const save = queue(snapshot);
  snapshot.models[0].apiKeys[0] = 'changed';
  snapshot.models[0].customHeaders['X-Test'] = 'changed';
  await save;
  assert.equal(received[0].models[0].apiKeys[0], 'original');
  assert.equal(received[0].models[0].customHeaders['X-Test'], 'original');
});

test('failed config save restores committed state and queue recovers', async () => {
  let fail = true;
  const { queue, config, configDraft } = harness(async () => { if (fail) throw Error('disk failed'); });
  await assert.rejects(queue({ userAgent: 'unsaved' }), /disk failed/);
  assert.equal(config.userAgent, 'committed');
  assert.equal(configDraft.userAgent, 'committed');
  fail = false;
  await queue({ userAgent: 'next' });
});

test('an older failed save cannot restore over a newer queued edit', async () => {
  const pending = deferred();
  let calls = 0, reads = 0;
  const { queue, config } = harness(() => ++calls === 1 ? pending.promise : Promise.resolve(), async () => { reads++; return { userAgent: 'old' }; });
  const first = queue({ userAgent: 'first' });
  const rejected = assert.rejects(first, /disk failed/);
  const second = queue({ userAgent: 'second' });
  config.userAgent = 'second';
  pending.reject(Error('disk failed'));
  await rejected;
  await second;
  assert.equal(reads, 0);
  assert.equal(config.userAgent, 'second');
});

test('a delayed recovery read cannot overwrite a newer edit', async () => {
  const read = deferred(), started = deferred();
  let calls = 0;
  const { queue, config } = harness(async () => { if (++calls === 1) throw Error('disk failed'); }, () => { started.resolve(); return read.promise; });
  const first = assert.rejects(queue({ userAgent: 'first' }), /disk failed/);
  await started.promise;
  const second = queue({ userAgent: 'second' });
  config.userAgent = 'second';
  read.resolve({ userAgent: 'old' });
  await first;
  await second;
  assert.equal(config.userAgent, 'second');
});
