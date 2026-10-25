// SPDX-License-Identifier: GPL-3.0-only
import assert from 'node:assert/strict';
import test from 'node:test';
import { readFileSync } from 'node:fs';
import { applyGeneratedImageResult, hasDurableImage, imageToolTitle } from './generatedImages.mjs';
import { normalizeAssistantBodyVisibility } from './toolPreview.mjs';
import { findToolEventByData, isStageBlockedTool, setToolStatus, toolEventId } from './toolEventState.mjs';
import { toolCardRenderSignature } from './toolCardSignature.mjs';
import { isActionKeyedTool } from './toolVerb.mjs';
import * as Vue from 'vue';
import { compile } from '@vue/compiler-dom';

const source = readFileSync(new URL('../App.vue', import.meta.url), 'utf8');
const ref = `image:${'a'.repeat(64)}/${'b'.repeat(32)}`;
const generated = { role: 'assistant', content: '', attachments: [{ id: ref, imageRef: ref, url: `/generated-images/${ref.slice(6)}`, previewUrl: 'data:image/png;base64,preview', dataUrl: 'data:image/png;base64,original', kind: 'image' }] };

test('parallel image results stay on their own tool cards, including repeated results and thumbnails', async () => {
  const events = readFileSync(new URL('../composables/useToolEvents.mjs', import.meta.url), 'utf8');
  const useToolEvents = new Function('setToolStatus', 'findToolEventByData', 'toolEventId', 'isStageBlockedTool', 'applyGeneratedImageResult', 'isActionKeyedTool',
    events.slice(events.indexOf('export function useToolEvents')).replace('export function', 'function') + '; return useToolEvents;',
  )(setToolStatus, findToolEventByData, toolEventId, isStageBlockedTool, applyGeneratedImageResult, isActionKeyedTool);
  const first = { role: 'tool_call', name: 'image_edit', toolCallId: 'first', title: 'front view', status: 'running' };
  const second = { ...first, toolCallId: 'second', title: 'back view' };
  const session = Vue.reactive({ id: 'session', messages: [first, second] });
  const handlers = new Map();
  const noop = () => {};
  useToolEvents({
    onRuntimeEvent: (name, fn) => handlers.set(name, fn), sessionByEvent: () => session,
    flushStreamBuffer: noop, flushToolUpdateBuffer: noop, finalizeStreamingMessageForRun: noop,
    refreshContextTokens: noop, isHiddenTool: () => false, activeSessionId: { value: 'session' },
    scheduleSaveSessions: noop, scrollMessagesToBottomIfStale: noop,
    parseToolResultData: value => JSON.parse(value).data,
    formatToolBody: () => '832 × 1248', formatToolChip: () => '', formatDurationShort: () => '30s',
    makeToolResultTitle: () => '', createImageThumbnailDataUrl: async url => `thumbnail:${url}`,
    appendToolEventFallback: () => assert.fail('image result missed its existing tool card'),
  });
  const secondRef = `image:${'a'.repeat(64)}/${'c'.repeat(32)}`;
  const result = (id, imageRef) => handlers.get('tool:result')({ name: 'image_edit', toolCallId: id,
    result: JSON.stringify({ data: { imageRef, mime: 'image/png', width: 832, height: 1248, bytes: 1234 } }) });
  const before = toolCardRenderSignature(session.messages[0]);
  result('second', secondRef);
  result('first', ref);
  result('first', ref);
  await Promise.resolve();
  assert.equal(session.messages.length, 2, 'tool output must not create an assistant image row');
  for (const [index, imageRef] of [ref, secondRef].entries()) {
    const card = session.messages[index];
    assert.equal(card.attachments.length, 1);
    assert.equal(card.attachments[0].imageRef, imageRef);
    assert.equal(card.attachments[0].previewUrl, `thumbnail:/generated-images/${imageRef.slice(6)}`);
    assert.equal(card.status, 'success');
    assert.equal(hasDurableImage(card), true);
  }
  assert.equal(session.messages[0].title, 'front view');
  assert.notEqual(toolCardRenderSignature(session.messages[0]), before);
  const invalid = {};
  assert.equal(applyGeneratedImageResult(invalid, { imageRef: 'image:../../file' }), null);
  assert.equal(invalid.attachments, undefined);
});

