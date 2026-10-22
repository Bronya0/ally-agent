// SPDX-License-Identifier: GPL-3.0-only
// File.text() retains a UTF-8 BOM in some hosts; JSON.parse does not accept it.
export function parseConfigJSON(text) {
  return JSON.parse(String(text ?? '').replace(/^\uFEFF/, ''));
}

export function isConfigObject(value) {
  return value !== null && typeof value === 'object' && !Array.isArray(value);
}
