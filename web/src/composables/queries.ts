import { computed, toValue } from 'vue'
import type { MaybeRefOrGetter } from 'vue'
import { keepPreviousData, useQueries, useQuery } from '@tanstack/vue-query'
import { ApiError, api } from '@/lib/api'
import { keys } from '@/lib/live'
import type { DbSource } from '@/lib/dbSource'
import { projectsFromApps } from '@/lib/projects'
import type { App, DbRowsQuery, EnvVar, MetricWindow, Project, TrafficWindow } from '@/lib/types'
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

/** Who can make an account here (no login needed): the login, invite and Members pages follow it. */
export function useSignupRules() {
  return useQuery({ queryKey: keys.signupRules, queryFn: api.signupRules, staleTime: 60_000 })
}

export function useApps() {
  return useQuery({ queryKey: keys.apps, queryFn: api.apps, refetchInterval: livePoll(REFRESH_MS) })
}

export interface ProjectList {
  projects: Project[]
  /** The server has no projects yet: each app is shown as a project of its own, and nothing can be added to one. */
  fallback: boolean
}

/**
 * The projects with their apps. A server without the projects endpoint gets one
 * project per app, made from /apps, so the dashboard works on both.
 */
export function useProjects() {
  return useQuery({
    queryKey: keys.projects,
    queryFn: async (): Promise<ProjectList> => {
      const list = await api.projects()
      if (list) return { projects: list, fallback: false }
      return { projects: projectsFromApps(await api.apps()), fallback: true }
    },
    refetchInterval: livePoll(REFRESH_MS),
  })
}

/** One project, from the list (so the sidebar, the page and the palette share one request). */
export function useProject(id: MaybeRefOrGetter<string>) {
  const list = useProjects()
  const project = computed(() => list.data.value?.projects.find((p) => p.id === toValue(id)) ?? null)
  return { list, project }
}

/**
 * The variables of every app of a project, by slug: the canvas draws who talks
 * to whom from their ${{alias.VAR}} references. Shares the cache of the
 * Variables tab.
 */
export function useProjectEnv(apps: MaybeRefOrGetter<readonly App[]>) {
  const results = useQueries({
    queries: computed(() =>
      toValue(apps).map((a) => ({ queryKey: keys.env(a.slug), queryFn: () => api.env(a.slug), staleTime: 30_000, retry: false })),
    ),
  })
  return computed(() => {
    const out: Record<string, EnvVar[] | undefined> = {}
    toValue(apps).forEach((a, i) => (out[a.slug] = results.value[i]?.data))
    return out
  })
}

/** Databases and other ready services; [] on a server without them. */
export function useServiceTemplates() {
  return useQuery({
    queryKey: keys.serviceTemplates,
    queryFn: async () => (await api.serviceTemplates()) ?? [],
    staleTime: 10 * 60_000,
    retry: false,
  })
}

/** The ready-made apps. They change with bitploy releases, so there is no polling. */
export function useCatalog() {
  return useQuery({ queryKey: keys.catalog, queryFn: api.catalog, staleTime: 5 * 60_000, retry: false })
}

export function useCatalogApp(id: MaybeRefOrGetter<string>, enabled: MaybeRefOrGetter<boolean> = true) {
  return useQuery({
    queryKey: computed(() => keys.catalogApp(toValue(id))),
    queryFn: () => api.catalogApp(toValue(id)),
    enabled: computed(() => !!toValue(id) && toValue(enabled)),
    staleTime: 5 * 60_000,
    retry: false,
  })
}

export function useApp(slug: MaybeRefOrGetter<string>, enabled: MaybeRefOrGetter<boolean> = true) {
  return useQuery({
    queryKey: computed(() => keys.app(toValue(slug))),
    queryFn: () => api.app(toValue(slug)),
    enabled: computed(() => toValue(enabled)),
    refetchInterval: livePoll(REFRESH_MS),
  })
}

/**
 * Traffic and the hour of metrics while a page shows them. The server reads the
 * access log every 2 seconds while someone asks (see traffic.Manager.Watch) and
 * the stats stream is about one reading a second, so these are seconds old.
 */
const TRAFFIC_MS = 5_000
const LIVE_METRICS_MS = 3_000

export function useAppTraffic(slug: MaybeRefOrGetter<string>, window: MaybeRefOrGetter<TrafficWindow>) {
  return useQuery({
    queryKey: computed(() => keys.traffic(toValue(slug), toValue(window))),
    queryFn: () => api.traffic(toValue(slug), toValue(window)),
    refetchInterval: TRAFFIC_MS,
  })
}

