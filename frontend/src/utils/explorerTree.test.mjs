import test from 'node:test';
import assert from 'node:assert/strict';
import { mergeExplorerNodes, refreshExpandedExplorerNodes, explorerNodeKeys, remapExplorerNodes, remapExplorerPath } from './explorerTree.mjs';

const dir = (path, children = []) => ({ key: path, path, dir: true, children });
const file = (path) => ({ key: path, path, dir: false });

test('refresh reuses surviving subtrees while updating metadata and removing deleted entries', () => {
  const child = dir('a/b', [file('a/b/c')]);
  const original = dir('a', [child]);
  original.size = 1;
  const result = mergeExplorerNodes([original, file('removed')], [{ ...dir('a'), size: 2 }, file('new')]);
  assert.equal(result[0], original);
  assert.equal(result[0].children[0], child);
  assert.equal(result[0].size, 2);
  assert.deepEqual(result.map((node) => node.key), ['a', 'new']);
  assert.deepEqual(mergeExplorerNodes([original], [file('a')]), [file('a')]);
});

test('full refresh loads only visible expanded descendants of surviving nodes', async () => {
  const root = dir('a', [dir('a/removed')]);
  const collapsed = dir('closed', [dir('closed/hidden')]);
  const expanded = new Set(['a', 'a/b', 'a/removed', 'closed/hidden']);
  const calls = [];
  await refreshExpandedExplorerNodes([root, collapsed], (key) => expanded.has(key), async (node) => {
    calls.push(node.path);
    if (node.path === 'a') node.children = mergeExplorerNodes(node.children, [dir('a/b')]);
    return true;
  }, () => true);
  assert.deepEqual(calls, ['a', 'a/b']);
});

test('refresh limits concurrent requests and stops discovering children after workspace changes', async () => {
  let active = 0;
  let peak = 0;
  let current = true;
  let calls = 0;
  await refreshExpandedExplorerNodes(Array.from({ length: 9 }, (_, i) => dir(String(i))), () => true, async (node) => {
    calls++;
    peak = Math.max(peak, ++active);
    await Promise.resolve();
    node.children = [dir(`${node.path}/child`)];
    current = false;
    active--;
    return true;
  }, () => current);
  assert.equal(peak, 4);
  assert.equal(calls, 4);
});

test('failed directories retain cached children and do not trigger descendant requests', async () => {
  const child = dir('a/b');
  const root = dir('a', [child]);
  const calls = [];
  await refreshExpandedExplorerNodes([root], () => true, async (node) => {
    calls.push(node.path);
    return false;
  }, () => true);
  assert.deepEqual(calls, ['a']);
  assert.equal(root.children[0], child);
});

test('rename migrates cached descendants and expansion identities without touching siblings', () => {
  const child = file('a/b/c');
  const root = dir('a', [dir('a/b', [child])]);
  const sibling = dir('ab');
  remapExplorerNodes([root, sibling], 'a', 'renamed');
  const merged = mergeExplorerNodes([root, sibling], [dir('renamed'), dir('ab')]);
  assert.equal(merged[0], root);
  assert.equal(child.path, 'renamed/b/c');
  assert.equal(sibling.path, 'ab');
  assert.deepEqual(['a', 'a/b'].map((key) => remapExplorerPath(key, 'a', 'renamed')), ['renamed', 'renamed/b']);
  const { keys, directories } = explorerNodeKeys([...merged, { key: 'placeholder', placeholder: true }]);
  assert.ok(keys.has('renamed/b/c'));
  assert.ok(directories.has('renamed/b'));
  assert.ok(!directories.has('renamed/b/c'));
  assert.ok(!keys.has('placeholder'));
});
