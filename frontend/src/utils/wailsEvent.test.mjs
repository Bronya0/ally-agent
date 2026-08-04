import test from 'node:test';
import assert from 'node:assert/strict';

import { unwrapWailsEvent } from './wailsEvent.mjs';

test('unwraps Wails v3 event data before application handlers receive it', () => {
  const payload = { runId: 'run-1', name: 'read', result: '{}' };
  assert.deepEqual(
    unwrapWailsEvent({ name: 'tool:result', data: payload, sender: 'main' }),
    payload,
  );
});

test('preserves null Wails event data instead of passing the wrapper', () => {
  assert.equal(unwrapWailsEvent({ name: 'run:done', data: null }), null);
});

test('leaves plain payloads unchanged', () => {
  const payload = { runId: 'run-1', content: '你好' };
  assert.equal(unwrapWailsEvent(payload, 'run:delta'), payload);
});

test('does not unwrap a plain payload that happens to contain name and data', () => {
  const payload = { name: 'read', data: { path: 'README.md' } };
  assert.equal(unwrapWailsEvent(payload, 'tool:result'), payload);
});
