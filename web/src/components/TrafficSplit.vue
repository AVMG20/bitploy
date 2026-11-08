<script setup lang="ts">
import { Info } from 'lucide-vue-next'
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip'
import { compactCount, formatCount } from '@/lib/format'
import { share, shortShare } from '@/lib/traffic'

export interface SplitPart {
  /** In full, for screen readers and on hover: "Bots and scripts". */
  label: string
  /** In the one-line summary, after the number: "bots". */
  short: string
  count: number
  /** A CSS colour for the bar segment and the summary dot. */
  color: string
}

export interface Split {
  /** One word: "Sent by", "Answers". */
  label: string
  /** What the parts mean, on demand behind the label. */
  hint: string
  /** The first part is the one you hope for; the summary gives its share. */
  parts: SplitPart[]
}

// Each whole split into a few parts, one compact row each: a label, a thin bar
// and a one-line summary like ">99% people · 3 bots".
defineProps<{ rows: Split[] }>()

const total = (s: Split) => s.parts.reduce((n, p) => n + p.count, 0)
// Segments narrower than this would be invisible; the summary has the numbers.
const width = (p: SplitPart, whole: number) => (whole > 0 && p.count > 0 ? Math.max(1, (p.count / whole) * 100) : 0)
</script>

<template>
  <ul class="grid gap-y-4 sm:grid-cols-[auto_minmax(4rem,1fr)_auto] sm:gap-x-5 sm:gap-y-3">
    <li v-for="s in rows" :key="s.label" class="grid grid-cols-[auto_minmax(0,1fr)] items-start gap-x-3 gap-y-1.5 sm:col-span-3 sm:grid-cols-subgrid">
      <Tooltip>
        <TooltipTrigger as-child>
          <button
            type="button"
            class="col-start-1 row-start-1 inline-flex cursor-help items-center gap-1 justify-self-start rounded-sm text-sm font-medium outline-none focus-visible:ring-2 focus-visible:ring-ring sm:self-center"
          >
            {{ s.label }}
            <Info class="size-3.5 text-muted-foreground" aria-hidden="true" />
          </button>
        </TooltipTrigger>
        <TooltipContent side="top" align="start">{{ s.hint }}</TooltipContent>
      </Tooltip>

      <div class="col-span-2 row-start-2 flex h-1.5 gap-0.5 self-center overflow-hidden rounded-full bg-muted sm:col-span-1 sm:col-start-2 sm:row-start-1" aria-hidden="true">
        <span v-for="p in s.parts" v-show="p.count > 0" :key="p.label" class="h-full" :style="{ width: `${width(p, total(s))}%`, background: p.color }" />
      </div>

      <p class="col-start-2 row-start-1 flex flex-wrap items-center justify-end gap-x-3 gap-y-0.5 self-center text-xs text-muted-foreground sm:col-start-3 sm:justify-start">
        <span
          v-for="(p, i) in s.parts"
          :key="p.label"
          class="inline-flex items-center gap-1 whitespace-nowrap"
          :title="`${p.label}: ${formatCount(p.count)} (${share(p.count, total(s))})`"
        >
          <span v-if="p.count > 0" class="size-2 shrink-0 rounded-full" :style="{ background: p.color }" aria-hidden="true" />
          <span aria-hidden="true">
            <span :class="['tabular-nums', p.count > 0 && 'font-medium text-foreground']">{{ i === 0 ? shortShare(p.count, total(s)) : compactCount(p.count) }}</span>
            {{ p.short }}
          </span>
          <span class="sr-only">{{ p.label }}: {{ formatCount(p.count) }} ({{ share(p.count, total(s)) }}){{ i < s.parts.length - 1 ? ',' : '' }}</span>
        </span>
      </p>
    </li>
  </ul>
</template>
