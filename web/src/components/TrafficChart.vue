<script setup lang="ts">
import { computed } from 'vue'
import { VisAxis, VisStackedBar, VisXYContainer } from '@unovis/vue'
import {
  ChartContainer,
  ChartCrosshair,
  ChartTooltip,
  ChartTooltipContent,
  componentToString,
} from '@/components/ui/chart'
import type { ChartConfig } from '@/components/ui/chart'
import type { TrafficPoint } from '@/lib/types'

export type TrafficField = 'visitors' | 'requests' | 'errors'

const props = defineProps<{
  points: TrafficPoint[]
  field: TrafficField
  granularity: 'hour' | 'day'
}>()

interface Datum {
  date: Date
  visitors: number
  requests: number
  errors: number
  ok: number // requests without the errors: the lower part of a requests bar
}

const errorColor = 'var(--destructive)'
// The failed part of a requests bar: red pulled towards the text colour, so it
// differs from the orange in lightness too, and from the dimmed newest bar.
const failedPart = 'color-mix(in oklch, var(--destructive) 70%, var(--foreground))'

// Datum keys in the config show in the tooltip, in datum order.
const config = computed((): ChartConfig => {
  switch (props.field) {
    case 'visitors':
      return { visitors: { label: 'Visitors', color: 'var(--chart-1)' } }
    case 'requests':
      return { requests: { label: 'Requests', color: 'var(--chart-1)' }, errors: { label: 'Failed (5xx)', color: failedPart } }
    default:
      return { errors: { label: 'Failed (5xx)', color: errorColor } }
  }
})

const data = computed<Datum[]>(() =>
  props.points.map((p) => ({
    date: new Date(p.ts),
    visitors: p.visitors,
    requests: p.requests,
    errors: p.errors,
    ok: Math.max(0, p.requests - p.errors),
  })),
)

const ys = computed<((d: Datum) => number)[]>(() => {
  switch (props.field) {
    case 'visitors':
      return [(d) => d.visitors]
    case 'requests':
      return [(d) => d.ok, (d) => d.errors]
    default:
      return [(d) => d.errors]
  }
})
const colors = computed(() => {
  switch (props.field) {
    case 'visitors':
      return ['var(--chart-1)']
    case 'requests':
      return ['var(--chart-1)', failedPart]
    default:
      return [errorColor]
  }
})

// The top of each bar: the crosshair dot and the y scale.
const top = (d: Datum) => d[props.field]
const maxY = computed(() => Math.max(4, ...data.value.map(top)))

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

const template = computed(() =>
  componentToString(config.value, ChartTooltipContent, {
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
        <VisStackedBar :x="x" :y="ys" :color="colors" :bar-style="barStyle" :bar-padding="0.25" :rounded-corners="2" />
        <VisAxis type="x" :x="x" :tick-values="tickValues" :tick-format="fmtTick" :grid-line="false" />
        <VisAxis type="y" :num-ticks="4" :tick-format="(t: number) => (Number.isInteger(t) ? `${t}` : '')" :domain-line="false" />
        <ChartCrosshair :x="x" :y="[top]" :template="template" :color="colors[0]" />
        <ChartTooltip />
      </VisXYContainer>
    </ChartContainer>
    <div v-if="field === 'requests'" class="mt-2 flex items-center gap-4 text-xs text-muted-foreground">
      <span class="flex items-center gap-1.5"><span class="size-2.5 rounded-xs bg-chart-1" aria-hidden="true" />Requests</span>
      <span class="flex items-center gap-1.5"><span class="size-2.5 rounded-xs" :style="{ background: failedPart }" aria-hidden="true" />Failed (5xx)</span>
    </div>
  </div>
</template>
