import assert from 'node:assert/strict';
import test from 'node:test';
import { assignConfig, defaultConfig } from './config.mjs';

test('data format validation remains opt-in when loading settings', () => {
  for (const key of ['autoValidationYaml', 'autoValidationXml']) {
    assert.equal(defaultConfig()[key], false);
    for (const input of [undefined, null, false, 'true', 1, true]) {
      const draft = defaultConfig();
      assignConfig(draft, { [key]: input });
      assert.equal(draft[key], input === true, `${key}: ${input}`);
    }
  }
});
