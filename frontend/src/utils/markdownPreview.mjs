// Resolve a Markdown image URL against the Markdown file's workspace-relative
// path. External/data/blob URLs stay in the WebView; only local workspace
// references are returned for loading through the backend's safe path resolver.
export function resolveMarkdownImagePath(markdownPath, source) {
  let raw = String(source || '').trim();
  if (!raw || /^(?:[a-z][a-z\d+.-]*:|\/\/|\\\\)/i.test(raw)) return '';
  const suffix = raw.search(/[?#]/);
  if (suffix >= 0) raw = raw.slice(0, suffix);
  if (!raw) return '';
  try {
    raw = decodeURIComponent(raw);
  } catch {
    // Preserve malformed percent escapes and let the backend report not found.
  }

  const normalize = (parts, allowAboveRoot) => {
    const result = [];
    for (const part of parts) {
      if (!part || part === '.') continue;
      if (part === '..') {
        if (!result.length) {
          if (allowAboveRoot) return null;
          continue;
        }
        result.pop();
        continue;
      }
      result.push(part);
    }
    return result;
  };

  const normalizedMarkdown = normalize(String(markdownPath || '').replaceAll('\\', '/').split('/'), false) || [];
  const sourceParts = raw.replaceAll('\\', '/').split('/');
  const base = raw.startsWith('/') ? [] : normalizedMarkdown.slice(0, -1);
  const resolved = normalize([...base, ...sourceParts], true);
  return resolved?.join('/') || '';
}
