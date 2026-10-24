// SPDX-License-Identifier: GPL-3.0-only
import { computed, ref } from 'vue';

const STORAGE_KEY = 'ally_avatar_style';
export const MAX_CUSTOM_AVATAR_BYTES = 1024 * 1024;
export const MAX_CUSTOM_AVATAR_EDGE = 1024;
const MAX_DATA_URL_CHARS = Math.ceil(MAX_CUSTOM_AVATAR_BYTES / 3) * 4 + 32;
const IMAGE_TYPES = new Set(['image/png', 'image/jpeg', 'image/webp', 'image/gif']);
export const CUSTOM_AVATAR_ACCEPT = [...IMAGE_TYPES].join(',');
export const AVATAR_STYLES = [
  { id: 'eye', labelKey: 'avatar.style.eye' },
  { id: 'whale', labelKey: 'avatar.style.whale' },
  { id: 'custom', labelKey: 'avatar.style.custom' },
];

export function normalizeAvatarStyle(value) {
  return AVATAR_STYLES.some((style) => style.id === value) ? value : 'eye';
}

function validImage(value) {
  return typeof value === 'string' && value.length <= MAX_DATA_URL_CHARS
    && /^data:image\/(?:png|jpeg|webp|gif);base64,[A-Za-z0-9+/]+={0,2}$/.test(value);
}

function storedPreference() {
  try {
    const raw = localStorage.getItem(STORAGE_KEY);
    // Migrate the original enum preference without introducing a second key.
    if (raw === 'eye' || raw === 'whale') return { style: raw, image: '' };
    if (!raw || raw.length > MAX_DATA_URL_CHARS + 100) return { style: 'eye', image: '' };
    const saved = JSON.parse(raw);
    const image = validImage(saved?.image) ? saved.image : '';
    const style = normalizeAvatarStyle(saved?.style);
    return { style: style === 'custom' && !image ? 'eye' : style, image };
  } catch {
    return { style: 'eye', image: '' };
  }
}

// One bounded image + one style in a single atomic storage entry. Replacing or
// removing an image releases the old value; never retain upload history.
const preference = ref(storedPreference());
const avatarStyle = computed(() => preference.value.style);
const customAvatar = computed(() => preference.value.image);
let uploadRevision = 0;

function commit(next, requireStorage = false) {
  try {
    localStorage.setItem(STORAGE_KEY, JSON.stringify(next));
  } catch {
    if (requireStorage) throw new Error('avatar.error.storage');
  }
  preference.value = next;
}

function setAvatarStyle(value) {
  const style = normalizeAvatarStyle(value);
  if (style === 'custom' && !customAvatar.value) return;
  uploadRevision++;
  commit({ ...preference.value, style });
}

async function setCustomAvatar(file) {
  if (!file || !IMAGE_TYPES.has(file.type)) throw new Error('avatar.error.type');
  if (!file.size || file.size > MAX_CUSTOM_AVATAR_BYTES) throw new Error('avatar.error.size');
  const revision = ++uploadRevision;
  const image = await new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve(reader.result);
    reader.onerror = reader.onabort = () => reject(new Error('avatar.error.read'));
    reader.readAsDataURL(file);
  });
  if (!validImage(image)) throw new Error('avatar.error.read');
  const probe = new Image();
  probe.src = image;
  let width = 0;
  let height = 0;
  try {
    await probe.decode();
    width = probe.naturalWidth;
    height = probe.naturalHeight;
  } catch {
    throw new Error('avatar.error.read');
  } finally {
    probe.src = '';
  }
  if (!width || !height || Math.max(width, height) > MAX_CUSTOM_AVATAR_EDGE) {
    throw new Error('avatar.error.dimensions');
  }
  // Persist first: quota failures must keep the previous avatar intact.
  if (revision !== uploadRevision) return;
  commit({ style: 'custom', image }, true);
}

function clearCustomAvatar() {
  commit({ style: avatarStyle.value === 'custom' ? 'eye' : avatarStyle.value, image: '' }, true);
  uploadRevision++;
}

export function useAvatarAppearance() {
  return { avatarStyle, customAvatar, setAvatarStyle, setCustomAvatar, clearCustomAvatar };
}
