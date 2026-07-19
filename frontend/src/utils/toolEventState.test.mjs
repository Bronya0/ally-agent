import test from 'node:test';
import assert from 'node:assert/strict';

import {
  commitToolEventMessage,
  findToolEventMessage,
} from './toolEventState.mjs';

test('streaming updates keep one tool card and update its arguments', () => {
  const session = { messages: [] };
  const eventId = 'run-1:tool:0:0';

  let existing = findToolEventMessage(session, eventId);
  commitToolEventMessage(session, eventId, existing, {
    role: 'tool_call',
    eventId,
    name: 'run_command',
    body: '',
  });

  existing = findToolEventMessage(session, eventId);
  commitToolEventMessage(session, eventId, existing, {
    role: 'tool_call',
    eventId,
    name: 'run_command',
    body: '{"command":"go test ./..."}',
  });

  assert.equal(session.messages.length, 1);
  assert.equal(session.messages[0].body, '{"command":"go test ./..."}');
  assert.equal(findToolEventMessage(session, eventId), session.messages[0]);
});
