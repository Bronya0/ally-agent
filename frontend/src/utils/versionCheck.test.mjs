import test from 'node:test';
import assert from 'node:assert/strict';
import { isNewerReleaseVersion } from './versionCheck.mjs';

test('detects a newer semantic release', () => {
  assert.equal(isNewerReleaseVersion('v0.2.0', 'v0.1.0'), true);
  assert.equal(isNewerReleaseVersion('1.0.0', 'v0.9.9'), true);
});

test('does not flag equal or older releases', () => {
  assert.equal(isNewerReleaseVersion('v0.1.0', 'v0.1.0'), false);
  assert.equal(isNewerReleaseVersion('v0.0.9', 'v0.1.0'), false);
});

test('ignores non-release development versions', () => {
  assert.equal(isNewerReleaseVersion('v0.2.0', 'dev'), false);
  assert.equal(isNewerReleaseVersion('v0.2.0', 'v20260712-154626'), false);
});

