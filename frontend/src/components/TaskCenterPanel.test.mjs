import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import { computed, nextTick, ref, watch } from 'vue';

// Execute the component's actual setup with a deferred desktop bridge. No
// WebView is needed to exercise response ordering and polling ownership.
const source = readFileSync(new URL('./TaskCenterPanel.vue', import.meta.url), 'utf8');
const setup = source.match(/<script setup>([\s\S]*?)<\/script>/)[1]
  .replace(/^import .*;\r?$/gm, '');

function harness() {
  const requests = [];
  const timers = new Map();
  const stops = [];
  let unmount;
  let sequence = 0;
  const document = { hidden: false };
  const dependencies = {
    computed, nextTick, ref,
    watch: (...args) => { const stop = watch(...args); stops.push(stop); return stop; },
    onUnmounted: (callback) => { unmount = callback; },
    defineProps: () => ({ tasks: [], services: [] }),
    defineEmits: () => {},
    GetServiceOutput: (id) => new Promise((resolve, reject) => requests.push({ id, resolve, reject })),
    isServiceActive: (service) => service.status === 'running',
    t: (key) => key,
    formatBytes: String,
    requestAnimationFrame: (callback) => callback(),
    window: {
      setInterval: (callback) => { const id = ++sequence; timers.set(id, callback); return id; },
      clearInterval: (id) => timers.delete(id),
    },
    document,
  };
  const state = new Function(...Object.keys(dependencies), `${setup}
    return { openServiceLog, openScheduledLog, refreshServiceLog, logVisible,
      logContent, logMeta, logLoading, logServiceActive };`)(...Object.values(dependencies));
  return { ...state, requests, timers, document, dispose() { unmount(); stops.forEach((stop) => stop()); } };
}

test('late service output cannot overwrite a subsequently opened scheduled log', async () => {
  const h = harness();
  try {
    const pending = h.openServiceLog({ id: 'a', status: 'running' });
    h.openScheduledLog({ id: 'scheduled', lastSummary: 'keep this summary' });
    const content = h.logContent.value;
    h.requests[0].resolve({ output: 'obsolete output' });
    await pending;
    assert.equal(h.logContent.value, content);
    assert.equal(h.logLoading.value, false);
    assert.equal(h.timers.size, 0);
  } finally { h.dispose(); }
});

test('stale failures and finally handlers cannot change the next service request', async () => {
  const h = harness();
  try {
    const first = h.openServiceLog({ id: 'a', status: 'running' });
    const second = h.openServiceLog({ id: 'b', status: 'running' });
    h.requests[0].reject(new Error('obsolete failure'));
    await first;
    assert.equal(h.logMeta.value, '');
    assert.equal(h.logLoading.value, true);
    assert.equal(h.timers.size, 1);
    h.requests[1].resolve({ output: 'service b' });
    await second;
    assert.equal(h.logContent.value, 'service b');
    assert.equal(h.logLoading.value, false);
  } finally { h.dispose(); }
});

test('closing and reopening the same service invalidates the previous response', async () => {
  const h = harness();
  try {
    const first = h.openServiceLog({ id: 'a', status: 'running' });
    h.logVisible.value = false;
    assert.equal(h.timers.size, 0);
    const second = h.openServiceLog({ id: 'a', status: 'running' });
    assert.equal(h.requests.length, 1, 'reopening must wait for the previous bridge request');
    h.requests[0].resolve({ output: 'old' });
    await first;
    assert.equal(h.requests.length, 2);
    h.requests[1].resolve({ output: 'new' });
    await second;
    assert.equal(h.logContent.value, 'new');
  } finally { h.dispose(); }
});

test('repeated reopening coalesces into one fresh request after the old request settles', async () => {
  const h = harness();
  try {
    const calls = [h.openServiceLog({ id: 'a', status: 'running' })];
    for (let i = 0; i < 10; i++) {
      h.logVisible.value = false;
      calls.push(h.openServiceLog({ id: 'a', status: 'running' }));
    }
    assert.equal(h.requests.length, 1);
    h.requests[0].resolve({ output: 'old' });
    await calls[0];
    assert.equal(h.requests.length, 2);
    h.requests[1].resolve({ output: 'latest' });
    await Promise.all(calls);
    assert.equal(h.logContent.value, 'latest');
  } finally { h.dispose(); }
});

test('polling is single flight, skips hidden windows and stops with the service', async () => {
  const h = harness();
  try {
    const pending = h.openServiceLog({ id: 'a', status: 'running' });
    const tick = () => [...h.timers.values()][0]();
    tick();
    await h.refreshServiceLog();
    assert.equal(h.requests.length, 1);
    h.requests[0].resolve({ output: 'ready' });
    await pending;
    h.document.hidden = true;
    tick();
    assert.equal(h.requests.length, 1);
    h.document.hidden = false;
    tick();
    assert.equal(h.requests.length, 2);
    h.logServiceActive.value = false;
    assert.equal(h.timers.size, 0);
    h.requests[1].resolve({ output: 'final' });
    await nextTick();
  } finally { h.dispose(); }
});

test('unmount stops polling and discards in-flight results', async () => {
  const h = harness();
  const pending = h.openServiceLog({ id: 'a', status: 'running', outputTail: 'tail' });
  h.dispose();
  assert.equal(h.timers.size, 0);
  h.requests[0].resolve({ output: 'late output' });
  await pending;
  assert.equal(h.logContent.value, 'tail');
});
