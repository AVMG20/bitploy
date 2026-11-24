import { computed, ref, watch } from 'vue'
import type { MaybeRefOrGetter } from 'vue'
import { toValue } from 'vue'
import { useQueries } from '@tanstack/vue-query'
import { api } from '@/lib/api'
import { aggregateResources, aggregateTraffic, homeMetricMeta, isHomeMetric } from '@/lib/home'
import type { HomeMetric, HomeMetricSummary } from '@/lib/home'
import { keys } from '@/lib/live'
import { isPublicApp } from '@/lib/projects'
import type { Metrics, Project, Quota, Traffic } from '@/lib/types'

export type CardMetricPref = 'match' | HomeMetric

const CHART_KEY = 'bitploy.home.metric'
const CARD_KEY = 'bitploy.home.cardMetric'

function readSavedChartMetric(): HomeMetric {
  try {
    const raw = window.localStorage.getItem(CHART_KEY)
    if (isHomeMetric(raw)) return raw
  } catch {
    // Private browsing or blocked storage: fall back to default.
  }
  return 'visitors'
}

function readSavedCardMetric(): CardMetricPref {
  try {
    const raw = window.localStorage.getItem(CARD_KEY)
    if (raw === 'match' || isHomeMetric(raw)) return raw
  } catch {
    // ignore storage errors
  }
  return 'match'
}

/**
 * Loads the selected 24h Home metrics (default: visitors) across the user's
 * projects. The overview chart and project cards can follow the same metric or
 * be customized independently (e.g. Memory chart + Requests on each card).
 * Queries only the endpoint kinds currently needed and shares Vue Query caches
 * with each app's tabs.
 */
export function useHomeMetrics(projects: MaybeRefOrGetter<readonly Project[]>, quota: MaybeRefOrGetter<Quota | undefined>) {
  const chartMetric = ref<HomeMetric>(readSavedChartMetric())
  const cardMetricPref = ref<CardMetricPref>(readSavedCardMetric())

  watch(chartMetric, (next) => {
    try {
      window.localStorage.setItem(CHART_KEY, next)
    } catch {
      // ignore storage errors
    }
  })

  watch(cardMetricPref, (next) => {
    try {
      window.localStorage.setItem(CARD_KEY, next)
    } catch {
      // ignore storage errors
    }
  })

  const cardMetric = computed<HomeMetric>(() => (cardMetricPref.value === 'match' ? chartMetric.value : cardMetricPref.value))

  const needsTraffic = computed(
    () => homeMetricMeta(chartMetric.value).kind === 'traffic' || homeMetricMeta(cardMetric.value).kind === 'traffic',
  )
  const needsResources = computed(
    () => homeMetricMeta(chartMetric.value).kind === 'resource' || homeMetricMeta(cardMetric.value).kind === 'resource',
  )

  const publicSlugs = computed(() => {
    if (!needsTraffic.value) return []
    const seen = new Set<string>()
    for (const p of toValue(projects)) {
      for (const a of p.apps) {
        if (isPublicApp(a) && a.status === 'running') seen.add(a.slug)
      }
    }
    return [...seen]
  })

  const resourceSlugs = computed(() => {
    if (!needsResources.value) return []
    const seen = new Set<string>()
    for (const p of toValue(projects)) {
      for (const a of p.apps) {
        if (a.status === 'running' && !a.static) seen.add(a.slug)
      }
    }
    return [...seen]
  })

  const trafficQueries = useQueries({
    queries: computed(() =>
      publicSlugs.value.map((slug) => ({
        queryKey: keys.traffic(slug, '24h'),
        queryFn: () => api.traffic(slug, '24h'),
        staleTime: 30_000,
        retry: false,
      })),
    ),
  })

  const metricQueries = useQueries({
    queries: computed(() =>
      resourceSlugs.value.map((slug) => ({
        queryKey: keys.metrics(slug, '24h', ''),
        queryFn: () => api.metrics(slug, '24h'),
        staleTime: 30_000,
        retry: false,
      })),
    ),
  })

  const trafficBySlug = computed(() => {
    const out: Record<string, Traffic | undefined> = {}
    publicSlugs.value.forEach((slug, i) => {
      out[slug] = trafficQueries.value[i]?.data
    })
    return out
  })

  const metricsBySlug = computed(() => {
    const out: Record<string, Metrics | undefined> = {}
    resourceSlugs.value.forEach((slug, i) => {
      out[slug] = metricQueries.value[i]?.data
    })
    return out
  })

  function buildSummary(m: HomeMetric): HomeMetricSummary {
    const list = toValue(projects)
    if (m === 'memory' || m === 'cpu') {
      const q = toValue(quota)
      const limit = q && !q.overallocate ? (m === 'memory' ? q.memory_mb : q.cpu_millis / 1000) : null
      return aggregateResources(list, metricsBySlug.value, m, limit)
    }
    return aggregateTraffic(list, trafficBySlug.value, m)
  }

  const chartSummary = computed<HomeMetricSummary>(() => buildSummary(chartMetric.value))
  const cardSummary = computed<HomeMetricSummary>(() =>
    cardMetric.value === chartMetric.value ? chartSummary.value : buildSummary(cardMetric.value),
  )

  const chartLoading = computed(() => {
    if (homeMetricMeta(chartMetric.value).kind === 'traffic') {
      return trafficQueries.value.length > 0 && trafficQueries.value.every((q) => q.isPending)
    }
    return metricQueries.value.length > 0 && metricQueries.value.every((q) => q.isPending)
  })

  return { chartMetric, cardMetricPref, cardMetric, chartSummary, cardSummary, chartLoading }
}
