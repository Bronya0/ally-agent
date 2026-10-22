// Startup work runs after the current render, at idle or at a bounded timeout.
// Disposing the owner cancels all tasks that have not started yet.
export function createStartupTasks(host = globalThis, onError = console.error) {
  const pending = new Set();
  let disposed = false;

  function schedule(task, { delay = 0, timeout = 1500 } = {}) {
    if (disposed) return () => {};
    let timer;
    let idle;
    let cancelled = false;
    const cancel = () => {
      cancelled = true;
      host.clearTimeout(timer);
      if (idle !== undefined) host.cancelIdleCallback?.(idle);
      pending.delete(cancel);
    };
    const run = () => {
      Promise.resolve().then(() => {
        pending.delete(cancel);
        if (!disposed && !cancelled) return task();
      }).catch(onError);
    };
    pending.add(cancel);
    timer = host.setTimeout(() => {
      if (disposed || cancelled) return;
      if (typeof host.requestIdleCallback === 'function') {
        idle = host.requestIdleCallback(run, { timeout });
      } else {
        run();
      }
    }, delay);
    return cancel;
  }

  return {
    schedule,
    dispose() {
      disposed = true;
      for (const cancel of pending) cancel();
    },
  };
}
