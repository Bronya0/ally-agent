/*
 * SPDX-License-Identifier: GPL-3.0-only
 *
 * Copyright (C) 2026 tangssst <tangssst@qq.com>
 * GitHub: https://github.com/Bronya0/ally-agent
 *
 * This file is part of ally-agent, licensed under the GNU General
 * Public License v3. See the LICENSE file for details.
 */
// 附件类型的兜底图标文字。类型本身由 App.vue 的 attachmentKind() 判定（image /
// video / audio / text / file），这里只负责「类型 → 文字」这一段。待发送列表
// （App.vue）与历史消息卡（MessageAttachments.vue）展示的是同一批附件，两处各写
// 一份判定必然漂移，所以收在这里。
const ATTACHMENT_ICONS = {
  image: 'IMG',
  video: 'VID',
  audio: 'AUD',
  text: 'TXT',
};

// 未识别的类型（file，以及将来新增而这里还没跟上的类型）统一落到 FILE。
const ATTACHMENT_ICON_FALLBACK = 'FILE';

export function attachmentIcon(att) {
  return ATTACHMENT_ICONS[att?.kind] || ATTACHMENT_ICON_FALLBACK;
}
