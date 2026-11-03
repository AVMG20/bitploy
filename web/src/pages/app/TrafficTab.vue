<script setup lang="ts">
import { computed, ref } from 'vue'
import { RouterLink } from 'vue-router'
import { ChartNoAxesColumn, ScrollText } from 'lucide-vue-next'
import EmptyState from '@/components/EmptyState.vue'
import QueryError from '@/components/QueryError.vue'
import TrafficChart from '@/components/TrafficChart.vue'
import type { TrafficField } from '@/components/TrafficChart.vue'
import TrafficList from '@/components/TrafficList.vue'
import type { TrafficRow } from '@/components/TrafficList.vue'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { useAppTraffic } from '@/composables/queries'
import { api } from '@/lib/api'
import { countryName, formatCount } from '@/lib/format'
import { cn } from '@/lib/utils'
import type { Traffic, TrafficCountry, TrafficItem, TrafficListName, TrafficWindow } from '@/lib/types'

const props = defineProps<{ slug: string }>()

const windowSel = ref<TrafficWindow>('7d')
const windows: { value: TrafficWindow; label: string; previous: string; lastly: string }[] = [
  { value: '24h', label: '24 hours', previous: 'the 24 hours before', lastly: 'Today and yesterday (UTC)' },
  { value: '7d', label: '7 days', previous: 'the 7 days before', lastly: 'Last 7 days' },
  { value: '30d', label: '30 days', previous: 'the 30 days before', lastly: 'Last 30 days' },
]
const windowInfo = computed(() => windows.find((w) => w.value === windowSel.value) ?? windows[1])
const traffic = useAppTraffic(() => props.slug, windowSel)
const data = computed(() => traffic.data.value)

const field = ref<TrafficField>('visitors')

/** The server sends the top this many per list. */
const TOP_N = 10
const DIRECT = '(direct)'

const itemRow = (i: TrafficItem): TrafficRow => ({ key: i.name, label: i.name, count: i.count })
const referrerRow = (i: TrafficItem): TrafficRow =>
  i.name === DIRECT ? { key: i.name, label: 'Direct / none', count: i.count, muted: true } : itemRow(i)
const countryRows = (cs: TrafficCountry[]): TrafficRow[] =>
  cs
    .filter((c) => c.visitors > 0)
    .map((c) => ({ key: c.code, label: countryName(c.code), prefix: /^[A-Za-z]{2}$/.test(c.code) ? c.code.toUpperCase() : '', count: c.visitors }))

const pages = computed(() => (data.value?.pages ?? []).map(itemRow))
const referrers = computed(() => (data.value?.referrers ?? []).map(referrerRow))
const countries = computed(() => countryRows(data.value?.countries ?? []))
const notFound = computed(() => (data.value?.not_found ?? []).map(itemRow))

/** Loads one list in full for "Show all". */
function fullList(list: TrafficListName, rows: (t: Traffic) => TrafficRow[]) {
  return async () => rows(await api.trafficList(props.slug, windowSel.value, list))
}
const allPages = fullList('pages', (t) => t.pages.map(itemRow))
const allCountries = fullList('countries', (t) => countryRows(t.countries))
const allReferrers = fullList('referrers', (t) => t.referrers.map(referrerRow))
const allNotFound = fullList('not_found', (t) => t.not_found.map(itemRow))

interface Delta {
  text: string
  tone: string
}

/** Change from prev to cur; none when there is nothing to compare with. */
function delta(cur: number, prev: number | undefined, upIsBad = false): Delta | null {
  if (prev === undefined || !(prev > 0)) return null
  const pct = Math.round(((cur - prev) / prev) * 100)
  if (pct === 0) return { text: '0%', tone: 'text-muted-foreground' }
  const n = Math.abs(pct) > 999 ? '999+' : `${Math.abs(pct)}`
  return { text: `${pct > 0 ? '↑' : '↓'} ${n}%`, tone: pct > 0 !== upIsBad ? 'text-success' : 'text-destructive' }
}

const rate = (errors: number, requests: number) => (requests > 0 ? errors / requests : 0)

function botShare(bots: number, requests: number): string {
  if (bots === 0) return 'No bots'
  const p = Math.round((bots / requests) * 100)
  return p === 0 ? 'Under 1% bots' : `${p}% bots`
}

interface Tile {
  field: TrafficField
  label: string
  value: string
  sub?: string
  subTitle?: string
  delta: Delta | null
}

const tiles = computed<Tile[]>(() => {
  const d = data.value
  if (!d) return []
  const t = d.totals
  const p = d.previous
  return [
    { field: 'visitors', label: 'Visitors', value: formatCount(t.visitors), delta: delta(t.visitors, p?.visitors) },
    {
      field: 'requests',
      label: 'Requests',
      value: formatCount(t.requests),
      sub: botShare(t.bots, t.requests),
      subTitle: 'Crawlers and scripts: counted in requests, not in visitors.',
      delta: delta(t.requests, p?.requests),
    },
    {
      field: 'errors',
      label: 'Errors',
      value: `${t.error_rate}%`,
      sub: `5xx: ${formatCount(t.errors)} · 4xx: ${formatCount(t.client_errors)}`,
      subTitle: '5xx: your app failed. 4xx: not found, refused or a bad request.',
      // The rate, not the count: more errors from more traffic is no change.
      delta: p ? delta(rate(t.errors, t.requests), p.errors > 0 ? rate(p.errors, p.requests) : undefined, true) : null,
    },
  ]
})

