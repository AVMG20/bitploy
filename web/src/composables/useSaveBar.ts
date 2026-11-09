import { computed, inject, onBeforeUnmount, provide, ref, shallowRef, toValue } from 'vue'
import type { InjectionKey, MaybeRefOrGetter } from 'vue'

// One save bar for a page of settings. Every card registers what it has
// changed and how to save or discard it; the page shows the bar while any card
// has changes.

export interface SaveEntryOptions {
  /** How many fields differ from what is saved; 0 when clean. */
  changes: MaybeRefOrGetter<number>
  /** The changes only take effect on the next deploy. */
  later?: MaybeRefOrGetter<boolean>
  /** Validates and saves. Resolves true when everything was saved, false when it was not (the card shows why). */
  save: () => Promise<boolean>
  discard: () => void
}

export function createSaveBar() {
  const entries = shallowRef<SaveEntryOptions[]>([])
  const saving = ref(false)
  const dirty = computed(() => entries.value.filter((e) => toValue(e.changes) > 0))
  const count = computed(() => dirty.value.reduce((n, e) => n + toValue(e.changes), 0))
  const later = computed(() => dirty.value.some((e) => !!toValue(e.later)))

  function register(entry: SaveEntryOptions) {
    entries.value = [...entries.value, entry]
    return () => (entries.value = entries.value.filter((e) => e !== entry))
  }
  async function save() {
    if (saving.value || !dirty.value.length) return
    saving.value = true
    try {
      // Every card saves; one that fails stays dirty and shows its own error.
      await Promise.allSettled(dirty.value.map((e) => e.save()))
    } finally {
      saving.value = false
    }
  }
  function discard() {
    for (const e of dirty.value) e.discard()
  }
  return { register, save, discard, saving, count, later }
}

export type SaveBar = ReturnType<typeof createSaveBar>
const key: InjectionKey<SaveBar> = Symbol('saveBar')

export function provideSaveBar(): SaveBar {
  const bar = createSaveBar()
  provide(key, bar)
  return bar
}

/**
 * Register a card with the page's save bar for as long as the card is shown.
 * The page itself passes the bar it made: a component does not inject what it provides.
 */
export function useSaveBarEntry(entry: SaveEntryOptions, own?: SaveBar) {
  const bar = own ?? inject(key)
  if (!bar) throw new Error('useSaveBarEntry needs a page that calls provideSaveBar')
  const off = bar.register(entry)
  onBeforeUnmount(off)
}
