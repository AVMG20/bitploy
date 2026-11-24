<script setup lang="ts">
import { computed, onBeforeUnmount, ref } from 'vue'
import { cn } from '@/lib/utils'

// A small interactive area/line chart of one value over time, with a crosshair,
// active dot and floating readout of the point under the pointer. Teleports its
// tooltip to body so cards or tables with overflow clipping never cut it off.
const props = defineProps<{
  values: number[]
  /** CSS colour of the line, like var(--chart-1). */
  color: string
  /** Top of the scale (the limit); the peak when missing. */
  max?: number
  /** Formats a value for the readout. */
  format: (v: number, index: number) => string
  /** Optional prefix label (e.g. hour timestamp) shown beside the formatted value on hover. */
  labelAt?: (index: number) => string
  /** Says what the line is, for screen readers. */
  label: string
  /** Optional height/sizing classes for the SVG (defaults to h-7). */
  svgClass?: string
}>()

const W = 120
const H = 28
const top = computed(() => {
  const peak = Math.max(0, ...props.values)
  return Math.max(props.max && props.max > 0 ? props.max : peak * 1.2, peak, 1e-9)
})
const pts = computed(() => {
  const n = props.values.length
  return props.values.map((v, i) => [n > 1 ? (i / (n - 1)) * W : W / 2, H - 1.5 - (Math.max(0, v) / top.value) * (H - 3)] as const)
})
const line = computed(() => pts.value.map(([x, y], i) => `${i ? 'L' : 'M'}${x.toFixed(2)},${y.toFixed(2)}`).join(''))
const area = computed(() => (pts.value.length > 1 ? `${line.value}L${W},${H}L0,${H}Z` : ''))

const hover = ref<number | null>(null)
const tipPos = ref<{ x: number; y: number; below: boolean } | null>(null)
let listening = false

function setListeners(on: boolean) {
  if (typeof window === 'undefined' || on === listening) return
  listening = on
  if (on) {
    window.addEventListener('scroll', clearHover, { capture: true, passive: true })
    window.addEventListener('resize', clearHover, { passive: true })
  } else {
    window.removeEventListener('scroll', clearHover, true)
    window.removeEventListener('resize', clearHover)
  }
}

function clearHover() {
  hover.value = null
  tipPos.value = null
  setListeners(false)
}

function updateFromPointer(e: PointerEvent) {
  const el = e.currentTarget as SVGElement
  const r = el.getBoundingClientRect()
  const n = props.values.length
  if (!n || r.width <= 0) return
  const idx = Math.min(n - 1, Math.max(0, Math.round(((e.clientX - r.left) / r.width) * (n - 1))))
  hover.value = idx
  const pt = pts.value[idx]
  const rawX = pt ? r.left + (pt[0] / W) * r.width : e.clientX
  const pad = 48
  const clampedX = typeof window !== 'undefined' ? Math.min(window.innerWidth - pad, Math.max(pad, rawX)) : rawX
  const below = r.top < 36
  tipPos.value = { x: clampedX, y: below ? r.bottom + 6 : r.top - 6, below }
  setListeners(true)
}

onBeforeUnmount(() => setListeners(false))

const hoverPt = computed(() => (hover.value !== null ? pts.value[hover.value] : undefined))
const hoverPrefix = computed(() => (hover.value !== null ? props.labelAt?.(hover.value) ?? '' : ''))
const last = computed(() => props.values[props.values.length - 1])
</script>

<template>
  <div class="relative">
    <svg
      :viewBox="`0 0 ${W} ${H}`"
      preserveAspectRatio="none"
      :class="cn('block w-full cursor-crosshair overflow-visible select-none', svgClass ?? 'h-7')"
      role="img"
      :aria-label="last !== undefined ? `${label}, now ${format(last, values.length - 1)}` : label"
      @pointermove="updateFromPointer"
      @pointerdown="updateFromPointer"
      @pointerleave="clearHover"
      @pointercancel="clearHover"
    >
      <!-- No readings (not running, or just started): a quiet baseline keeps the card's shape. -->
      <line v-if="pts.length < 2" x1="0" :x2="W" :y1="H - 1.5" :y2="H - 1.5" stroke="currentColor" stroke-opacity="0.2" stroke-dasharray="3 3" vector-effect="non-scaling-stroke" />
      <path v-if="area" :d="area" :fill="color" fill-opacity="0.14" />
      <path v-if="pts.length > 1" :d="line" fill="none" :stroke="color" stroke-width="1.75" stroke-linejoin="round" stroke-linecap="round" vector-effect="non-scaling-stroke" />
      <line v-if="hoverPt" :x1="hoverPt[0]" :x2="hoverPt[0]" y1="0" :y2="H" stroke="currentColor" stroke-opacity="0.32" vector-effect="non-scaling-stroke" />
    </svg>
    <span
      v-if="hoverPt && pts.length > 1"
      aria-hidden="true"
      class="pointer-events-none absolute size-1.5 -translate-x-1/2 -translate-y-1/2 rounded-full ring-2 ring-card"
      :style="{ left: `${(hoverPt[0] / W) * 100}%`, top: `${(hoverPt[1] / H) * 100}%`, background: color }"
    />
    <Teleport to="body">
      <span
        v-if="hover !== null && values[hover] !== undefined && tipPos"
        :class="cn(
          'pointer-events-none fixed z-50 -translate-x-1/2 whitespace-nowrap rounded-md border bg-popover px-1.5 py-0.5 font-mono text-[10px] leading-tight text-popover-foreground tabular-nums shadow-md',
          tipPos.below ? '' : '-translate-y-full',
        )"
        :style="{ left: `${tipPos.x}px`, top: `${tipPos.y}px` }"
      >
        <span v-if="hoverPrefix" class="mr-1.5 text-muted-foreground">{{ hoverPrefix }}</span>
        <span class="font-medium">{{ format(values[hover]!, hover) }}</span>
      </span>
    </Teleport>
  </div>
</template>
