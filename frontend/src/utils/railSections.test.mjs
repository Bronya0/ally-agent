import test from 'node:test';
import assert from 'node:assert/strict';
import { loadRailSectionCollapsed, saveRailSectionCollapsed } from './railSections.mjs';

function memoryStorage() {
  const values = new Map();
  return {
    getItem: (key) => values.get(key) ?? null,
    setItem: (key, value) => values.set(key, value),
    values,
  };
}

test('both rail sections default to collapsed without saved preferences', () => {
  const storage = memoryStorage();
  for (const section of ['workspaces', 'sessions']) {
    assert.equal(loadRailSectionCollapsed(section, storage), true);
  }
  assert.equal(storage.values.size, 0);
});

test('each section remembers expansion and collapse independently across reloads', () => {
  const storage = memoryStorage();
  saveRailSectionCollapsed('workspaces', false, storage);
  assert.equal(loadRailSectionCollapsed('workspaces', storage), false);
  assert.equal(loadRailSectionCollapsed('sessions', storage), true);
  saveRailSectionCollapsed('sessions', false, storage);
  saveRailSectionCollapsed('workspaces', true, storage);
  assert.equal(loadRailSectionCollapsed('sessions', storage), false);
  assert.equal(loadRailSectionCollapsed('workspaces', storage), true);
  assert.equal(storage.values.size, 2);
});

test('invalid storage and unknown sections fail safely without creating extra keys', () => {
  const storage = memoryStorage();
  storage.setItem('ally_rail_sessions_expanded', 'unexpected');
  assert.equal(loadRailSectionCollapsed('sessions', storage), true);
  saveRailSectionCollapsed('unknown', false, storage);
  assert.equal(storage.values.size, 1);
  const blocked = {
    getItem() { throw new Error('blocked'); },
    setItem() { throw new Error('blocked'); },
  };
  assert.equal(loadRailSectionCollapsed('sessions', blocked), true);
  assert.doesNotThrow(() => saveRailSectionCollapsed('sessions', false, blocked));
});
