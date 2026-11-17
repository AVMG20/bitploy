<script setup lang="ts">
import { computed, ref } from 'vue'
import { useQueryParam } from '@/composables/useQueryParam'
import { RouterLink } from 'vue-router'
import { ChartNoAxesColumn, ExternalLink, ScrollText } from 'lucide-vue-next'
import CompactCount from '@/components/CompactCount.vue'
import EmptyState from '@/components/EmptyState.vue'
import QueryError from '@/components/QueryError.vue'
import TrafficChart from '@/components/TrafficChart.vue'
import type { TrafficField } from '@/components/TrafficChart.vue'
import TrafficList from '@/components/TrafficList.vue'
import type { TrafficRow } from '@/components/TrafficList.vue'
import TrafficSplit from '@/components/TrafficSplit.vue'
import type { Split } from '@/components/TrafficSplit.vue'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { useQuery } from '@tanstack/vue-query'
import { keys, useApp, useAppMetrics } from '@/composables/queries'
import { api } from '@/lib/api'
import { CPU_HINT, countryName, formatCPU, formatMB } from '@/lib/format'
import { isMulti } from '@/lib/services'
import MetricChart from '@/components/MetricChart.vue'
import type { MetricWindow } from '@/lib/types'
import { change, formatSeconds, pointChange, share, timeChange } from '@/lib/traffic'
import type { Change } from '@/lib/traffic'
import { cn } from '@/lib/utils'
import type { Traffic, TrafficCountry, TrafficItem, TrafficListName, TrafficWindow } from '@/lib/types'

const props = defineProps<{ slug: string }>()

// Metrics: what the app uses (every app) and who visits it (websites). The two
// have their own period, in their own address parameter.
const usageSel = useQueryParam<MetricWindow>('usage', '1h', ['1h', '24h', '7d'])
const usageWindows: { value: MetricWindow; label: string }[] = [
  { value: '1h', label: '1 hour' },
  { value: '24h', label: '24 hours' },
  { value: '7d', label: '7 days' },
]
const windowSel = useQueryParam<TrafficWindow>('window', '7d', ['24h', '7d', '30d'])
const windows: { value: TrafficWindow; label: string; period: string; previous: string; lists: string }[] = [
  { value: '24h', label: '24 hours', period: 'the last 24 hours', previous: 'the 24 hours before', lists: 'Today and yesterday (UTC)' },
  { value: '7d', label: '7 days', period: 'the last 7 days', previous: 'the 7 days before', lists: 'Last 7 days' },
  { value: '30d', label: '30 days', period: 'the last 30 days', previous: 'the 30 days before', lists: 'Last 30 days' },
]
const windowInfo = computed(() => windows.find((w) => w.value === windowSel.value) ?? windows[1]!)
const app = useApp(() => props.slug)
const traffic = useQuery({
  queryKey: computed(() => keys.traffic(props.slug, windowSel.value)),
  queryFn: () => api.traffic(props.slug, windowSel.value),
  // A worker or database has no visitors to count.
  enabled: computed(() => app.data.value?.type === 'web'),
  refetchInterval: 5_000,
})
const data = computed(() => traffic.data.value)
const appUrl = computed(() => app.data.value?.url?.replace(/\/+$/, '') ?? '')
const isWeb = computed(() => app.data.value?.type === 'web')
// A project with several services shows its numbers per service on the Overview.
const multi = computed(() => isMulti(app.data.value))
const hasRun = computed(() => !!app.data.value?.current_deployment)
const metrics = useAppMetrics(() => props.slug, usageSel, () => hasRun.value && !multi.value)
const usage = computed(() => metrics.data.value?.points ?? [])
const cpuLimit = computed(() => (app.data.value?.cpu_millis ?? 0) / 1000)
const memoryLimit = computed(() => metrics.data.value?.memory_limit_mb ?? app.data.value?.memory_mb ?? 0)

const field = ref<TrafficField>('visitors')

/** The server sends the top this many per list. */
const TOP_N = 10
const DIRECT = '(direct)'

