// SPDX-License-Identifier: GPL-3.0-only

// Original artifacts outlive ordinary message-tail eviction. Rendering still
// uses the existing bounded display list; these records contain no base64.
export function hasDurableImage(message) {
  return Array.isArray(message?.attachments) && message.attachments.some(attachment => /^image:[a-f0-9]{64}\/[a-f0-9]{32}$/.test(attachment?.imageRef || ''));
}

export function imageToolTitle(args = {}) {
  return [args.aspect_ratio, args.prompt].filter(Boolean).join(' · ');
}

// Consume the artifact from the matched tool:result, never from the position
// of the last assistant message. Parallel results may arrive in any order.
export function generatedImageAttachment(result = {}) {
  if (!/^image:[a-f0-9]{64}\/[a-f0-9]{32}$/.test(result.imageRef || '')) return null;
  return {
    id: result.imageRef, imageRef: result.imageRef,
    url: `/generated-images/${result.imageRef.slice(6)}`,
    name: '', kind: 'image', type: result.mime, size: result.bytes,
    width: result.width, height: result.height, generated: true,
  };
}

export function applyGeneratedImageResult(card, result = {}) {
  const attachment = generatedImageAttachment(result);
  if (!attachment) return null;
  card.attachments = [attachment];
  if (!card.title) card.title = imageToolTitle(result);
  return attachment;
}
