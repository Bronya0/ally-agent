import assert from 'node:assert/strict';
import test from 'node:test';
import {
  codePreviewWindow,
  displaySourceMessages,
  estimateMessageRenderChars,
} from './toolPreview.mjs';

test('collapsed create preview uses the latest generated lines', () => {
  const code = Array.from({ length: 12 }, (_, i) => `line ${i + 1}`).join('\n');

  const preview = codePreviewWindow(code, {
    collapsed: true,
    maxLines: 6,
    mode: 'tail',
  });

  assert.equal(preview.startLine, 7);
  assert.deepEqual(preview.lines, ['line 7', 'line 8', 'line 9', 'line 10', 'line 11', 'line 12']);
  assert.equal(preview.omittedBefore, true);
  assert.equal(preview.omittedAfter, false);
});

test('collapsed create card is estimated by preview size, not full content size', () => {
  const hugeCode = Array.from({ length: 50000 }, (_, i) => `line ${i + 1}`).join('\n');
  const createMsg = {
    role: 'tool_call',
    kind: 'create',
    expanded: false,
    codeContent: hugeCode,
  };
  const editMsg = {
    role: 'tool_call',
    kind: 'edit',
    expanded: false,
    codeContent: hugeCode,
  };

  assert.ok(estimateMessageRenderChars(createMsg) < 2000);
  assert.ok(estimateMessageRenderChars(editMsg) > 220000);
});

test('latest collapsed create card remains visible when message list is archived', () => {
  const hugeCode = Array.from({ length: 50000 }, (_, i) => `line ${i + 1}`).join('\n');
  const session = {
    id: 's1',
    messages: [
      { role: 'user', content: 'create a large file' },
      {
        role: 'tool_call',
        kind: 'create',
        status: 'success',
        eventId: 'run-1:tool:0',
        expanded: false,
        codeContent: hugeCode,
      },
    ],
  };

  const display = displaySourceMessages(session, new Set(), {
    maxMessages: 180,
    maxChars: 220000,
  });

  assert.equal(display.at(-1).eventId, 'run-1:tool:0');
});

test('trailing invisible run message does not hide the latest create card', () => {
  const hugeCode = Array.from({ length: 50000 }, (_, i) => `line ${i + 1}`).join('\n');
  const session = {
    id: 's1',
    messages: [
      { role: 'user', content: 'create a large file' },
      {
        role: 'tool_call',
        kind: 'create',
        status: 'success',
        eventId: 'run-1:tool:0',
        expanded: false,
        codeContent: hugeCode,
      },
      {
        role: 'tool_call',
        kind: 'run',
        status: 'success',
        eventId: 'run',
      },
    ],
  };

  const display = displaySourceMessages(session, new Set(), {
    maxMessages: 180,
    maxChars: 220000,
  });

  assert.equal(display.at(-1).eventId, 'run-1:tool:0');
  assert.equal(display.some((msg) => msg.kind === 'run'), false);
});

test('latest edit card remains visible even when its diff is large', () => {
  const hugeDiff = Array.from({ length: 50000 }, (_, i) => `+line ${i + 1}`).join('\n');
  const session = {
    id: 's1',
    messages: [
      { role: 'user', content: 'edit a large file' },
      {
        role: 'tool_call',
        kind: 'edit',
        status: 'success',
        eventId: 'run-1:tool:1',
        expanded: false,
        editDiff: hugeDiff,
      },
    ],
  };

  const display = displaySourceMessages(session, new Set(), {
    maxMessages: 180,
    maxChars: 220000,
  });

  assert.equal(display.at(-1).eventId, 'run-1:tool:1');
});

test('trailing invisible run message does not hide the latest edit card', () => {
  const hugeDiff = Array.from({ length: 50000 }, (_, i) => `+line ${i + 1}`).join('\n');
  const session = {
    id: 's1',
    messages: [
      { role: 'user', content: 'edit a large file' },
      {
        role: 'tool_call',
        kind: 'edit',
        status: 'success',
        eventId: 'run-1:tool:1',
        expanded: false,
        editDiff: hugeDiff,
      },
      {
        role: 'tool_call',
        kind: 'run',
        status: 'success',
        eventId: 'run',
      },
    ],
  };

  const display = displaySourceMessages(session, new Set(), {
    maxMessages: 180,
    maxChars: 220000,
  });

  assert.equal(display.at(-1).eventId, 'run-1:tool:1');
  assert.equal(display.some((msg) => msg.kind === 'run'), false);
});

test('expanded archives remain bounded instead of rendering the whole session', () => {
  const session = {
    id: 's1',
    messages: Array.from({ length: 1000 }, (_, i) => ({ role: 'assistant', content: `message ${i}` })),
  };

  const display = displaySourceMessages(session, new Set(['s1']), {
    maxMessages: 100,
    maxChars: 100000,
    expandedMaxMessages: 200,
    expandedMaxChars: 200000,
  });

  assert.equal(display[0].role, 'archive');
  assert.equal(display[0].expanded, true);
  assert.ok(display.length <= 201);
  assert.equal(display.at(-1).content, 'message 999');
});
