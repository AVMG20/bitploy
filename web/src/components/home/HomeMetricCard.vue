<script setup lang="ts">
// Left card of the Home "At a glance" row: a selectable 24-hour fleet chart
// (Visitors by default, switchable to Requests, Page views, Errors, Memory or
// CPU) rendered with Unovis + interactive crosshair/tooltip, with a top-3
// breakdown underneath.
import { computed } from 'vue'
import { RouterLink } from 'vue-router'
import { CurveType } from '@unovis/ts'
import { VisArea, VisAxis, VisLine, VisScatter, VisXYContainer } from '@unovis/vue'
import {
  ChartContainer,
  ChartCrosshair,
  ChartTooltip,
  ChartTooltipContent,
  componentToString,
} from '@/components/ui/chart'
import type { ChartConfig } from '@/components/ui/chart'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Skeleton } from '@/components/ui/skeleton'
import { projectRoute } from '@/lib/appLinks'
import { compactCount, formatMB } from '@/lib/format'
import { formatHomeMetricPoint, HOME_METRICS, isHomeMetric } from '@/lib/home'
import type { HomeMetric, HomeMetricSummary } from '@/lib/home'
import { cn } from '@/lib/utils'

const props = defineProps<{
  summary: HomeMetricSummary
  loading?: boolean
}>()

const metric = defineModel<HomeMetric>('metric', { required: true })

function onMetricChange(v: unknown) {
  if (isHomeMetric(v)) metric.value = v
}

interface Datum {
  date: Date
  value: number
  [key: string]: string | number | Date
}

const color = computed(() => (props.summary.tone === 'danger' ? 'var(--destructive)' : 'var(--chart-1)'))

const config = computed<ChartConfig>(() => ({
  [props.summary.metric]: { label: props.summary.meta.label, color: color.value },
}))

const data = computed<Datum[]>(() =>
  props.summary.buckets
    .map((b) => {
      const date = new Date(b.ts)
      if (Number.isNaN(date.getTime())) return null
      return {
        date,
        value: b.value,
        [props.summary.metric]: formatHomeMetricPoint(props.summary.metric, b.value),
      }
    })
    .filter((d): d is Datum => d !== null),
)

const maxY = computed(() => {
  const peak = Math.max(0, ...data.value.map((d) => d.value))
  if (props.summary.metric === 'cpu') return Math.max(0.05, peak * 1.15)
  if (props.summary.metric === 'memory') return Math.max(16, peak * 1.15)
  return Math.max(4, Math.ceil(peak * 1.12))
})

const x = (d: Datum) => d.date.getTime()
const y = (d: Datum) => d.value

const lastTime = computed(() => data.value.at(-1)?.date.getTime())
const isCurrent = (ms: number) => props.summary.meta.kind === 'traffic' && ms === lastTime.value

// Label whole hours counted back from the newest: every 6th hour across 24h.
const tickValues = computed(() => {
  const d = data.value
  const n = d.length
  if (n < 2) return d.map((p) => p.date.getTime())
  const step = n <= 6 ? Math.max(1, Math.floor(n / 2)) : 6
  return d.filter((_, i) => (n - 1 - i) % step === 0).map((p) => p.date.getTime())
})

function fmtTick(ms: number): string {
  const d = new Date(ms)
  return d.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit', hour12: false })
}

function fmtY(t: number): string {
  if (props.summary.metric === 'memory') return t === 0 ? '0' : formatMB(t)
  if (props.summary.metric === 'cpu') return t === 0 ? '0' : `${Math.round(t * 100) / 100}`
  return Number.isInteger(t) ? compactCount(t) : ''
}

const template = computed(() =>
  componentToString(config.value, ChartTooltipContent, {
    class: 'min-w-36 [&_.justify-between]:gap-4',
    labelFormatter: (d: number | Date) => {
      const date = d instanceof Date ? d : new Date(d)
      const label = date.toLocaleString(undefined, { weekday: 'short', hour: '2-digit', minute: '2-digit', hour12: false })
      return isCurrent(date.getTime()) ? `${label} (so far)` : label
    },
  }),
)

const maxTop = computed(() => Math.max(1, ...props.summary.top.map((t) => t.value)))

const BADGE_CLASS = {
  good: 'bg-success/12 text-success',
  bad: 'bg-destructive/12 text-destructive',
  neutral: 'bg-muted text-muted-foreground',
} as const
</script>

