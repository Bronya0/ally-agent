import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';

const source = readFileSync(new URL('../App.vue', import.meta.url), 'utf8');
const start = source.indexOf('async function refreshSessionIndex()');
const end = source.indexOf('async function migrateLegacySessionStorage()', start);
assert.ok(start >= 0 && end > start);
function deferred() {
  let resolve;
  const promise = new Promise((yes) => { resolve = yes; });
  return { promise, resolve };
}
function fixture(reads) {
  const sessions = { value: [] };
  const calls = [];
  const bindings = {
    sessions,
    activeSessionId: { value: 'active' }, workspaceTabs: { value: [] },
    sessionIndexRequestVersion: 0, startupDisposed: false,
    sessionWriteTail: Promise.resolve(),
    ListSessionIndex: () => { calls.push(true); return reads.shift(); },
    applySessionIndexEntry: (session, entry) => Object.assign(session, entry),
    sessionFromIndexEntry: (entry) => ({ ...entry }),
    sessionMayHaveBackgroundRun: () => false, trimRuntimeSessions: () => {},
  };
  const api = new Function(...Object.keys(bindings), `
    function flushSessionWrites() { return sessionWriteTail; }
    ${source.slice(start, end)}
    return { refreshSessionIndex,
      setWrites(value) { sessionWriteTail = value; },
      dispose() { startupDisposed = true; sessionIndexRequestVersion++; } };
  `)(...Object.values(bindings));
  return { ...api, sessions, calls };
}
const tick = () => new Promise((resolve) => setImmediate(resolve));

test('session index loading waits for pending local writes', async () => {
  const writes = deferred();
  const f = fixture([Promise.resolve([{ id: 'saved' }])]);
  f.setWrites(writes.promise);
  const pending = f.refreshSessionIndex();
  await tick();
  assert.equal(f.calls.length, 0);
  writes.resolve();
  await pending;
  assert.equal(f.sessions.value[0].id, 'saved');
});

test('background repair refresh cannot restore a row deleted during its read', async () => {
  const stale = deferred(), deletion = deferred();
  const f = fixture([stale.promise, Promise.resolve([])]);
  f.sessions.value = [{ id: 'deleted' }];
  const pending = f.refreshSessionIndex();
  await tick();
  f.sessions.value = [];
  f.setWrites(deletion.promise);
  stale.resolve([{ id: 'deleted' }]);
  await tick();
  assert.equal(f.calls.length, 1);
  assert.deepEqual(f.sessions.value, []);
  deletion.resolve();
  await pending;
  assert.equal(f.calls.length, 2);
  assert.deepEqual(f.sessions.value, []);
});

test('the latest index request wins and unmount suppresses late results', async () => {
  const old = deferred(), latest = deferred();
  const f = fixture([old.promise, latest.promise]);
  const first = f.refreshSessionIndex();
  await tick();
  const second = f.refreshSessionIndex();
  await tick();
  latest.resolve([{ id: 'latest' }]);
  await second;
  old.resolve([{ id: 'obsolete' }]);
  await first;
  assert.equal(f.sessions.value[0].id, 'latest');
  const late = deferred();
  const disposed = fixture([late.promise]);
  const pending = disposed.refreshSessionIndex();
  await tick();
  disposed.dispose();
  late.resolve([{ id: 'late' }]);
  await pending;
  assert.deepEqual(disposed.sessions.value, []);
});
