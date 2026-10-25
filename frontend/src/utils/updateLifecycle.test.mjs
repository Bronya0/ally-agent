// SPDX-License-Identifier: GPL-3.0-only
import assert from 'node:assert/strict';
import test from 'node:test';
import { createUpdateRestartHandler } from './updateLifecycle.mjs';

test('update waits for writes before preparing and again before quitting', async () => {
  const events = [];
  let finish;
  const blocked = new Promise(resolve => { finish = resolve; });
  let saves = 0;
  const update = createUpdateRestartHandler({
    flush: async () => { events.push(`save-${++saves}`); if (saves === 2) await blocked; },
    apply: async version => { events.push(version); return { ok: true }; },
    quit: async () => { events.push('quit'); },
  });
  const pending = update('v2.0.0');
  assert.equal(update('v2.0.0'), pending);
  // Wait until the second persistence barrier has actually begun.
  while (saves < 2) await Promise.resolve();
  assert.deepEqual(events, ['save-1', 'v2.0.0', 'save-2']);
  finish();
  await pending;
  assert.equal(events.at(-1), 'quit');
});

test('a failed post-prepare save keeps the app open and permits retry', async () => {
  let saves = 0;
  let quits = 0;
  let applies = 0;
  const update = createUpdateRestartHandler({
    flush: () => { if (++saves === 2) throw new Error('disk full'); },
    apply: async () => {
      assert.equal(++applies, 1, 'the installation must only be replaced once');
      return { ok: true };
    },
    quit: () => { quits++; },
  });
  await assert.rejects(update('v2.0.0'), /disk full/);
  assert.equal(quits, 0);
  await update('v2.0.0');
  assert.equal(quits, 1);
  assert.equal(applies, 1);
});

test('a stalled save times out without installing or quitting', async t => {
  t.mock.timers.enable({ apis: ['setTimeout'] });
  let applies = 0;
  let quits = 0;
  const update = createUpdateRestartHandler({
    flush: () => new Promise(() => {}),
    apply: () => { applies++; return { ok: true }; },
    quit: () => { quits++; },
  });
  const pending = update('v2.0.0');
  const rejected = assert.rejects(pending, /Timed out saving/);
  t.mock.timers.tick(15000);
  await rejected;
  assert.equal(applies, 0);
  assert.equal(quits, 0);
});

test('a preparation failure does not quit', async () => {
  let quits = 0;
  const update = createUpdateRestartHandler({ flush: () => {}, apply: () => ({ ok: false, error: 'bad archive' }), quit: () => { quits++; } });
  await assert.rejects(update('v2.0.0'), /bad archive/);
  assert.equal(quits, 0);
});

test('a failed quit does not replace the installation again', async () => {
  let applies = 0;
  let quits = 0;
  const update = createUpdateRestartHandler({
    flush: () => {},
    apply: () => { applies++; return { ok: true }; },
    quit: () => { if (++quits === 1) throw new Error('quit failed'); },
  });
  await assert.rejects(update('v2.0.0'), /quit failed/);
  await update('v2.0.0');
  assert.equal(applies, 1);
  assert.equal(quits, 2);
});
