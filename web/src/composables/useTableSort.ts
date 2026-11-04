import { computed } from 'vue'
import { useQueryParam } from './useQueryParam'

/**
 * Sorting of a list by one column, kept in the address as ?sort=name or
 * ?sort=-name (newest/biggest first). Clicking the sorted column flips it;
 * another column starts in its own natural direction.
 */
export function useTableSort<K extends string>(columns: readonly K[], fallback: K, firstDesc: readonly K[] = [], param = 'sort') {
  const raw = useQueryParam<string>(param, fallback)
  const key = computed<K>(() => {
    const k = raw.value.replace(/^-/, '') as K
    return columns.includes(k) ? k : fallback
  })
  const desc = computed(() => raw.value.startsWith('-') && columns.includes(raw.value.slice(1) as K))

  function toggle(k: K) {
    if (k === key.value) raw.value = desc.value ? k : `-${k}`
    else raw.value = firstDesc.includes(k) ? `-${k}` : k
  }
  function set(k: K, d: boolean) {
    raw.value = d ? `-${k}` : k
  }
  return { key, desc, toggle, set }
}
