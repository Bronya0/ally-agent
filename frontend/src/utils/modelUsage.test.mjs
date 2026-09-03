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
  getModelUsage,
  getSpecificModelUsage,
  recordModelUsage,
} from './modelUsage.mjs';

const mockStorage = new Map();

globalThis.localStorage = {
  getItem: (key) => mockStorage.get(key) || null,
  setItem: (key, val) => mockStorage.set(key, String(val)),
  removeItem: (key) => mockStorage.delete(key),
  clear: () => mockStorage.clear(),
};

beforeEach(() => {
  mockStorage.clear();
});

test('model usage records provider and specific model counts', () => {
  recordModelUsage('openai', ['openai'], 'openai\0gpt-4', ['openai\0gpt-4']);
  recordModelUsage('openai', ['openai'], 'openai\0gpt-4', ['openai\0gpt-4']);
  recordModelUsage('deepseek', ['openai', 'deepseek'], 'deepseek\0deepseek-chat', ['openai\0gpt-4', 'deepseek\0deepseek-chat']);

  assert.deepEqual(getModelUsage(), { openai: 2, deepseek: 1 });
  assert.deepEqual(getSpecificModelUsage(), {
    'openai\0gpt-4': 2,
    'deepseek\0deepseek-chat': 1,
  });
});

test('model usage prunes removed models when validModelIdentities is given', () => {
  recordModelUsage('openai', ['openai'], 'openai\0old-model', ['openai\0old-model']);
  assert.equal(getSpecificModelUsage()['openai\0old-model'], 1);

  // New model recorded, old model no longer in valid identities
  recordModelUsage('openai', ['openai'], 'openai\0new-model', ['openai\0new-model']);
  const specificUsage = getSpecificModelUsage();
  assert.equal(specificUsage['openai\0new-model'], 1);
  assert.equal(specificUsage['openai\0old-model'], undefined);
});
