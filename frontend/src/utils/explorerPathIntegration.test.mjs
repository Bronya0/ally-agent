// SPDX-License-Identifier: GPL-3.0-only
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import { joinWorkspacePath } from './workspacePath.mjs';

const source = readFileSync(new URL('../components/WorkspaceExplorer.vue', import.meta.url), 'utf8');
function section(start, end) {
  const from = source.indexOf(start);
  const to = source.indexOf(end, from);
  assert.ok(from >= 0 && to > from);
  return source.slice(from, to);
}

for (const [root, expected] of [
  ['/tmp/repo\\', '/tmp/repo\\/dir/a.txt'],
  ['/', '/dir/a.txt'],
  ['C:\\', 'C:\\dir\\a.txt'],
  ['//server/share/repo/', '\\\\server\\share\\repo\\dir\\a.txt'],
]) {
  test(`copy full path preserves root semantics: ${root}`, () => {
    let copied;
    new Function('props', 'copyTextToClipboard', 'joinWorkspacePath', `${section('function copyNodePaths(', '// 资源树内 Ctrl/Cmd+C')}
      copyNodePaths(['dir/a.txt'], true);`)(
      { workspace: root }, (text) => { copied = text; }, joinWorkspacePath,
    );
    assert.equal(copied, expected);
  });
}

test('skill directory hints use the shared join without corrupting POSIX names', () => {
  const skills = readFileSync(new URL('../components/SkillsPanel.vue', import.meta.url), 'utf8');
  const from = skills.indexOf('const skillTargetHint =');
  const to = skills.indexOf('function openSkillEditor()', from);
  assert.ok(from >= 0 && to > from);
  const hint = new Function('props', 'skillDraft', 'computed', 't', 'joinWorkspacePath',
    `${skills.slice(from, to)}; return skillTargetHint;`)(
    { projectRoot: '/work/Repo\\' }, { target: 'project' }, (fn) => fn(),
    (key, values) => values.path, joinWorkspacePath,
  );
  assert.equal(hint, '/work/Repo\\/.agents/skills');
});

test('browser attachments never invent workspace paths from display filenames', () => {
  const app = readFileSync(new URL('../App.vue', import.meta.url), 'utf8');
  const from = app.indexOf('  const rawPath = String(file.path');
  const to = app.indexOf("  try {\n    if (kind === 'image')", from);
  assert.ok(from >= 0 && to > from);
  const resolve = new Function('file', 'activeRunWorkspace', 'joinWorkspacePath',
    `const base = { filePath: '' }; ${app.slice(from, to)}; return base.filePath;`);
  assert.equal(resolve({ name: 'pasted.png' }, { value: '/work/Repo\\' }, joinWorkspacePath), '');
  assert.equal(resolve({ name: 'pasted.jpg' }, { value: 'C:/Work/Repo' }, joinWorkspacePath), '');
  assert.equal(resolve({ name: 'a.txt', path: '/actual/a.txt' }, { value: '/work' }, joinWorkspacePath), '/actual/a.txt');
});
