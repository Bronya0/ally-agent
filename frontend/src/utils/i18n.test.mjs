/*
 * SPDX-License-Identifier: GPL-3.0-only
 *
 * Copyright (C) 2026 tangssst <tangssst@qq.com>
 * GitHub: https://github.com/Bronya0/ally-agent
 *
 * This file is part of ally-agent, licensed under the GNU General
 * Public License v3. See the LICENSE file for details.
 */
import test from 'node:test';
import assert from 'node:assert/strict';
import { readdirSync, readFileSync } from 'node:fs';
import { dirname, extname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import {
  detectLocale,
  hasTranslation,
  LOCALE_EN_US,
  LOCALE_ZH_CN,
} from '../i18n.mjs';

test('detectLocale uses the primary browser language', () => {
  assert.equal(detectLocale(['zh-CN', 'en-US']), LOCALE_ZH_CN);
  assert.equal(detectLocale(['zh-TW']), LOCALE_ZH_CN);
  assert.equal(detectLocale(['en-US', 'zh-CN']), LOCALE_EN_US);
  assert.equal(detectLocale(['ja-JP']), LOCALE_EN_US);
  assert.equal(detectLocale([]), LOCALE_EN_US);
});

test('both locale tables contain representative UI entries', () => {
  assert.equal(hasTranslation('settings.title'), true);
  assert.equal(hasTranslation('app.composer.placeholder'), true);
  assert.equal(hasTranslation('tools.status.using'), true);
  assert.equal(hasTranslation('app.push.prompt'), true);
});

test('/push uses a localized prompt that preserves the conversation language', () => {
  const sourceRoot = join(dirname(fileURLToPath(import.meta.url)), '..');
  const appSource = readFileSync(join(sourceRoot, 'App.vue'), 'utf8');
  const i18nSource = readFileSync(join(sourceRoot, 'i18n.mjs'), 'utf8');

  assert.match(appSource, /history\.push\(\{ role: 'user', content: t\('app\.push\.prompt'\) \}\)/);
  assert.doesNotMatch(appSource, /const PUSH_PROMPT =/);
  assert.equal((i18nSource.match(/'app\.push\.prompt':/g) || []).length, 2);
  assert.match(i18nSource, /保持当前会话已经使用的回复语言/);
  assert.match(i18nSource, /Preserve the response language already used/);
});

test('every translation key used by the UI exists in both locale tables', () => {
  const sourceRoot = join(dirname(fileURLToPath(import.meta.url)), '..');
  const files = [];
  const visit = (dir) => {
    for (const entry of readdirSync(dir, { withFileTypes: true })) {
      const path = join(dir, entry.name);
      if (entry.isDirectory()) visit(path);
      else if (['.vue', '.js', '.mjs'].includes(extname(entry.name)) && entry.name !== 'i18n.mjs') files.push(path);
    }
  };
  visit(sourceRoot);

  const missing = new Set();
  const keyPattern = /(?:\$t|\bt)\(\s*['"]([^'"]+)['"]/g;
  for (const file of files) {
    const source = readFileSync(file, 'utf8');
    for (const match of source.matchAll(keyPattern)) {
      if (!hasTranslation(match[1])) missing.add(match[1]);
    }
  }
  assert.deepEqual([...missing].sort(), []);
});
