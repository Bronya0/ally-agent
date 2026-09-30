// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
import test from 'node:test';
import assert from 'node:assert/strict';

import {
  contextDriftInsertAt,
  contextDriftNoticeKey,
  insertContextDriftNotices,
  shouldRecordContextDriftNotice,
} from './contextDriftNotice.mjs';

// 插入的是提示对象本体（行内容由它自己携带），比较时只看行的身份：
// 普通行是字符串，提示行取它的 id。
const rowIds = (rows) => rows.map((row) => (typeof row === 'string' ? row : row.id));

test('drift notice key identifies the reason, not the time', () => {
  assert.equal(contextDriftNoticeKey({ kind: 'head' }), 'head');
  assert.equal(contextDriftNoticeKey({ kind: 'message', index: 2 }), 'message:2');
  assert.equal(contextDriftNoticeKey({ kind: 'removed', index: 7 }), 'removed:7');
  assert.equal(contextDriftNoticeKey({}), 'head');
});

test('a repeated drift without new messages is recorded once', () => {
  const notices = [{ key: 'message:2', messageCount: 9 }];
  assert.equal(shouldRecordContextDriftNotice(notices, 'message:2', 9), false);
  assert.equal(shouldRecordContextDriftNotice(notices, 'message:3', 9), true);
  assert.equal(shouldRecordContextDriftNotice(notices, 'message:2', 11), true);
  assert.equal(shouldRecordContextDriftNotice([], 'head', 0), true);
});

test('notices keep their place in the stream while later rows are appended', () => {
  const rows = ['a', 'b', 'c'];
  const notices = [{ id: 'n1', insertAt: 2 }];
  assert.deepEqual(rowIds(insertContextDriftNotices(rows, notices)), ['a', 'b', 'n1', 'c']);
  assert.deepEqual(rowIds(insertContextDriftNotices(['a', 'b', 'c', 'd'], notices)), ['a', 'b', 'n1', 'c', 'd']);
});

test('several notices keep their order, each one shifted by the ones before it', () => {
  const notices = [
    { id: 'n1', insertAt: 1 },
    { id: 'n2', insertAt: 1 },
    { id: 'n3', insertAt: 3 },
  ];
  // n2 与 n1 记在同一位置：先记的在前；n3 记在 3 行之后，两处插入把它顶到了新的末尾。
  assert.deepEqual(rowIds(insertContextDriftNotices(['a', 'b', 'c'], notices)), ['a', 'n1', 'n2', 'b', 'c', 'n3']);
});

test('an out-of-range or missing insertAt lands on the tail without dropping rows', () => {
  assert.deepEqual(rowIds(insertContextDriftNotices(['a', 'b'], [{ id: 'n1', insertAt: 99 }])), ['a', 'b', 'n1']);
  assert.deepEqual(rowIds(insertContextDriftNotices(['a', 'b'], [{ id: 'n1' }])), ['a', 'b', 'n1']);
  assert.deepEqual(rowIds(insertContextDriftNotices(['a', 'b'], [{ id: 'n1', insertAt: -5 }])), ['n1', 'a', 'b']);
});

test('no notices returns the very same list, so nothing is rebuilt needlessly', () => {
  const rows = ['a', 'b'];
  assert.equal(insertContextDriftNotices(rows, []), rows);
  assert.equal(insertContextDriftNotices(rows, null), rows);
});

// 插入基准是"非提示行"的条数：调用方拿到的是已合并的列表，所以要减掉已有提示。
// 这个换算就是上面那个插入位置成立的前提，写成函数才能被测到。
test('the insert position counts the rows that are not notices', () => {
  assert.equal(contextDriftInsertAt(['a', 'b', 'c'], 0), 3);
  assert.equal(contextDriftInsertAt(['a', 'n1', 'b', 'c'], 1), 3);
  assert.equal(contextDriftInsertAt(['a', 'n1', 'n2', 'b'], 2), 2);
  assert.equal(contextDriftInsertAt([], 0), 0);
  assert.equal(contextDriftInsertAt(null, 0), 0);
  // 坏输入不得变成负下标：那会把提示插到列表前面去。
  assert.equal(contextDriftInsertAt(['a', 'b'], undefined), 2);
  assert.equal(contextDriftInsertAt(['a'], 9), 0);
});
