// One timer for all presentation clocks. Task execution never depends on it.
export function createDisplayClock({ now = Date.now, schedule = setTimeout, cancel = clearTimeout,
  onError = (error) => console.error('[display-clock]', error) } = {}) {
  const subscribers = new Map();
  let timer = null;
  let visible = true;
  function publish(callback, time) {
    try { callback(time); }
    catch (error) {
      // A broken display must not stop other subscribers or the shared timer.
      try { onError(error); } catch (_) { /* reporting must not break the clock */ }
    }
  }
  function stop() {
    if (timer !== null) cancel(timer);
    timer = null;
  }
  function arm() {
    stop();
    if (!visible || !subscribers.size) return;
    let next = Infinity;
    for (const entry of subscribers.values()) next = Math.min(next, entry.next);
    timer = schedule(tick, Math.max(0, next - now()));
  }
  function tick() {
    timer = null;
    const time = now();
    for (const [callback, entry] of subscribers) {
      if (time < entry.next) continue;
      entry.next = time + entry.period;
      publish(callback, time);
    }
    arm();
  }
  return {
    subscribe(callback, period = 1000) {
      period = Math.max(100, Number(period) || 1000);
      subscribers.set(callback, { period, next: now() + period });
      publish(callback, now());
      arm();
      return () => { subscribers.delete(callback); arm(); };
    },
    setVisible(value) {
      if (visible === value) return;
      visible = value;
      if (visible) {
        const time = now();
        for (const [callback, entry] of subscribers) {
          entry.next = time + entry.period;
          publish(callback, time);
        }
      }
      arm();
    },
  };
}
