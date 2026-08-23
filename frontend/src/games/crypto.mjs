const encoder = new TextEncoder();
const decoder = new TextDecoder();

export function base64url(bytes) {
  let binary = '';
  for (const byte of bytes) binary += String.fromCharCode(byte);
  return btoa(binary).replaceAll('+', '-').replaceAll('/', '_').replaceAll('=', '');
}

export function fromBase64url(value) {
  const padded = String(value).replaceAll('-', '+').replaceAll('_', '/') + '==='.slice((String(value).length + 3) % 4);
  const binary = atob(padded);
  return Uint8Array.from(binary, (char) => char.charCodeAt(0));
}

export async function deriveKey(secret) {
  const digest = await crypto.subtle.digest('SHA-256', encoder.encode(`ally-game-wire-v1:${secret}`));
  return crypto.subtle.importKey('raw', digest, 'AES-GCM', false, ['encrypt', 'decrypt']);
}

export async function encryptJSON(key, value, aad = '') {
  const iv = crypto.getRandomValues(new Uint8Array(12));
  const data = await crypto.subtle.encrypt({ name: 'AES-GCM', iv, additionalData: encoder.encode(aad) }, key, encoder.encode(JSON.stringify(value)));
  return { iv: base64url(iv), data: base64url(new Uint8Array(data)) };
}

export async function decryptJSON(key, packet, aad = '') {
  if (!packet || typeof packet.iv !== 'string' || typeof packet.data !== 'string') throw new Error('invalid encrypted packet');
  const plain = await crypto.subtle.decrypt({ name: 'AES-GCM', iv: fromBase64url(packet.iv), additionalData: encoder.encode(aad) }, key, fromBase64url(packet.data));
  return JSON.parse(decoder.decode(plain));
}

export async function accessToken(secret) {
  const digest = await crypto.subtle.digest('SHA-256', encoder.encode(`ally-game-access-v1:${secret}`));
  return base64url(new Uint8Array(digest));
}
