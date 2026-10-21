import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import { computed, nextTick, reactive, ref, watch } from 'vue';
import { effectiveTaskScope } from '../utils/taskWorkspace.mjs';

// Execute the component's actual setup with a deferred desktop bridge. No
// WebView is needed to exercise response ordering and polling ownership.
const source = readFileSync(new URL('./TaskCenterPanel.vue', import.meta.url), 'utf8');
const setup = source.match(/<script setup>([\s\S]*?)<\/script>/)[1]
  .replace(/^import .*;\r?$/gm, '');

function harness() {
  const requests = [];
  const timers = new Map();
  const frames = [];
  const stops = [];
  let unmount;
  let sequence = 0;
  const document = { hidden: false };
  const props = reactive({ tasks: [], services: [], scope: 'workspace', workspace: 'F:/a' });
  const dependencies = {
    computed, nextTick, ref, effectiveTaskScope,
    watch: (...args) => { const stop = watch(...args); stops.push(stop); return stop; },
    onUnmounted: (callback) => { unmount = callback; },
    defineProps: () => props,
    defineEmits: () => {},
    GetServiceOutput: (id) => new Promise((resolve, reject) => requests.push({ id, resolve, reject })),
    isServiceActive: (service) => service.status === 'running',
    t: (key) => key,
    formatBytes: String,
    requestAnimationFrame: (callback) => frames.push(callback),
    window: {
      setInterval: (callback) => { const id = ++sequence; timers.set(id, callback); return id; },
      clearInterval: (id) => timers.delete(id),
    },
    document,
  };
  const state = new Function(...Object.keys(dependencies), `${setup}
    return { openServiceLog, openScheduledLog, refreshServiceLog, logVisible,
      logContent, logMeta, logLoading, logServiceActive, viewScope, logPre,
      logFollowing, onLogScroll, resumeLogFollowing };`)(...Object.values(dependencies));
  return { ...state, props, requests, timers, frames, document,
    flushFrames() { frames.splice(0).forEach((callback) => callback()); },
    dispose() { unmount(); stops.forEach((stop) => stop()); } };
}

test('empty workspace cannot leave the disabled workspace filter selected', () => {
  const h = harness();
  try {
    h.props.scope = 'workspace';
    h.props.workspace = '';
    assert.equal(h.viewScope.value, 'all');
  } finally { h.dispose(); }
});

test('changing workspace or scope closes logs and rejects stale output', async () => {
  for (const change of [(props) => { props.workspace = 'F:/b'; }, (props) => { props.scope = 'all'; }]) {
    const h = harness();
    try {
      const pending = h.openServiceLog({ id: 'a', status: 'running' });
      h.logContent.value = 'previous content';
      change(h.props);
      assert.equal(h.logVisible.value, false);
      assert.equal(h.timers.size, 0);
      h.requests[0].resolve({ output: 'obsolete response' });
      await pending;
      assert.equal(h.logContent.value, 'previous content');
    } finally { h.dispose(); }
  }
});

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

test('reading history pauses polling and manual refresh preserves the scroll position', async () => {
  const h = harness();
  try {
    const pending = h.openServiceLog({ id: 'a', status: 'running' });
    h.logPre.value = { scrollTop: 0, clientHeight: 200, scrollHeight: 1000 };
    h.requests[0].resolve({ output: 'initial output' });
    await pending;
    await nextTick();
    h.flushFrames();
    assert.equal(h.logPre.value.scrollTop, 1000, 'opening follows the latest output');

    h.logPre.value.scrollTop = 120;
    h.onLogScroll();
    assert.equal(h.logFollowing.value, false);
    [...h.timers.values()][0]();
    assert.equal(h.requests.length, 1, 'polling must pause while reading history');

    const refresh = h.refreshServiceLog();
    h.requests[1].resolve({ output: 'more output' });
    await refresh;
    h.flushFrames();
    assert.equal(h.logContent.value, 'more output');
    assert.equal(h.logPre.value.scrollTop, 120);

    h.resumeLogFollowing();
    h.requests[2].resolve({ output: 'latest output' });
    await nextTick();
    await nextTick();
    h.flushFrames();
    assert.equal(h.logFollowing.value, true);
    assert.equal(h.logPre.value.scrollTop, 1000);
  } finally { h.dispose(); }
});

test('scrolling up during an automatic request discards its response and queued bottom scrolls', async () => {
  const h = harness();
  try {
    const pending = h.openServiceLog({ id: 'a', status: 'running' });
    h.logPre.value = { scrollTop: 800, clientHeight: 200, scrollHeight: 1000 };
    h.requests[0].resolve({ output: 'readable history' });
    await pending;
    const refresh = h.refreshServiceLog({ automatic: true });
    h.logPre.value.scrollTop = 120;
    h.onLogScroll();
    h.flushFrames();
    assert.equal(h.logPre.value.scrollTop, 120, 'a queued frame must respect scrolling up');
    h.requests[1].resolve({ output: 'new rolling buffer' });
    await refresh;
    assert.equal(h.logContent.value, 'readable history');
    assert.equal(h.logPre.value.scrollTop, 120);

    h.logPre.value.scrollTop = 800;
    h.onLogScroll();
    assert.equal(h.logFollowing.value, true, 'returning to the bottom resumes following');
  } finally { h.dispose(); }
});

test('queued scrolls cannot jump a newly opened log before its own render', async () => {
  const h = harness();
  try {
    const pending = h.openServiceLog({ id: 'a', status: 'running' });
    await nextTick();
    h.openScheduledLog({ id: 'scheduled', lastSummary: 'summary' });
    h.logPre.value = { scrollTop: 120, clientHeight: 200, scrollHeight: 1000 };
    h.flushFrames();
    assert.equal(h.logPre.value.scrollTop, 120, 'the previous log frame is stale');
    h.requests[0].resolve({ output: 'stale service output' });
    await pending;
  } finally { h.dispose(); }
});
