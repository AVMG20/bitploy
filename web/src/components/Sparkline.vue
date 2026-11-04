<script setup lang="ts">
import { computed, ref } from 'vue'

// A small line of one value over time (a service card's CPU or memory), with
// a readout of the point under the pointer. One series, so no legend: the
// label next to it names it.
const props = defineProps<{
  values: number[]
  /** CSS colour of the line, like var(--chart-1). */
  color: string
  /** Top of the scale (the limit); the peak when missing. */
  max?: number
  /** Formats a value for the readout. */
  format: (v: number) => string
  /** Says what the line is, for screen readers. */
  label: string
}>()

const W = 120
const H = 28
const top = computed(() => {
  const peak = Math.max(0, ...props.values)
  return Math.max(props.max && props.max > 0 ? props.max : peak * 1.2, peak, 1e-9)
})
const pts = computed(() => {
  const n = props.values.length
  return props.values.map((v, i) => [n > 1 ? (i / (n - 1)) * W : W / 2, H - 1 - (Math.max(0, v) / top.value) * (H - 2)] as const)
})
const line = computed(() => pts.value.map(([x, y], i) => `${i ? 'L' : 'M'}${x.toFixed(2)},${y.toFixed(2)}`).join(''))
const area = computed(() => (pts.value.length > 1 ? `${line.value}L${W},${H}L0,${H}Z` : ''))

const hover = ref<number | null>(null)
function onMove(e: PointerEvent) {
  const el = e.currentTarget as SVGElement
  const r = el.getBoundingClientRect()
  const n = props.values.length
  if (!n || r.width <= 0) return
  hover.value = Math.min(n - 1, Math.max(0, Math.round(((e.clientX - r.left) / r.width) * (n - 1))))
}
const hoverPt = computed(() => (hover.value !== null ? pts.value[hover.value] : undefined))
const last = computed(() => props.values[props.values.length - 1])
</script>

<template>
  <div class="relative">
    <svg
      :viewBox="`0 0 ${W} ${H}`"
      preserveAspectRatio="none"
      class="block h-7 w-full overflow-visible"
      role="img"
      :aria-label="last !== undefined ? `${label}, now ${format(last)}` : label"
      @pointermove="onMove"
      @pointerleave="hover = null"
    >
      <!-- No readings (not running, or just started): a quiet baseline keeps the card's shape. -->
      <line v-if="pts.length < 2" x1="0" :x2="W" :y1="H - 1" :y2="H - 1" stroke="currentColor" stroke-opacity="0.2" stroke-dasharray="3 3" vector-effect="non-scaling-stroke" />
      <path v-if="area" :d="area" :fill="color" fill-opacity="0.12" />
      <path v-if="pts.length > 1" :d="line" fill="none" :stroke="color" stroke-width="2" stroke-linejoin="round" stroke-linecap="round" vector-effect="non-scaling-stroke" />
      <line v-if="hoverPt" :x1="hoverPt[0]" :x2="hoverPt[0]" y1="0" :y2="H" stroke="currentColor" stroke-opacity="0.25" vector-effect="non-scaling-stroke" />
    </svg>
    <span
      v-if="hover !== null && values[hover] !== undefined"
      class="pointer-events-none absolute -top-5 right-0 rounded border bg-popover px-1 font-mono text-[10px] text-popover-foreground tabular-nums shadow-sm"
    >{{ format(values[hover]!) }}</span>
  </div>
</template>