const pageRow = (i: TrafficItem): TrafficRow => ({ key: i.name, label: i.name, count: i.count, href: appUrl.value ? appUrl.value + i.name : undefined })
const itemRow = (i: TrafficItem): TrafficRow => ({ key: i.name, label: i.name, count: i.count })
const referrerRow = (i: TrafficItem): TrafficRow =>
  i.name === DIRECT
    ? { key: i.name, label: 'Direct (no link)', count: i.count, muted: true }
    : { ...itemRow(i), href: `https://${i.name}` }
const countryRows = (cs: TrafficCountry[]): TrafficRow[] =>
  cs
    .filter((c) => c.visitors > 0)
    .map((c) => ({
      key: c.code,
      label: countryName(c.code),
      prefix: /^[A-Za-z]{2}$/.test(c.code) ? c.code.toUpperCase() : '',
      count: c.visitors,
      extra: c.requests,
      muted: !/^[A-Za-z]{2}$/.test(c.code),
    }))

const pages = computed(() => (data.value?.pages ?? []).map(pageRow))
const referrers = computed(() => (data.value?.referrers ?? []).map(referrerRow))
const countries = computed(() => countryRows(data.value?.countries ?? []))
const notFound = computed(() => (data.value?.not_found ?? []).map(itemRow))

/** Loads one list in full for "Show all". */
function fullList(list: TrafficListName, rows: (t: Traffic) => TrafficRow[]) {
  return async () => rows(await api.trafficList(props.slug, windowSel.value, list))
}
const allPages = fullList('pages', (t) => t.pages.map(pageRow))
const allCountries = fullList('countries', (t) => countryRows(t.countries))
const allReferrers = fullList('referrers', (t) => t.referrers.map(referrerRow))
const allNotFound = fullList('not_found', (t) => t.not_found.map(itemRow))

const rate = (errors: number, requests: number) => (requests > 0 ? errors / requests : 0)

interface Tile {
  field: TrafficField
  label: string
  /** A count, shown short with the exact number on hover; or value, as text. */
  count?: number
  value?: string
  /** The sub line, after subCount when there is one. */
  subCount?: number
  sub: string
  /** The longer explanation, on hover. */
  hint: string
  change: Change | null
}

const tiles = computed<Tile[]>(() => {
  const d = data.value
  if (!d) return []
  const t = d.totals
  const p = d.previous
  return [
    {
      field: 'visitors',
      label: 'Visitors',
      count: t.visitors,
      subCount: t.page_views,
      sub: `page ${t.page_views === 1 ? 'view' : 'views'}`,
      hint: 'Different people: one browser on one device, counted once per day. Page views are the pages they opened.',
      change: change(t.visitors, p?.visitors),
    },
    {
      field: 'requests',
      label: 'Requests',
      count: t.requests,
      sub: t.bots === 0 ? 'None from bots' : `${share(t.bots, t.requests)} from bots`,
      hint: 'Everything your app was asked for: every page, image, script and file, by people and by bots.',
      change: change(t.requests, p?.requests),
    },
    {
      field: 'speed',
      label: 'Answer time',
      value: t.median_ms === null ? '-' : formatSeconds(t.median_ms),
      sub: t.median_ms === null ? 'Not measured yet' : 'Typical: half were faster',
      hint: 'How long your app took to answer people. Half of the answers were faster than this.',
      // Slower or faster, not up or down: slower is the bad direction.
      change: timeChange(t.median_ms, p?.median_ms),
    },
    {
      field: 'errors',
      label: 'Errors',
      value: `${t.error_rate}%`,
      subCount: t.errors === 0 ? undefined : t.errors,
      sub: t.errors === 0 ? 'No failed requests' : `failed ${t.errors === 1 ? 'request' : 'requests'}`,
      hint: 'The share of requests your app failed to answer (status 500 and up).',
      // The rate, not the count: more errors from more traffic is no change.
      // In percentage points, so 1% to 2% reads "↑ 1 pt" and not "↑ 100%".
      change: p && p.requests > 0 ? pointChange(rate(t.errors, t.requests), rate(p.errors, p.requests)) : null,
    },
  ]
})

const toneClass = { good: 'text-success', bad: 'text-destructive', same: 'text-muted-foreground' } as const

