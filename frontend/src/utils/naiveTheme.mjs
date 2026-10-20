/*
 * SPDX-License-Identifier: GPL-3.0-only
 * Copyright (C) 2026 tangssst <tangssst@qq.com>
 */

// Naive performs JS color calculations, so give it literals from the current
// CSS palette rather than var()/color-mix() strings or a second palette table.
function mixHex(color, target, weight) {
  const channels = [1, 3, 5].map((offset) => {
    const from = parseInt(color.slice(offset, offset + 2), 16);
    const to = parseInt(target.slice(offset, offset + 2), 16);
    return Math.round(from + (to - from) * weight).toString(16).padStart(2, '0');
  });
  return `#${channels.join('')}`;
}

// Custom properties keep color-mix() as text even after var() substitution.
// Resolve the palette's opaque sRGB mixes before Naive's JS color arithmetic.
function paletteColor(value) {
  const match = value.match(/^color-mix\(in srgb,\s*(#[\da-f]{6})\s+([\d.]+)%,\s*(#[\da-f]{6})\s*\)$/i);
  return match ? mixHex(match[1], match[3], 1 - Number(match[2]) / 100) : value;
}

function alpha(hex, opacity) {
  return `rgba(${[1, 3, 5].map((offset) => parseInt(hex.slice(offset, offset + 2), 16)).join(', ')}, ${opacity})`;
}

export function readWorkbenchThemeOverrides(mode, styles = getComputedStyle(document.documentElement)) {
  const token = (name) => styles.getPropertyValue(`--ally-${name}`).trim();
  const inputBorder = token('border-input');
  return {
    headerTabs: {
      tabGapSmallBar: '0',
      tabPaddingSmallBar: '0',
      barColor: paletteColor(token(mode === 'light' ? 'accent-bright' : 'accent-dim')),
    },
    composerInput: {
      color: token('composer-glass'),
      colorFocus: token('composer-glass-focus'),
      borderRadius: '8px',
      borderHover: `1px solid ${inputBorder}`,
      borderFocus: `1px solid ${inputBorder}`,
      boxShadowFocus: `0 0 0 2px color-mix(in srgb, ${inputBorder} 25%, transparent)`,
    },
  };
}

export function readNaiveThemeOverrides(mode, styles = getComputedStyle(document.documentElement)) {
  const light = mode === 'light';
  const token = (name) => styles.getPropertyValue(`--ally-${name}`).trim();
  const primary = token('accent-ui');
  const content = token('surface-content');
  const panel = token('surface-panel');
  const border = token('border');
  const hover = mixHex(primary, light ? '#000000' : '#ffffff', 0.12);
  const pressed = mixHex(primary, '#000000', light ? 0.24 : 0.12);
  const color = (name) => paletteColor(token(name));
  const fill = color('accent-fill');
  const fillHover = color('accent-fill-hover');
  const fillPressed = color('accent-fill-pressed');
  const ink = color('accent-ink');
  const accent = color('accent');
  const strong = color('accent-strong');
  const bright = color('accent-bright');
  const muted = color('text-muted');
  const disabled = mixHex(accent, color('surface-raised'), 0.74);
  const disabledChecked = mixHex(accent, color('surface-raised'), 0.62);

  return {
    common: {
      bodyColor: content,
      baseColor: light ? panel : content,
      cardColor: light ? panel : content,
      modalColor: token('overlay-modal'),
      popoverColor: light ? panel : content,
      tableColor: light ? panel : content,
      primaryColor: primary,
      primaryColorHover: hover,
      primaryColorPressed: pressed,
      primaryColorSuppl: hover,
      successColor: token('success'),
      errorColor: token('danger'),
      warningColor: token('warning'),
      infoColor: token('info'),
      borderColor: border,
      dividerColor: border,
      textColorBase: token('text-primary'),
      textColor1: token('text-primary'),
      textColor2: token('text-body'),
      textColor3: token('text-muted'),
      placeholderColor: token('text-faint'),
      borderRadius: '10px',
      fontFamily: 'Inter',
    },
    Layout: {
      color: light ? 'transparent' : content,
      siderColor: token('surface-chrome'),
      headerColor: token('surface-chrome'),
    },
    Card: {
      color: light ? panel : content,
      colorEmbedded: light ? token('surface-chrome') : content,
    },
    Input: {
      color: light ? panel : content,
      colorFocus: light ? panel : content,
      textColor: token('text-primary'),
      placeholderColor: token('text-faint'),
      border: light ? `1px solid ${token('border-strong')}` : '1px solid rgba(255,255,255,0.12)',
      borderFocus: light ? `1px solid ${primary}` : '1px solid rgba(255,255,255,0.32)',
    },
    Button: {
      colorPrimary: fill,
      colorHoverPrimary: fillHover,
      colorPressedPrimary: fillPressed,
      colorFocusPrimary: fillHover,
      colorDisabledPrimary: disabled,
      textColorPrimary: ink,
      textColorHoverPrimary: ink,
      textColorPressedPrimary: ink,
      textColorFocusPrimary: ink,
      textColorDisabledPrimary: muted,
      textColorGhostPrimary: bright,
      textColorGhostHoverPrimary: strong,
      textColorGhostPressedPrimary: accent,
      textColorGhostFocusPrimary: strong,
      textColorGhostDisabledPrimary: muted,
      textColorTextPrimary: bright,
      textColorTextHoverPrimary: strong,
      textColorTextPressedPrimary: accent,
      textColorTextFocusPrimary: strong,
      textColorTextDisabledPrimary: muted,
      rippleColorPrimary: bright,
      borderPrimary: `1px solid ${fill}`,
      borderHoverPrimary: `1px solid ${fillHover}`,
      borderPressedPrimary: `1px solid ${fillPressed}`,
      borderFocusPrimary: `1px solid ${fillHover}`,
      borderDisabledPrimary: `1px solid ${disabled}`,
    },
    Checkbox: {
      colorChecked: fill,
      colorDisabledChecked: disabledChecked,
      borderChecked: `1px solid ${fill}`,
      borderFocus: `1px solid ${strong}`,
      borderDisabledChecked: `1px solid ${disabledChecked}`,
      boxShadowFocus: `0 0 0 2px ${alpha(accent, 0.28)}`,
      checkMarkColor: ink,
      checkMarkColorDisabledChecked: ink,
    },
    Radio: {
      dotColorActive: fill,
      boxShadowActive: `0 0 0 1px ${fill} inset`,
      boxShadowHover: `0 0 0 1px ${fill} inset`,
      boxShadowFocus: `0 0 0 1px ${strong} inset, 0 0 0 3px ${alpha(accent, 0.24)}`,
    },
    Switch: {
      railColorActive: fill,
      loadingColor: strong,
    },
  };
}
