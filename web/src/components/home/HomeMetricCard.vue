<script setup lang="ts">
// Left card of the Home "At a glance" row: a selectable 24-hour fleet chart
// (Visitors by default, switchable to Requests, Page views, Errors, Memory or
// CPU) with a top-3 breakdown underneath.
import { computed } from 'vue'
import { RouterLink } from 'vue-router'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Skeleton } from '@/components/ui/skeleton'
import { projectRoute } from '@/lib/appLinks'
import { HOME_METRICS, isHomeMetric } from '@/lib/home'
import type { HomeMetric, HomeMetricSummary } from '@/lib/home'
import { cn } from '@/lib/utils'

const props = defineProps<{
  summary: HomeMetricSummary
  loading?: boolean
}>()

const metric = defineModel<HomeMetric>('metric', { required: true })

function onMetricChange(v: unknown) {
  if (isHomeMetric(v)) metric.value = v
}

const maxBucket = computed(() => Math.max(0, ...props.summary.buckets.map((b) => b.value)))
const nonZeroBuckets = computed(() => props.summary.buckets.map((b) => b.value).filter((v) => v > 0))
const minBucket = computed(() => (nonZeroBuckets.value.length ? Math.min(...nonZeroBuckets.value) : 0))
const maxTop = computed(() => Math.max(1, ...props.summary.top.map((t) => t.value)))

function isPeakBar(v: number): boolean {
  if (v <= 0 || maxBucket.value <= 0) return false
  const spread = maxBucket.value - minBucket.value
  // Flat series (e.g. steady RAM): only highlight bars when there is a meaningful rise.
  if (spread < maxBucket.value * 0.12) return false
  const floor = props.summary.meta.kind === 'resource' ? minBucket.value : 0
  return v >= floor + (maxBucket.value - floor) * 0.72
}

function barHeightPct(v: number): number {
  if (maxBucket.value <= 0 || v <= 0) return 5
  return Math.max(8, Math.round((v / maxBucket.value) * 100))
}

function axisTime(ts: string | undefined): string {
  if (!ts) return ''
  const d = new Date(ts)
  if (Number.isNaN(d.getTime())) return ''
  return d.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit', hour12: false })
}

const axisLabels = computed(() => {
  const b = props.summary.buckets
  if (b.length < 3) return null
  return {
    start: axisTime(b[0]?.ts),
    mid: axisTime(b[Math.floor(b.length / 2)]?.ts),
    end: 'Now',
  }
})

const BADGE_CLASS = {
  good: 'bg-success/12 text-success',
  bad: 'bg-destructive/12 text-destructive',
  neutral: 'bg-muted text-muted-foreground',
} as const
</script>

<template>
  <section class="flex h-full flex-col overflow-hidden rounded-xl border bg-card shadow-card" aria-label="Fleet metrics" data-testid="home-metric-card">
    <div class="flex items-center justify-between gap-2 px-4 pt-3 pb-1.5">
      <Select :model-value="metric" @update:model-value="onMetricChange">
        <SelectTrigger
          size="sm"
          aria-label="Choose overview metric"
          class="-ml-2 h-7 border-transparent bg-transparent px-2 text-sm font-semibold shadow-none hover:bg-muted/60 dark:bg-transparent dark:hover:bg-muted/60"
        >
          <SelectValue />
        </SelectTrigger>
        <SelectContent position="popper" align="start">
          <SelectItem v-for="m in HOME_METRICS" :key="m.key" :value="m.key">{{ m.label }}</SelectItem>
        </SelectContent>
      </Select>
      <span class="font-mono text-[11px] text-muted-foreground">24h</span>
    </div>

    <div v-if="loading" class="grid flex-1 gap-3 px-4 pt-1 pb-4">
      <Skeleton class="h-7 w-28" />
      <Skeleton class="h-16 w-full" />
      <Skeleton class="h-12 w-full" />
    </div>

    <template v-else>
      <div class="flex flex-wrap items-baseline gap-2 px-4 pb-2" :title="summary.totalTitle">
        <span class="text-2xl font-semibold tracking-tight tabular-nums">{{ summary.totalDisplay }}</span>
        <span class="text-xs text-muted-foreground">{{ summary.meta.heroUnit }}</span>
        <span
          v-if="summary.badge"
          :class="cn('rounded-full px-2 py-0.5 text-[11px] font-medium tabular-nums', BADGE_CLASS[summary.badge.tone])"
          :title="summary.badge.title"
        >
          {{ summary.badge.text }}
        </span>
      </div>

      <div class="mt-auto px-4 pb-3">
        <div
          v-if="summary.buckets.length"
          class="flex h-[68px] items-end gap-[3px]"
          role="img"
          :aria-label="`${summary.meta.label} over the last 24 hours`"
        >
          <div
            v-for="b in summary.buckets"
            :key="b.ts"
            :title="b.label"
            :style="{ height: `${barHeightPct(b.value)}%` }"
            :class="cn(
              'min-h-[3px] flex-1 rounded-t-[2px] transition-colors',
              b.value <= 0
                ? 'bg-muted'
                : summary.tone === 'danger'
                  ? isPeakBar(b.value) ? 'bg-destructive hover:bg-destructive/80' : 'bg-destructive/45 hover:bg-destructive/75'
                  : isPeakBar(b.value) ? 'bg-primary hover:bg-primary/85' : 'bg-primary/40 hover:bg-primary/75',
            )"
          />
        </div>
        <div v-else class="flex h-[68px] items-center text-xs text-muted-foreground">
          {{ summary.emptyText }}
        </div>
        <div v-if="axisLabels" class="mt-1.5 flex justify-between font-mono text-[10px] text-muted-foreground/75">
          <span>{{ axisLabels.start }}</span>
          <span>{{ axisLabels.mid }}</span>
          <span>{{ axisLabels.end }}</span>
        </div>
      </div>

      <div class="border-t px-4 py-2.5">
        <ul v-if="summary.top.length" class="grid gap-1.5">
          <li v-for="item in summary.top" :key="item.projectId">
            <RouterLink
              :to="projectRoute(item.projectId)"
              class="group flex items-center gap-2.5 text-xs transition-colors"
            >
              <span class="min-w-0 flex-1 truncate font-mono text-foreground/90 group-hover:text-foreground group-hover:underline">
                {{ summary.meta.kind === 'traffic' ? item.subtitle : item.name }}
              </span>
              <span class="h-1 w-14 shrink-0 overflow-hidden rounded-full bg-muted">
                <span
                  :class="cn('block h-full rounded-full', summary.tone === 'danger' ? 'bg-destructive' : 'bg-primary')"
                  :style="{ width: `${Math.max(8, Math.round((item.value / maxTop) * 100))}%` }"
                />
              </span>
              <span class="min-w-14 shrink-0 text-right font-mono text-muted-foreground tabular-nums">{{ item.display }}</span>
            </RouterLink>
          </li>
        </ul>
        <p v-else class="text-xs text-muted-foreground">{{ summary.emptyText }}</p>
      </div>
    </template>
  </section>
</template>
