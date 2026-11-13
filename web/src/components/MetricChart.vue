<script setup lang="ts">
import { computed } from 'vue'
import { AxisType } from '@unovis/ts'
import { VisArea, VisAxis, VisLine, VisPlotband, VisXYContainer } from '@unovis/vue'
import {
  ChartContainer,
  ChartCrosshair,
  ChartTooltip,
  ChartTooltipContent,
  componentToString,
} from '@/components/ui/chart'
import type { ChartConfig } from '@/components/ui/chart'
import { bandsAt, bandTooltipLines, buildBands, escapeHtml } from '@/lib/builds'
import type { BuildRecord, MetricPoint, MetricWindow } from '@/lib/types'

const props = defineProps<{
  /** App metrics, or the server's history (the same fields). */
  points: Pick<MetricPoint, 'ts' | 'cpu' | 'memory_mb'>[]
  field: 'cpu' | 'memory_mb'
  label: string
  unit: string
  window: MetricWindow
  /** Upper bound for the y axis (the app's limit, or the server's RAM), in the scaled unit. */
  limit?: number
  /** Multiplies the value, e.g. 0.01 to show CPU (sent as % of one core) in CPUs, or 1/1024 to show memory in GB. */
  scale?: number
  color: string
  /** Builds to shade behind the line (Admin > Server); the crosshair tooltip names the builds at its time. */
  builds?: BuildRecord[] | null
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
    [props.field]: props.field === 'cpu' ? Math.round(p.cpu * (props.scale ?? 1) * 1000) / 1000 : Math.round(p.memory_mb * (props.scale ?? 1) * 100) / 100,
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

const valueTooltip = componentToString(config.value, ChartTooltipContent, {
  labelFormatter: (d: number | Date) => (d instanceof Date ? d : new Date(d)).toLocaleString(),
})

// Builds as faint bands over the time of the chart's points; a failed one is tinted red.
const bands = computed(() => {
  const d = data.value
  if (!props.builds?.length || d.length < 2) return []
  return buildBands(props.builds, [d[0]!.date.getTime(), d[d.length - 1]!.date.getTime()])
})
// The crosshair snaps to the nearest point, so a build between two points counts for both.
const snap = computed(() => {
  const d = data.value
  return d.length < 2 ? 0 : (d[d.length - 1]!.date.getTime() - d[0]!.date.getTime()) / (d.length - 1) / 2
})
function template(d: Datum, x: number | Date): string {
  const html = valueTooltip?.(d, x) ?? ''
  if (!bands.value.length) return html
  const hits = bandsAt(bands.value, x instanceof Date ? x.getTime() : x, snap.value)
  if (!hits.length) return html
  const lines = bandTooltipLines(hits)
    .map((l) => `<div class="text-muted-foreground">${escapeHtml(l)}</div>`)
    .join('')
  const end = html.lastIndexOf('</div>') // inside the tooltip's box
  const extra = `<div class="grid gap-1 border-t pt-1.5">${lines}</div>`
  return end < 0 ? html + extra : html.slice(0, end) + extra + html.slice(end)
}
</script>

<template>
  <ChartContainer :config="config" class="aspect-auto! h-48 w-full" cursor>
    <VisXYContainer :data="data" :y-domain="[0, maxY]" :margin="{ top: 8, left: 4, right: 4 }">
      <VisPlotband
        v-for="b in bands"
        :key="b.id"
        :axis="AxisType.X"
        :from="b.from"
        :to="b.to"
        :color="b.failed ? 'var(--build-band-failed)' : 'var(--build-band)'"
        :duration="0"
      />
      <VisArea :x="x" :y="y" :color="`var(--color-${field})`" :opacity="0.15" />
      <VisLine :x="x" :y="y" :color="`var(--color-${field})`" />
      <VisAxis type="x" :x="x" :num-ticks="4" :tick-format="(t: number) => fmtTime(t)" :grid-line="false" />
      <VisAxis type="y" :num-ticks="4" :tick-format="(t: number) => `${field === 'cpu' ? Math.round(t * 100) / 100 : Math.round(t * 10) / 10}`" :domain-line="false" />
      <ChartCrosshair :x="x" :y="[y]" :template="template" :color="`var(--color-${field})`" />
      <ChartTooltip />
    </VisXYContainer>
  </ChartContainer>
</template>
