import { QueryClient } from '@tanstack/vue-query'
import { ApiError } from './api'

export const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      staleTime: 2_000,
      refetchOnWindowFocus: true,
      retry: (count, error) => {
        // Do not hammer on client errors (401/403/404/422...).
        if (error instanceof ApiError && error.status >= 400 && error.status < 500) return false
        return count < 2
      },
    },
  },
})
