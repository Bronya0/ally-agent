import assert from 'node:assert/strict';
import test from 'node:test';
import { resolveMarkdownImagePath } from './markdownPreview.mjs';

test('resolves Markdown images relative to the current document', () => {
  assert.equal(resolveMarkdownImagePath('docs/guide/readme.md', './images/demo.png'), 'docs/guide/images/demo.png');
  assert.equal(resolveMarkdownImagePath('docs/guide/readme.md', '../assets/demo image.png'), 'docs/assets/demo image.png');
  assert.equal(resolveMarkdownImagePath('docs/guide/readme.md', '../assets/demo%20image.png?raw=1#x'), 'docs/assets/demo image.png');
});

test('supports workspace-root image references and blocks escapes', () => {
  assert.equal(resolveMarkdownImagePath('docs/readme.md', '/assets/logo.svg'), 'assets/logo.svg');
  assert.equal(resolveMarkdownImagePath('readme.md', '../outside.png'), '');
});

test('leaves non-workspace image URLs untouched', () => {
  assert.equal(resolveMarkdownImagePath('docs/readme.md', 'https://example.com/a.png'), '');
  assert.equal(resolveMarkdownImagePath('docs/readme.md', 'data:image/png;base64,abc'), '');
  assert.equal(resolveMarkdownImagePath('docs/readme.md', 'blob:https://example.com/id'), '');
  assert.equal(resolveMarkdownImagePath('docs/readme.md', '//cdn.example.com/a.png'), '');
  assert.equal(resolveMarkdownImagePath('docs/readme.md', String.raw`\\server\share\a.png`), '');
  assert.equal(resolveMarkdownImagePath('docs/readme.md', String.raw`C:\images\a.png`), '');
});