/** Only today's numbers (no chart, no lists): cheap enough for the Overview. */
export function useAppVisitorsToday(slug: MaybeRefOrGetter<string>) {
  return useQuery({
    queryKey: computed(() => keys.traffic(toValue(slug), 'today')),
    queryFn: () => api.traffic(toValue(slug), 'today'),
    refetchInterval: TRAFFIC_MS,
  })
}

export function useAppMetrics(
  slug: MaybeRefOrGetter<string>,
  window: MaybeRefOrGetter<MetricWindow>,
  enabled?: MaybeRefOrGetter<boolean>,
  /** One service of a project with several; empty for the whole project. */
  service: MaybeRefOrGetter<string> = '',
  /** Sparklines on the service cards do not need a reading every few seconds. */
  slow = false,
) {
  return useQuery({
    queryKey: computed(() => keys.metrics(toValue(slug), toValue(window), toValue(service))),
    queryFn: () => api.metrics(toValue(slug), toValue(window), toValue(service) || undefined),
    // The hour chart and the "now" numbers end at a live reading: ask often.
    // The longer windows are 5-minute buckets. Hidden tabs do not poll at all.
    refetchInterval: () => (slow ? 15_000 : toValue(window) === '1h' ? LIVE_METRICS_MS : 15_000),
    enabled: computed(() => (enabled === undefined ? true : toValue(enabled))),
  })
}

/** The CPU and memory history of the whole server (admin). A new sample is stored every 15 seconds. */
export function useServerMetrics(window: MaybeRefOrGetter<MetricWindow>) {
  return useQuery({
    queryKey: computed(() => keys.adminServerMetrics(toValue(window))),
    queryFn: () => api.adminServerMetrics(toValue(window)),
    refetchInterval: 15_000,
  })
}

/** The backups of an app; refreshes every 2 seconds while one is being made or restored (and live events are not arriving). */
export function useBackups(slug: MaybeRefOrGetter<string>, enabled: MaybeRefOrGetter<boolean> = true) {
  return useQuery({
    enabled: () => toValue(enabled),
    queryKey: computed(() => keys.backups(toValue(slug))),
    queryFn: () => api.backups(toValue(slug)),
    refetchInterval: (q) => liveMs(q.state.data?.busy || q.state.data?.restore?.state === 'running' ? 2_000 : 15_000),
  })
}

/** The files of one folder of a volume; '' is the default (first) volume. Shared by the Storage tab and the Database tab. */
export function filesKey(slug: string, volume: string, path: string) {
  return volume ? (['files', slug, path, volume] as const) : (['files', slug, path] as const)
}

/** A busy database (locked, or too many queries at once) often is free a second later: ask once more. Other client errors are not repeated. */
function dbRetry(count: number, error: unknown): boolean {
  if (error instanceof ApiError && error.code === 'db_busy') return count < 1
  if (error instanceof ApiError && error.status >= 400 && error.status < 500) return false
  return count < 2
}

/** Tables, views, columns and indexes of a database. Not polled: the page refreshes it after its own writes. */
export function useDbSchema(source: MaybeRefOrGetter<DbSource>, enabled: MaybeRefOrGetter<boolean> = true) {
  return useQuery({
    queryKey: computed(() => toValue(source).schemaKey),
    queryFn: () => toValue(source).schema(),
    enabled: computed(() => toValue(enabled)),
    staleTime: 10_000,
    retry: dbRetry,
    retryDelay: 1_000,
  })
}

/** One page of rows of a table or view. The previous page stays on screen while the next loads. */
export function useDbRows(source: MaybeRefOrGetter<DbSource>, query: MaybeRefOrGetter<DbRowsQuery>) {
  return useQuery({
    queryKey: computed(() => [...toValue(source).rowsKey, toValue(query)] as const),
    queryFn: () => toValue(source).rows(toValue(query)),
    placeholderData: keepPreviousData,
    staleTime: 5_000,
    retry: dbRetry,
    retryDelay: 1_000,
  })
}

/**
 * The files at the top of /data, for the Database tab. It is the Storage tab's own query (same key, same
 * function), so they share the cache. Each call starts a helper container: a generous staleTime, no
 * refetch on focus, no retry, and nothing at all while /data is off.
 */
export function useDataRoot(slug: MaybeRefOrGetter<string>, enabled: MaybeRefOrGetter<boolean>) {
  return useQuery({
    queryKey: computed(() => filesKey(toValue(slug), '', '')),
    queryFn: () => api.files(toValue(slug), ''),
    enabled: computed(() => toValue(enabled)),
    staleTime: 5 * 60_000,
    refetchOnWindowFocus: false,
    retry: false,
  })
}
