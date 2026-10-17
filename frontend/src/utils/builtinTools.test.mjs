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
import test, { beforeEach } from 'node:test';
import {
  defaultToggleableToolNames,
  formatBuiltinToolLabel,
  getToggleableToolNames,
  isProtectedTool,
  isToggleableTool,
  orderBuiltinToolSettings,
  protectedToolNames,
  registerDiscoveredTools,
  resetDiscoveredTools,
  toggleableToolNames,
} from './builtinTools.mjs';

beforeEach(() => {
  resetDiscoveredTools();
});

test('isProtectedTool checks core 5 tools case-insensitively', () => {
  for (const name of ['read', 'edit', 'create', 'delete', 'command']) {
    assert.equal(isProtectedTool(name), true, `${name} must be protected`);
    assert.equal(isProtectedTool(` ${name.toUpperCase()} `), true, `${name} trimmed upper must be protected`);
  }
  assert.equal(isProtectedTool('remote_transfer'), false);
  assert.equal(isProtectedTool('list_files'), false);
  assert.equal(isProtectedTool(''), false);
  assert.equal(isProtectedTool(null), false);
});

test('isToggleableTool includes default tools and excludes protected tools', () => {
  assert.equal(isToggleableTool('remote_transfer'), true, 'remote_transfer should be toggleable by default');
  assert.equal(isToggleableTool('list_files'), true);
  assert.equal(isToggleableTool('read'), false, 'read must not be toggleable');
  assert.equal(isToggleableTool('edit'), false, 'edit must not be toggleable');
  assert.equal(isToggleableTool('unknown_custom_tool'), false);
});

test('registerDiscoveredTools dynamically discovers new tools', () => {
  registerDiscoveredTools(['my_new_tool', 'another_tool']);
  assert.equal(isToggleableTool('my_new_tool'), true);
  assert.equal(isToggleableTool('another_tool'), true);
  assert.ok(getToggleableToolNames().includes('my_new_tool'));
  assert.ok(toggleableToolNames.includes('my_new_tool'));

  // Should ignore protected tools
  registerDiscoveredTools(['read', 'edit']);
  assert.equal(isToggleableTool('read'), false);

  // Supports objects with .name
  registerDiscoveredTools([{ name: 'object_scanned_tool', source: 'built-in' }]);
  assert.equal(isToggleableTool('object_scanned_tool'), true);
});

test('orderBuiltinToolSettings puts enabled tools first and keeps order inside each group', () => {
  const items = [
    { name: 'read', enabled: true },
    { name: 'screenshot', enabled: false },
    { name: 'list_files', enabled: true },
    { name: 'remote_read' },
    { name: 'grep', enabled: true },
  ];
  assert.deepEqual(
    orderBuiltinToolSettings(items).map((item) => item.name),
    ['read', 'list_files', 'grep', 'screenshot', 'remote_read'],
    'enabled first, declaration order kept inside each group, missing flag counts as disabled',
  );
  assert.deepEqual(orderBuiltinToolSettings([]), []);
  assert.deepEqual(orderBuiltinToolSettings(null), []);
});

test('formatBuiltinToolLabel formats with and without translate function', () => {
  const mockTranslate = (key) => {
    if (key === 'settings.tool.remote_transfer') return '远程传输';
    return key;
  };

  assert.equal(formatBuiltinToolLabel('remote_transfer', mockTranslate), '远程传输');
  assert.equal(formatBuiltinToolLabel('unknown_action_tool', mockTranslate), 'Unknown Action Tool');
  assert.equal(formatBuiltinToolLabel('single', null), 'Single');
  assert.equal(formatBuiltinToolLabel(''), '');
  assert.equal(formatBuiltinToolLabel(null), '');
});
