// SPDX-License-Identifier: GPL-3.0-only
import test from 'node:test';
import assert from 'node:assert/strict';
import { createTextCache } from './highlightCache.mjs';

test('text cache budgets source keys and rendered lines, evicting least recently used', () => {
  const cache = createTextCache({ maxEntries: 3, maxChars: 12, maxEntryChars: 8 });
  cache.set('a', ['xx']); // four chars, including the line separator
  cache.set('b', 'xxx');
  cache.set('c', 'xxx');
  assert.equal(cache.chars, 12);
  assert.deepEqual(cache.get('a'), ['xx']);
  cache.set('d', 'xxx');
  assert.equal(cache.get('b'), undefined);
  assert.equal(cache.size, 3);
  assert.equal(cache.chars, 12);
});

test('oversized replacements are not retained and release the previous cost', () => {
  const cache = createTextCache({ maxEntries: 3, maxChars: 10, maxEntryChars: 6 });
  cache.set('a', 'xx');
  cache.set('a', 'x'.repeat(10));
  assert.equal(cache.get('a'), undefined);
  assert.equal(cache.chars, 0);
  cache.set('long-key', 'x');
  assert.equal(cache.size, 0);
  for (let i = 0; i < 1000; i++) cache.set(String(i), 'xx');
  assert.ok(cache.size <= 3);
  assert.ok(cache.chars <= 10);
});
