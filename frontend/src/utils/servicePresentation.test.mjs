// SPDX-License-Identifier: GPL-3.0-only
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import { serviceCapacityCaption, serviceDescription } from './servicePresentation.mjs';
import { serviceListRows, serviceRow } from './toolPreview.mjs';

const translate = (key, params) => ({
  'service.promotedDescription': () => `Moved to background after ${params.seconds}s`,
  'service.promotedBadge': () => 'Promoted on timeout',
  'app.tools.services.capacity': () => `${params.active} in workspace; ${params.global}/${params.max} globally`,
  'app.tools.services.active': () => `${params.active}/${params.max} active`,
}[key]());

test('service buffer captions match backend output and finished-record limits', () => {
  const read = (relative) => readFileSync(new URL(relative, import.meta.url), 'utf8');
  const source = read('../../../internal/tools/service/service.go');
  const outputKiB = Number(source.match(/OutputLimit\s*=\s*(\d+)\s*\*\s*1024/)[1]);
  const finished = Number(read('../../../internal/app/orch_services.go').match(/maxFinishedServices\s*=\s*(\d+)/)[1]);
  const translations = read('../i18n.mjs');
  for (const key of ['taskCenter.latestRetained', 'service.bufferPolicy']) {
    const captions = [...translations.matchAll(new RegExp(`'${key.replaceAll('.', '\\.')}': '([^']*)'`, 'g'))];
    assert.equal(captions.length, 2, `${key} must have Chinese and English captions`);
    for (const [, caption] of captions) {
      assert.ok(caption.includes(`${outputKiB} KiB`), `${key} must match the backend buffer limit`);
      if (key === 'service.bufferPolicy') assert.match(caption, new RegExp(`\\b${finished}\\b`));
    }
  }
});

test('promoted service cards localize structured metadata from start and list results', () => {
  const info = { id: 'svc', promoted: true, timeoutSeconds: 12 };
  for (const row of [serviceRow(info), serviceListRows({ services: [info] })[0]]) {
    assert.equal(serviceDescription(row, translate), 'Moved to background after 12s');
  }
  assert.equal(serviceDescription({ promoted: true }, translate), 'Promoted on timeout');
  assert.equal(serviceDescription({ description: ' Project preview ', promoted: true }, translate), 'Project preview');
  assert.equal(serviceDescription({}, translate), '');
});

test('capacity captions pair global occupancy with the global limit', () => {
  assert.equal(serviceCapacityCaption({ activeCount: 1, globalActiveCount: 7, maxActive: 8 }, translate), '1 in workspace; 7/8 globally');
  assert.equal(serviceCapacityCaption({ activeCount: 2, maxActive: 8 }, translate), '2/8 active');
  assert.equal(serviceCapacityCaption({}, translate), '');
});
