// SPDX-License-Identifier: GPL-3.0-only
import assert from 'node:assert/strict';
import test from 'node:test';
import { reactive } from 'vue';
import { diffSettingsPatch, settingsSnapshot, isValidProxyURL } from './configAutosave.mjs';

test('leaving with no edits saves nothing', () => {
  const draft = reactive({ userAgent: '', hiddenModes: [] });
  const entry = settingsSnapshot(draft);
  assert.deepEqual(diffSettingsPatch(entry, draft, { snapshot: settingsSnapshot }), {});
});

test('leave-save sends only changed fields, including new fields and cleared values', () => {
  const draft = reactive({ userAgent: '', hiddenModes: [], customPrompt: '', future: { value: 1 } });
  const entry = settingsSnapshot(draft);
  draft.userAgent = 'claude-cli/audit';
  draft.hiddenModes.push('games');
  draft.future.value = 2;
  draft.newSetting = true;
  const patch = diffSettingsPatch(entry, draft, { snapshot: settingsSnapshot });
  assert.deepEqual(patch, {
    userAgent: 'claude-cli/audit',
    hiddenModes: ['games'],
    future: { value: 2 },
    newSetting: true,
  });
  assert.deepEqual(patch.hiddenModes, ['games'], 'patch must own its nested values');
});

test('unfinished proxy edits stay in the editor until valid', () => {
  const draft = reactive({ proxyMode: 'off', proxyUrl: '', proxyNoProxy: '', userAgent: '', backgroundImage: '' });
  const entry = settingsSnapshot(draft);
  draft.proxyMode = 'manual';
  assert.deepEqual(
    diffSettingsPatch(entry, draft, { snapshot: settingsSnapshot }),
    {},
    'unfinished proxy config must not be saved',
  );
  draft.userAgent = 'selected';
  draft.proxyNoProxy = 'intranet.test';
  draft.backgroundImage = 'already-saved.png';
  assert.deepEqual(
    diffSettingsPatch(entry, draft, { snapshot: settingsSnapshot }),
    { userAgent: 'selected' },
    'other settings save while the proxy address is unfinished',
  );
  draft.proxyUrl = 'socks5://127.0.0.1:7890';
  assert.deepEqual(
    diffSettingsPatch(entry, draft, { snapshot: settingsSnapshot }),
    { proxyMode: 'manual', proxyUrl: 'socks5://127.0.0.1:7890', proxyNoProxy: 'intranet.test', userAgent: 'selected' },
  );
});

test('manual proxies accept supported schemes and reject incomplete addresses', () => {
  for (const url of ['127.0.0.1:7890', 'https://proxy.test:443', 'socks5://user:pass@proxy.test:1080', 'socks5h://proxy.test:1080']) assert.equal(isValidProxyURL(url), true, url);
  for (const url of ['', 'http://', 'socks5://', 'ftp://proxy.test', 'http://bad host']) assert.equal(isValidProxyURL(url), false, url);
});
