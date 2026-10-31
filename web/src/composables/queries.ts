import { computed, toValue } from 'vue'
import type { MaybeRefOrGetter } from 'vue'
import { useQuery } from '@tanstack/vue-query'
import { api } from '@/lib/api'
import type { MetricWindow } from '@/lib/types'

export const REFRESH_MS = 5_000

export const keys = {
  me: ['me'] as const,
  apps: ['apps'] as const,
  app: (slug: string) => ['app', slug] as const,
  metrics: (slug: string, window: MetricWindow) => ['metrics', slug, window] as const,
  deployments: (slug: string) => ['deployments', slug] as const,
  env: (slug: string) => ['env', slug] as const,
  domains: (slug: string) => ['domains', slug] as const,
  backups: (slug: string) => ['backups', slug] as const,
  tokens: ['tokens'] as const,
  sessions: ['sessions'] as const,
  github: ['github'] as const,
  adminUsers: ['admin', 'users'] as const,
  adminInvites: ['admin', 'invites'] as const,
  adminServer: ['admin', 'server'] as const,
  adminApps: ['admin', 'apps'] as const,
  adminAudit: ['admin', 'audit'] as const,
}

export function useMe() {
  return useQuery({ queryKey: keys.me, queryFn: api.me, refetchInterval: REFRESH_MS })
}

export function useApps() {
  return useQuery({ queryKey: keys.apps, queryFn: api.apps, refetchInterval: REFRESH_MS })
}

export function useApp(slug: MaybeRefOrGetter<string>) {
  return useQuery({
    queryKey: computed(() => keys.app(toValue(slug))),
    queryFn: () => api.app(toValue(slug)),
    refetchInterval: REFRESH_MS,
  })
}

export function useAppMetrics(slug: MaybeRefOrGetter<string>, window: MaybeRefOrGetter<MetricWindow>, enabled?: MaybeRefOrGetter<boolean>) {
  return useQuery({
    queryKey: computed(() => keys.metrics(toValue(slug), toValue(window))),
    queryFn: () => api.metrics(toValue(slug), toValue(window)),
    refetchInterval: 15_000,
    enabled: computed(() => (enabled === undefined ? true : toValue(enabled))),
  })
}

/** The backups of an app; refreshes every 2 seconds while one is being made or restored. */
export function useBackups(slug: MaybeRefOrGetter<string>) {
  return useQuery({
    queryKey: computed(() => keys.backups(toValue(slug))),
    queryFn: () => api.backups(toValue(slug)),
    refetchInterval: (q) => (q.state.data?.busy || q.state.data?.restore?.state === 'running' ? 2_000 : 15_000),
  })
}
