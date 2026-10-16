import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import { THEMES } from './theme.mjs';
import { readNaiveThemeOverrides } from './naiveTheme.mjs';

const css = readFileSync(new URL('../style.css', import.meta.url), 'utf8');

// Read the actual palette declarations so regressions fail without maintaining
// a second set of expected colors. Browser rendering covers component cascade.
function declarations(selector) {
  const start = css.indexOf(`${selector} {`);
  assert.ok(start >= 0, `missing palette: ${selector}`);
  const body = css.slice(css.indexOf('{', start) + 1, css.indexOf('}', start));
  return Object.fromEntries([...body.matchAll(/(--[\w-]+):\s*([^;]+);/g)].map(([, key, value]) => [key, value.trim()]));
}

function palette(theme, mode) {
  const values = { ...declarations(':root') };
  if (mode === 'light') Object.assign(values, declarations(':root[data-mode="light"]'));
  if (theme !== 'amber') {
    Object.assign(values, declarations(`:root[data-theme="${theme}"]`));
    Object.assign(values, declarations(mode === 'light'
      ? `:root[data-theme="${theme}"][data-mode="light"]`
      : ':root[data-theme]:not([data-mode="light"])'));
  }
  const resolve = (value) => value.replace(/var\((--[\w-]+)\)/g, (_, name) => resolve(values[name]));
  return { getPropertyValue: (name) => resolve(values[name] || '') };
}

function rgb(hex) {
  assert.match(hex, /^#[\da-f]{6}$/i, `expected a literal hex color: ${hex}`);
  return [1, 3, 5].map((offset) => parseInt(hex.slice(offset, offset + 2), 16));
}

function luminance(color) {
  return color.map((channel) => {
    const s = channel / 255;
    return s <= 0.04045 ? s / 12.92 : ((s + 0.055) / 1.055) ** 2.4;
  }).reduce((sum, channel, index) => sum + channel * [0.2126, 0.7152, 0.0722][index], 0);
}

function readable(foreground, background, label) {
  const a = luminance(rgb(foreground));
  const b = luminance(rgb(background));
  const ratio = (Math.max(a, b) + 0.05) / (Math.min(a, b) + 0.05);
  assert.ok(ratio >= 4.5, `${label}: ${ratio.toFixed(2)}:1 is below 4.5:1`);
}

for (const theme of THEMES) {
  for (const mode of ['light', 'dark']) {
    test(`${mode} ${theme.id}: Naive uses the CSS palette and readable primary states`, () => {
      const styles = palette(theme.id, mode);
      const overrides = readNaiveThemeOverrides(mode, styles);
      const { common } = overrides;
      assert.equal(styles.getPropertyValue('--ally-accent'), theme.swatch);
      assert.equal(common.primaryColor, styles.getPropertyValue('--ally-accent-ui'));
      assert.equal(common.bodyColor, styles.getPropertyValue('--ally-surface-content'));
      for (const state of ['primaryColor', 'primaryColorHover', 'primaryColorPressed', 'primaryColorSuppl']) {
        readable(styles.getPropertyValue('--ally-accent-ink'), common[state], `${theme.id} ${mode} ${state}`);
      }
      for (const color of ['textColor1', 'textColor2', 'textColor3']) {
        readable(common[color], common.bodyColor, `${theme.id} ${mode} ${color}`);
      }
      if (mode === 'light') {
        for (const token of ['text-faint', 'text-ghost', 'accent-ui', 'success-text', 'danger-pale', 'warning-text', 'info-soft']) {
          readable(styles.getPropertyValue(`--ally-${token}`), common.bodyColor, `${theme.id} ${token}`);
        }
      }
    });
  }
}
