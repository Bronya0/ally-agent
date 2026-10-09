/*
 * SPDX-License-Identifier: GPL-3.0-only
 *
 * Copyright (C) 2026 tangssst <tangssst@qq.com>
 * GitHub: https://github.com/Bronya0/ally-agent
 *
 * This file is part of ally-agent, licensed under the GNU General
 * Public License v3. See the LICENSE file for details.
 */
import assert from 'node:assert/strict';
import test from 'node:test';

import {
  RELEASE_NOTES_SEEN_KEY,
  isReleaseTag,
  markReleaseNotesSeen,
  readSeenVersion,
  releaseNotes,
  shouldShowReleaseNotes,
} from './releaseNotes.mjs';

// 假 storage：与 promptHistoryStore 的测试同一套路，绝不碰真实 localStorage。
function memoryStorage() {
  const data = new Map();
  return {
    getItem: (key) => (data.has(key) ? data.get(key) : null),
    setItem: (key, value) => data.set(key, String(value)),
    data,
  };
}

const NOTES = { version: 'v1.6.0', body: '## 修复\n\n- 修好了' };

test('正式版本号、有正文、又没看过时才弹', () => {
  assert.equal(shouldShowReleaseNotes({ notes: NOTES, seenVersion: '' }), true);
});

test('dev 构建（时间戳版本号）不弹，哪怕带上了正文', () => {
  assert.equal(shouldShowReleaseNotes({
    notes: { version: 'v20261009-173000', body: '不该展示' },
    seenVersion: '',
  }), false);
});

test('没拿到正文（手动触发、本地打包）不弹', () => {
  assert.equal(shouldShowReleaseNotes({ notes: { version: 'v1.6.0', body: '   ' }, seenVersion: '' }), false);
  assert.equal(shouldShowReleaseNotes({ notes: { version: '', body: '' }, seenVersion: '' }), false);
});

test('这一版已经看过就不再弹，换了版本仍会弹', () => {
  assert.equal(shouldShowReleaseNotes({ notes: NOTES, seenVersion: 'v1.6.0' }), false);
  assert.equal(shouldShowReleaseNotes({ notes: NOTES, seenVersion: 'v1.5.0' }), true);
});

test('正式版本号的判定：带不带 v 都算，预发布与时间戳都不算', () => {
  for (const value of ['v1.6.0', '1.6.0', 'v10.20.30']) {
    assert.equal(isReleaseTag(value), true, value);
  }
  for (const value of ['v1.6.0-beta.1', 'v1.6', 'v20261009-173000', 'dev', '', null, undefined]) {
    assert.equal(isReleaseTag(value), false, String(value));
  }
});

test('“已看过”的读写同用一个键，存的是版本号本身', () => {
  const storage = memoryStorage();
  assert.equal(readSeenVersion(storage), '');
  assert.equal(markReleaseNotesSeen('v1.6.0', storage), true);
  assert.equal(storage.data.get(RELEASE_NOTES_SEEN_KEY), 'v1.6.0');
  assert.equal(readSeenVersion(storage), 'v1.6.0');
});

test('空版本号不落标记（免得记成“看过空版本”）', () => {
  const storage = memoryStorage();
  assert.equal(markReleaseNotesSeen('   ', storage), false);
  assert.equal(storage.data.size, 0);
});

test('storage 不可用（配额满、隐私模式）时不抛错', () => {
  const throwing = {
    getItem() { throw new Error('denied'); },
    setItem() { throw new Error('quota exceeded'); },
  };
  assert.equal(readSeenVersion(throwing), '');
  assert.equal(markReleaseNotesSeen('v1.6.0', throwing), false);
});

test('没有注入值时读到空内容（node --test 里没有 vite 的 define）', () => {
  // 这条同时钉住模块里的 typeof 守卫：写成裸引用就会在这里 ReferenceError。
  assert.deepEqual(releaseNotes, { version: '', body: '' });
});
