// SPDX-License-Identifier: GPL-3.0-only
import { readonly, ref } from 'vue';

const STORAGE_KEY = 'ally_avatar_style';
export const AVATAR_STYLES = [
  { id: 'eye', labelKey: 'avatar.style.eye' },
  { id: 'whale', labelKey: 'avatar.style.whale' },
];

export function normalizeAvatarStyle(value) {
  return AVATAR_STYLES.some((style) => style.id === value) ? value : 'eye';
}

function storedStyle() {
  try {
    return normalizeAvatarStyle(localStorage.getItem(STORAGE_KEY));
  } catch {
    return 'eye';
  }
}

// A single enum preference, shared by all workspace Tabs and restored before
// their first render. Keep it out of the model/backend configuration.
const style = ref(storedStyle());
const avatarStyle = readonly(style);

function setAvatarStyle(value) {
  style.value = normalizeAvatarStyle(value);
  try {
    localStorage.setItem(STORAGE_KEY, style.value);
  } catch {
    // The current session still switches when storage is unavailable.
  }
}

export function useAvatarAppearance() {
  return { avatarStyle, setAvatarStyle };
}
