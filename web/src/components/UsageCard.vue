<script setup lang="ts">
import { computed } from 'vue'
import { Infinity as InfinityIcon } from 'lucide-vue-next'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'
import { usageBar } from '@/lib/usage'

// One resource on three levels: what the apps use right now (the solid part of
// the bar and the big number), what they have allocated (the light part) and
// the most that may be allocated (the whole bar), or "No limit".
const props = withDefaults(
  defineProps<{
    title: string
    loading?: boolean
    hint?: string
    /** The big number: what is in use, like "964 MB". */
    value?: string
    /** After the big number, like "in use" or "running". */
    valueLabel?: string
    used: number
    allocated: number
    /** null or 0: no max. */
    max: number | null
    /** Like "6.0 GB". */
    allocatedText?: string
    allocatedLabel?: string
    /** Like "8.0 GB"; left out when there is no max. */
    maxText?: string
    alert?: string
  }>(),
  { valueLabel: 'in use', allocatedLabel: 'allocated' },
)

const bar = computed(() => usageBar(props.used, props.allocated, props.max))
const hasMax = computed(() => props.max !== null && props.max > 0)
const tone = computed(() => ({ ok: 'bg-primary', warn: 'bg-warning', full: 'bg-destructive', over: 'bg-destructive' })[bar.value.level])
const toneLight = computed(() => ({ ok: 'bg-primary/30', warn: 'bg-warning/35', full: 'bg-destructive/30', over: 'bg-destructive/30' })[bar.value.level])
// The bar in words; the numbers themselves are in the text around it.
const barText = computed(() => {
  const share = (n: number, of: number) => `${of > 0 ? Math.round((n / of) * 100) : 0}%`
  if (!hasMax.value) return `${share(props.used, props.allocated)} of the allocation in use, no limit`
  return `${share(props.allocated, props.max!)} of the max allocated, ${share(props.used, props.max!)} in use`
})
</script>

<template>
  <Card>
    <CardHeader class="pb-1">
      <CardTitle class="text-xs font-medium tracking-wide text-muted-foreground uppercase" :title="hint">{{ title }}</CardTitle>
    </CardHeader>
    <CardContent class="grid gap-2">
      <template v-if="loading">
        <Skeleton class="h-8 w-24" />
        <Skeleton class="h-1.5 w-full" />
        <Skeleton class="h-3 w-32" />
      </template>
      <template v-else>
        <div class="flex flex-wrap items-baseline gap-x-1.5">
          <span class="text-2xl leading-tight font-semibold tracking-tight tabular-nums sm:text-[1.65rem]">{{ value }}</span>
          <span class="inline-flex items-center gap-1.5 text-sm whitespace-nowrap text-muted-foreground">
            <span :class="['size-2 shrink-0 rounded-[2px]', tone]" aria-hidden="true" />{{ valueLabel }}
          </span>
        </div>
        <div role="img" :aria-label="barText" class="relative h-1.5 w-full overflow-hidden rounded-full bg-muted">
          <div :class="['absolute inset-y-0 left-0 rounded-full transition-[width]', toneLight]" :style="{ width: `${bar.allocatedPct}%` }" />
          <div :class="['absolute inset-y-0 left-0 rounded-full transition-[width]', tone]" :style="{ width: `${bar.usedPct}%` }" />
          <div v-if="bar.maxMarkPct !== null" class="absolute inset-y-0 w-0.5 -translate-x-1/2 bg-foreground" :style="{ left: `${bar.maxMarkPct}%` }" />
        </div>
        <ul class="flex flex-wrap gap-x-3 gap-y-1 text-xs text-muted-foreground">
          <li class="inline-flex items-center gap-1.5 whitespace-nowrap">
            <span :class="['size-2 shrink-0 rounded-[2px]', toneLight]" aria-hidden="true" />
            <span class="font-medium text-foreground tabular-nums">{{ allocatedText }}</span>{{ allocatedLabel }}
          </li>
          <li v-if="hasMax" class="inline-flex items-center gap-1.5 whitespace-nowrap">
            <span class="size-2 shrink-0 rounded-[2px] bg-muted ring-1 ring-foreground/20 ring-inset" aria-hidden="true" />
            <span class="font-medium text-foreground tabular-nums">{{ maxText }}</span>max
          </li>
          <li v-else class="inline-flex items-center gap-1 whitespace-nowrap"><InfinityIcon class="size-3" aria-hidden="true" />No limit</li>
        </ul>
        <p v-if="alert" class="text-xs font-medium text-destructive" role="status">{{ alert }}</p>
      </template>
    </CardContent>
  </Card>
</template>
