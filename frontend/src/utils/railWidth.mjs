// Shared bounds for stored preferences, dragging and keyboard resizing.
export const RAIL_DEFAULT_WIDTH = 220;
export const RAIL_MIN_WIDTH = 160;
export const RAIL_MAX_WIDTH = 360;
export const RAIL_WIDTH_KEY = 'ally_mode_rail_width';

export function normalizeRailWidth(value) {
  if (typeof value !== 'number' && typeof value !== 'string') return RAIL_DEFAULT_WIDTH;
  if (typeof value === 'string' && !value.trim()) return RAIL_DEFAULT_WIDTH;
  const width = Number(value);
  if (!Number.isFinite(width)) return RAIL_DEFAULT_WIDTH;
  return Math.round(Math.min(RAIL_MAX_WIDTH, Math.max(RAIL_MIN_WIDTH, width)));
}
