/**
 * Format tool error text for UI display.
 * Backend safety errors intentionally append the blocked command for the model,
 * but the command card title already shows it, so strip that trailing echo.
 */
export function formatToolErrorBody(body) {
  const text = String(body || '').replace(/\r\n/g, '\n');
  if (!text) return '';

  const lines = text.split('\n');
  let cut = -1;
  for (let i = lines.length - 1; i >= 0; i -= 1) {
    if (/^\s*被拦截的命令[：:]/.test(lines[i])) {
      cut = i;
      break;
    }
  }
  if (cut >= 0) {
    lines.splice(cut);
  }

  return lines.join('\n').replace(/\s+$/u, '');
}
