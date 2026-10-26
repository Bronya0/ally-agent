// SPDX-License-Identifier: GPL-3.0-only
import test from 'node:test';
import assert from 'node:assert/strict';

let sequence = 0;
const gif = 'data:image/gif;base64,R0lGODlhAQABAIAAAAAAAP///ywAAAAAAQABAAACAUwAOw==';
const png = 'data:image/png;base64,aGVsbG8=';
const file = (data = gif, type = 'image/gif', size = 64) => ({ data, type, size });

async function fixture(t, saved = 'eye') {
  const descriptors = Object.fromEntries(['localStorage', 'FileReader', 'Image']
    .map((key) => [key, Object.getOwnPropertyDescriptor(globalThis, key)]));
  t.after(() => {
    for (const [key, descriptor] of Object.entries(descriptors)) {
      if (descriptor) Object.defineProperty(globalThis, key, descriptor);
      else delete globalThis[key];
    }
  });
  const state = { saved, failStorage: false, failRead: false, failDecode: false, edge: 100, decode: null };
  Object.defineProperty(globalThis, 'localStorage', { configurable: true, value: {
    getItem: () => state.saved,
    setItem: (key, value) => {
      assert.equal(key, 'ally_avatar_style');
      if (state.failStorage) throw new Error('quota');
      state.saved = value;
    },
  } });
  globalThis.FileReader = class {
    readAsDataURL(input) {
      queueMicrotask(() => {
        this.result = input.data;
        if (state.failRead) this.onerror();
        else this.onload();
      });
    }
  };
  globalThis.Image = class {
    get naturalWidth() { return state.edge; }
    get naturalHeight() { return state.edge; }
    async decode() {
      if (state.failDecode) throw new Error('decode');
      if (state.decode) await state.decode;
    }
  };
  const reload = () => import(`./avatarAppearance.mjs?test=${++sequence}`);
  const module = await reload();
  return { ...module, ...module.useAvatarAppearance(), state, reload };
}

test('legacy style migrates; custom cannot be selected without an image', async (t) => {
  const f = await fixture(t, 'whale');
  assert.equal(f.avatarStyle.value, 'whale');
  f.setAvatarStyle('custom');
  assert.equal(f.avatarStyle.value, 'whale');
  f.setAvatarStyle('eye');
  assert.deepEqual(JSON.parse(f.state.saved), { style: 'eye', image: '' });
  f.setAvatarStyle('unknown');
  assert.equal(f.avatarStyle.value, 'eye');
});

test('GIF bytes are preserved and shared; restore, replace and remove use one entry', async (t) => {
  const f = await fixture(t);
  await f.setCustomAvatar(file());
  assert.equal(f.avatarStyle.value, 'custom');
  assert.equal(f.useAvatarAppearance().customAvatar.value, gif);
  assert.deepEqual(JSON.parse(f.state.saved), { style: 'custom', image: gif });
  const restored = (await f.reload()).useAvatarAppearance();
  assert.equal(restored.customAvatar.value, gif);
  assert.equal(restored.avatarStyle.value, 'custom');
  await f.setCustomAvatar(file(png, 'image/png'));
  assert.equal(f.customAvatar.value, png);
  assert.ok(!f.state.saved.includes(gif));
  f.clearCustomAvatar();
  assert.equal(f.avatarStyle.value, 'eye');
  assert.equal(f.customAvatar.value, '');
  await f.setCustomAvatar(file());
  f.setAvatarStyle('whale');
  f.clearCustomAvatar();
  assert.equal(f.avatarStyle.value, 'whale');
  assert.equal(JSON.parse(f.state.saved).image, '');
});

test('invalid uploads and quota failures preserve the previous avatar', async (t) => {
  const f = await fixture(t);
  await f.setCustomAvatar(file());
  const previous = f.state.saved;
  await assert.rejects(f.setCustomAvatar(file(png, 'image/svg+xml')), /avatar.error.type/);
  await assert.rejects(f.setCustomAvatar(file(gif, 'image/gif', f.MAX_CUSTOM_AVATAR_BYTES + 1)), /avatar.error.size/);
  f.state.edge = f.MAX_CUSTOM_AVATAR_EDGE + 1;
  await assert.rejects(f.setCustomAvatar(file()), /avatar.error.dimensions/);
  f.state.edge = 100;
  f.state.failRead = true;
  await assert.rejects(f.setCustomAvatar(file()), /avatar.error.read/);
  f.state.failRead = false;
  f.state.failDecode = true;
  await assert.rejects(f.setCustomAvatar(file()), /avatar.error.read/);
  f.state.failDecode = false;
  f.state.failStorage = true;
  await assert.rejects(f.setCustomAvatar(file(png, 'image/png')), /avatar.error.storage/);
  assert.throws(() => f.clearCustomAvatar(), /avatar.error.storage/);
  assert.equal(f.avatarStyle.value, 'custom');
  assert.equal(f.customAvatar.value, gif);
  assert.equal(f.state.saved, previous);
});

test('late uploads cannot overwrite a newer style selection', async (t) => {
  const f = await fixture(t);
  let finish;
  f.state.decode = new Promise((resolve) => { finish = resolve; });
  const uploading = f.setCustomAvatar(file());
  await Promise.resolve();
  f.setAvatarStyle('whale');
  finish();
  await uploading;
  assert.equal(f.avatarStyle.value, 'whale');
  assert.equal(f.customAvatar.value, '');
});

test('corrupt or oversized stored images fall back safely', async (t) => {
  const f = await fixture(t, JSON.stringify({ style: 'custom', image: 'https://example.com/image.gif' }));
  assert.equal(f.avatarStyle.value, 'eye');
  assert.equal(f.customAvatar.value, '');
  f.state.saved = '{invalid';
  assert.equal((await f.reload()).useAvatarAppearance().avatarStyle.value, 'eye');
  f.state.saved = JSON.stringify({ style: 'custom', image: 'x'.repeat(2_000_000) });
  assert.equal((await f.reload()).useAvatarAppearance().customAvatar.value, '');
});

test('every bundled style is selectable and a bare-id preference still restores', async (t) => {
  const f = await fixture(t);
  for (const id of ['whale', 'gpt', 'claude', 'deepseek', 'gemini', 'glm', 'qwen']) {
    f.setAvatarStyle(id);
    assert.equal(f.avatarStyle.value, id);
    assert.deepEqual(JSON.parse(f.state.saved), { style: id, image: '' });
    // 旧版本可能把枚举直接存成裸字符串；新增形象后这条恢复路径不能退化成 eye。
    assert.equal((await f.reload()).useAvatarAppearance().avatarStyle.value, id);
  }
});
