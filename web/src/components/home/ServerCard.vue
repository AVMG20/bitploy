<script setup lang="ts">
// "Your server" on Home: memory, CPU, storage and apps as a 4-column segmented
// strip. Bars stay calm while there is plenty of room, turn orange/amber when
// nearing a limit, and red over it. Storage is interactive (opens per-app view).
import { computed } from 'vue'
import { ChevronDown } from 'lucide-vue-next'
import { Skeleton } from '@/components/ui/skeleton'
import { cpuFromPercent, formatCPUMillis, formatMB } from '@/lib/format'
import { usageBar } from '@/lib/usage'
import type { UsageBar } from '@/lib/usage'
import { cn } from '@/lib/utils'
import { useServerUsage } from './useServerUsage'

const emit = defineEmits<{ storage: [] }>()
const { me, usage, quota, unlimited, storageTaken, storageUsed } = useServerUsage()

interface Tile {
  key: string
  label: string
  main: string
  of: string
  pct: number
  pctLabel: string
  tone: 'calm' | 'near' | 'warn' | 'over'
  title: string
}

/** Splits "19.5 / 60.0 GB" into primary ("19.5") and denominator ("/ 60.0 GB"). */
function splitPair(a: number, b: number, f: (n: number) => string): { main: string; of: string } {
  const x = f(a)
  const y = f(b)
  const unit = (s: string) => s.match(/ (\D+)$/)?.[1]
  const u = unit(x)
  const left = u && u === unit(y) ? x.slice(0, -u.length - 1) : x
  return { main: left, of: `/ ${y}` }
}

function toneOf(pct: number, level: UsageBar['level']): Tile['tone'] {
  if (level === 'over') return 'over'
  if (level === 'warn' || level === 'full') return 'warn'
  if (pct >= 75) return 'near'
  return 'calm'
}

const tiles = computed<Tile[]>(() => {
  const u = usage.value
  const q = quota.value
  if (!u || !q) return []
  const pct = (n: number, of: number) => (of > 0 ? Math.min(100, (n / of) * 100) : 0)

  const limited = (key: string, label: string, used: number, allocated: number, max: number, f: (n: number) => string): Tile => {
    const p = pct(allocated, max)
    const bar = usageBar(used, allocated, max)
    const parts = splitPair(allocated, max, f)
    return {
      key,
      label,
      main: parts.main,
      of: parts.of,
      pct: p,
      pctLabel: `${Math.round(p)}%`,
      tone: toneOf(p, bar.level),
      title: `${f(allocated)} allocated of ${f(max)}, ${f(used)} in use`,
    }
  }

  const open = (key: string, label: string, used: number, allocated: number, f: (n: number) => string): Tile => {
    const p = pct(used, allocated)
    const parts = splitPair(used, allocated, f)
    return {
      key,
      label,
      main: parts.main,
      of: parts.of,
      pct: p,
      pctLabel: `${Math.round(p)}%`,
      tone: 'calm',
      title: `${f(used)} in use of ${f(allocated)} allocated. No limit.`,
    }
  }

  const cpuUsed = cpuFromPercent(u.cpu_now_percent) * 1000
  return [
    unlimited.value
      ? open('memory', 'Memory', u.memory_used_mb, u.memory_reserved_mb, formatMB)
      : limited('memory', 'Memory', u.memory_used_mb, u.memory_reserved_mb, q.memory_mb, formatMB),
    unlimited.value
      ? open('cpu', 'CPU', cpuUsed, u.cpu_reserved_millis, formatCPUMillis)
      : limited('cpu', 'CPU', cpuUsed, u.cpu_reserved_millis, q.cpu_millis, formatCPUMillis),
    q.volume_mb > 0
      ? limited('storage', 'Storage', storageUsed.value, storageTaken.value, q.volume_mb, formatMB)
      : open('storage', 'Storage', storageUsed.value, storageTaken.value, formatMB),
    { ...limited('apps', 'Apps', u.apps_running, u.apps, q.apps, String), title: `${u.apps} apps of ${q.apps}, ${u.apps_running} running` },
  ]
})

const BAR_CLASS: Record<Tile['tone'], string> = {
  calm: 'bg-muted-foreground/55',
  near: 'bg-primary',
  warn: 'bg-warning',
  over: 'bg-destructive',
}

const PCT_CLASS: Record<Tile['tone'], string> = {
  calm: 'text-muted-foreground',
  near: 'text-primary-text font-medium',
  warn: 'text-warning-text font-medium',
  over: 'text-destructive font-medium',
}
</script>

<template>
  <section class="overflow-hidden rounded-xl border bg-card shadow-card" aria-label="Your server" data-testid="server-card">
    <div v-if="me.isPending.value" class="p-4">
      <Skeleton class="h-12 w-full" />
    </div>
    <ul v-else-if="tiles.length" class="grid grid-cols-2 lg:grid-cols-4">
      <li
        v-for="(t, i) in tiles"
        :key="t.key"
        :class="[
          i % 2 === 1 && 'border-l',
          i >= 2 && 'border-t lg:border-t-0',
          i > 0 && 'lg:border-l',
        ]"
      >
        <component
          :is="t.key === 'storage' ? 'button' : 'div'"
          :type="t.key === 'storage' ? 'button' : undefined"
          :class="cn('grid w-full gap-2 px-4 py-3 text-left transition-colors', t.key === 'storage' && 'group cursor-pointer hover:bg-muted/45')"
          :title="t.key === 'storage' ? `${t.title}. Click to see storage per app.` : t.title"
          @click="t.key === 'storage' && emit('storage')"
        >
          <span class="flex items-center justify-between gap-2 text-xs">
            <span class="text-muted-foreground group-hover:text-foreground">{{ t.label }}</span>
            <span :class="cn('inline-flex items-center gap-0.5 font-mono text-[11px] tabular-nums', PCT_CLASS[t.tone])">
              {{ t.pctLabel }}
              <ChevronDown v-if="t.key === 'storage'" class="size-3 text-muted-foreground transition-transform group-hover:translate-y-0.5 group-hover:text-foreground" aria-hidden="true" />
            </span>
          </span>
          <span class="flex items-baseline gap-1 font-mono tabular-nums">
            <span class="text-[15px] font-semibold text-foreground">{{ t.main }}</span>
            <span class="truncate text-xs text-muted-foreground">{{ t.of }}</span>
          </span>
          <div role="img" :aria-label="`${t.label}: ${t.title}`" class="h-1 w-full overflow-hidden rounded-full bg-muted">
            <div :class="cn('h-full rounded-full transition-[width]', BAR_CLASS[t.tone])" :style="{ width: `${Math.max(t.pct > 0 ? 2 : 0, t.pct)}%` }" />
          </div>
        </component>
      </li>
    </ul>
  </section>
</template>
