import { computed } from 'vue'
import type { WritableComputedRef } from 'vue'
import { useRoute, useRouter } from 'vue-router'

/**
 * A view setting (a tab, a time window, a filter) that lives in the address,
 * so a reload or a shared link opens the same view. The default value is left
 * out of the address to keep it short. Changes replace the history entry.
 *
 * With `allowed`, any other value in the address reads as the default.
 */
export function useQueryParam<T extends string = string>(name: string, fallback: NoInfer<T>, allowed?: readonly T[]): WritableComputedRef<T> {
  const route = useRoute()
  const router = useRouter()
  return computed({
    get: () => {
      const raw = route.query[name]
      const v = (Array.isArray(raw) ? raw[0] : raw) ?? ''
      if (!v) return fallback
      if (allowed && !allowed.includes(v as T)) return fallback
      return v as T
    },
    set: (v) => {
      const query = { ...route.query, [name]: v && v !== fallback ? v : undefined }
      void router.replace({ query })
    },
  })
}
