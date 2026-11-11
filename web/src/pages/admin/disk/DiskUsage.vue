<script setup lang="ts">
// The filesystem at a glance: free space (it glides to a new value after a
// cleanup), one bar split into where the used space goes, and a legend.
// Hovering a legend row or a part of the bar lights that part up.
import { computed, ref } from 'vue'
import { useTween } from '@/composables/useTween'
import { SEGMENTS, share } from '@/lib/disk'
import { formatBytes } from '@/lib/format'
import type { DiskSegment, DiskSegmentKey } from '@/lib/types'
import { cn } from '@/lib/utils'

const props = defineProps<{
  totalBytes: number
  freeBytes: number
  segments: DiskSegment[]
  /** Below this much free (%) the automatic cleanup runs; null when it is off. */
  autoPercent: number | null
  pausedPercent: number
  /** Below this much free (%) admins get a warning; null when it is off. */
  warnPercent: number | null
  /** Just freed by a cleanup: shown next to the free space for a moment. */
  freed: number
}>()

const free = useTween(() => props.freeBytes)
const freePct = computed(() => share(free.value, props.totalBytes))
const tone = computed(() => {
  const p = share(props.freeBytes, props.totalBytes)
  if (p < props.pausedPercent) return 'text-destructive'
  if (p < (props.warnPercent ?? 20)) return 'text-warning-text'
  return 'text-foreground'
})

// When a cleanup freed space before the next measurement, the parts shrink
// together so the bar still adds up.
const parts = computed(() => {
  const used = Math.max(0, props.totalBytes - props.freeBytes)
  const sum = props.segments.reduce((n, s) => n + s.bytes, 0)
  const scale = sum > used && sum > 0 ? used / sum : 1
  return props.segments.map((s) => ({ ...s, ...SEGMENTS[s.key], width: share(s.bytes * scale, props.totalBytes) }))
})

const active = ref<DiskSegmentKey | 'free' | null>(null)
const dim = (key: DiskSegmentKey | 'free') => active.value !== null && active.value !== key

const markers = computed(() => {
  const out: { at: number; label: string; paused?: boolean }[] = []
  if (props.warnPercent !== null) out.push({ at: 100 - props.warnPercent, label: `Warn admins below ${props.warnPercent}%` })
  out.push({ at: 100 - props.pausedPercent, label: `Pause builds below ${props.pausedPercent}%`, paused: true })
  if (props.autoPercent !== null) out.push({ at: 100 - props.autoPercent, label: `Clean up below ${props.autoPercent}%` })
  return out
})
</script>

<template>
  <div class="grid gap-5">
    <div class="flex flex-wrap items-end justify-between gap-x-6 gap-y-2">
      <div class="min-w-0">
        <p class="text-xs font-medium tracking-wide text-muted-foreground uppercase">Free</p>
        <div class="flex flex-wrap items-baseline gap-x-2 gap-y-1">
          <span class="text-4xl font-semibold tracking-tight tabular-nums" :class="tone" data-testid="disk-free">{{ formatBytes(free) }}</span>
          <span class="text-sm text-muted-foreground">of {{ formatBytes(totalBytes) }}</span>
          <Transition name="disk-freed">
            <span v-if="freed > 0" :key="freed" class="rounded-full bg-success/15 px-2 py-0.5 text-sm font-medium text-success tabular-nums">+{{ formatBytes(freed) }}</span>
          </Transition>
        </div>
      </div>
      <p class="text-right">
        <span class="text-4xl font-semibold tracking-tight tabular-nums" :class="tone">{{ freePct.toFixed(freePct < 10 ? 1 : 0) }}</span>
        <span class="text-lg font-medium text-muted-foreground">% free</span>
      </p>
    </div>

    <div class="relative">
      <div
        class="flex h-3.5 w-full gap-px overflow-hidden rounded-full bg-muted"
        role="img"
        :aria-label="`${formatBytes(totalBytes - freeBytes)} used of ${formatBytes(totalBytes)}`"
        @mouseleave="active = null"
      >
        <div
          v-for="p in parts"
          :key="p.key"
          class="h-full shrink-0 transition-[width,opacity] duration-700 ease-out first:rounded-l-full"
          :class="dim(p.key) && 'opacity-25'"
          :style="{ width: `${p.width}%`, background: p.color }"
          :title="`${p.label}: ${formatBytes(p.bytes)}`"
          @mouseenter="active = p.key"
        />
      </div>
      <span
        v-for="m in markers"
        :key="m.label"
        class="pointer-events-none absolute -top-1 -bottom-1 w-0.5 -translate-x-1/2 rounded-full bg-foreground/60"
        :style="{ left: `${m.at}%` }"
        :title="m.label"
      />
    </div>

    <ul class="grid gap-x-8 gap-y-0.5 sm:grid-cols-2 lg:grid-cols-3" @mouseleave="active = null">
      <li v-for="p in parts" :key="p.key">
        <div class="flex w-full items-center gap-2.5 rounded-md px-2 py-1.5 text-sm transition-opacity hover:bg-muted/50" :class="dim(p.key) && 'opacity-40'" @mouseenter="active = p.key">
          <span class="size-2.5 shrink-0 rounded-[3px]" :style="{ background: p.color }" />
          <span class="min-w-0 flex-1 truncate">{{ p.label }}</span>
          <span class="font-medium tabular-nums">{{ formatBytes(p.bytes) }}</span>
        </div>
      </li>
      <li>
        <div
          class="flex w-full items-center gap-2.5 rounded-md px-2 py-1.5 text-sm transition-opacity"
          :class="dim('free') && 'opacity-40'"
          @mouseenter="active = 'free'"
        >
          <span class="size-2.5 shrink-0 rounded-[3px] border bg-muted" />
          <span class="min-w-0 flex-1 truncate">Free</span>
          <span class="font-medium tabular-nums">{{ formatBytes(freeBytes) }}</span>
        </div>
      </li>
    </ul>

    <p class="flex flex-wrap gap-x-5 gap-y-1 text-xs text-muted-foreground">
      <span v-for="m in markers" :key="m.label" class="inline-flex items-center gap-1.5">
        <span class="h-3 w-0.5 rounded-full bg-foreground/60" />
        <span :class="cn(m.paused && share(freeBytes, totalBytes) < pausedPercent && 'font-medium text-destructive')">{{ m.label }}</span>
      </span>
    </p>
  </div>
</template>

<style scoped>
.disk-freed-enter-active {
  transition:
    opacity 300ms ease-out,
    transform 300ms ease-out;
}
.disk-freed-leave-active {
  transition:
    opacity 600ms ease-in,
    transform 600ms ease-in;
}
.disk-freed-enter-from {
  opacity: 0;
  transform: translateY(6px);
}
.disk-freed-leave-to {
  opacity: 0;
  transform: translateY(-8px);
}
</style>
