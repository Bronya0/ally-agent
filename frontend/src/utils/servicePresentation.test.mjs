// SPDX-License-Identifier: GPL-3.0-only
import assert from 'node:assert/strict';
import test from 'node:test';
import { serviceCapacityCaption, serviceDescription } from './servicePresentation.mjs';
import { serviceListRows, serviceRow } from './toolPreview.mjs';

const translate = (key, params) => ({
  'service.promotedDescription': () => `Moved to background after ${params.seconds}s`,
  'service.promotedBadge': () => 'Promoted on timeout',
  'app.tools.services.capacity': () => `${params.active} in workspace; ${params.global}/${params.max} globally`,
  'app.tools.services.active': () => `${params.active}/${params.max} active`,
}[key]());

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
