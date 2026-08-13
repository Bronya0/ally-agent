/*
 * SPDX-License-Identifier: GPL-3.0-only
 *
 * Copyright (C) 2026 tangssst <tangssst@qq.com>
 * GitHub: https://github.com/Bronya0/ally-agent
 *
 * This file is part of ally-agent, licensed under the GNU General
 * Public License v3. See the LICENSE file for details.
 */
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