test('image tool summaries include the prompt and aspect ratio', () => {
  assert.equal(imageToolTitle({ prompt: '画人物正面', aspect_ratio: '2:3' }), '2:3 · 画人物正面');
  const start = source.indexOf('function makeToolTitle(');
  const end = source.indexOf("  if (name === 'plan')", start);
  const title = new Function('isMcpToolName', 'parseToolArgsForMeta', 'imageToolTitle',
    `${source.slice(start, end)} return ''; }; return makeToolTitle;`)(() => false, JSON.parse, imageToolTitle);
  for (const tool of ['image_generate', 'image_edit']) {
    assert.equal(title(tool, '{"prompt":"画人物背面","aspect_ratio":"2:3"}'), '2:3 · 画人物背面');
  }
});

test('native image thumbnails update through reactive message and attachment objects', async () => {
  const start = source.indexOf("  onRuntimeEvent('run:image',");
  const end = source.indexOf('  const applyToolProgressEvent', start);
  const session = Vue.reactive({ id: 'session', messages: [] });
  const displayedPreview = Vue.computed(() => session.messages[0]?.attachments?.[0]?.previewUrl);
  assert.equal(displayedPreview.value, undefined);
  let receiveImage;
  let finishThumbnail;
  const noop = () => {};
  new Function('onRuntimeEvent', 'clearRetryBanner', 'flushStreamBuffer', 'sessionByEvent', 'endReasoningStatus', 't', 'dataUrlByteLength', 'createImageThumbnailDataUrl', 'scheduleSaveSessions', 'activeSessionId', 'scrollMessagesToBottom', source.slice(start, end))(
    (name, handler) => { receiveImage = handler; }, noop, noop, () => session, noop,
    key => key, () => 0, () => new Promise(resolve => { finishThumbnail = resolve; }), noop, { value: 'session' }, noop,
  );
  const url = `/generated-images/${ref.slice(6)}`;
  receiveImage({ id: 'native-image', runId: 'run', imageRef: ref, url, mimeType: 'image/png' });
  assert.equal(displayedPreview.value, url);
  finishThumbnail('data:image/webp;base64,thumbnail');
  await Promise.resolve();
  assert.equal(displayedPreview.value, 'data:image/webp;base64,thumbnail');
});

test('preview omits download; card export blocks duplicate dialogs and recovers after failure', async () => {
  const component = readFileSync(new URL('../components/MessageAttachments.vue', import.meta.url), 'utf8');
  const start = component.indexOf('const downloading =');
  const end = component.indexOf('defineProps(', start);
  const errors = [];
  let calls = 0;
  let finish;
  let nativeExport = (imageRef) => {
    assert.equal(imageRef, ref);
    calls++;
    return new Promise(resolve => { finish = resolve; });
  };
  const api = new Function('ref', 't', 'ExportGeneratedImage', 'message',
    `${component.slice(start, end)}; return { downloading, saveOriginal, renderPreviewToolbar };`)(
    value => ({ value }), key => key,
    (...args) => nativeExport(...args), { error: error => errors.push(error) },
  );
  const nodes = Object.fromEntries(['rotateCounterclockwise', 'rotateClockwise', 'resizeToOriginalSize', 'zoomOut', 'zoomIn', 'download', 'close'].map(key => [key, key]));
  const toolbar = api.renderPreviewToolbar({ nodes });
  assert.deepEqual(toolbar, ['rotateCounterclockwise', 'rotateClockwise', 'resizeToOriginalSize', 'zoomOut', 'zoomIn', 'close']);
  const pending = api.saveOriginal({ imageRef: ref });
  await api.saveOriginal({ imageRef: ref });
  assert.equal(calls, 1, 'repeated clicks must not stack native dialogs');
  assert.equal(api.downloading.value, true);
  finish('saved.png');
  await pending;
  assert.equal(api.downloading.value, false);
  assert.equal(errors.length, 0);
  nativeExport = async () => { throw new Error('save failed'); };
  await api.saveOriginal({ imageRef: ref });
  assert.equal(api.downloading.value, false);
  assert.equal(errors.length, 1);
});

