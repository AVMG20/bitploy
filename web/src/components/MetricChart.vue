<script setup lang="ts">
import { computed } from 'vue'
import { VisArea, VisAxis, VisLine, VisXYContainer } from '@unovis/vue'
import {
  ChartContainer,
  ChartCrosshair,
  ChartTooltip,
  ChartTooltipContent,
  componentToString,
} from '@/components/ui/chart'
import type { ChartConfig } from '@/components/ui/chart'
import type { MetricPoint, MetricWindow } from '@/lib/types'

const props = defineProps<{
  points: MetricPoint[]
  field: 'cpu' | 'memory_mb'
  label: string
  unit: string
  window: MetricWindow
  /** Upper bound for the y axis (the app's limit). */
  limit?: number
  color: string
}>()

interface Datum {
  date: Date
  [key: string]: number | Date
}

const config = computed<ChartConfig>(() => ({
  [props.field]: { label: `${props.label} (${props.unit})`, color: props.color },
}))

const data = computed<Datum[]>(() =>
  props.points.map((p) => ({
    date: new Date(p.ts),
    [props.field]: Math.round((props.field === 'cpu' ? p.cpu : p.memory_mb) * 10) / 10,
  })),
)

const maxY = computed(() => {
  const peak = data.value.reduce((m, d) => Math.max(m, d[props.field] as number), 0)
  const top = props.limit && props.limit > 0 ? Math.max(props.limit, peak) : peak * 1.2
  return top > 0 ? top : 1
})

const x = (d: Datum) => d.date.getTime()
const y = (d: Datum) => d[props.field] as number

function fmtTime(ms: number | Date): string {
  const d = ms instanceof Date ? ms : new Date(ms)
  return props.window === '7d'
    ? d.toLocaleDateString(undefined, { weekday: 'short', day: 'numeric' })
    : d.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' })
}

const template = componentToString(config.value, ChartTooltipContent, {
  labelFormatter: (d: number | Date) => (d instanceof Date ? d : new Date(d)).toLocaleString(),
})
</script>

<template>
  <ChartContainer :config="config" class="aspect-auto! h-48 w-full" cursor>
    <VisXYContainer :data="data" :y-domain="[0, maxY]" :margin="{ top: 8, left: 4, right: 4 }">
      <VisArea :x="x" :y="y" :color="`var(--color-${field})`" :opacity="0.15" />
      <VisLine :x="x" :y="y" :color="`var(--color-${field})`" />
      <VisAxis type="x" :x="x" :num-ticks="4" :tick-format="(t: number) => fmtTime(t)" :grid-line="false" />
      <VisAxis type="y" :num-ticks="4" :tick-format="(t: number) => `${Math.round(t * 10) / 10}`" :domain-line="false" />
      <ChartCrosshair :x="x" :y="[y]" :template="template" :color="`var(--color-${field})`" />
      <ChartTooltip />
    </VisXYContainer>
  </ChartContainer>
</template>
