// SPDX-License-Identifier: GPL-3.0-only
import assert from 'node:assert/strict';
import test from 'node:test';
import { reactive } from 'vue';
import { parseMcpConfig, mcpConfigToForm, mcpFormToConfig, parseMcpArgs, parseMcpStringMap } from './mcpConfigIO.mjs';

let key = 0;
const rowsFor = (doc) => mcpConfigToForm(doc, () => String(++key));
const sample = () => ({
  $schema: 'https://example.invalid/schema', vendorRoot: { nested: ['keep', null, 3] },
  mcpServers: {
    ' files ': { command: 'node', args: ['C:\\Program Files\\server.js', '--label', 'hello world', '', '"quoted"', '\\n', '中文'],
      env: { PADDED: ' leading trailing ', LINES: 'first\nsecond', EMPTY: '' },
      cwd: 'C:/Work Space', startupTimeoutSec: 12, toolTimeoutSec: 600, name: 'vendor-name', vendor: { keep: true } },
    network: { transport: 'http', url: 'https://example.invalid/mcp', headers: { Authorization: 'Bearer fake', Special: ' a:b ' }, enabled: false, disabledTools: ['tool-a'], vendor: [1, 2] },
  },
});

test('MCP form/export/import preserves all JSON fields and exact string values', () => {
  const doc = sample();
  const before = JSON.stringify(doc);
  const rows = reactive(rowsFor(doc));
  const exported = mcpFormToConfig(rows, reactive(doc));
  assert.deepEqual(exported, doc);
  const imported = parseMcpConfig('\ufeff' + JSON.stringify(exported));
  assert.deepEqual(mcpFormToConfig(rowsFor(imported), imported), doc);
  assert.equal(JSON.stringify(doc), before, 'converters must not mutate their input');
});

test('toggle and single-field edits preserve unrelated raw server and root fields', () => {
  const doc = sample();
  const rows = rowsFor(doc);
  const file = rows.find((row) => row.name === ' files ');
  file.enabled = false;
  let got = mcpFormToConfig(rows, doc);
  assert.deepEqual(got, { ...doc, mcpServers: { ...doc.mcpServers, ' files ': { ...doc.mcpServers[' files '], enabled: false } } });
  file.args = JSON.stringify(['new path/with spaces', '', '\\']);
  got = mcpFormToConfig(rows, doc);
  assert.deepEqual(got.mcpServers[' files '].args, ['new path/with spaces', '', '\\']);
  assert.deepEqual(got.mcpServers[' files '].env, doc.mcpServers[' files '].env);
  assert.deepEqual(got.vendorRoot, doc.vendorRoot);
});

test('edited JSON maps preserve whitespace, newlines, empty values and prototype-like keys', () => {
  const doc = { mcpServers: { server: { command: 'node' } } };
  const rows = rowsFor(doc);
  rows[0].env = '{"__proto__":"literal","MULTI":"a\\nb","PAD":" x ","EMPTY":""}';
  const got = mcpFormToConfig(rows, doc);
  assert.deepEqual(got.mcpServers.server.env, JSON.parse(rows[0].env));
  assert.equal(Object.getPrototypeOf(got.mcpServers.server.env), Object.prototype);
});

test('transport changes clear obsolete fields and retain the newly active inputs', () => {
  const doc = { mcpServers: { s: { command: 'node', args: ['keep'], env: { X: 'x' }, url: 'https://example.invalid', headers: { X: 'x' } } } };
  const rows = rowsFor(doc);
  rows[0].transport = 'sse';
  let got = mcpFormToConfig(rows, doc).mcpServers.s;
  assert.equal(got.url, doc.mcpServers.s.url);
  assert.deepEqual(got.headers, { X: 'x' });
  assert.equal(got.command, undefined);
  assert.equal(got.args, undefined);
  assert.equal(got.env, undefined);
  const next = rowsFor({ mcpServers: { s: got } });
  next[0].transport = 'stdio'; next[0].command = 'python'; next[0].args = '["a b"]';
  got = mcpFormToConfig(next, { mcpServers: { s: got } }).mcpServers.s;
  assert.equal(got.command, 'python');
  assert.deepEqual(got.args, ['a b']);
  assert.equal(got.url, undefined);
  assert.equal(got.headers, undefined);
});

test('MCP validation rejects bad roots, server entries and known field types', () => {
  const invalid = [null, [], { mcpServers: [] }, { mcpServers: null },
    ...[null, [], 'server', 3, { command: null }, { command: 3 }, { args: 'a b' }, { args: [null] },
      { args: [3] }, { env: [] }, { env: { X: null } }, { headers: { X: 3 } }, { enabled: 'false' },
      { disabledTools: [null] }, { startupTimeoutSec: '30' }].map((value) => ({ mcpServers: { s: value } }))];
  for (const doc of invalid) assert.throws(() => parseMcpConfig(JSON.stringify(doc)), JSON.stringify(doc));
  assert.throws(() => parseMcpConfig('{}'), (err) => err.code === 'SERVERS_REQUIRED');
  assert.deepEqual(parseMcpConfig('{"extra":1}', { allowEmpty: true }), { extra: 1, mcpServers: {} });
  assert.deepEqual(parseMcpConfig('{"mcpServers":null}', { allowEmpty: true }), { mcpServers: {} });
});

test('editor rejects duplicate names and ambiguous or invalid field input', () => {
  const doc = sample();
  const rows = rowsFor(doc);
  rows[1].name = rows[0].name;
  assert.throws(() => mcpFormToConfig(rows, doc), (err) => err.code === 'MCP_NAME_INVALID');
  for (const value of ['["x",null]', '["unterminated"', '--label "hello world"']) {
    assert.throws(() => parseMcpArgs(value), (err) => err.code === 'MCP_ARGS_INVALID');
  }
  assert.deepEqual(parseMcpArgs('-y @example/server --flag'), ['-y', '@example/server', '--flag']);
  assert.deepEqual(parseMcpStringMap('A= x \nEMPTY=', '='), { A: ' x ', EMPTY: '' });
  assert.throws(() => parseMcpStringMap('A=x\nnot-a-pair', '='));
  assert.throws(() => parseMcpStringMap('A=x\nA=y', '='));
  assert.throws(() => parseMcpStringMap('{"X":null}', ':'));
});

test('prototype-like MCP server names remain literal JSON keys', () => {
  const doc = parseMcpConfig('{"mcpServers":{"__proto__":{"command":"node"}}}');
  assert.deepEqual(mcpFormToConfig(rowsFor(doc), doc), doc);
});
