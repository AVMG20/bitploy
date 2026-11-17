<script setup lang="ts">
// "Your server": the limits in one plain sentence, and for each of memory, CPU,
// storage and apps what is in use, what the apps have allocated and the most
// that may be allocated (what the four big usage cards used to show).
import { computed } from 'vue'
import { Infinity as InfinityIcon } from 'lucide-vue-next'
import MeterBar from '@/components/shell/MeterBar.vue'
import { Skeleton } from '@/components/ui/skeleton'
import { CPU_HINT, cpuFromPercent, formatCPU, formatCPUMillis, formatMB } from '@/lib/format'
import { usageBar } from '@/lib/usage'
import type { UsageBar } from '@/lib/usage'
import { useServerUsage } from './useServerUsage'

const emit = defineEmits<{ storage: [] }>()
const { me, usage, quota, unlimited, storageTaken, storageUsed } = useServerUsage()

interface Row {
  key: string
  label: string
  inUse: string
  allocated: string
  max: string
  bar: UsageBar
  pct: number
  hint?: string
}
const rows = computed<Row[]>(() => {
  const u = usage.value
  const q = quota.value
  if (!u || !q) return []
  const row = (key: string, label: string, used: number, allocated: number, max: number | null, f: (n: number) => string, hint?: string): Row => {
    const hasMax = max !== null && max > 0
    return {
      key,
      label,
      inUse: f(used),
      allocated: f(allocated),
      max: hasMax ? f(max) : '',
      bar: usageBar(used, allocated, max),
      pct: hasMax ? Math.min(100, (allocated / max) * 100) : 0,
      hint,
    }
  }
  const n = (x: number) => String(x)
  return [
    row('memory', 'Memory', u.memory_used_mb, u.memory_reserved_mb, unlimited.value ? null : q.memory_mb, formatMB),
    row('cpu', 'CPU', cpuFromPercent(u.cpu_now_percent) * 1000, u.cpu_reserved_millis, unlimited.value ? null : q.cpu_millis, formatCPUMillis, CPU_HINT),
    row('storage', 'Storage', storageUsed.value, storageTaken.value, q.volume_mb, formatMB),
    row('apps', 'Apps', u.apps_running, u.apps, q.apps, n),
  ]
})
// "Apps" counts running ones as in use; its numbers are plain counts.
const sentence = computed(() => {
  const q = quota.value
  if (!q) return ''
  if (unlimited.value) return `You may run ${q.apps} apps. Memory and CPU have no limit for you. Plain websites are free.`
  return `You may run ${q.apps} apps that reserve up to ${formatMB(q.memory_mb)} memory and ${formatCPU(q.cpu_millis / 1000)} together. Plain websites are free.`
})
const deploysText = computed(() => {
  const n = usage.value?.deploys_last_hour
  if (n === undefined) return ''
  return `${n} ${n === 1 ? 'deploy' : 'deploys'} in the last hour.`
})
const intro = computed(() => [sentence.value, deploysText.value].filter(Boolean).join(' '))
</script>

<template>
  <section class="rounded-xl border bg-card p-4 shadow-card" aria-label="Your server" data-testid="server-card">
    <div class="flex flex-wrap items-baseline justify-between gap-x-4 gap-y-1">
      <h2 class="text-sm font-semibold">Your server</h2>
      <p v-if="rows.length" class="text-xs text-muted-foreground">
        {{ intro }}
      </p>
    </div>
    <Skeleton v-if="me.isPending.value" class="mt-4 h-16 w-full" />
    <template v-else-if="rows.length">
      <ul class="mt-4 grid gap-x-8 gap-y-4 sm:grid-cols-2 lg:grid-cols-4">
        <li v-for="r in rows" :key="r.key" class="grid gap-1.5">
          <component :is="r.key === 'storage' ? 'button' : 'div'" :type="r.key === 'storage' ? 'button' : undefined" :class="['flex items-baseline justify-between gap-2 text-left text-sm', r.key === 'storage' && 'hover:underline']" :title="r.key === 'storage' ? 'See storage per app' : r.hint" @click="r.key === 'storage' && emit('storage')">
            <span class="font-medium">{{ r.label }}</span>
            <span class="font-mono text-xs text-muted-foreground tabular-nums">{{ r.inUse }} in use</span>
          </component>
          <MeterBar v-if="r.max" :pct="r.pct" :level="r.bar.level" :label="`${r.label}: ${r.allocated} allocated of ${r.max}`" />
          <p class="flex flex-wrap gap-x-3 text-xs text-muted-foreground">
            <span><span class="font-medium text-foreground tabular-nums">{{ r.allocated }}</span> allocated</span>
            <span v-if="r.max"><span class="font-medium text-foreground tabular-nums">{{ r.max }}</span> max</span>
            <span v-else class="inline-flex items-center gap-1"><InfinityIcon class="size-3" aria-hidden="true" />No limit</span>
          </p>
        </li>
      </ul>
    </template>
  </section>
</template>