const per = computed(() => `per ${data.value?.granularity ?? 'day'}`)
const chart = computed(() => {
  switch (field.value) {
    case 'visitors':
      return {
        title: `Visitors ${per.value}`,
        about: 'How many different people opened your app. Point at a bar to see the pages they opened too.',
      }
    case 'requests':
      return {
        title: `Requests ${per.value}`,
        about: 'Everything your app was asked for. One page view often brings a dozen requests for its images, styles and scripts.',
      }
    case 'speed':
      return {
        title: `Answer time ${per.value}`,
        about: 'How long your app took to answer people, for pages and data (not images or other files). Typical: half of the answers were faster. Slowest 1 in 20: the other 19 were faster.',
      }
    default:
      return {
        title: `Failed requests ${per.value}`,
        about: 'Requests your app could not answer: it crashed, gave an error or was not running. A page that does not exist is not counted here; see Missing pages.',
      }
  }
})

const splits = computed<Split[]>(() => {
  const t = data.value?.totals
  if (!t) return []
  const out: Split[] = [
    {
      label: 'Sent by',
      of: 'requests',
      hint: 'Bots are search engines, link previews, uptime checks and scripts. They never count as visitors.',
      parts: [
        { label: 'People', short: 'people', count: Math.max(0, t.requests - t.bots), color: 'var(--chart-1)' },
        { label: 'Bots and scripts', short: 'bots', count: t.bots, color: 'color-mix(in oklch, var(--muted-foreground) 45%, transparent)' },
      ],
    },
    {
      label: 'Answers',
      of: 'requests',
      hint: 'Not found also counts refused requests: a missing page, a wrong password or a bad request. Failed: an error in your app.',
      parts: [
        { label: 'Worked', short: 'worked', count: Math.max(0, t.requests - t.errors - t.client_errors), color: 'var(--success)' },
        { label: 'Not found or refused', short: 'not found', count: t.client_errors, color: 'var(--warning)' },
        { label: 'Failed', short: 'failed', count: t.errors, color: 'var(--destructive)' },
      ],
    },
  ]
  if (t.timed > 0) {
    out.push({
      label: 'Speed',
      of: 'timed requests: pages and data, not files',
      hint: 'How fast people got an answer, for pages and data (not images or other files). Slow: a second or more, a wait people notice.',
      parts: [
        { label: 'Under 1 second', short: 'under 1 s', count: Math.max(0, t.timed - t.slow), color: 'var(--success)' },
        { label: '1 second or more', short: 'slow', count: t.slow, color: 'var(--warning)' },
      ],
    })
  }
  return out
})

const label = 'text-xs font-medium tracking-wide text-muted-foreground uppercase'
const value = 'mt-1 flex flex-wrap items-baseline gap-x-2 text-xl font-semibold tracking-tight tabular-nums'
</script>

