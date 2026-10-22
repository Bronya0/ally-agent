// SPDX-License-Identifier: GPL-3.0-only
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';

// Execute the actual footer controller with deferred desktop bridge replies.
const source = readFileSync(new URL('../App.vue', import.meta.url), 'utf8');
const start = source.indexOf('const contextTokens =');
const end = source.indexOf('function openGitDiff(', start);
assert.ok(start >= 0 && end > start);
const ref = (value) => ({ value });
function deferred() {
  let resolve, reject;
  const promise = new Promise((yes, no) => { resolve = yes; reject = no; });
  return { promise, resolve, reject };
}
function fixture({ startupReady = Promise.resolve() } = {}) {
  const git = deferred(), usage = deferred(), context = deferred();
  const contextCalls = [];
  const bindings = {
    ref, activeWorkspaceId: ref('tab'), activeSessionId: ref('session'),
    workspaceTabs: ref([{ id: 'tab', path: '/project' }]),
    activeRunWorkspace: ref('/project'), config: { workspace: '/project' },
    runWorkspaceForTab: (tab) => tab?.path || '',
    GetGitStatus: () => git.promise,
    GetWorkspaceTokenUsage: () => usage.promise,
    GetContextBreakdown: (...args) => { contextCalls.push(args); return context.promise; },
    startupContextReadyPromise: startupReady, startupDisposed: false,
    refreshContextPendingSid: null, refreshContextTokens: () => {},
  };
  const api = new Function(...Object.keys(bindings), `${source.slice(start, end)}
    return { refreshFooterStats, prepareFooterStatsForTarget, captureFooterSnapshot,
      applyFooterSnapshot, contextTokens, contextBreakdown, gitStatus,
      workspaceTokenUsage, footerStatsLoading, gitStatsLoading, usageStatsLoading };
  `)(...Object.values(bindings));
  return { ...api, ...bindings, git, usage, context, contextCalls };
}
const tick = () => new Promise((resolve) => setImmediate(resolve));

test('cold startup defers context computation while other statistics publish', async () => {
  const ready = deferred();
  const f = fixture({ startupReady: ready.promise });
  const pending = f.refreshFooterStats();
  f.git.resolve({ isRepo: true, branch: 'main' });
  f.usage.resolve({ inputTokens: 45 });
  await tick();
  assert.equal(f.gitStatus.value.branch, 'main');
  assert.equal(f.workspaceTokenUsage.value.inputTokens, 45);
  assert.equal(f.contextCalls.length, 0);
  assert.equal(f.footerStatsLoading.value, true);
  ready.resolve();
  await tick();
  assert.deepEqual(f.contextCalls, [['session', '/project']]);
  f.context.resolve({ total: 123 });
  await pending;
  assert.equal(f.contextTokens.value, 123);
  assert.equal(f.footerStatsLoading.value, false);
});

test('a startup context request is skipped if its Tab target changed before release', async () => {
  const ready = deferred();
  const f = fixture({ startupReady: ready.promise });
  const pending = f.refreshFooterStats();
  f.activeSessionId.value = 'next';
  f.prepareFooterStatsForTarget('tab', '/project');
  f.git.resolve({ isRepo: true, branch: 'stale' });
  f.usage.resolve({ inputTokens: 99 });
  ready.resolve();
  await pending;
  assert.equal(f.contextCalls.length, 0);
  assert.equal(f.contextTokens.value, 0);
});

test('context and usage display while Git is still pending', async () => {
  const f = fixture();
  const pending = f.refreshFooterStats();
  f.context.resolve({ total: 123 });
  f.usage.resolve({ inputTokens: 45, outputTokens: 6 });
  await tick();
  assert.equal(f.contextTokens.value, 123);
  assert.equal(f.footerStatsLoading.value, false);
  assert.equal(f.usageStatsLoading.value, false);
  assert.equal(f.gitStatsLoading.value, true);
  f.git.resolve({ isRepo: true, branch: 'main' });
  await pending;
  assert.equal(f.gitStatus.value.branch, 'main');
  assert.equal(f.gitStatsLoading.value, false);
});

test('a failed statistic does not discard successful replies', async () => {
  const f = fixture();
  const pending = f.refreshFooterStats();
  f.git.reject(new Error('git unavailable'));
  f.context.resolve({ total: 321 });
  f.usage.reject(new Error('usage unavailable'));
  await pending;
  assert.equal(f.contextTokens.value, 321);
  assert.equal(f.gitStatus.value.isRepo, false);
  assert.equal(f.footerStatsLoading.value, false);
  assert.equal(f.gitStatsLoading.value, false);
  assert.equal(f.usageStatsLoading.value, false);
});

test('late replies cannot update a different session in the same Tab', async () => {
  const f = fixture();
  const pending = f.refreshFooterStats();
  f.activeSessionId.value = 'next';
  f.prepareFooterStatsForTarget('tab', '/project');
  f.git.resolve({ isRepo: true, branch: 'stale' });
  f.usage.resolve({ inputTokens: 99 });
  f.context.resolve({ total: 999 });
  await pending;
  assert.equal(f.contextTokens.value, 0);
  assert.equal(f.gitStatus.value.isRepo, false);
  assert.equal(f.workspaceTokenUsage.value.inputTokens, 0);
});

test('footer snapshots require the same session and workspace', async () => {
  const f = fixture();
  const pending = f.refreshFooterStats();
  f.git.resolve({ isRepo: true, branch: 'main' });
  f.usage.resolve({ inputTokens: 1, outputTokens: 2 });
  f.context.resolve({ total: 123 });
  await pending;
  assert.equal(f.applyFooterSnapshot('tab', '/project'), true);
  assert.equal(f.applyFooterSnapshot('tab', '/other'), false);
  f.activeSessionId.value = 'next';
  assert.equal(f.applyFooterSnapshot('tab', '/project'), false);
});
