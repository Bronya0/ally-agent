// SPDX-License-Identifier: GPL-3.0-only
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import vm from 'node:vm';
import { createWindowCloseHandler } from './windowClose.mjs';
import { createUpdateRestartHandler } from './updateLifecycle.mjs';

// Exercise App.vue's real barrier without mounting the desktop application.
const appSource = readFileSync(new URL('../App.vue', import.meta.url), 'utf8');
const barrierSource = appSource.slice(
  appSource.indexOf('async function flushPendingWindowWrites()'),
  appSource.indexOf('const requestWindowClose ='),
);

for (const action of ['close', 'update']) {
  for (const failedQueue of ['sessionWriteTail', 'configSaveQueue']) {
    test(`${action} waits for the other save despite an earlier ${failedQueue} failure`, async () => {
      const events = [];
      let finishSave;
      const writing = new Promise(resolve => { finishSave = resolve; });
      const failed = Promise.reject(new Error('old save failed'));
      failed.catch(() => {});
      const context = {
        settingsModalRef: { value: null },
        saveSessionsTimer: 0,
        nextTick: async () => {},
        sessionWriteTail: failedQueue === 'sessionWriteTail' ? failed : writing,
        configSaveQueue: failedQueue === 'configSaveQueue' ? failed : writing,
      };
      const flush = vm.runInNewContext(`${barrierSource}\nflushPendingWindowWrites`, context);
      const options = {
        flush,
        apply: () => { events.push('apply'); return { ok: true }; },
        quit: () => { events.push('quit'); },
      };
      const run = action === 'close'
        ? createWindowCloseHandler(options)
        : createUpdateRestartHandler(options);
      const pending = run('v2.0.0');
      // Attach immediately so a regression produces assertions, not an unhandled rejection.
      const settled = pending.then(() => null, error => error);
      await new Promise(resolve => setImmediate(resolve));
      const beforeSave = [...events];
      events.push('saved');
      finishSave();
      const error = await settled;
      assert.deepEqual(beforeSave, [], 'must wait for the pending write before applying or quitting');
      assert.equal(error, null, 'an old queue failure must not block shutdown or updates');
      assert.deepEqual(events, action === 'close' ? ['saved', 'quit'] : ['saved', 'apply', 'quit']);
    });
  }
}
