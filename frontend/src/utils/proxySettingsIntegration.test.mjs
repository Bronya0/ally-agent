// SPDX-License-Identifier: GPL-3.0-only
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';

const source = readFileSync(new URL('../components/SettingsModal.vue', import.meta.url), 'utf8');
const start = source.indexOf('async function testProxy()');
const end = source.indexOf('// Network page:', start);
assert.ok(start >= 0 && end > start, 'proxy test handler must exist');

function createHandler(TestProxy) {
  const notifications = [];
  const proxyTesting = { value: false };
  const handler = new Function('TestProxy', 'message', 't', 'proxyTesting', 'draft',
    `${source.slice(start, end)}\nreturn testProxy;`)(
    TestProxy,
    {
      success: (message) => notifications.push({ type: 'success', ...message }),
      error: (message) => notifications.push({ type: 'error', ...message }),
    },
    (key, params) => ({ key, params }),
    proxyTesting,
    { proxyMode: 'system', proxyUrl: '', proxyNoProxy: 'intranet.test' },
  );
  return { handler, notifications, proxyTesting };
}

test('proxy connection failures returned as data show an error', async () => {
  const { handler, notifications, proxyTesting } = createHandler(async (request) => {
    assert.equal(request.noProxy, 'intranet.test');
    return { ok: false, error: 'connection refused', statusCode: 0 };
  });
  await handler();
  assert.deepEqual(notifications, [{
    type: 'error', key: 'settings.proxyTestFailed', params: { error: 'connection refused' },
  }]);
  assert.equal(proxyTesting.value, false);
});

test('proxy bridge errors clear the loading state and show an error', async () => {
  const { handler, notifications, proxyTesting } = createHandler(async () => { throw new Error('bridge failed'); });
  await handler();
  assert.equal(notifications.length, 1);
  assert.equal(notifications[0].type, 'error');
  assert.match(notifications[0].params.error, /bridge failed/);
  assert.equal(proxyTesting.value, false);
});

test('reachable HTTP endpoint shows connection success even when it requires authentication', async () => {
  const { handler, notifications, proxyTesting } = createHandler(async () => ({
    ok: true, statusCode: 401, durationMs: 23, proxy: 'http://127.0.0.1:7890',
  }));
  await handler();
  assert.deepEqual(notifications, [{
    type: 'success', key: 'settings.proxyTestSuccess',
    params: { status: 401, duration: 23, proxy: 'http://127.0.0.1:7890' },
  }]);
  assert.equal(proxyTesting.value, false);
});
