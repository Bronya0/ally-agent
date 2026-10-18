// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 tangssst <tangssst@qq.com>
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import { compile } from '@vue/compiler-dom';
import * as Vue from 'vue';
import { findToolEventMessage, commitToolEventMessage, normalizeToolStatus } from './toolEventState.mjs';
import { toolKindOf, toolStartsCollapsed } from './toolKind.mjs';
import { parsePartialToolArgs } from './toolArgsPartial.mjs';

const appSource = readFileSync(new URL('../App.vue', import.meta.url), 'utf8');
const eventsSource = readFileSync(new URL('../composables/useToolEvents.mjs', import.meta.url), 'utf8');
const cardSource = readFileSync(new URL('../components/ToolCallCard.vue', import.meta.url), 'utf8');
function section(source, start, end) {
  const from = source.indexOf(start);
  const to = source.indexOf(end, from);
  assert.ok(from >= 0 && to > from, `Missing source section: ${start}`);
  return source.slice(from, to);
}
function evaluate(code, bindings = {}) {
  return new Function(...Object.keys(bindings), code)(...Object.values(bindings));
}

// Exercise the actual event handlers and Vue template without a desktop runtime.
const applyEditDiff = evaluate(`${section(eventsSource, '  function applyEditDiff(', '\n  // ── 卡片网格')}; return applyEditDiff;`);
const template = section(cardSource, '<template>', '</template>').slice('<template>'.length);
const render = evaluate(compile(template, { mode: 'function' }).code, {
  Vue: { ...Vue, resolveComponent: (name) => name },
});
function renderNodes(msg) {
  const completedEditEntries = evaluate(`${section(cardSource, 'const completedEditEntries = computed(', 'const DiffView =')}; return completedEditEntries;`, {
    props: { msg }, computed: Vue.computed,
  });
  const root = render({
    msg, completedEditEntries: completedEditEntries.value,
    statusMark: msg.status, displayDuration: '', deleteRowsShown: false,
    transferFields: [], cardGrid: null, isBodyLive: msg.status === 'running',
    toolVerb: () => '', toolDisplayName: () => '', hasExpandableBody: () => false,
    isValidationWarning: () => false, errorReasonText: () => msg.body || '',
    $t: (key) => key, t: (key) => key, handleToggle: () => {},
  }, []);
  const nodes = [];
  function visit(node) {
    if (Array.isArray(node)) return node.forEach(visit);
    if (!node || typeof node !== 'object') return;
    nodes.push(node);
    visit(node.children);
  }
  visit(root);
  return nodes;
}
const diffNodes = (msg) => renderNodes(msg).filter((node) => node.type === 'DiffView');
const backendDiff = '@@ -40,1 +40,1 @@\n-old\n+new';

function fixture(name = 'edit') {
  const session = { id: 'test', messages: [] };
  const update = evaluate(`${section(appSource, 'function displayToolBodyForStatus(', 'function toggleToolExpand(')}; return updateToolEvent;`, {
    activeSession: { value: session }, activeSessionId: { value: '' },
    findToolEventById: findToolEventMessage, commitToolEventById: commitToolEventMessage,
    normalizeToolStatus, toolKindOf, toolStartsCollapsed,
    parseToolArgsForMeta: parsePartialToolArgs, toolActionFromArgs: () => '',
    formatToolBody: (_name, body) => body, formatDurationShort: () => '',
  });
  return {
    session,
    progress: (args) => update('edit-event', name, 'demo.go', JSON.stringify(args), 'running', {}, session),
    get msg() { return session.messages[0]; },
  };
}

