import test from 'node:test';
import assert from 'node:assert/strict';

import {
  buildHtmlRenderDocument,
  normalizeHtmlFrameHeight,
} from './htmlRender.mjs';

test('render document height measurement does not grow with iframe viewport height', () => {
  const document = buildHtmlRenderDocument('<div>content</div>', 'frame-token');

  assert.doesNotMatch(document, /html, body\s*\{[^}]*min-height:\s*100%/s);
  assert.match(document, /document\.body\.getBoundingClientRect\(\)\.height/);
  assert.equal(normalizeHtmlFrameHeight(201.2), 202);
  assert.equal(normalizeHtmlFrameHeight(600), 600);
  assert.equal(normalizeHtmlFrameHeight(601), 600);
});
