// SPDX-License-Identifier: GPL-3.0-only
export function serviceDescription(service, translate) {
  const description = String(service?.description || '').trim();
  if (description || !service?.promoted) return description;
  const seconds = Number(service.timeoutSeconds) || 0;
  return seconds > 0
    ? translate('service.promotedDescription', { seconds })
    : translate('service.promotedBadge');
}

export function serviceCapacityCaption(data, translate) {
  if (typeof data?.activeCount !== 'number' || typeof data?.maxActive !== 'number') return '';
  return typeof data.globalActiveCount === 'number'
    ? translate('app.tools.services.capacity', { active: data.activeCount, global: data.globalActiveCount, max: data.maxActive })
    : translate('app.tools.services.active', { active: data.activeCount, max: data.maxActive });
}
