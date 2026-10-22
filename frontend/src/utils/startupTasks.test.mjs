import test from 'node:test';
import assert from 'node:assert/strict';
import { createStartupTasks } from './startupTasks.mjs';

function fakeHost(idle = true) {
  let id = 0;
  const timers = new Map();
  const idles = new Map();
  return {
    timers, idles,
    setTimeout(fn, delay) { timers.set(++id, { fn, delay }); return id; },
    clearTimeout(key) { timers.delete(key); },
    ...(idle ? {
      requestIdleCallback(fn, options) { idles.set(++id, { fn, ...options }); return id; },
      cancelIdleCallback(key) { idles.delete(key); },
    } : {}),
    flushTimers() { const jobs = [...timers.values()]; timers.clear(); for (const job of jobs) job.fn(); },
    flushIdle() { const jobs = [...idles.values()]; idles.clear(); for (const job of jobs) job.fn(); },
  };
}

test('startup requests wait for their delay and an idle slot with a timeout', async () => {
  const host = fakeHost();
  const tasks = createStartupTasks(host);
  let ran = false;
  tasks.schedule(() => { ran = true; }, { delay: 10000, timeout: 2000 });
  assert.equal([...host.timers.values()][0].delay, 10000);
  assert.equal(ran, false);
  host.flushTimers();
  assert.equal(ran, false);
  assert.equal([...host.idles.values()][0].timeout, 2000);
  host.flushIdle();
  await Promise.resolve();
  assert.equal(ran, true);
});

test('unmount cancels delayed, idle and microtask-queued startup requests', async () => {
  for (const phase of ['timer', 'idle', 'microtask']) {
    const host = fakeHost();
    const tasks = createStartupTasks(host);
    let ran = false;
    tasks.schedule(() => { ran = true; });
    if (phase !== 'timer') host.flushTimers();
    if (phase === 'microtask') host.flushIdle();
    tasks.dispose();
    host.flushTimers();
    host.flushIdle();
    tasks.schedule(() => { ran = true; });
    await Promise.resolve();
    assert.equal(ran, false, phase);
    assert.equal(host.timers.size + host.idles.size, 0);
  }
});

test('startup tasks work without requestIdleCallback and report async failures', async () => {
  const host = fakeHost(false);
  const errors = [];
  const tasks = createStartupTasks(host, (error) => errors.push(error));
  const failure = new Error('failed');
  let ran = 0;
  tasks.schedule(() => { ran++; });
  tasks.schedule(async () => { throw failure; });
  const cancel = tasks.schedule(() => { ran++; });
  cancel();
  host.flushTimers();
  await new Promise((resolve) => setImmediate(resolve));
  assert.equal(ran, 1);
  assert.deepEqual(errors, [failure]);
});
