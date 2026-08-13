/*
 * SPDX-License-Identifier: GPL-3.0-only
 *
 * Copyright (C) 2026 tangssst <tangssst@qq.com>
 * GitHub: https://github.com/Bronya0/ally-agent
 *
 * This file is part of ally-agent, licensed under the GNU General
 * Public License v3. See the LICENSE file for details.
 */
/**
 * Unwrap the payload delivered by Wails v3's @wailsio/runtime Events.On.
 *
 * Wails v3 callbacks receive a WailsEvent object ({ name, data, sender }),
 * while the application event handlers consume the backend payload itself.
 * Keep the fallback for plain values so this helper remains harmless in
 * isolated tests and non-Wails callers.
 */
export function unwrapWailsEvent(event, expectedName = '') {
  if (
    event !== null &&
    typeof event === 'object' &&
    Object.prototype.hasOwnProperty.call(event, 'name') &&
    Object.prototype.hasOwnProperty.call(event, 'data') &&
    (!expectedName || event.name === expectedName)
  ) {
    return event.data;
  }
  return event;
}
