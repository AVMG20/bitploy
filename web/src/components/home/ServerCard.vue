<script setup lang="ts">
// "Your server" on Home: memory, CPU, storage and apps, each one number and a
// bar. With a limit the bar is what the apps take of it; without one (an admin
// lets this person use as much as they like) it is what they use of what they
// reserve. The details are in each tile's tooltip.
import { computed } from 'vue'
import MeterBar from '@/components/shell/MeterBar.vue'
import { Skeleton } from '@/components/ui/skeleton'
import { cpuFromPercent, formatCPUMillis, formatMB } from '@/lib/format'
import { usageBar } from '@/lib/usage'
import type { UsageBar } from '@/lib/usage'
import { useServerUsage } from './useServerUsage'

const emit = defineEmits<{ storage: [] }>()
const { me, usage, quota, unlimited, storageTaken, storageUsed } = useServerUsage()

interface Tile {
  key: string
  label: string
  value: string
  pct: number
  level: UsageBar['level']
  title: string
}

/** "46.1 / 60.0 GB": the unit once when both sides share it. */
function pair(a: number, b: number, f: (n: number) => string): string {
  const x = f(a)
  const y = f(b)
  const unit = (s: string) => s.match(/ (\D+)$/)?.[1]
  return unit(x) && unit(x) === unit(y) ? `${x.slice(0, -unit(x)!.length - 1)} / ${y}` : `${x} / ${y}`
}

const tiles = computed<Tile[]>(() => {
  const u = usage.value
  const q = quota.value
  if (!u || !q) return []
  const pct = (n: number, of: number) => (of > 0 ? Math.min(100, (n / of) * 100) : 0)
  // Against a limit: allocated of max, amber near it and red over it.
  const limited = (key: string, label: string, used: number, allocated: number, max: number, f: (n: number) => string): Tile => ({
    key,
    label,
    value: pair(allocated, max, f),
    pct: pct(allocated, max),
    level: usageBar(used, allocated, max).level,
    title: `${f(allocated)} allocated of ${f(max)}, ${f(used)} in use`,
  })
  // No limit: in use of allocated, always calm.
  const open = (key: string, label: string, used: number, allocated: number, f: (n: number) => string): Tile => ({
    key,
    label,
    value: pair(used, allocated, f),
    pct: pct(used, allocated),
    level: 'ok',
    title: `${f(used)} in use of ${f(allocated)} allocated. No limit.`,
  })
  const cpuUsed = cpuFromPercent(u.cpu_now_percent) * 1000
  return [
    unlimited.value ? open('memory', 'Memory', u.memory_used_mb, u.memory_reserved_mb, formatMB) : limited('memory', 'Memory', u.memory_used_mb, u.memory_reserved_mb, q.memory_mb, formatMB),
    unlimited.value ? open('cpu', 'CPU', cpuUsed, u.cpu_reserved_millis, formatCPUMillis) : limited('cpu', 'CPU', cpuUsed, u.cpu_reserved_millis, q.cpu_millis, formatCPUMillis),
    q.volume_mb > 0 ? limited('storage', 'Storage', storageUsed.value, storageTaken.value, q.volume_mb, formatMB) : open('storage', 'Storage', storageUsed.value, storageTaken.value, formatMB),
    { ...limited('apps', 'Apps', u.apps_running, u.apps, q.apps, String), title: `${u.apps} apps of ${q.apps}, ${u.apps_running} running` },
  ]
})
</script>

<template>
  <section class="rounded-xl border bg-card px-4 py-3.5 shadow-card" aria-label="Your server" data-testid="server-card">
    <Skeleton v-if="me.isPending.value" class="h-12 w-full" />
    <ul v-else-if="tiles.length" class="grid grid-cols-2 gap-x-8 gap-y-4 lg:grid-cols-4">
      <li v-for="t in tiles" :key="t.key">
        <component
          :is="t.key === 'storage' ? 'button' : 'div'"
          :type="t.key === 'storage' ? 'button' : undefined"
          :class="['grid w-full gap-2 text-left', t.key === 'storage' && 'group cursor-pointer']"
          :title="t.key === 'storage' ? `${t.title}. See storage per app.` : t.title"
          @click="t.key === 'storage' && emit('storage')"
        >
          <span class="flex items-baseline justify-between gap-2">
            <span class="text-xs text-muted-foreground group-hover:text-foreground">{{ t.label }}</span>
            <span class="font-mono text-sm font-medium tabular-nums">{{ t.value }}</span>
          </span>
          <MeterBar :pct="t.pct" :level="t.level" :label="`${t.label}: ${t.title}`" />
        </component>
      </li>
    </ul>
  </section>
</template>
