/*
 * SPDX-License-Identifier: GPL-3.0-only
 *
 * Copyright (C) 2026 tangssst <tangssst@qq.com>
 * GitHub: https://github.com/Bronya0/ally-agent
 *
 * This file is part of ally-agent, licensed under the GNU General
 * Public License v3. See the LICENSE file for details.
 */
import assert from 'node:assert/strict';
import test from 'node:test';

import {
  findSessionWorkspaceTab,
  isEditableNavigationTarget,
  shouldAcceptRunTerminal,
} from './sessionState.mjs';

test('findSessionWorkspaceTab returns the tab linked to the session', () => {
  const tabs = [
    { id: 'workspace-a', sessionId: 'session-a' },
    { id: 'workspace-b', sessionId: 'session-b' },
  ];
  assert.equal(findSessionWorkspaceTab(tabs, 'session-b'), tabs[1]);
  assert.equal(findSessionWorkspaceTab(tabs, 'missing'), null);
});

test('terminal run events only match the currently registered run', () => {
  assert.equal(shouldAcceptRunTerminal('run-new', 'run-new'), true);
  assert.equal(shouldAcceptRunTerminal('run-new', 'run-old'), false);
  assert.equal(shouldAcceptRunTerminal('', 'run-old'), false);
  assert.equal(shouldAcceptRunTerminal('run-new', ''), false);
});

test('editable navigation targets include nested contenteditable elements', () => {
  const textarea = { tagName: 'TEXTAREA', parentElement: null };
  const editor = {
    tagName: 'DIV',
    isContentEditable: true,
    getAttribute: () => 'true',
    parentElement: null,
  };
  const nested = { tagName: 'SPAN', parentElement: editor };
  const plain = { tagName: 'DIV', getAttribute: () => null, parentElement: null };

  assert.equal(isEditableNavigationTarget(textarea), true);
  assert.equal(isEditableNavigationTarget(nested), true);
  assert.equal(isEditableNavigationTarget(plain), false);
});