<template>
  <div class="grid gap-4">
    <Card>
      <CardHeader class="flex flex-row flex-wrap items-center justify-between gap-2">
        <CardTitle>Usage</CardTitle>
        <Tabs v-model="usageSel">
          <TabsList aria-label="Period of the usage">
            <TabsTrigger v-for="w in usageWindows" :key="w.value" :value="w.value">{{ w.label }}</TabsTrigger>
          </TabsList>
        </Tabs>
      </CardHeader>
      <CardContent>
        <p v-if="multi" class="py-10 text-center text-sm text-muted-foreground">
          Each service has its own numbers on the
          <RouterLink :to="{ name: 'app', params: { slug } }" class="underline">Services</RouterLink> tab.
        </p>
        <p v-else-if="!hasRun" class="py-10 text-center text-sm text-muted-foreground">Nothing runs yet. Usage shows here once the first deploy is live.</p>
        <QueryError v-else-if="metrics.isError.value && !metrics.data.value" :error="metrics.error.value" @retry="metrics.refetch()" />
        <Skeleton v-else-if="metrics.isPending.value" class="h-48 w-full" />
        <p v-else-if="usage.length < 2" class="py-10 text-center text-sm text-muted-foreground">
          No data yet. Charts fill in about a minute after the app starts running.
        </p>
        <div v-else class="grid gap-6 md:grid-cols-2">
          <div class="min-w-0">
            <p class="mb-2 flex items-baseline justify-between gap-2 text-sm">
              <span class="font-medium">CPU</span>
              <span class="text-xs text-muted-foreground" :title="CPU_HINT">limit {{ formatCPU(cpuLimit) }}</span>
            </p>
            <MetricChart :points="usage" field="cpu" label="CPU" unit="CPU" :scale="0.01" :window="usageSel" :limit="cpuLimit" color="var(--chart-1)" />
          </div>
          <div class="min-w-0">
            <p class="mb-2 flex items-baseline justify-between gap-2 text-sm">
              <span class="font-medium">Memory</span>
              <span class="text-xs text-muted-foreground">MB, limit {{ formatMB(memoryLimit) }}</span>
            </p>
            <MetricChart :points="usage" field="memory_mb" label="Memory" unit="MB" :window="usageSel" :limit="memoryLimit" color="var(--chart-2)" />
          </div>
        </div>
      </CardContent>
    </Card>

    <p v-if="app.data.value && !isWeb" class="text-sm text-muted-foreground">
      This app has no web address, so there are no visitors to count. Its output is on the
      <RouterLink :to="{ name: 'app-logs', params: { slug } }" class="underline">Logs</RouterLink> tab.
    </p>

    <template v-if="isWeb">
    <!-- Visitors have periods of their own (up to 30 days), so the section has its own heading and switch. -->
    <div class="mt-4 flex flex-wrap items-end justify-between gap-x-4 gap-y-2">
      <div>
        <h2 class="text-base font-semibold">Visitors</h2>
        <p class="text-xs text-muted-foreground">No cookies, no tracking scripts, no IP addresses stored.</p>
      </div>
      <Tabs v-model="windowSel">
        <TabsList aria-label="Period of the visitor numbers">
          <TabsTrigger v-for="w in windows" :key="w.value" :value="w.value">{{ w.label }}</TabsTrigger>
        </TabsList>
      </Tabs>
    </div>

    <QueryError v-if="traffic.isError.value && !data" :error="traffic.error.value" @retry="traffic.refetch()" />
    <div v-else-if="!data" class="grid gap-4">
      <Skeleton class="h-24 w-full" />
      <Skeleton class="h-64 w-full" />
    </div>

    <EmptyState
      v-else-if="data.totals.requests === 0"
      :icon="ChartNoAxesColumn"
      :title="windowSel === '30d' ? 'No visits yet.' : `No visits in ${windowInfo.period}.`"
      description="Visits show up here within seconds of someone opening your app."
    >
      <Button v-if="windowSel !== '30d'" variant="outline" @click="windowSel = '30d'">Show the last 30 days</Button>
      <Button v-else-if="appUrl" as-child variant="outline">
        <a :href="appUrl" target="_blank" rel="noopener noreferrer"><ExternalLink /> Open website</a>
      </Button>
    </EmptyState>

    <template v-else>
      <div>
        <div class="grid grid-cols-2 gap-px overflow-hidden rounded-xl border bg-border shadow-card lg:grid-cols-4" role="group" aria-label="Show in chart">
          <button
            v-for="t in tiles"
            :key="t.field"
            type="button"
            :aria-pressed="field === t.field"
            :title="t.hint"
            :class="
              cn(
                'relative flex flex-col justify-start bg-card p-4 text-left transition-colors outline-none hover:bg-accent/50 focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-inset',
                field === t.field && 'ring-1 ring-primary/40 ring-inset',
              )
            "
            @click="field = t.field"
          >
            <span v-if="field === t.field" class="absolute inset-x-0 top-0 h-0.5 bg-primary" aria-hidden="true" />
            <span :class="cn(label, field === t.field && 'text-foreground')">{{ t.label }}</span>
            <span :class="value">
              <CompactCount v-if="t.count !== undefined" :n="t.count" /><template v-else>{{ t.value }}</template>
              <span v-if="t.change" :class="cn('text-xs font-medium tracking-normal', toneClass[t.change.tone])" :title="`Compared with ${windowInfo.previous}`">
                <span aria-hidden="true">{{ t.change.text }}</span><span class="sr-only">{{ t.change.words }} compared with {{ windowInfo.previous }}</span>
              </span>
            </span>
            <span class="mt-0.5 block text-xs text-muted-foreground"><template v-if="t.subCount !== undefined"><CompactCount :n="t.subCount" />{{ ' ' }}</template>{{ t.sub }}</span>
          </button>
        </div>
        <p class="mt-2 text-xs text-muted-foreground">
          {{ data.previous ? `Arrows compare with ${windowInfo.previous}.` : `No arrows yet: there are no complete numbers for ${windowInfo.previous}.` }} Days are UTC days.
        </p>
      </div>

      <Card>
        <CardHeader>
          <div class="flex flex-wrap items-center justify-between gap-2">
            <CardTitle>{{ chart.title }}</CardTitle>
            <RouterLink
              v-if="field === 'errors'"
              :to="{ name: 'app-logs', params: { slug } }"
              class="inline-flex items-center gap-1.5 text-xs text-muted-foreground hover:text-foreground"
            >
              <ScrollText class="size-3.5" /> Open logs
            </RouterLink>
          </div>
          <CardDescription class="text-xs">{{ chart.about }}</CardDescription>
        </CardHeader>
        <CardContent>
          <p v-if="field === 'errors' && data.totals.errors === 0" class="flex h-48 items-center justify-center text-sm text-muted-foreground">No failed requests in {{ windowInfo.period }}.</p>
          <p v-else-if="field === 'speed' && data.totals.timed === 0" class="flex h-48 items-center justify-center px-6 text-center text-sm text-muted-foreground">
            No answer times yet. They are measured for pages people open from now on.
          </p>
          <TrafficChart v-else :points="data.points" :field="field" :granularity="data.granularity" />
        </CardContent>
      </Card>

      <p v-if="windowSel === '24h'" class="-mb-1 text-xs text-muted-foreground">The lists below count whole days: today and yesterday (UTC).</p>
      <div class="grid gap-4 lg:grid-cols-2">
        <TrafficList
          title="Top pages"
          hint="Pages people opened. Click one to open it."
          unit="Views"
          :rows="pages"
          empty="No pages opened yet."
          :more="data.pages.length >= TOP_N"
          :fetch-all="allPages"
          :description="windowInfo.lists"
        />
        <TrafficList
          title="Came from"
          hint="Sites with a link people followed to get here. Direct: typed in, a bookmark or an app."
          unit="Visits"
          :rows="referrers"
          empty="No visits yet."
          :more="data.referrers.length >= TOP_N"
          :fetch-all="allReferrers"
          :description="windowInfo.lists"
        />
        <TrafficList
          title="Countries"
          hint="Where visitors were, from their internet address. The address itself is not kept."
          unit="Visitors"
          extra-unit="Requests"
          extra-hint="Everything asked for from there: pages, files, and requests from bots."
          :rows="countries"
          empty="No countries yet."
          :more="data.countries.length >= TOP_N"
          :fetch-all="allCountries"
          :description="windowInfo.lists"
        >
          <template #footer>
            Country data by
            <a href="https://db-ip.com" target="_blank" rel="noopener noreferrer" class="underline underline-offset-2 hover:text-foreground">DB-IP.com</a>
          </template>
        </TrafficList>
        <TrafficList
          title="Missing pages"
          hint="Addresses people asked for that do not exist. Fix the link or add a redirect. Odd ones like /wp-login.php are bots trying their luck."
          unit="Times"
          :rows="notFound"
          empty="No missing pages or files."
          :more="data.not_found.length >= TOP_N"
          :fetch-all="allNotFound"
          :description="windowInfo.lists"
        />
      </div>

      <Card>
        <CardHeader>
          <div class="flex items-baseline justify-between gap-2">
            <CardTitle>All <CompactCount :n="data.totals.requests" /> requests</CardTitle>
            <span class="text-xs text-muted-foreground">Last {{ windowInfo.label }}</span>
          </div>
        </CardHeader>
        <CardContent>
          <TrafficSplit :rows="splits" />
        </CardContent>
      </Card>
    </template>
    </template>
  </div>
</template>
