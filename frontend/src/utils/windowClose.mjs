// SPDX-License-Identifier: GPL-3.0-only

// Keep the normal backend shutdown reachable even if a frontend write stalls
// or fails. Repeated close requests share the same flush and deadline.
export function createWindowCloseHandler({ flush, quit, timeoutMs = 5000 }) {
  let pending;
  return function closeWindow() {
    if (pending) return pending;
    pending = (async () => {
      let timer;
      try {
        await Promise.race([
          Promise.resolve().then(flush),
          new Promise((resolve) => { timer = setTimeout(resolve, timeoutMs); }),
        ]);
      } catch (error) {
        console.error('[close] Failed to flush pending writes', error);
      } finally {
        clearTimeout(timer);
      }
      await quit();
    })().finally(() => { pending = undefined; });
    return pending;
  };
}