for (const name of ['edit', 'remote_edit', 'replace_exact', 'replace_lines']) {
  test(`${name}: pending arguments show no diff; completion shows only the backend diff`, () => {
    const f = fixture(name);
    for (const changes of [
      [{ oldText: 'old', newText: 'new' }],
      [{ lineRange: { start: 40, end: 40 }, newText: 'new' }],
    ]) {
      f.progress({ path: 'demo.go', changes, oldText: 'old', newText: 'new' });
      assert.equal(f.msg.editFilePath, 'demo.go');
      assert.equal(f.msg.body, '');
      assert.equal(f.msg.editOldString, '');
      assert.equal(f.msg.editNewString, '');
      assert.deepEqual(diffNodes(f.msg), []);
    }
    f.msg.status = 'success';
    applyEditDiff(f.msg, { name }, {
      files: [{ path: 'demo.go', diff: backendDiff, addedLines: 1, removedLines: 1 }],
      addedLines: 1, removedLines: 1,
    });
    assert.deepEqual(diffNodes(f.msg).map((node) => node.props['diff-text']), [backendDiff]);
    assert.equal(f.msg.editStats, '+1 -1');
    f.progress({ path: 'demo.go', changes: [{ oldText: 'old', newText: 'late' }] });
    assert.equal(f.session.messages.length, 1);
    assert.equal(f.msg.status, 'success');
    assert.deepEqual(diffNodes(f.msg).map((node) => node.props['diff-text']), [backendDiff]);
  });
}

test('no-op results clear stale argument previews and counts', () => {
  const msg = {
    kind: 'edit', status: 'success', body: 'summary', editDiff: backendDiff,
    editOldString: 'old', editNewString: 'new', editAdded: 1, editRemoved: 1,
    editEntries: [{ path: 'demo.go', changes: [{ oldText: 'old', newText: 'new' }] }],
  };
  applyEditDiff(msg, {}, { files: [{ path: 'demo.go', diff: '' }] });
  assert.equal(msg.body, '');
  assert.equal(msg.editDiff, '');
  assert.equal(msg.editOldString, '');
  assert.equal(msg.editNewString, '');
  assert.equal(msg.editStats, '');
  assert.equal(msg.editAdded + msg.editRemoved, 0);
  assert.equal(msg.editEntries[0].changes, undefined);
  assert.deepEqual(diffNodes(msg), []);
});

test('running and failed edits never render stale previews or diffs', () => {
  const f = fixture();
  f.progress({ path: 'demo.go' });
  Object.assign(f.msg, {
    editDiff: backendDiff, editOldString: 'old', editNewString: 'new',
    editEntries: [{ path: 'demo.go', diff: backendDiff }],
  });
  assert.deepEqual(diffNodes(f.msg), []);
  Object.assign(f.msg, { status: 'error', body: 'version mismatch' });
  assert.deepEqual(diffNodes(f.msg), []);
  assert.ok(renderNodes(f.msg).some((node) => node.props?.class === 'tool-error-reason' && node.children === 'version mismatch'));
  f.progress({ path: 'demo.go' });
  assert.equal(f.msg.status, 'error');
  assert.equal(f.msg.body, 'version mismatch');
});

test('multi-file results skip unchanged files; legacy backend diff remains supported', () => {
  const msg = { kind: 'edit', status: 'success' };
  applyEditDiff(msg, {}, { files: [
    { path: 'a.go', diff: backendDiff }, { path: 'noop.go', diff: '' },
    { path: 'b.go', diff: backendDiff },
  ] });
  assert.deepEqual(diffNodes(msg).map((node) => node.props['file-path']), ['a.go', 'b.go']);
  applyEditDiff(msg, {}, { path: 'legacy.go', diff: backendDiff });
  assert.equal(diffNodes(msg).length, 1);
  assert.equal(diffNodes(msg)[0].props['diff-text'], backendDiff);
  assert.equal(diffNodes(msg)[0].props['file-path'], 'legacy.go');
});

test('expanded cards do not duplicate a backend diff with changed-lines text', () => {
  const msg = { kind: 'edit', status: 'success', expanded: true };
  applyEditDiff(msg, {}, { diff: backendDiff, changedLinesBlock: 'changed lines' });
  assert.equal(diffNodes(msg).length, 1);
  const changedBlocks = (card) => renderNodes(card).filter((node) => node.type === 'pre' && node.children === 'changed lines');
  assert.equal(changedBlocks(msg).length, 0);
  applyEditDiff(msg, {}, { changedLinesBlock: 'changed lines' });
  assert.equal(changedBlocks(msg).length, 1);
});
