// 统一剪贴板复制入口：优先 navigator.clipboard，失败或不可用时回退到
// execCommand（Wails WebView 环境下 clipboard API 可能因焦点问题失败）。
// 返回 Promise<boolean>：true 表示复制成功，调用方自行决定成功/失败提示。

function fallbackCopy(text) {
  const ta = document.createElement('textarea');
  ta.value = text;
  ta.style.position = 'fixed';
  ta.style.opacity = '0';
  document.body.appendChild(ta);
  ta.select();
  try {
    return document.execCommand('copy');
  } finally {
    document.body.removeChild(ta);
  }
}

export async function copyText(text) {
  const value = String(text || '');
  if (!value) return false;
  try {
    if (navigator.clipboard?.writeText) {
      await navigator.clipboard.writeText(value);
      return true;
    }
  } catch (_) {
    // fall through to legacy path
  }
  try {
    return fallbackCopy(value);
  } catch (_) {
    return false;
  }
}
