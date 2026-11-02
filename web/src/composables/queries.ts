import { computed, toValue } from 'vue'
import type { MaybeRefOrGetter } from 'vue'
import { useQuery } from '@tanstack/vue-query'
import { api } from '@/lib/api'
import { keys } from '@/lib/live'
import type { MetricWindow, TrafficWindow } from '@/lib/types'
import { eventsConnected } from './useEvents'

export const REFRESH_MS = 5_000
/** While the live event stream is open, polling is only a safety net. */
export const LIVE_FALLBACK_MS = 60_000

export { keys }

/**
 * The polling interval for a query that live events keep fresh: the given one
 * while the event stream is down, a slow safety net while it is up. Use it as
 * `refetchInterval: () => liveMs(5_000)`. Vue Query asks again after every
 * fetch, so a change of the stream state applies at the next refresh.
 */
export function liveMs(ms: number): number {
  return eventsConnected.value ? Math.max(ms, LIVE_FALLBACK_MS) : ms
}

/** `refetchInterval` for a query that live events keep fresh. */
export const livePoll = (ms: number) => () => liveMs(ms)

export function useMe() {
  return useQuery({ queryKey: keys.me, queryFn: api.me, refetchInterval: livePoll(REFRESH_MS) })
}

export function useApps() {
  return useQuery({ queryKey: keys.apps, queryFn: api.apps, refetchInterval: livePoll(REFRESH_MS) })
}

export function useApp(slug: MaybeRefOrGetter<string>) {
  return useQuery({
    queryKey: computed(() => keys.app(toValue(slug))),
    queryFn: () => api.app(toValue(slug)),
    refetchInterval: livePoll(REFRESH_MS),
  })
}

export function useAppTraffic(slug: MaybeRefOrGetter<string>, window: MaybeRefOrGetter<TrafficWindow>) {
  return useQuery({
    queryKey: computed(() => keys.traffic(toValue(slug), toValue(window))),
    queryFn: () => api.traffic(toValue(slug), toValue(window)),
    refetchInterval: 30_000,
  })
}

/** Only today's numbers (no chart, no lists): cheap enough for the Overview. */
export function useAppVisitorsToday(slug: MaybeRefOrGetter<string>) {
  return useQuery({
    queryKey: computed(() => keys.traffic(toValue(slug), 'today')),
    queryFn: () => api.traffic(toValue(slug), 'today'),
    refetchInterval: 30_000,
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

/** The backups of an app; refreshes every 2 seconds while one is being made or restored (and live events are not arriving). */
export function useBackups(slug: MaybeRefOrGetter<string>) {
  return useQuery({
    queryKey: computed(() => keys.backups(toValue(slug))),
    queryFn: () => api.backups(toValue(slug)),
    refetchInterval: (q) => liveMs(q.state.data?.busy || q.state.data?.restore?.state === 'running' ? 2_000 : 15_000),
  })
}
