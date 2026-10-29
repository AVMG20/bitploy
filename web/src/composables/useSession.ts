import { computed } from 'vue'
import { useQuery } from '@tanstack/vue-query'
import { api } from '@/lib/api'

export const SESSION_KEY = ['session'] as const

export function useSession() {
  const query = useQuery({
    queryKey: SESSION_KEY,
    queryFn: api.session,
    staleTime: 5 * 60_000,
    retry: false,
  })
  const user = computed(() => query.data.value?.user ?? null)
  const isAdmin = computed(() => user.value?.role === 'admin')
  return { ...query, user, isAdmin }
}
