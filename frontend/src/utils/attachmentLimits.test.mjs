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
import { readFileSync } from 'node:fs';
import test from 'node:test';

import { MAX_MODEL_IMAGE_BYTES, MAX_TEXT_ATTACHMENT_BYTES } from './attachmentLimits.mjs';

// 前后端的附件上限是"同一个数"，但跨语言没法共用常量，只能把两边钉在一起：
// 读后端源码取出常量值、和这边比。谁单独改了一边，这里就红——否则症状是前端照收
// 附件、后端悄悄截断或不发，用户只看到"我传了图，模型没看到"。
function goConst(source, name) {
  const match = new RegExp(`${name}\\s*=\\s*([0-9_\\s*]+)`).exec(source);
  assert.ok(match, `Go 侧找不到常量 ${name}（改名了？两边要一起改）`);
  return match[1]
    .split('*')
    .reduce((product, factor) => product * Number(factor.trim().replace(/_/g, '')), 1);
}

const read = (relative) => readFileSync(new URL(relative, import.meta.url), 'utf8');

test('附件上限与后端是同一组数', () => {
  assert.equal(
    MAX_MODEL_IMAGE_BYTES,
    goConst(read('../../../internal/app/biz_context.go'), 'maxAttachmentImageBytes'),
    '图片上限与后端 maxAttachmentImageBytes 不一致',
  );
  assert.equal(
    MAX_TEXT_ATTACHMENT_BYTES,
    goConst(read('../../../internal/app/app.go'), 'maxAttachmentText'),
    '文本上限与后端 maxAttachmentText 不一致',
  );
});
