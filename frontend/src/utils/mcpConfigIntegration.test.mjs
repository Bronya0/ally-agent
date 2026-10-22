// SPDX-License-Identifier: GPL-3.0-only
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import { ref, reactive } from 'vue';
import { parseMcpConfig, mcpConfigToForm, mcpFormToConfig } from './mcpConfigIO.mjs';

const source = readFileSync(new URL('../components/McpPanel.vue', import.meta.url), 'utf8');
const state = source.slice(source.indexOf('const mcpConfigText ='), source.indexOf('// 工具勾选面板的展开状态'));
const editor = source.slice(source.indexOf('function openMcpEditor('), source.indexOf('function openMcpImport('));
const io = source.slice(source.indexOf('async function importMcpConfig('), source.indexOf('function removeMcpServer('));
const initial = () => ({ extraRoot: { keep: true }, mcpServers: { demo: { command: 'node', args: ['a b', ''], env: { X: ' x\ny ' } } } });
const tick = () => new Promise((resolve) => setImmediate(resolve));

function fixture(save = async () => {}) {
  const saved = [], files = [], errors = [];
  const bindings = {
    ref, reactive, parseMcpConfig, mcpConfigToForm, mcpFormToConfig,
    t: (key) => key, emit: () => {}, loadBuiltinTools: async () => {},
    message: { success() {}, warning(error) { errors.push(error); }, error(error) { errors.push(error); } },
    GetMcpConfig: async () => '\ufeff' + JSON.stringify(initial()),
    GetMcpServers: async () => [], ReconcileMcpServers: async () => {},
    SaveMcpConfig: async (raw) => { saved.push(JSON.parse(raw)); await save(raw); },
    saveTextFile: async (file) => { files.push(file); return { saved: true }; },
  };
  const api = new Function(...Object.keys(bindings), `${state}\n${editor}\n${io}
    return { loadMcpConfig, autoApplyMcpConfig, importMcpConfig, exportMcpConfig,
      openMcpEditor, commitMcpEditor, mcpEditorDraft, mcpEditorVisible,
      getRows: () => mcpFormServers.value, getDoc: () => JSON.parse(mcpConfigText.value) };
  `)(...Object.values(bindings));
  return { ...api, saved, files, errors };
}

test('actual MCP panel loads BOM JSON, exports valid JSON and preserves all data', async () => {
  const f = fixture();
  await f.loadMcpConfig();
  assert.deepEqual(f.getDoc(), initial());
  assert.deepEqual(f.errors, []);
  await f.exportMcpConfig();
  assert.deepEqual(JSON.parse(f.files[0].content), initial());
  await f.importMcpConfig({ target: { files: [{ size: 100, text: async () => '\ufeff' + f.files[0].content }] } });
  assert.deepEqual(f.getDoc(), initial());
  assert.deepEqual(f.errors, []);
});

test('invalid MCP imports and editor drafts leave the previous config untouched', async () => {
  const f = fixture();
  await f.loadMcpConfig();
  for (const payload of [{ mcpServers: { s: null } }, { mcpServers: { s: { args: 'broken' } } }, null]) {
    await f.importMcpConfig({ target: { files: [{ size: 100, text: async () => JSON.stringify(payload) }] } });
    assert.deepEqual(f.getDoc(), initial());
  }
  f.openMcpEditor(0);
  f.mcpEditorDraft.args = '["broken", null]';
  f.commitMcpEditor();
  assert.equal(f.mcpEditorVisible.value, true);
  assert.deepEqual(f.getDoc(), initial());
  assert.equal(f.saved.length, 0);
  assert.equal(f.errors.length, 4);
});

test('queued MCP saves capture each change and preserve a rapid toggle back', async () => {
  let release;
  const gate = new Promise((resolve) => { release = resolve; });
  let count = 0;
  const f = fixture(async () => { if (++count === 1) await gate; });
  await f.loadMcpConfig();
  f.getRows()[0].enabled = false;
  const first = f.autoApplyMcpConfig();
  f.getRows()[0].enabled = true;
  const second = f.autoApplyMcpConfig();
  await tick();
  assert.equal(f.saved.length, 1);
  assert.equal(f.saved[0].mcpServers.demo.enabled, false);
  release();
  await Promise.all([first, second]);
  assert.equal(f.saved.length, 2);
  assert.deepEqual(f.saved[1], initial());
  assert.deepEqual(f.errors, []);
});
