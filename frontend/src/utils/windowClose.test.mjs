// SPDX-License-Identifier: GPL-3.0-only
import assert from 'node:assert/strict';
import test from 'node:test';
import { createWindowCloseHandler } from './windowClose.mjs';

test('close waits for writes and shares the pending request', async () => {
  let finish;
  let flushes = 0;
  let quits = 0;
  const writes = new Promise((resolve) => { finish = resolve; });
  const close = createWindowCloseHandler({
    flush: () => { flushes += 1; return writes; },
    quit: () => { quits += 1; },
  });
  const first = close();
  assert.equal(close(), first);
  await Promise.resolve();
  assert.equal(flushes, 1);
  assert.equal(quits, 0);
  finish();
  await first;
  assert.equal(quits, 1);
});

test('a stalled write cannot prevent normal shutdown', async (t) => {
  t.mock.timers.enable({ apis: ['setTimeout'] });
  let quits = 0;
  const close = createWindowCloseHandler({
    flush: () => new Promise(() => {}),
    quit: () => { quits += 1; },
  });
  const pending = close();
  t.mock.timers.tick(4999);
  await Promise.resolve();
  assert.equal(quits, 0);
  assert.equal(close(), pending);
  t.mock.timers.tick(1);
  await pending;
  assert.equal(quits, 1);
});

test('a failed write still quits; a failed quit can be retried', async (t) => {
  t.mock.method(console, 'error', () => {});
  let attempts = 0;
  const close = createWindowCloseHandler({
    flush: () => Promise.reject(new Error('write failed')),
    quit: () => { if (++attempts === 1) throw new Error('quit failed'); },
  });
  await assert.rejects(close(), /quit failed/);
  await close();
  assert.equal(attempts, 2);
});
