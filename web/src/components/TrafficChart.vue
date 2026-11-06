<script setup lang="ts">
import { computed } from 'vue'
import { CurveType } from '@unovis/ts'
import { VisAxis, VisLine, VisScatter, VisStackedBar, VisXYContainer } from '@unovis/vue'
import {
  ChartContainer,
  ChartCrosshair,
  ChartTooltip,
  ChartTooltipContent,
  componentToString,
} from '@/components/ui/chart'
import type { ChartConfig } from '@/components/ui/chart'
import { formatCount } from '@/lib/format'
import { formatSeconds } from '@/lib/traffic'
import type { TrafficPoint } from '@/lib/types'

export type TrafficField = 'visitors' | 'requests' | 'speed' | 'errors'

const props = defineProps<{
  points: TrafficPoint[]
  field: TrafficField
  granularity: 'hour' | 'day'
}>()

// What the bars and lines are drawn from (numbers) is kept apart from what the
// tooltip shows (the keys in the config, as text), so the tooltip can say
// "0.12 s" and "1,204" while the axes get plain numbers.
interface Datum {
  date: Date
  top: number
  ys: (number | undefined)[]
  [label: string]: string | number | Date | (number | undefined)[] | undefined
}

const errorColor = 'var(--destructive)'
// Bots: a quiet grey next to the orange of people, so people stand out.
const botColor = 'color-mix(in oklch, var(--muted-foreground) 45%, transparent)'
// The slow answers in amber, next to the orange of the typical ones: never green, which reads as good.
const slowColor = 'var(--chart-3)'

interface Series {
  key: string
  label: string
  color: string
}

const series = computed((): Series[] => {
  switch (props.field) {
    case 'visitors':
      return [{ key: 'visitors', label: 'Visitors', color: 'var(--chart-1)' }]
    case 'requests':
      return [
        { key: 'people', label: 'People', color: 'var(--chart-1)' },
        { key: 'bots', label: 'Bots and scripts', color: botColor },
      ]
    case 'speed':
      return [
        { key: 'typical', label: 'Typical', color: 'var(--chart-1)' },
        { key: 'slowest', label: 'Slowest 1 in 20', color: slowColor },
      ]
    default:
      return [{ key: 'failed', label: 'Failed requests', color: errorColor }]
  }
})

// Tooltip rows: the series, plus page views next to visitors.
const config = computed((): ChartConfig => {
  const c: ChartConfig = {}
  for (const s of series.value) c[s.key] = { label: s.label, color: s.color }
  if (props.field === 'visitors') c.views = { label: 'Page views', color: 'transparent' }
  return c
})

const ms = (v: number | null) => (v === null ? undefined : v / 1000)

const data = computed<Datum[]>(() =>
  props.points.map((p) => {
    const date = new Date(p.ts)
    switch (props.field) {
      case 'visitors':
        return { date, top: p.visitors, ys: [p.visitors], visitors: formatCount(p.visitors), views: formatCount(p.page_views) }
      case 'requests': {
        const people = Math.max(0, p.requests - p.bots)
        return { date, top: p.requests, ys: [people, p.bots], people: formatCount(people), bots: formatCount(p.bots) }
      }
      case 'speed':
        return {
          date,
          top: ms(p.p95_ms) ?? 0,
          ys: [ms(p.median_ms), ms(p.p95_ms)],
          typical: p.median_ms === null ? 'No answers' : formatSeconds(p.median_ms),
          slowest: p.p95_ms === null ? undefined : formatSeconds(p.p95_ms),
        }
      default:
        return { date, top: p.errors, ys: [p.errors], failed: formatCount(p.errors) }
    }
  }),
)

const ys = computed(() => series.value.map((_, i) => (d: Datum) => d.ys[i]))
const colors = computed(() => series.value.map((s) => s.color))
const isLine = computed(() => props.field === 'speed')

const top = (d: Datum) => d.top
const maxY = computed(() => {
  const peak = Math.max(0, ...data.value.map(top))
  return isLine.value ? Math.max(0.1, peak * 1.15) : Math.max(4, peak)
})

// The newest bar (this hour, today) is still counting.
const lastTime = computed(() => data.value.at(-1)?.date.getTime())
const isCurrent = (ms: number) => ms === lastTime.value
const barStyle = (d: Datum) => (isCurrent(d.date.getTime()) ? { opacity: '0.45' } : {})

// Label whole bars, counted back from the newest: every 6th hour, or about 6 days.
const tickValues = computed(() => {
  const n = data.value.length
  const step = props.granularity === 'hour' ? 6 : Math.max(1, Math.ceil(n / 6))
  return data.value.filter((_, i) => (n - 1 - i) % step === 0).map((d) => d.date.getTime())
})

const x = (d: Datum) => d.date.getTime()

function fmtTick(ms: number): string {
  const d = new Date(ms)
  return props.granularity === 'hour'
    ? d.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' })
    : d.toLocaleDateString(undefined, { month: 'short', day: 'numeric', timeZone: 'UTC' }) // days are UTC midnights
}

function fmtY(t: number): string {
  if (isLine.value) return t === 0 ? '0' : `${Math.round(t * 100) / 100} s`
  return Number.isInteger(t) ? formatCount(t) : ''
}

const template = computed(() =>
  componentToString(config.value, ChartTooltipContent, {
    // Room between a label and its number.
    class: 'min-w-40 [&_.justify-between]:gap-4',
    labelFormatter: (d: number | Date) => {
      const date = d instanceof Date ? d : new Date(d)
      const label =
        props.granularity === 'hour'
          ? date.toLocaleString(undefined, { weekday: 'short', hour: '2-digit', minute: '2-digit' })
          : date.toLocaleDateString(undefined, { weekday: 'short', month: 'short', day: 'numeric', timeZone: 'UTC' })
      return isCurrent(date.getTime()) ? `${label} (so far)` : label
    },
  }),
)
</script>

<template>
  <div>
    <ChartContainer :config="config" class="aspect-auto! h-48 w-full" cursor>
      <VisXYContainer :data="data" :y-domain="[0, maxY]" :margin="{ top: 8, left: 4, right: 4 }">
        <template v-if="isLine">
          <VisLine v-for="(y, i) in ys" :key="series[i]!.key" :x="x" :y="y" :color="colors[i]" :line-width="2" :curve-type="CurveType.Linear" />
          <!-- A dot per point, so a lone hour between quiet ones still shows. -->
          <VisScatter v-for="(y, i) in ys" :key="`${series[i]!.key}-dot`" :x="x" :y="y" :color="colors[i]" :size="4" />
        </template>
        <VisStackedBar v-else :x="x" :y="ys" :color="colors" :bar-style="barStyle" :bar-padding="0.25" :rounded-corners="2" />
        <VisAxis type="x" :x="x" :tick-values="tickValues" :tick-format="fmtTick" :grid-line="false" />
        <VisAxis type="y" :num-ticks="4" :tick-format="fmtY" :domain-line="false" />
        <ChartCrosshair :x="x" :y="isLine ? ys : [top]" :template="template" :color="isLine ? colors : colors[0]" />
        <ChartTooltip />
      </VisXYContainer>
    </ChartContainer>
    <div v-if="series.length > 1" class="mt-2 flex flex-wrap items-center gap-x-4 gap-y-1 text-xs text-muted-foreground">
      <span v-for="s in series" :key="s.key" class="flex items-center gap-1.5">
        <span :class="isLine ? 'h-0.5 w-3 rounded-full' : 'size-2.5 rounded-xs'" :style="{ background: s.color }" aria-hidden="true" />{{ s.label }}
      </span>
    </div>
  </div>
</template>
