/*
 * SPDX-License-Identifier: GPL-3.0-only
 *
 * Copyright (C) 2026 tangssst <tangssst@qq.com>
 * GitHub: https://github.com/Bronya0/ally-agent
 *
 * This file is part of ally-agent, licensed under the GNU General
 * Public License v3. See the LICENSE file for details.
 */
import assert from 'node:assert/strict';
import test from 'node:test';

import {
  PROMPT_HISTORY_MAX_ENTRIES,
  PROMPT_HISTORY_MAX_ENTRY_CHARS,
  PROMPT_HISTORY_MAX_TOTAL_CHARS,
  PROMPT_HISTORY_STORAGE_KEY,
  createPromptHistoryStore,
} from './promptHistoryStore.mjs';

function fakeStorage(initial = {}) {
  const map = new Map(Object.entries(initial));
  return {
    getItem: (key) => (map.has(key) ? map.get(key) : null),
    setItem: (key, value) => { map.set(key, String(value)); },
    removeItem: (key) => { map.delete(key); },
    entries: () => [...map.entries()],
  };
}

const store = (storage) => createPromptHistoryStore(storage);

test('history survives a new store instance (restart) per workspace', () => {
  const storage = fakeStorage();
  const first = store(storage);
  first.record('d:/ws/a', 'first');
  first.record('d:/ws/a', 'second');
  first.record('d:/ws/b', 'other');

  const afterRestart = store(storage);
  assert.deepEqual(afterRestart.list('d:/ws/a'), ['first', 'second']);
  assert.deepEqual(afterRestart.list('d:/ws/b'), ['other']);
  assert.deepEqual(afterRestart.list('d:/ws/c'), []);
});

test('per-workspace entry cap keeps the newest entries', () => {
  const storage = fakeStorage();
  const history = store(storage);
  for (let i = 0; i < PROMPT_HISTORY_MAX_ENTRIES + 5; i++) {
    history.record('d:/ws/a', `prompt-${i}`);
  }

  const entries = store(storage).list('d:/ws/a');
  assert.equal(entries.length, PROMPT_HISTORY_MAX_ENTRIES);
  assert.equal(entries[0], 'prompt-5');
  assert.equal(entries.at(-1), `prompt-${PROMPT_HISTORY_MAX_ENTRIES + 4}`);
});

test('recording an existing prompt moves it to the newest position', () => {
  const storage = fakeStorage();
  const history = store(storage);
  history.record('d:/ws/a', 'one');
  history.record('d:/ws/a', 'two');
  assert.deepEqual(history.record('d:/ws/a', 'one'), ['two', 'one']);
});

test('oversized prompts are not stored and never clear existing history', () => {
  const storage = fakeStorage();
  const history = store(storage);
  history.record('d:/ws/a', 'keep me');
  const oversized = 'x'.repeat(PROMPT_HISTORY_MAX_ENTRY_CHARS + 1);

  assert.deepEqual(history.record('d:/ws/a', oversized), ['keep me']);
  history.record('d:/ws/b', oversized);
  assert.deepEqual(store(storage).list('d:/ws/a'), ['keep me']);
  assert.deepEqual(store(storage).list('d:/ws/b'), []);
});

test('whole-store budget evicts the least recently written workspace first', () => {
  const storage = fakeStorage();
  const history = store(storage);
  const bigEntry = (tag) => `${String(tag).padStart(4, '0')}${'x'.repeat(PROMPT_HISTORY_MAX_ENTRY_CHARS - 4)}`;
  for (let i = 0; i < PROMPT_HISTORY_MAX_ENTRIES; i++) history.record('d:/ws/old', bigEntry(i));
  for (let i = 0; i < PROMPT_HISTORY_MAX_ENTRIES; i++) history.record('d:/ws/new', bigEntry(i));

  const reloaded = store(storage);
  const total = [...reloaded.list('d:/ws/old'), ...reloaded.list('d:/ws/new')]
    .reduce((sum, entry) => sum + entry.length, 0);
  assert.ok(total <= PROMPT_HISTORY_MAX_TOTAL_CHARS, `total ${total} exceeds budget`);
  assert.equal(reloaded.list('d:/ws/new').length, PROMPT_HISTORY_MAX_ENTRIES);
  assert.ok(reloaded.list('d:/ws/old').length < PROMPT_HISTORY_MAX_ENTRIES);
});

test('remove drops only the requested workspace and persists', () => {
  const storage = fakeStorage();
  const history = store(storage);
  history.record('d:/ws/a', 'a');
  history.record('d:/ws/b', 'b');
  history.remove('d:/ws/a');

  const reloaded = store(storage);
  assert.deepEqual(reloaded.list('d:/ws/a'), []);
  assert.deepEqual(reloaded.list('d:/ws/b'), ['b']);
});

test('corrupt or foreign payloads degrade to an empty history', () => {
  for (const raw of ['not json', '[]', '{"version":1,"buckets":[["d:/ws/a",["x"]]]}', '{"version":2,"buckets":"nope"}']) {
    const history = store(fakeStorage({ [PROMPT_HISTORY_STORAGE_KEY]: raw }));
    assert.deepEqual(history.list('d:/ws/a'), [], `payload ${raw}`);
  }
});

test('stored payload is sanitized on load', () => {
  const payload = JSON.stringify({
    version: 2,
    buckets: [
      ['d:/ws/a', ['ok', 42, '', 'ok', 'y'.repeat(PROMPT_HISTORY_MAX_ENTRY_CHARS + 1)]],
      ['d:/ws/b', []],
      ['d:/ws/c', 'not-an-array'],
    ],
  });
  const history = store(fakeStorage({ [PROMPT_HISTORY_STORAGE_KEY]: payload }));
  assert.deepEqual(history.list('d:/ws/a'), ['ok']);
  assert.deepEqual(history.list('d:/ws/b'), []);
  assert.deepEqual(history.list('d:/ws/c'), []);
});

test('a storage that rejects writes keeps the history for this session', () => {
  const storage = {
    getItem: () => null,
    setItem: () => { throw new Error('QuotaExceededError'); },
  };
  const history = store(storage);
  assert.deepEqual(history.record('d:/ws/a', 'kept in memory'), ['kept in memory']);
  assert.deepEqual(history.record('d:/ws/a', 'second'), ['kept in memory', 'second']);
  assert.deepEqual(history.list('d:/ws/a'), ['kept in memory', 'second']);
});
