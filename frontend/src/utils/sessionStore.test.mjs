import assert from 'node:assert/strict';
import test from 'node:test';
import { createSerialWriteQueue } from './sessionStore.mjs';

test('serial write queue preserves completed snapshot order', async () => {
  const queue = createSerialWriteQueue();
  const events = [];
  let releaseFirst;
  let signalFirstStarted;
  const firstGate = new Promise((resolve) => { releaseFirst = resolve; });
  const firstStarted = new Promise((resolve) => { signalFirstStarted = resolve; });

  const first = queue.enqueue(async () => {
    events.push('first:start');
    signalFirstStarted();
    await firstGate;
    events.push('first:end');
  });
  const second = queue.enqueue(async () => {
    events.push('second');
  });

  await firstStarted;
  assert.deepEqual(events, ['first:start']);
  releaseFirst();
  await Promise.all([first, second, queue.flush()]);
  assert.deepEqual(events, ['first:start', 'first:end', 'second']);
});

test('serial write queue continues after a failed snapshot', async () => {
  const queue = createSerialWriteQueue();
  const events = [];
  await assert.rejects(queue.enqueue(async () => {
    events.push('failed');
    throw new Error('quota');
  }), /quota/);
  await queue.enqueue(async () => {
    events.push('recovered');
  });
  await queue.flush();
  assert.deepEqual(events, ['failed', 'recovered']);
});
