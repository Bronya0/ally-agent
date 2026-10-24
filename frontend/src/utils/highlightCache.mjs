// SPDX-License-Identifier: GPL-3.0-only

// Shared across mounted cards. Account for both source keys and rendered HTML;
// a count limit alone lets a few expanded files retain megabytes per card.
export function createTextCache({ maxEntries, maxChars, maxEntryChars }) {
  const entries = new Map();
  let chars = 0;
  function remove(key) {
    const entry = entries.get(key);
    if (!entry) return;
    chars -= entry.chars;
    entries.delete(key);
  }
  return {
    get size() { return entries.size; },
    get chars() { return chars; },
    get(key) {
      const entry = entries.get(key);
      if (!entry) return undefined;
      entries.delete(key);
      entries.set(key, entry);
      return entry.value;
    },
    set(key, value) {
      remove(key);
      const cost = key.length + (Array.isArray(value)
        ? value.reduce((total, line) => total + line.length + 1, 0) : value.length);
      if (cost > maxEntryChars || cost > maxChars) return;
      entries.set(key, { value, chars: cost });
      chars += cost;
      while (entries.size > maxEntries || chars > maxChars) {
        remove(entries.keys().next().value);
      }
    },
  };
}

export const commandHighlightCache = createTextCache({
  maxEntries: 128, maxChars: 256_000, maxEntryChars: 32_000,
});
export const codeHighlightCache = createTextCache({
  maxEntries: 32, maxChars: 1_000_000, maxEntryChars: 200_000,
});
