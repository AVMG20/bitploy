<script setup lang="ts">
import { computed } from 'vue'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { formatCount } from '@/lib/format'

export interface TrafficRow {
  key: string
  label: string
  count: number
  /** Shown before the label, for example a flag. */
  prefix?: string
}

const props = defineProps<{ title: string; unit: string; rows: TrafficRow[]; empty: string }>()

const top = computed(() => props.rows.reduce((m, r) => Math.max(m, r.count), 0))
const width = (count: number) => (top.value > 0 ? Math.max(2, (count / top.value) * 100) : 0)
</script>

<template>
  <Card>
    <CardHeader class="flex flex-row items-baseline justify-between gap-2">
      <CardTitle>{{ title }}</CardTitle>
      <span class="text-xs text-muted-foreground">{{ unit }}</span>
    </CardHeader>
    <CardContent>
      <p v-if="rows.length === 0" class="py-6 text-center text-sm text-muted-foreground">{{ empty }}</p>
      <ul v-else class="grid grid-cols-[minmax(0,1fr)] gap-1">
        <li v-for="r in rows" :key="r.key" class="relative flex items-center justify-between gap-3 rounded-md px-2 py-1 text-sm">
          <span class="absolute inset-y-0 left-0 rounded-md bg-primary/10" :style="{ width: `${width(r.count)}%` }" aria-hidden="true" />
          <span class="relative flex min-w-0 items-center gap-2" :title="r.label">
            <span v-if="r.prefix" aria-hidden="true">{{ r.prefix }}</span>
            <span class="truncate">{{ r.label }}</span>
          </span>
          <span class="relative shrink-0 font-medium tabular-nums">{{ formatCount(r.count) }}</span>
        </li>
      </ul>
    </CardContent>
  </Card>
</template>
