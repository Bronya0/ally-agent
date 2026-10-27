// SPDX-License-Identifier: GPL-3.0-only
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import { ref } from 'vue';

const app = readFileSync(new URL('../App.vue', import.meta.url), 'utf8');
const navigation = app.slice(app.indexOf('async function switchMode('), app.indexOf('const kbPicking ='));

function harness(hidden = false) {
  const mode = ref('kb');
  const preOverlayMode = ref('chat');
  let refreshes = 0;
  const bindings = {
    mode, preOverlayMode,
    isModeEnterable: (key) => key !== 'tasks' || !hidden,
    isOverlayMode: (key) => ['tasks', 'settings'].includes(key),
    pluginForMode: () => null,
    refreshTaskCenter: async () => { refreshes++; },
    focusChatWorkspace: async () => { mode.value = 'chat'; },
  };
  const switchMode = new Function(...Object.keys(bindings), `${navigation}; return switchMode;`)(...Object.values(bindings));
  return { mode, preOverlayMode, switchMode, refreshes: () => refreshes };
}

test('task page preserves the return workspace and refreshes once on each entry', async () => {
  const h = harness();
  await h.switchMode('tasks');
  assert.equal(h.mode.value, 'tasks');
  assert.equal(h.preOverlayMode.value, 'kb');
  assert.equal(h.refreshes(), 1);
  await h.switchMode('tasks');
  assert.equal(h.refreshes(), 1);
  await h.switchMode('chat');
  await h.switchMode('tasks');
  assert.equal(h.refreshes(), 2);
  assert.equal(h.preOverlayMode.value, 'chat');
});

test('hidden task page rejects programmatic navigation without loading tasks', async () => {
  const h = harness(true);
  await h.switchMode('tasks');
  assert.equal(h.mode.value, 'chat');
  assert.equal(h.refreshes(), 0);
});
