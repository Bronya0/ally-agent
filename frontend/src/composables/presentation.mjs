import { computed, inject, onScopeDispose, ref, toValue, watch } from 'vue';
import { createDisplayClock } from '../utils/displayClock.mjs';

export const PRESENTATION_ACTIVE = Symbol('presentation-active');
const visible = ref(typeof document === 'undefined' || !document.hidden);
const clock = createDisplayClock();
let visibilityUsers = 0;

function syncVisibility() {
  const next = !document.hidden;
  clock.setVisible(next);
  visible.value = next;
}

export function useDocumentVisible() {
  if (typeof document !== 'undefined') {
    if (visibilityUsers++ === 0) {
      syncVisibility();
      document.addEventListener('visibilitychange', syncVisibility);
    }
    onScopeDispose(() => {
      if (--visibilityUsers === 0) document.removeEventListener('visibilitychange', syncVisibility);
    });
  }
  return visible;
}

export function usePresentationActive() {
  const parent = inject(PRESENTATION_ACTIVE, true);
  const pageVisible = useDocumentVisible();
  return computed(() => toValue(parent) && pageVisible.value);
}

export function useDisplayClock(enabled, period = 1000) {
  const time = ref(Date.now());
  const pageVisible = useDocumentVisible();
  let unsubscribe;
  watch(() => toValue(enabled) && pageVisible.value, (active) => {
    unsubscribe?.();
    unsubscribe = active ? clock.subscribe((value) => { time.value = value; }, period) : undefined;
  }, { immediate: true, flush: 'sync' });
  onScopeDispose(() => unsubscribe?.());
  return time;
}
