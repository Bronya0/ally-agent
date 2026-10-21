// SPDX-License-Identifier: GPL-3.0-only
import assert from 'node:assert/strict';
import test from 'node:test';
import { effectScope, nextTick, reactive } from 'vue';
import { watchConfigChanges, settingsSnapshot, isValidProxyURL } from './configAutosave.mjs';

function editor(initial, options) {
  const scope = effectScope();
  const draft = reactive(initial);
  const saves = [];
  const flush = scope.run(() => watchConfigChanges(draft, (patch) => saves.push(patch), options));
  return { draft, saves, flush, stop: () => scope.stop() };
}

test('all fields autosave, including new fields, nested edits and clearing values', async () => {
  const { draft, saves, stop } = editor({ userAgent: '', hiddenModes: [], customPrompt: '', future: { value: 1 } });
  try {
    await nextTick();
    assert.deepEqual(saves, [], 'opening settings must not write config');
    draft.userAgent = 'claude-cli/audit';
    draft.hiddenModes.push('games');
    await nextTick();
    assert.deepEqual(saves, [{ userAgent: 'claude-cli/audit', hiddenModes: ['games'] }]);
    draft.future.value = 2;
    draft.newSetting = true;
    await nextTick();
    assert.deepEqual(saves[1], { future: { value: 2 }, newSetting: true });
    draft.userAgent = '';
    draft.hiddenModes.length = 0;
    await nextTick();
    assert.deepEqual(saves[2], { userAgent: '', hiddenModes: [] });
    assert.deepEqual(saves[0].hiddenModes, ['games'], 'queued saves must own their nested values');
  } finally { stop(); }
});

test('a final edit flushes before unmount without a duplicate queued save', async () => {
  const { draft, saves, flush, stop } = editor({ userAgent: '' });
  draft.userAgent = 'last selection';
  flush();
  stop();
  await nextTick();
  assert.deepEqual(saves, [{ userAgent: 'last selection' }]);
});

test('partial saves preserve changes made by other config writers', async () => {
  const live = { userAgent: '', models: [{ model: 'old' }], disabledTools: [] };
  const { draft, saves, stop } = editor(JSON.parse(JSON.stringify(live)));
  try {
    live.models = [{ model: 'new' }];
    live.disabledTools = ['web_fetch'];
    draft.userAgent = 'selected';
    await nextTick();
    Object.assign(live, saves[0]);
    assert.deepEqual(live, { userAgent: 'selected', models: [{ model: 'new' }], disabledTools: ['web_fetch'] });
  } finally { stop(); }
});

test('unfinished proxy edits do not block other settings and apply together when valid', async () => {
  const { draft, saves, stop } = editor({ proxyMode: 'off', proxyUrl: '', proxyNoProxy: '', userAgent: '', backgroundImage: '' }, { snapshot: settingsSnapshot });
  try {
    draft.proxyMode = 'manual';
    await nextTick();
    assert.deepEqual(saves, []);
    draft.userAgent = 'selected';
    draft.proxyNoProxy = 'intranet.test';
    draft.backgroundImage = 'already-saved.png';
    await nextTick();
    assert.deepEqual(saves, [{ userAgent: 'selected' }]);
    draft.proxyUrl = 'socks5://127.0.0.1:7890';
    await nextTick();
    assert.deepEqual(saves[1], { proxyMode: 'manual', proxyUrl: 'socks5://127.0.0.1:7890', proxyNoProxy: 'intranet.test' });
    draft.proxyUrl = '';
    await nextTick();
    assert.equal(saves.length, 2);
    draft.proxyMode = 'off';
    await nextTick();
    assert.deepEqual(saves[2], { proxyMode: 'off', proxyUrl: '', proxyNoProxy: 'intranet.test' });
  } finally { stop(); }
});

test('manual proxies accept supported schemes and reject incomplete addresses', () => {
  for (const url of ['127.0.0.1:7890', 'https://proxy.test:443', 'socks5://user:pass@proxy.test:1080', 'socks5h://proxy.test:1080']) assert.equal(isValidProxyURL(url), true, url);
  for (const url of ['', 'http://', 'socks5://', 'ftp://proxy.test', 'http://bad host']) assert.equal(isValidProxyURL(url), false, url);
});
