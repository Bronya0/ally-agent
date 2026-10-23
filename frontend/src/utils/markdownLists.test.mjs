// SPDX-License-Identifier: GPL-3.0-only
import assert from 'node:assert/strict';
import test from 'node:test';
import MarkdownIt from 'markdown-it';
import { orderedListMarkerPlugin } from './markdownLists.mjs';

const md = new MarkdownIt({ html: false }).use(orderedListMarkerPlugin);
const lists = (text) => md.parse(text, {}).filter((token) => token.type === 'ordered_list_open');

test('single-digit suggestions retain ordinary list indentation', () => {
  const [list] = lists('1. 下一步建议\n2. 检查结果');
  assert.equal(list.attrGet('style'), '--md-list-marker-digits:1');
  assert.equal(list.attrGet('start'), null);
  assert.match(md.render('1. 下一步建议'), /<li>下一步建议<\/li>/);
});

test('marker width follows the actual largest ordinal, including a digit boundary', () => {
  for (const [text, digits, start] of [
    ['9. first\n10. second', 2, 9],
    ['99. first\n100. second', 3, 99],
    ['999999999. first\n100000000. second', 10, 999999999],
  ]) {
    const [list] = lists(text);
    assert.equal(list.attrGet('style'), `--md-list-marker-digits:${digits}`);
    assert.equal(list.attrGet('start'), start);
  }
});

test('nested lists count their own items and reset inherited marker widths', () => {
  const text = '99. parent\n    1. child\n    2. child\n        - bullet\n        - bullet\n100. parent';
  assert.deepEqual(lists(text).map((list) => list.attrGet('style')), [
    '--md-list-marker-digits:3', '--md-list-marker-digits:1',
  ]);
  assert.match(md.render(text), /<ul>/);
});

test('partial streaming lists grow their marker gutter without changing content', () => {
  assert.equal(lists('9. first')[0].attrGet('style'), '--md-list-marker-digits:1');
  assert.equal(lists('9. first\n10. second')[0].attrGet('style'), '--md-list-marker-digits:2');
  assert.deepEqual(lists('```\n123. code, not a list\n```'), []);
});
