// SPDX-License-Identifier: GPL-3.0-only
// 控制器状态机：定时器拥有权、说话打断、暂停/减少动效的边界。
import test from 'node:test';
import assert from 'node:assert/strict';
import { createPosePlayer, POSE_HOLD_MS } from './avatarPose.mjs';

function fixture(t) {
  t.mock.timers.enable({ apis: ['setTimeout'] });
  const state = { active: true, reduced: false, names: [], pose: '' };
  const player = createPosePlayer({
    canPlay: () => state.active && !state.reduced,
    idleNames: () => state.names,
    hasPose: (name) => ['proud', 'stretch', 'happy'].includes(name),
    show: (name) => { state.pose = name; },
    random: () => 0,
  });
  t.after(player.stop);
  return { state, ...player };
}

test('switching from eye starts poses and successive idle actions alternate', (t) => {
  const f = fixture(t);
  f.schedule();
  t.mock.timers.tick(6000);
  assert.equal(f.state.pose, '');
  f.state.names = ['proud', 'stretch'];
  f.reset();
  t.mock.timers.tick(6000);
  assert.equal(f.state.pose, 'proud');
  t.mock.timers.tick(POSE_HOLD_MS);
  assert.equal(f.state.pose, '');
  t.mock.timers.tick(6000);
  assert.equal(f.state.pose, 'stretch');
});

test('speech interrupts idle without the old timer ending it early', (t) => {
  const f = fixture(t);
  f.play('proud');
  t.mock.timers.tick(1000);
  f.play('happy', 3400);
  t.mock.timers.tick(1600);
  assert.equal(f.state.pose, 'happy');
  t.mock.timers.tick(1800);
  assert.equal(f.state.pose, '');
});

test('stop clears all scheduled work; reduced motion prevents restart', (t) => {
  const f = fixture(t);
  f.state.names = ['proud', 'stretch'];
  f.play('proud');
  f.stop();
  t.mock.timers.tick(60000);
  assert.equal(f.state.pose, '');
  f.schedule();
  f.stop();
  t.mock.timers.tick(60000);
  assert.equal(f.state.pose, '');
  f.state.reduced = true;
  f.reset();
  assert.equal(f.play('happy'), false);
  t.mock.timers.tick(60000);
  assert.equal(f.state.pose, '');
});

test('character reset clears an old pose; hidden callbacks cannot start a pose', (t) => {
  const f = fixture(t);
  f.state.names = ['proud', 'stretch'];
  f.play('stretch');
  f.reset();
  assert.equal(f.state.pose, '');
  f.state.active = false;
  t.mock.timers.tick(60000);
  assert.equal(f.state.pose, '');
  assert.equal(f.play('unknown'), false);
});
