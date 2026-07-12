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
