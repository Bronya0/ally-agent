// SPDX-License-Identifier: GPL-3.0-only
import { isConfigObject, parseConfigJSON } from './configJSON.mjs';

function invalid(path, code = 'MCP_CONFIG_INVALID') {
  return Object.assign(new Error(path), { code });
}
const stringFields = ['command', 'cwd', 'transport', 'url'];
const arrayFields = ['args', 'disabledTools'];
const mapFields = ['env', 'headers'];

function stringArray(value) {
  return Array.isArray(value) && value.every((item) => typeof item === 'string');
}
function stringMap(value) {
  return isConfigObject(value) && Object.values(value).every((item) => typeof item === 'string');
}

export function validateMcpConfig(doc, { allowEmpty = false } = {}) {
  if (!isConfigObject(doc)) throw invalid('root');
  if (!Object.hasOwn(doc, 'mcpServers') || doc.mcpServers === null) {
    if (!allowEmpty) throw invalid('mcpServers', 'SERVERS_REQUIRED');
    doc = { ...doc, mcpServers: {} };
  }
  if (!isConfigObject(doc.mcpServers)) throw invalid('mcpServers', 'SERVERS_REQUIRED');
  for (const [name, cfg] of Object.entries(doc.mcpServers)) {
    const path = `mcpServers[${JSON.stringify(name)}]`;
    if (!name.trim() || !isConfigObject(cfg)) throw invalid(path);
    for (const key of stringFields) {
      if (Object.hasOwn(cfg, key) && typeof cfg[key] !== 'string') throw invalid(`${path}.${key}`);
    }
    for (const key of arrayFields) {
      if (Object.hasOwn(cfg, key) && !stringArray(cfg[key])) throw invalid(`${path}.${key}`);
    }
    for (const key of mapFields) {
      if (Object.hasOwn(cfg, key) && !stringMap(cfg[key])) throw invalid(`${path}.${key}`);
    }
    if (Object.hasOwn(cfg, 'enabled') && typeof cfg.enabled !== 'boolean') throw invalid(`${path}.enabled`);
    for (const key of ['startupTimeoutSec', 'toolTimeoutSec']) {
      if (Object.hasOwn(cfg, key) && (typeof cfg[key] !== 'number' || !Number.isFinite(cfg[key]))) throw invalid(`${path}.${key}`);
    }
  }
  return doc;
}

export function parseMcpConfig(text, options) {
  let doc;
  try { doc = parseConfigJSON(text); } catch { throw invalid('JSON', 'JSON_INVALID'); }
  return validateMcpConfig(doc, options);
}

export function normalizeMcpTransport(cfg = {}) {
  const value = String(cfg.transport || '').trim().toLowerCase();
  if (value === 'sse') return 'sse';
  if (['streamable-http', 'http', 'rest'].includes(value)) return 'streamable-http';
  if (!value && cfg.url && !cfg.command) return 'streamable-http';
  return 'stdio';
}

function projectServer(cfg) {
  return {
    command: cfg.command ?? '', args: JSON.stringify(cfg.args ?? []),
    env: JSON.stringify(cfg.env ?? {}, null, 2),
    transport: normalizeMcpTransport(cfg), url: cfg.url ?? '',
    headers: JSON.stringify(cfg.headers ?? {}, null, 2),
    enabled: cfg.enabled !== false, disabledTools: [...(cfg.disabledTools ?? [])],
  };
}

export function mcpConfigToForm(doc, nextKey) {
  validateMcpConfig(doc);
  return Object.entries(doc.mcpServers).map(([name, raw]) => {
    const cfg = JSON.parse(JSON.stringify(raw));
    return { _key: nextKey(), name, ...projectServer(cfg), _raw: cfg };
  }).sort((a, b) => Number(a.enabled === false) - Number(b.enabled === false));
}

// A JSON array represents argument boundaries exactly, including empty strings.
// Simple legacy whitespace-separated input remains accepted, but ambiguous
// quote syntax must be written as JSON instead of silently splitting it.
export function parseMcpArgs(text) {
  const value = String(text ?? '').trim();
  if (!value) return [];
  if (value.startsWith('[')) {
    let args;
    try { args = parseConfigJSON(value); } catch { throw invalid('args', 'MCP_ARGS_INVALID'); }
    if (!stringArray(args)) throw invalid('args', 'MCP_ARGS_INVALID');
    return args;
  }
  if (/["']/.test(value)) throw invalid('args', 'MCP_ARGS_INVALID');
  return value.split(/\s+/);
}

export function parseMcpStringMap(text, separator) {
  const value = String(text ?? '');
  if (!value.trim()) return {};
  if (value.trimStart().startsWith('{')) {
    let parsed;
    try { parsed = parseConfigJSON(value); } catch { throw invalid(separator === '=' ? 'env' : 'headers'); }
    if (!stringMap(parsed)) throw invalid(separator === '=' ? 'env' : 'headers');
    return parsed;
  }
  const entries = [];
  const seen = new Set();
  for (const line of value.split(/\r?\n/)) {
    if (!line.trim()) continue;
    const index = line.indexOf(separator);
    const key = line.slice(0, index).trim();
    if (index <= 0 || !key || seen.has(key)) throw invalid(separator === '=' ? 'env' : 'headers');
    seen.add(key);
    entries.push([key, line.slice(index + 1)]);
  }
  return Object.fromEntries(entries);
}

export function mcpFormToConfig(rows, original = { mcpServers: {} }) {
  const entries = [];
  const names = new Set();
  for (const row of rows) {
    const name = String(row.name ?? '');
    if (!name.trim() || names.has(name)) throw invalid('server name', 'MCP_NAME_INVALID');
    names.add(name);
    const cfg = JSON.parse(JSON.stringify(row._raw ?? {}));
    const initial = projectServer(cfg);
    const transportChanged = row.transport !== initial.transport;
    const activeFields = row.transport === 'stdio' ? ['command', 'args', 'env'] : ['url', 'headers'];
    const changed = (key) => !row._raw || (transportChanged && activeFields.includes(key)) || JSON.stringify(row[key]) !== JSON.stringify(initial[key]);
    const assign = (key, value, empty) => {
      if (!changed(key)) return;
      if (empty) delete cfg[key]; else Object.defineProperty(cfg, key, { value, enumerable: true, configurable: true, writable: true });
    };
    if (changed('transport')) {
      for (const key of row.transport === 'stdio' ? ['url', 'headers'] : ['command', 'args', 'env']) delete cfg[key];
      if (row.transport === 'stdio') delete cfg.transport; else cfg.transport = row.transport;
    }
    if (row.transport === 'stdio') {
      assign('command', row.command, !row.command);
      if (changed('args')) {
        const args = parseMcpArgs(row.args);
        assign('args', args, args.length === 0);
      }
      if (changed('env')) {
        const env = parseMcpStringMap(row.env, '=');
        assign('env', env, Object.keys(env).length === 0);
      }
    } else {
      assign('url', row.url, !row.url);
      if (changed('headers')) {
        const headers = parseMcpStringMap(row.headers, ':');
        assign('headers', headers, Object.keys(headers).length === 0);
      }
    }
    assign('enabled', row.enabled, false);
    assign('disabledTools', [...row.disabledTools], row.disabledTools.length === 0);
    entries.push([name, cfg]);
  }
  return validateMcpConfig({ ...original, mcpServers: Object.fromEntries(entries) });
}
