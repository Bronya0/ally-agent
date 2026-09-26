/*
 * SPDX-License-Identifier: GPL-3.0-only
 *
 * Copyright (C) 2026 tangssst <tangssst@qq.com>
 * GitHub: https://github.com/Bronya0/ally-agent
 *
 * This file is part of ally-agent, licensed under the GNU General
 * Public License v3. See the LICENSE file for details.
 */
// Attachment size label. Images pasted or picked into the composer are
// re-encoded through canvas on their way to the model (2048px WebP), so the
// bytes actually sent are usually an order of magnitude below the file the user
// picked. Both attachment surfaces — the composer's pending list and the
// message cards — must read that the same way, hence this single helper.
import { formatBytes } from './format.mjs';

// compactBytes drops the ".0" and the space before the unit: "412.0 KB" ->
// "412KB", "20.0 MB" -> "20MB". Attachment labels and error sentences sit next to a
// file name or inside prose, so the default "48.8 KB" spelling is too long there.
// Exported so a caller can put a limit inside a translated string's placeholder.
export function compactBytes(bytes) {
  return formatBytes(bytes).replace(/\.0(?= )/, '').replace(' ', '');
}

// formatAttachmentSize renders "412KB(原6MB)": the bytes actually sent to the
// model, with the original file size in brackets when the two differ visibly.
// A single number is used when nothing was saved (tiny passthrough images, text
// attachments, model-generated images) — including when both sizes only differ
// below the rendered precision, since "18KB(原18KB)" would just look broken.
//
// The bracket label is passed in instead of imported: i18n.mjs pulls in
// naive-ui, which does not load under node --test, so this module stays free of
// i18n and both callers (which already hold `t`/`$t`) supply the translation.
export function formatAttachmentSize(att, originalLabel = '原') {
  const original = Number(att?.size || 0);
  const sent = Number(att?.sentSize || 0);
  const originalText = compactBytes(original);
  if (!(sent > 0) || sent >= original) return originalText;
  const sentText = compactBytes(sent);
  if (sentText === originalText) return originalText;
  return `${sentText}(${originalLabel}${originalText})`;
}
