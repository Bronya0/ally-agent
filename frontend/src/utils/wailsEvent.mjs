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
