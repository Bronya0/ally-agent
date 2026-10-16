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
  contextDriftAnchor,
  contextDriftNoticeKey,
  insertContextDriftNotices,
  shouldRecordContextDriftNotice,
  sourceWindowMapper,
} from './contextDriftNotice.mjs';

// 插入的是提示对象本体（行内容由它自己携带），比较时只看行的身份：
// 普通行是字符串，提示行取它的 id。
const rowIds = (rows) => rows.map((row) => (typeof row === 'string' ? row : row.id));

test('drift notice key identifies the reason, not the time', () => {
  assert.equal(contextDriftNoticeKey({ kind: 'head' }), 'head');
  assert.equal(contextDriftNoticeKey({ kind: 'tail', index: -1 }), 'tail:-1');
  assert.equal(contextDriftNoticeKey({ kind: 'removed', index: 7 }), 'removed:7');
  assert.equal(contextDriftNoticeKey({}), 'head');
});

test('a repeated drift without new messages is recorded once', () => {
  const notices = [{ key: 'tail:-1', messageCount: 9 }];
  assert.equal(shouldRecordContextDriftNotice(notices, 'tail:-1', 9), false);
  assert.equal(shouldRecordContextDriftNotice(notices, 'head', 9), true);
  assert.equal(shouldRecordContextDriftNotice(notices, 'tail:-1', 11), true);
  assert.equal(shouldRecordContextDriftNotice([], 'head', 0), true);
});

test('提示停在它发生的来源位置，后来的行接在它后面', () => {
  const notices = [{ id: 'n1', anchor: 3 }];
  assert.deepEqual(rowIds(insertContextDriftNotices(['a', 'b', 'c'], notices, [0, 1, 2], 0)), ['a', 'b', 'c', 'n1']);
  assert.deepEqual(
    rowIds(insertContextDriftNotices(['a', 'b', 'c', 'd'], notices, [0, 1, 2, 3], 0)),
    ['a', 'b', 'c', 'n1', 'd'],
  );
});

// 回归：窗口长度触顶后每加一条就从头部掉一条，行号不再指向同一批内容。旧实现存行号，
// 于是新提示恒定落在窗口末尾、永远贴在底部。
test('窗口从头部滑动时提示仍贴住原位置，不会永远掉到末尾', () => {
  const notices = [{ id: 'n1', anchor: 3 }];
  // 窗口 4 行、起点滑到 2（前两条已归档），锚点 3 仍落在 d 之前。
  assert.deepEqual(
    rowIds(insertContextDriftNotices(['c', 'd', 'e', 'f'], notices, [2, 3, 4, 5], 2)),
    ['c', 'n1', 'd', 'e', 'f'],
  );
});

test('锚点滚出可见段后提示被丢弃，而不是贴到窗口的头或尾', () => {
  assert.deepEqual(
    rowIds(insertContextDriftNotices(['c', 'd'], [{ id: 'n1', anchor: 1 }], [2, 3], 2)),
    ['c', 'd'],
  );
});

test('同一锚点上的多条提示保持记录顺序', () => {
  const notices = [{ id: 'n1', anchor: 2 }, { id: 'n2', anchor: 2 }];
  assert.deepEqual(rowIds(insertContextDriftNotices(['a', 'b', 'c'], notices, [0, 1, 2], 0)), ['a', 'b', 'n1', 'n2', 'c']);
});

test('锚点为 0（记录时还没有任何消息）的提示插在最前面', () => {
  assert.deepEqual(rowIds(insertContextDriftNotices(['a'], [{ id: 'n1', anchor: 0 }], [0], 0)), ['n1', 'a']);
});

test('没有来源位置信息时提示落到末尾，不丢行', () => {
  assert.deepEqual(rowIds(insertContextDriftNotices(['a', 'b'], [{ id: 'n1', anchor: 1 }], [], 0)), ['a', 'b', 'n1']);
});

test('没有提示可插时原样返回同一个列表，不做无谓重建', () => {
  const rows = ['a', 'b'];
  assert.equal(insertContextDriftNotices(rows, [], [], 0), rows);
  assert.equal(insertContextDriftNotices(rows, null, [], 0), rows);
  // 全部锚点都已过期：同样原样返回。
  assert.equal(insertContextDriftNotices(rows, [{ id: 'n1', anchor: 0 }], [5, 6], 5), rows);
});

test('窗口下标换算把归档行算在起点之前那一格', () => {
  const plain = sourceWindowMapper(['a', 'b', 'c']);
  assert.equal(plain.start, 0);
  assert.equal(plain.indexOf(0), 0);
  assert.equal(plain.indexOf(2), 2);

  const archived = sourceWindowMapper([{ role: 'archive', count: 7 }, 'a', 'b']);
  assert.equal(archived.start, 7);
  assert.equal(archived.indexOf(0), 6);
  assert.equal(archived.indexOf(1), 7);
  assert.equal(archived.indexOf(2), 8);
});

test('锚点记的是可见来源消息的条数', () => {
  assert.equal(contextDriftAnchor(['a', 'b', 'c']), 3);
  assert.equal(contextDriftAnchor([{ role: 'archive', count: 7 }, 'a', 'b']), 9);
  assert.equal(contextDriftAnchor([]), 0);
  assert.equal(contextDriftAnchor(null), 0);
});
