// SPDX-License-Identifier: GPL-3.0-only
import assert from 'node:assert/strict';
import test from 'node:test';
import { effectiveTaskScope, tasksForView, tasksForWorkspace } from './taskWorkspace.mjs';
import { sameWorkspacePath } from './workspacePath.mjs';
import { isScheduledTaskRunning, isServiceActive } from './taskStatus.mjs';

test('workspace views use ownership rather than cwd or path containment', () => {
  const items = [
    { id: 'owned-extra-cwd', workspace: 'F:/project', cwd: 'F:/other' },
    { id: 'other-owned', workspace: 'F:/other', cwd: 'F:/project' },
    { id: 'nested-workspace', workspace: 'F:/project/nested' },
    { id: 'sibling', workspace: 'F:/project-other' },
    { id: 'legacy', cwd: 'F:/project' },
  ];
  assert.deepEqual(tasksForWorkspace(items, 'f:\\PROJECT\\').map((item) => item.id), ['owned-extra-cwd']);
  assert.deepEqual(tasksForWorkspace(items, ''), []);
  assert.equal(items.length, 5, 'global management must retain all records');
});

test('user views and badges fall back to all when no workspace is selected', () => {
  const services = [{ workspace: 'F:/a', status: 'running' }, { workspace: 'F:/b', status: 'starting' }, { status: 'exited' }];
  const tasks = [{ workspace: 'F:/a', running: true }, { workspace: 'F:/b', running: true }];
  assert.equal(effectiveTaskScope('workspace', ''), 'all');
  assert.equal(effectiveTaskScope('workspace', '  '), 'all');
  assert.equal(tasksForView(services, '').filter(isServiceActive).length, 2);
  assert.equal(tasksForView(tasks, '').filter(isScheduledTaskRunning).length, 2);
  assert.equal(tasksForView(services, 'F:/a').filter(isServiceActive).length, 1);
  assert.equal(tasksForView(services, 'F:/a', 'all'), services);
});

test('normal, knowledge-base and temporary workspace views stay independent', () => {
  const items = ['F:/chat', 'F:/kb', 'F:/temp/run'].map((workspace) => ({ workspace }));
  for (const workspace of items.map((item) => item.workspace)) {
    assert.deepEqual(tasksForWorkspace(items, workspace), [{ workspace }]);
  }
  assert.equal(sameWorkspacePath('/work/A', '/work/a'), false);
  assert.equal(sameWorkspacePath('\\\\server\\Share\\project', '//SERVER/share/project/'), true);
});
