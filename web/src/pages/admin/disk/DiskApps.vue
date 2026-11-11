<script setup lang="ts">
// What every app takes: its live images, older versions kept for a
// rollback, its volumes and files its containers wrote. Largest first.
import { computed, ref } from 'vue'
import { RouterLink } from 'vue-router'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { appPath } from '@/lib/appLinks'
import { formatBytes } from '@/lib/format'
import type { DiskAppUsage } from '@/lib/types'

const LIMIT = 8
const props = defineProps<{ apps: DiskAppUsage[] }>()

const all = ref(false)
const rows = computed(() => (all.value ? props.apps : props.apps.slice(0, LIMIT)))
const max = computed(() => Math.max(1, ...props.apps.map((a) => a.total_bytes)))

const PARTS = [
  { key: 'image_bytes', label: 'Live version', color: 'var(--chart-1)' },
  { key: 'rollback_bytes', label: 'Rollback versions', color: 'color-mix(in oklch, var(--chart-1) 55%, transparent)' },
  { key: 'volume_bytes', label: 'Volumes', color: 'var(--chart-2)' },
  { key: 'container_bytes', label: 'Container files', color: 'color-mix(in oklch, var(--chart-5) 50%, transparent)' },
] as const

function detail(a: DiskAppUsage): string {
  const out: string[] = []
  if (a.volume_bytes > 0) out.push(`volumes ${formatBytes(a.volume_bytes)}`)
  if (a.rollback_count > 0) out.push(`${a.rollback_count} older ${a.rollback_count === 1 ? 'version' : 'versions'} ${formatBytes(a.rollback_bytes)}`)
  if (a.container_bytes > 0) out.push(`container files ${formatBytes(a.container_bytes)}`)
  return out.join(' · ')
}
</script>

<template>
  <Card>
    <CardHeader>
      <CardTitle>By app</CardTitle>
      <CardDescription>Layers apps share with other images are not counted.</CardDescription>
    </CardHeader>
    <CardContent class="grid gap-4">
      <p v-if="apps.length === 0" class="py-6 text-center text-sm text-muted-foreground">No app takes measurable space yet.</p>
      <ul v-else class="grid gap-3.5">
        <li v-for="a in rows" :key="a.app.id" class="grid gap-1.5" :data-testid="`disk-app-${a.app.slug}`">
          <div class="flex items-baseline gap-3">
            <RouterLink :to="appPath(a.app.slug)" class="min-w-0 truncate text-sm font-medium hover:underline">{{ a.app.name || a.app.slug }}</RouterLink>
            <span class="min-w-0 flex-1 truncate text-xs text-muted-foreground">{{ detail(a) }}</span>
            <span class="text-sm font-medium tabular-nums">{{ formatBytes(a.total_bytes) }}</span>
          </div>
          <div class="flex h-2 overflow-hidden rounded-full bg-muted">
            <div class="flex h-full gap-px" :style="{ width: `${(a.total_bytes / max) * 100}%` }">
              <div
                v-for="p in PARTS"
                v-show="a[p.key] > 0"
                :key="p.key"
                class="h-full first:rounded-l-full last:rounded-r-full"
                :style="{ flexGrow: a[p.key], background: p.color }"
                :title="`${p.label}: ${formatBytes(a[p.key])}`"
              />
            </div>
          </div>
        </li>
      </ul>
      <div class="flex flex-wrap items-center justify-between gap-3">
        <p class="flex flex-wrap gap-x-4 gap-y-1 text-xs text-muted-foreground">
          <span v-for="p in PARTS" :key="p.key" class="inline-flex items-center gap-1.5">
            <span class="size-2 rounded-[3px]" :style="{ background: p.color }" />{{ p.label }}
          </span>
        </p>
        <Button v-if="apps.length > LIMIT" variant="ghost" size="sm" @click="all = !all">{{ all ? 'Show fewer' : `Show all ${apps.length}` }}</Button>
      </div>
    </CardContent>
  </Card>
</template>