test('image attachment template hides generated labels and keeps the card download control', () => {
  const component = readFileSync(new URL('../components/MessageAttachments.vue', import.meta.url), 'utf8');
  const template = component.slice(component.indexOf('<template>') + '<template>'.length, component.lastIndexOf('</template>'));
  const render = new Function('Vue', compile(template, { mode: 'function' }).code)({ ...Vue, resolveComponent: name => name });
  const attachment = { ...generated.attachments[0], name: '模型生成的图片', generated: true };
  let saved;
  const root = render({ attachments: [attachment], downloading: false, t: key => key,
    attachmentState: () => '123 KB', attachmentTitle: () => '', attachmentSource: () => '',
    renderPreviewToolbar: () => 'preview-toolbar', saveOriginal: value => { saved = value; },
  });
  const all = [];
  function visit(node) {
    if (!node || typeof node !== 'object') return;
    all.push(node);
    if (Array.isArray(node.children)) node.children.forEach(visit);
  }
  visit(root);
  const image = all.find(node => node.type === 'n-image');
  assert.equal(image.props['render-toolbar']({}), 'preview-toolbar');
  const button = all.find(node => node.type === 'n-button');
  button.props.onClick({ stopPropagation() {} });
  assert.equal(saved, attachment);
  assert.equal(all.some(node => typeof node.children === 'string' && node.children.includes('模型生成的图片')), false);
});

test('generated originals and source references survive persisted message sanitization', () => {
  const start = source.indexOf('function sanitizeStoredMessage(msg)');
  const end = source.indexOf('function sessionFromIndexEntry', start);
  const sanitize = new Function('t', 'MAX_STORED_MESSAGE_CHARS', 'MAX_STORED_TOOL_BODY_CHARS', 'MAX_STORED_ATTACHMENT_TEXT_CHARS', 'normalizeAssistantBodyVisibility', `${source.slice(start, end)}; return sanitizeStoredMessage;`)(key => key, 10000, 10000, 10000, normalizeAssistantBodyVisibility);
  const restored = JSON.parse(JSON.stringify(sanitize(generated)));
  assert.equal(restored.attachments[0].imageRef, ref);
  assert.equal(restored.attachments[0].url, generated.attachments[0].url);
  assert.equal(restored.attachments[0].dataUrl, '');
  assert.equal(hasDurableImage(restored), true);
  assert.equal(hasDurableImage({ attachments: [{ imageRef: 'image:../../config.json' }] }), false);
});

test('old image results survive the ordinary runtime and persisted message tail limits', () => {
  const messages = [generated, ...Array.from({ length: 5 }, (_, i) => ({ role: 'assistant', content: `text ${i}` }))];
  const start = source.indexOf('function trimRuntimeSessionMessages(session)');
  const end = source.indexOf('function sanitizeStoredMessage(msg)', start);
  const functions = new Function('sessionBusy', 'isRenderableMessage', 'isModelHistoryMessage', 'MAX_RUNTIME_RENDERABLE_MESSAGES', 'MAX_MODEL_HISTORY_MESSAGES', 'releaseMessageAttachments', 'hasDurableImage', 'sanitizeStoredMessage', `${source.slice(start, end)}; return { trimRuntimeSessionMessages, sanitizeStoredMessages };`)(() => false, () => true, () => true, 2, 2, () => {}, hasDurableImage, value => value);
  const session = { messages: [...messages] };
  functions.trimRuntimeSessionMessages(session);
  assert.equal(session.messages[0], generated);
  assert.equal(session.messages.length, 3);
  const stored = functions.sanitizeStoredMessages(messages);
  assert.equal(stored[0], generated);
  assert.equal(stored.length, 3);
});

test('thumbnails reuse saved originals without accepting arbitrary remote URLs', async () => {
  const start = source.indexOf('async function createImageThumbnailDataUrl(dataUrl)');
  const end = source.indexOf('function readFileAsDataUrl', start);
  const fetched = [];
  let closed = 0;
  const createBitmap = async () => ({ width: 1920, height: 1080, close: () => closed++ });
  const fetchImage = async (url) => {
    fetched.push(url);
    return { blob: async () => ({}) };
  };
  const canvas = { getContext: () => ({ drawImage() {} }), toDataURL: () => 'data:image/webp;base64,thumbnail' };
  const thumbnail = new Function('createImageBitmap', 'fetch', 'document', `${source.slice(start, end)}; return createImageThumbnailDataUrl;`)(createBitmap, fetchImage, { createElement: () => canvas });
  assert.equal(await thumbnail(generated.attachments[0].url), 'data:image/webp;base64,thumbnail');
  assert.equal(canvas.width, 960);
  assert.equal(canvas.height, 540);
  assert.equal(closed, 1);
  assert.equal(await thumbnail('https://untrusted.example/image.png'), '');
  assert.deepEqual(fetched, [generated.attachments[0].url]);
});
