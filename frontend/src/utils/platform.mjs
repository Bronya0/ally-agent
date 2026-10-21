// SPDX-License-Identifier: GPL-3.0-only

// The WebView UA identifies the host OS; model API User-Agent settings are
// independent of it. Resolve before mounting so window chrome never flashes.
export function detectPlatform(userAgent = globalThis.navigator?.userAgent || '') {
  if (/macintosh|mac os x/i.test(userAgent)) return 'darwin';
  if (/windows/i.test(userAgent)) return 'windows';
  if (/linux|crOS|android/i.test(userAgent)) return 'linux';
  return '';
}
