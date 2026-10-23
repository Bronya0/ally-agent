// SPDX-License-Identifier: GPL-3.0-only
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';

const source = readFileSync(new URL('../App.vue', import.meta.url), 'utf8');
function section(start, end) {
  const from = source.indexOf(start);
  const to = source.indexOf(end, from);
  assert.ok(from >= 0 && to > from);
  return source.slice(from, to);
}
function evaluate(code, bindings) {
  return new Function(...Object.keys(bindings), code)(...Object.values(bindings));
}
const noop = () => {};

for (const name of ['Init', 'Remember', 'Lesson', 'Review', 'Push']) {
  test(`${name} command startup failure stays in its originating session after switching Tabs`, async () => {
    const origin = { id: 'origin', messages: [], runId: '', isRunning: false };
    const other = { id: 'other', messages: [] };
    const activeSession = { value: origin };
    const saved = [], scrolled = [];
    let pending;
    const code = section(`function handle${name}Command()`, '\n}\n') + '\n}';
    evaluate(`${section('function pushMessage(', 'function setConversationMessagesRef(')}
      ${code}; handle${name}Command();`, {
      activeSession, commandRunBlocked: () => false, isDefaultSessionTitle: () => false,
      scrollMessagesToBottom: (options, sid) => scrolled.push(sid), saveSessions: noop,
      isModelHistoryMessage: () => true, t: (key) => key,
      INIT_PROMPT: 'init', REMEMBER_PROMPT: 'remember', LESSON_PROMPT: 'lesson', REVIEW_PROMPT: 'review',
      markSessionRunning: (session) => { session.isRunning = true; },
      markTransientTurn: noop, commandRunConfig: () => ({}),
      persistCompletedSession: (session) => saved.push(session.id),
      StartChat: () => ({ catch: (handler) => {
        activeSession.value = other;
        pending = Promise.resolve().then(() => handler(new Error('bridge failed')));
      } }),
    });
    await pending;
    assert.equal(other.messages.length, 0);
    assert.equal(origin.messages.at(-1).error, true);
    assert.equal(origin.isRunning, false);
    assert.equal(scrolled.at(-1), 'origin');
    assert.deepEqual(saved, ['origin']);
  });
}

test('manual summary failure after a Tab switch is persisted to the original session', async () => {
  const origin = { id: 'origin', messages: [] }, other = { id: 'other', messages: [] };
  const activeSession = { value: origin };
  const saved = [];
  await evaluate(`${section('function pushMessage(', 'function setConversationMessagesRef(')}
    ${section('async function handleCompactCommand()', 'async function handleFastCompactCommand()')}
    return handleCompactCommand();`, {
    activeSession, compactStateFor: () => null,
    workspaceTabs: { value: [] }, activeWorkspaceId: { value: 'tab' },
    runWorkspaceForTab: () => '', startCompactTracking: noop, saveSessions: noop,
    chatConfig: { value: {} }, config: {}, compactingSessions: {}, t: (key) => key,
    scrollMessagesToBottom: noop, persistCompletedSession: (session) => saved.push(session.id),
    CompactSession: async () => { activeSession.value = other; throw new Error('summary failed'); },
  });
  assert.equal(other.messages.length, 0);
  assert.equal(origin.messages[0]?.error, true);
  assert.deepEqual(saved, ['origin']);
});

for (const reject of [false, true]) {
  test(`ordinary send keeps its model and session after a Tab switch (reject=${reject})`, async () => {
    const session = { id: 'origin', messages: [] };
    const other = { id: 'other', messages: [] };
    const activeSession = { value: session };
    const saved = [];
    const activeWorkspaceId = { value: 'a' };
    const chatConfig = { value: { model: 'model-a', baseURL: 'https://a.invalid' } };
    const config = { workspace: '/a' };
    let request;
    await evaluate(`${section('function pushMessage(', 'function setConversationMessagesRef(')}
      async function sendTail() {
      ${section('  try {\n    const history = buildSessionMessagesForModel(session', '// 运行中注入:')}
      return sendTail();`, {
      session, activeSession, userMessage: {}, sendText: 'question', attachments: [], sessionWorkspace: '/a',
      buildSessionMessagesForModel: () => [], attachmentsForModel: () => [], markSessionRunning: noop,
      activeWorkspaceId, modelByTab: { a: { model: 'model-a' }, b: { model: 'model-b' } },
      chatConfig, config, rememberLastUsedModel: async () => {
        activeWorkspaceId.value = 'b';
        chatConfig.value = { model: 'model-b', baseURL: 'https://b.invalid' };
        config.workspace = '/b';
        activeSession.value = other;
      },
      StartChat: async (value) => { request = value; if (reject) throw new Error('start failed'); },
      markTransientTurn: noop, scrollMessagesToBottom: noop, t: (key) => key,
      persistCompletedSession: (target) => saved.push(target.id),
    });
    assert.equal(request.sessionId, 'origin');
    assert.equal(request.config.model, 'model-a');
    assert.equal(request.config.baseURL, 'https://a.invalid');
    assert.equal(request.config.workspace, '/a');
    assert.equal(other.messages.length, 0);
    assert.equal(session.messages.length, reject ? 1 : 0);
    assert.deepEqual(saved, reject ? ['origin'] : []);
  });
}
