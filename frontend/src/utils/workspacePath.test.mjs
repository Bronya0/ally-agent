// SPDX-License-Identifier: GPL-3.0-only
import assert from 'node:assert/strict';
import test from 'node:test';
import { sameWorkspacePath, workspaceHistoryDedupeKey, joinWorkspacePath } from './workspacePath.mjs';

test('POSIX literal backslashes do not merge distinct workspace directories', () => {
  assert.equal(sameWorkspacePath('/work/a\\b', '/work/a/b'), false);
  assert.equal(sameWorkspacePath('/work/repo\\', '/work/repo'), false);
  assert.equal(workspaceHistoryDedupeKey('/work/repo\\/'), '/work/repo\\');
  assert.equal(sameWorkspacePath('/work/A', '/work/a'), false);
});

test('shared join preserves case, platform separators and literal POSIX characters', () => {
  for (const [root, child, expected] of [
    ['/work/Repo\\', '.agents/skills', '/work/Repo\\/.agents/skills'],
    ['/', 'a.txt', '/a.txt'],
    ['C:\\', 'dir/a.txt', 'C:\\dir\\a.txt'],
    ['C:/Work/Repo/', '.agents/skills', 'C:\\Work\\Repo\\.agents\\skills'],
    ['//Server/Share/Repo/', 'a.txt', '\\\\Server\\Share\\Repo\\a.txt'],
    ['', 'a.txt', 'a.txt'],
    ['/work/Repo\\', '', '/work/Repo\\'],
  ]) {
    assert.equal(joinWorkspacePath(root, child), expected);
  }
});

test('UNC identity preserves its network prefix and remains case insensitive', () => {
  assert.equal(sameWorkspacePath('\\\\server\\Share\\repo', '//SERVER/share/repo/'), true);
  assert.equal(sameWorkspacePath('//server/share/repo', '/server/share/repo'), false);
  assert.equal(workspaceHistoryDedupeKey('\\\\SERVER\\Share\\repo\\'), '//server/share/repo');
  assert.equal(sameWorkspacePath('C:\\Work\\Repo\\', 'c:/work/repo'), true);
  assert.equal(workspaceHistoryDedupeKey('C:\\'), 'c:/');
});
