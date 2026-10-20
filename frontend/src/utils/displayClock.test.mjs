import assert from 'node:assert/strict';
import test from 'node:test';
import { createDisplayClock } from './displayClock.mjs';

function fixture(options = {}) {
  let time = 1000;
  let sequence = 0;
  const timers = new Map();
  const clock = createDisplayClock({
    now: () => time,
    schedule: (callback, delay) => { const id = ++sequence; timers.set(id, { callback, at: time + delay }); return id; },
    cancel: (id) => timers.delete(id),
    ...options,
  });
  return { clock, timers, advance(value) {
    time = value;
    const due = [...timers].filter(([, timer]) => timer.at <= time);
    for (const [id, timer] of due) { timers.delete(id); timer.callback(); }
  } };
}

test('many presentation subscribers share one timer and release it on disposal', () => {
  const h = fixture();
  const counts = Array(50).fill(0);
  const stops = counts.map((_, index) => h.clock.subscribe(() => counts[index]++));
  assert.equal(h.timers.size, 1);
  h.advance(2000);
  assert.ok(counts.every((count) => count === 2));
  for (const stop of stops) stop();
  assert.equal(h.timers.size, 0);
  // Cleanup is idempotent, including after all other subscriptions have gone.
  stops[0]();
  assert.equal(h.timers.size, 0);
});

test('throwing subscribers do not starve other displays or stop the shared timer', () => {
  const errors = [];
  const h = fixture({ onError: (error) => errors.push(error.message) });
  let ticks = 0;
  h.clock.subscribe(() => { throw new Error('broken display'); });
  h.clock.subscribe(() => ticks++);
  h.advance(2000);
  h.advance(3000);
  assert.equal(ticks, 3);
  assert.equal(errors.length, 3);
  assert.equal(h.timers.size, 1);
  h.clock.setVisible(false);
  h.clock.setVisible(true);
  assert.equal(ticks, 4);
  assert.equal(h.timers.size, 1);
});

test('different cadences stay independent and overdue ticks do not replay a backlog', () => {
  const h = fixture();
  const fast = [], slow = [];
  h.clock.subscribe((time) => fast.push(time), 250);
  h.clock.subscribe((time) => slow.push(time), 1000);
  h.advance(1250);
  assert.deepEqual(fast, [1000, 1250]);
  assert.deepEqual(slow, [1000]);
  h.advance(9000);
  assert.deepEqual(fast, [1000, 1250, 9000]);
  assert.deepEqual(slow, [1000, 9000]);
  assert.equal(h.timers.size, 1);
});

test('hidden windows have no timer and resume immediately from absolute time', () => {
  const h = fixture();
  const values = [];
  h.clock.subscribe((time) => values.push(time));
  h.clock.setVisible(false);
  assert.equal(h.timers.size, 0);
  h.advance(15000);
  assert.deepEqual(values, [1000]);
  h.clock.setVisible(true);
  assert.deepEqual(values, [1000, 15000]);
  assert.equal(h.timers.size, 1);
  h.clock.setVisible(true);
  assert.deepEqual(values, [1000, 15000]);
});
