import test from 'node:test';
import assert from 'node:assert/strict';
import { getRecentModelUsage, getSpecificModelUsage, recordModelUsage, recordRecentModelUsage } from './modelUsage.mjs';

test('recent models track selection order, deduplicate, cap and prune removed models', () => {
  const previous = Object.getOwnPropertyDescriptor(globalThis, 'localStorage');
  const values = new Map();
  Object.defineProperty(globalThis, 'localStorage', {
    configurable: true,
    value: { getItem: (key) => values.get(key) ?? null, setItem: (key, value) => values.set(key, value) },
  });
  try {
    const pick = (id, valid = ['a', 'b', 'c', 'd']) => recordModelUsage('provider', ['provider'], id, valid);
    assert.deepEqual(getRecentModelUsage(), []);
    for (const id of ['a', 'b', 'c', 'a']) pick(id);
    assert.deepEqual(getRecentModelUsage(), ['a', 'c', 'b']);
    pick('d');
    assert.deepEqual(getRecentModelUsage(), ['d', 'a', 'c']);
    pick('c', ['c', 'd']);
    assert.deepEqual(getRecentModelUsage(), ['c', 'd']);
    const counts = getSpecificModelUsage();
    recordRecentModelUsage('d', ['c', 'd']);
    assert.deepEqual(getRecentModelUsage(), ['d', 'c']);
    assert.deepEqual(getSpecificModelUsage(), counts);
    recordRecentModelUsage('removed', ['c', 'd']);
    assert.deepEqual(getRecentModelUsage(), ['d', 'c']);
    values.set('ally_recent_models', '{broken');
    assert.deepEqual(getRecentModelUsage(), []);
  } finally {
    if (previous) Object.defineProperty(globalThis, 'localStorage', previous);
    else delete globalThis.localStorage;
  }
});
