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

const props = defineProps<{
  points: TrafficPoint[]
  field: 'visitors' | 'requests'
  granularity: 'hour' | 'day'
}>()

interface Datum {
  date: Date
  visitors: number
  requests: number
}

const labels = { visitors: 'Visitors', requests: 'Requests' }
const config = computed<ChartConfig>(() => ({ [props.field]: { label: labels[props.field], color: 'var(--chart-1)' } }))

const data = computed<Datum[]>(() =>
  props.points.map((p) => ({ date: new Date(p.ts), visitors: p.visitors, requests: p.requests })),
)

const maxY = computed(() => Math.max(4, ...data.value.map((d) => d[props.field])))

// Label whole bars, counted back from the newest: every 6th hour, or about 6 days.
const tickValues = computed(() => {
  const n = data.value.length
  const step = props.granularity === 'hour' ? 6 : Math.max(1, Math.ceil(n / 6))
  return data.value.filter((_, i) => (n - 1 - i) % step === 0).map((d) => d.date.getTime())
})

const x = (d: Datum) => d.date.getTime()
const y = (d: Datum) => d[props.field]

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
      return props.granularity === 'hour'
        ? date.toLocaleString(undefined, { weekday: 'short', hour: '2-digit', minute: '2-digit' })
        : date.toLocaleDateString(undefined, { weekday: 'short', month: 'short', day: 'numeric', timeZone: 'UTC' })
    },
  }),
)
</script>

<template>
  <ChartContainer :config="config" class="aspect-auto! h-48 w-full" cursor>
    <VisXYContainer :data="data" :y-domain="[0, maxY]" :margin="{ top: 8, left: 4, right: 4 }">
      <VisStackedBar :x="x" :y="[y]" color="var(--chart-1)" :bar-padding="0.25" :rounded-corners="2" />
      <VisAxis type="x" :x="x" :tick-values="tickValues" :tick-format="fmtTick" :grid-line="false" />
      <VisAxis type="y" :num-ticks="4" :tick-format="(t: number) => (Number.isInteger(t) ? `${t}` : '')" :domain-line="false" />
      <ChartCrosshair :x="x" :y="[y]" :template="template" color="var(--chart-1)" />
      <ChartTooltip />
    </VisXYContainer>
  </ChartContainer>
</template>