<template>
  <section class="flex h-full flex-col overflow-hidden rounded-xl border bg-card shadow-card" aria-label="Fleet metrics" data-testid="home-metric-card">
    <div class="flex items-center justify-between gap-2 px-4 pt-3 pb-1.5">
      <Select :model-value="metric" @update:model-value="onMetricChange">
        <SelectTrigger
          size="sm"
          aria-label="Choose overview metric"
          class="-ml-2 h-7 border-transparent bg-transparent px-2 text-sm font-semibold shadow-none hover:bg-muted/60 dark:bg-transparent dark:hover:bg-muted/60"
        >
          <SelectValue />
        </SelectTrigger>
        <SelectContent position="popper" align="start">
          <SelectItem v-for="m in HOME_METRICS" :key="m.key" :value="m.key">{{ m.label }}</SelectItem>
        </SelectContent>
      </Select>
      <span class="font-mono text-[11px] text-muted-foreground">24h</span>
    </div>

    <div v-if="loading" class="grid flex-1 gap-3 px-4 pt-1 pb-4">
      <Skeleton class="h-7 w-28" />
      <Skeleton class="h-32 w-full" />
      <Skeleton class="h-12 w-full" />
    </div>

    <template v-else>
      <div class="flex flex-wrap items-baseline gap-2 px-4 pb-1" :title="summary.totalTitle">
        <span class="text-2xl font-semibold tracking-tight tabular-nums">{{ summary.totalDisplay }}</span>
        <span class="text-xs text-muted-foreground">{{ summary.meta.heroUnit }}</span>
        <span
          v-if="summary.badge"
          :class="cn('rounded-full px-2 py-0.5 text-[11px] font-medium tabular-nums', BADGE_CLASS[summary.badge.tone])"
          :title="summary.badge.title"
        >
          {{ summary.badge.text }}
        </span>
      </div>

      <div class="mt-auto px-3 pb-2" role="region" :aria-label="`${summary.meta.label} over the last 24 hours`">
        <ChartContainer v-if="data.length" :config="config" class="aspect-auto! h-32 w-full" cursor>
          <VisXYContainer :data="data" :y-domain="[0, maxY]" :margin="{ top: 8, left: 4, right: 8 }">
            <VisArea :x="x" :y="y" :color="color" :opacity="0.14" :curve-type="CurveType.MonotoneX" />
            <VisLine :x="x" :y="y" :color="color" :line-width="2" :curve-type="CurveType.MonotoneX" />
            <VisScatter v-if="data.length === 1" :x="x" :y="y" :color="color" :size="5" />
            <VisAxis type="x" :x="x" :tick-values="tickValues" :tick-format="fmtTick" :grid-line="false" />
            <VisAxis type="y" :num-ticks="3" :tick-format="fmtY" :domain-line="false" />
            <ChartCrosshair :x="x" :y="[y]" :template="template" :color="color" />
            <ChartTooltip />
          </VisXYContainer>
        </ChartContainer>
        <div v-else class="flex h-32 items-center px-1 text-xs text-muted-foreground">
          {{ summary.emptyText }}
        </div>
      </div>

      <div class="border-t px-4 py-2.5">
        <ul v-if="summary.top.length" class="grid gap-1.5">
          <li v-for="item in summary.top" :key="item.projectId">
            <RouterLink
              :to="projectRoute(item.projectId)"
              class="group flex items-center gap-2.5 text-xs transition-colors"
            >
              <span class="min-w-0 flex-1 truncate font-mono text-foreground/90 group-hover:text-foreground group-hover:underline">
                {{ summary.meta.kind === 'traffic' ? item.subtitle : item.name }}
              </span>
              <span class="h-1 w-14 shrink-0 overflow-hidden rounded-full bg-muted">
                <span
                  :class="cn('block h-full rounded-full', summary.tone === 'danger' ? 'bg-destructive' : 'bg-primary')"
                  :style="{ width: `${Math.max(8, Math.round((item.value / maxTop) * 100))}%` }"
                />
              </span>
              <span class="min-w-14 shrink-0 text-right font-mono text-muted-foreground tabular-nums">{{ item.display }}</span>
            </RouterLink>
          </li>
        </ul>
        <p v-else class="text-xs text-muted-foreground">{{ summary.emptyText }}</p>
      </div>
    </template>
  </section>
</template>
