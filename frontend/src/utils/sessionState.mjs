export function findSessionWorkspaceTab(tabs, sessionId) {
  if (!sessionId || !Array.isArray(tabs)) return null;
  return tabs.find((tab) => tab?.sessionId === sessionId) || null;
}

export function shouldAcceptRunTerminal(sessionRunId, eventRunId) {
  const current = String(sessionRunId || '');
  const incoming = String(eventRunId || '');
  return current.length > 0 && incoming.length > 0 && current === incoming;
}

export function isEditableNavigationTarget(target) {
  let element = target;
  while (element) {
    const tagName = String(element.tagName || '').toLowerCase();
    if (tagName === 'input' || tagName === 'textarea' || tagName === 'select') return true;
    if (element.isContentEditable) return true;
    const contentEditable = typeof element.getAttribute === 'function'
      ? String(element.getAttribute('contenteditable') || '').toLowerCase()
      : '';
    if (contentEditable && contentEditable !== 'false') return true;
    element = element.parentElement || null;
  }
  return false;
}