const chartTitle = computed(() => {
  const per = `per ${data.value?.granularity ?? 'day'}`
  return field.value === 'visitors' ? `Visitors ${per}` : field.value === 'requests' ? `Requests ${per}` : `Failed requests (5xx) ${per}`
})

const label = 'text-xs font-medium tracking-wide text-muted-foreground uppercase'
const value = 'mt-1 flex flex-wrap items-baseline gap-x-2 text-xl font-semibold tracking-tight tabular-nums'
</script>

<template>
  <div class="grid gap-4">
    <div>
      <Tabs v-model="windowSel">
        <TabsList>
          <TabsTrigger v-for="w in windows" :key="w.value" :value="w.value">{{ w.label }}</TabsTrigger>
        </TabsList>
      </Tabs>
    </div>

    <QueryError v-if="traffic.isError.value && !data" :error="traffic.error.value" @retry="traffic.refetch()" />
    <div v-else-if="!data" class="grid gap-4">
      <Skeleton class="h-20 w-full" />
      <Skeleton class="h-64 w-full" />
    </div>

    <EmptyState
      v-else-if="data.totals.requests === 0"
      :icon="ChartNoAxesColumn"
      title="No visits yet."
      description="Visits show up here a minute after someone opens your app."
    />

    <template v-else>
      <div class="grid grid-cols-3 overflow-hidden rounded-xl border bg-card shadow-card" role="group" aria-label="Show in chart">
        <button
          v-for="(t, i) in tiles"
          :key="t.field"
          type="button"
          :aria-pressed="field === t.field"
          :class="
            cn(
              'relative flex flex-col justify-start p-4 text-left transition-colors outline-none hover:bg-accent/50 focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-inset',
              i > 0 && 'border-l',
              field === t.field && 'bg-accent/60 hover:bg-accent/60',
            )
          "
          @click="field = t.field"
        >
          <span v-if="field === t.field" class="absolute inset-x-0 top-0 h-0.5 bg-primary" aria-hidden="true" />
          <span :class="cn(label, field === t.field && 'text-foreground')">{{ t.label }}</span>
          <span :class="value">
            {{ t.value }}
            <span v-if="t.delta" :class="cn('text-xs font-medium tracking-normal', t.delta.tone)" :title="`Compared with ${windowInfo.previous}`">
              {{ t.delta.text }}<span class="sr-only"> compared with {{ windowInfo.previous }}</span>
            </span>
          </span>
          <span v-if="t.sub" class="mt-0.5 block truncate text-xs text-muted-foreground" :title="t.subTitle">{{ t.sub }}</span>
        </button>
      </div>

      <Card>
        <CardHeader class="flex flex-row flex-wrap items-center justify-between gap-2">
          <CardTitle>{{ chartTitle }}</CardTitle>
          <RouterLink
            v-if="field === 'errors'"
            :to="{ name: 'app-logs', params: { slug } }"
            class="inline-flex items-center gap-1.5 text-xs text-muted-foreground hover:text-foreground"
          >
            <ScrollText class="size-3.5" /> Open logs
          </RouterLink>
        </CardHeader>
        <CardContent>
          <p v-if="field === 'errors' && data.totals.errors === 0" class="flex h-48 items-center justify-center text-sm text-muted-foreground">No failed requests.</p>
          <TrafficChart v-else :points="data.points" :field="field" :granularity="data.granularity" />
        </CardContent>
      </Card>

      <div class="grid gap-4 lg:grid-cols-2">
        <TrafficList
          title="Top pages"
          unit="Views"
          :rows="pages"
          empty="No pages yet."
          :more="data.pages.length >= TOP_N"
          :fetch-all="allPages"
          :description="windowInfo.lastly"
        />
        <TrafficList
          title="Countries"
          unit="Visitors"
          :rows="countries"
          empty="No countries yet."
          :more="data.countries.length >= TOP_N"
          :fetch-all="allCountries"
          :description="windowInfo.lastly"
        />
        <TrafficList
          title="Came from"
          unit="Visits"
          :rows="referrers"
          empty="No visits yet."
          :more="data.referrers.length >= TOP_N"
          :fetch-all="allReferrers"
          :description="windowInfo.lastly"
        />
        <TrafficList
          title="Not found"
          unit="Hits"
          :rows="notFound"
          empty="No missing pages or files."
          :more="data.not_found.length >= TOP_N"
          :fetch-all="allNotFound"
          :description="windowInfo.lastly"
        />
      </div>
    </template>

    <p class="text-xs text-muted-foreground">
      Visitors count once per UTC day. Requests include files and bots. Errors are 5xx answers from your app. Arrows compare
      with the period before. No cookies, no IP addresses stored. Country data by
      <a href="https://db-ip.com" target="_blank" rel="noopener noreferrer" class="underline">DB-IP.com</a>.
    </p>
  </div>
</template>
