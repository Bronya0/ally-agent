/*
 * SPDX-License-Identifier: GPL-3.0-only
 *
 * Copyright (C) 2026 tangssst <tangssst@qq.com>
 * GitHub: https://github.com/Bronya0/ally-agent
 *
 * This file is part of ally-agent, licensed under the GNU General
 * Public License v3. See the LICENSE file for details.
 */
// 附件能发多大：字节上限的唯一一份。两个数与 Go 后端是同一个数（后端按**压缩之后**
// 的字节判定发不发），跨语言没法共用常量，所以由 attachmentLimits.test.mjs 读后端
// 源码把两边钉在一起——谁改了数、另一边没跟上，测试就红。以前这些数直接写在 App.vue
// 里，只靠一句注释说"前后端同一个数"。
//
// 另外两个只有前端有（预览上限、直传阈值），后端没有对应物，改它们不用动 Go。
//
// 判定口径（沿用原来的注释）：比的是压缩之后的字节。超过 MODEL_IMAGE_PASSTHROUGH_MAX_BYTES
// 的图先重编码成 2048px 的 WebP，压完仍超过 MAX_MODEL_IMAGE_BYTES 就整张不发并说明
// 原因——原图多大与判定无关（一张 30MB 的截图压到 1MB 照发），也不算 base64 膨胀
// （传输必然膨胀，与图片本身无关）。

// 视频/音频附件的预览上限（只影响能否在卡片里预览，不影响发送）。
export const MAX_ATTACHMENT_PREVIEW_BYTES = 8 * 1024 * 1024;

// 单张图片能发给模型的字节上限。后端 internal/app/biz_context.go 的
// maxAttachmentImageBytes 同数。
export const MAX_MODEL_IMAGE_BYTES = 5 * 1024 * 1024;

// 文本附件的字节上限。后端 internal/app/app.go 的 maxAttachmentText 同数。
export const MAX_TEXT_ATTACHMENT_BYTES = 200 * 1024;

// 小于它就按原图直传（不再重编码）：小图重编码只会更小一点，却要付一次画布转码。
export const MODEL_IMAGE_PASSTHROUGH_MAX_BYTES = 256 * 1024;
