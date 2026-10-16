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

export function readNaiveThemeOverrides(mode, styles = getComputedStyle(document.documentElement)) {
  const light = mode === 'light';
  const token = (name) => styles.getPropertyValue(`--ally-${name}`).trim();
  const primary = token('accent-ui');
  const content = token('surface-content');
  const panel = token('surface-panel');
  const border = token('border');
  const hover = mixHex(primary, light ? '#000000' : '#ffffff', 0.12);
  const pressed = mixHex(primary, '#000000', light ? 0.24 : 0.12);

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
  };
}
