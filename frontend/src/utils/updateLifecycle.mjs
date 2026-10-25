// SPDX-License-Identifier: GPL-3.0-only
export function createUpdateRestartHandler({ flush, apply, quit, onApplying, onRestarting, timeoutMs = 15000 }) {
  let pending;
  let applied = false;
  const save = async () => {
    let timer;
    try {
      await Promise.race([
        Promise.resolve().then(flush),
        new Promise((_, reject) => { timer = setTimeout(() => reject(new Error('Timed out saving before update; the app remains open.')), timeoutMs); }),
      ]);
    } finally {
      clearTimeout(timer);
    }
  };
  return function update(version) {
    if (pending) return pending;
    pending = (async () => {
      onApplying?.();
      await save();
      if (!applied) {
        const result = await apply(version);
        if (!result?.ok) throw new Error(result?.error || 'Unable to prepare update');
        applied = true;
        // Stopping runs during prepare may enqueue more session writes.
        await save();
      }
      onRestarting?.();
      await quit();
    })().finally(() => { pending = undefined; });
    return pending;
  };
}
