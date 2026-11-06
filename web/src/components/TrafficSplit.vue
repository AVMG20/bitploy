<script setup lang="ts">
import { computed } from 'vue'
import { formatCount } from '@/lib/format'
import { share } from '@/lib/traffic'

export interface SplitPart {
  label: string
  count: number
  /** A CSS colour for the bar segment and the legend dot. */
  color: string
}

// One whole split into a few parts, as one bar with a legend underneath:
// "who sent the requests", "how the app answered".
const props = defineProps<{ title: string; hint?: string; parts: SplitPart[] }>()

const total = computed(() => props.parts.reduce((n, p) => n + p.count, 0))
// Segments narrower than this would be invisible; the legend has the numbers.
const width = (p: SplitPart) => (total.value > 0 && p.count > 0 ? Math.max(1, (p.count / total.value) * 100) : 0)
</script>

<template>
  <div class="grid gap-2">
    <div>
      <p class="text-sm font-medium">{{ title }}</p>
      <p v-if="hint" class="text-xs text-muted-foreground">{{ hint }}</p>
    </div>
    <div class="flex h-2.5 gap-0.5 overflow-hidden rounded-full bg-muted" aria-hidden="true">
      <span v-for="p in parts" v-show="p.count > 0" :key="p.label" class="h-full" :style="{ width: `${width(p)}%`, background: p.color }" />
    </div>
    <ul class="flex flex-wrap gap-x-4 gap-y-1 text-xs">
      <li v-for="p in parts" :key="p.label" class="flex items-center gap-1.5">
        <span class="size-2.5 shrink-0 rounded-xs" :style="{ background: p.color }" aria-hidden="true" />
        <span class="text-muted-foreground">{{ p.label }}</span>
        <span class="font-medium tabular-nums">{{ formatCount(p.count) }}</span>
        <span class="text-muted-foreground tabular-nums">({{ share(p.count, total) }})</span>
      </li>
    </ul>
  </div>
</template>
