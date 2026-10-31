<script setup lang="ts">
import { computed, ref } from 'vue'
import { ChartNoAxesColumn } from 'lucide-vue-next'
import EmptyState from '@/components/EmptyState.vue'
import QueryError from '@/components/QueryError.vue'
import TrafficChart from '@/components/TrafficChart.vue'
import TrafficList from '@/components/TrafficList.vue'
import type { TrafficRow } from '@/components/TrafficList.vue'
import { Card, CardContent } from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { useAppTraffic } from '@/composables/queries'
import { countryFlag, countryName, formatCount } from '@/lib/format'
import { cn } from '@/lib/utils'
import type { TrafficWindow } from '@/lib/types'

const props = defineProps<{ slug: string }>()

const windowSel = ref<TrafficWindow>('7d')
const windows: { value: TrafficWindow; label: string }[] = [
  { value: '24h', label: '24 hours' },
  { value: '7d', label: '7 days' },
  { value: '30d', label: '30 days' },
]
const traffic = useAppTraffic(() => props.slug, windowSel)
const data = computed(() => traffic.data.value)

const field = ref<'visitors' | 'requests'>('visitors')

const pages = computed<TrafficRow[]>(() => (data.value?.pages ?? []).map((p) => ({ key: p.name, label: p.name, count: p.count })))
const referrers = computed<TrafficRow[]>(() => (data.value?.referrers ?? []).map((r) => ({ key: r.name, label: r.name, count: r.count })))
const countries = computed<TrafficRow[]>(() =>
  (data.value?.countries ?? [])
    .filter((c) => c.visitors > 0)
    .map((c) => ({ key: c.code, label: countryName(c.code), prefix: countryFlag(c.code), count: c.visitors })),
)

const cell = 'p-4 text-left'
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
      <div class="grid grid-cols-3 overflow-hidden rounded-xl border bg-card shadow-card">
        <button type="button" :aria-pressed="field === 'visitors'" :class="cn(cell, 'transition-colors hover:bg-muted/40', field === 'visitors' && 'bg-muted/50 shadow-[inset_0_-2px_0_var(--primary)]')" @click="field = 'visitors'">
          <span class="text-xs font-medium tracking-wide text-muted-foreground uppercase">Visitors</span>
          <span class="mt-1 block text-xl font-semibold tracking-tight tabular-nums">{{ formatCount(data.totals.visitors) }}</span>
        </button>
        <button type="button" :aria-pressed="field === 'requests'" :class="cn(cell, 'border-l transition-colors hover:bg-muted/40', field === 'requests' && 'bg-muted/50 shadow-[inset_0_-2px_0_var(--primary)]')" @click="field = 'requests'">
          <span class="text-xs font-medium tracking-wide text-muted-foreground uppercase">Requests</span>
          <span class="mt-1 block text-xl font-semibold tracking-tight tabular-nums">{{ formatCount(data.totals.requests) }}</span>
        </button>
        <div :class="cn(cell, 'border-l')">
          <span class="text-xs font-medium tracking-wide text-muted-foreground uppercase">Error rate</span>
          <span class="mt-1 block text-xl font-semibold tracking-tight tabular-nums">
            {{ data.totals.error_rate }}%
            <span v-if="data.totals.errors > 0" class="text-sm font-normal tracking-normal text-muted-foreground">{{ formatCount(data.totals.errors) }} failed</span>
          </span>
        </div>
      </div>

      <Card>
        <CardContent>
          <TrafficChart :points="data.points" :field="field" :granularity="data.granularity" />
        </CardContent>
      </Card>

      <div class="grid gap-4 lg:grid-cols-3">
        <TrafficList title="Top pages" unit="Views" :rows="pages" empty="No pages yet." />
        <TrafficList title="Countries" unit="Visitors" :rows="countries" empty="No countries yet." />
        <TrafficList title="Referrers" unit="Visits" :rows="referrers" empty="Nobody came from another site." />
      </div>
    </template>

    <p class="text-xs text-muted-foreground">
      No cookies, no IP addresses stored. A visitor counts once per day.
      Country data by <a href="https://db-ip.com" target="_blank" rel="noopener noreferrer" class="underline">DB-IP.com</a>.
    </p>
  </div>
</template>
