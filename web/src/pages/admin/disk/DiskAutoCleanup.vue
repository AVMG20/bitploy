<script setup lang="ts">
// The automatic cleanup: on or off, the threshold, and its last runs
// (PATCH /admin/disk/auto).
import { computed, ref, watch } from 'vue'
import { useMutation, useQueryClient } from '@tanstack/vue-query'
import RelativeTime from '@/components/RelativeTime.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Switch } from '@/components/ui/switch'
import { keys } from '@/composables/queries'
import { api } from '@/lib/api'
import { STEP_NAMES } from '@/lib/disk'
import { formatBytes } from '@/lib/format'
import { toastError, toastOk } from '@/lib/notify'
import type { DiskAutoCleanup, DiskCleanupRun, DiskReport } from '@/lib/types'

const MIN = 2
const MAX = 50
const BUILDS_MIN = 1
const CACHE_DAYS_MAX = 90
const MARGIN = 2

const props = defineProps<{ auto: DiskAutoCleanup }>()
const qc = useQueryClient()

const percent = ref(props.auto.min_free_percent)
const builds = ref(props.auto.builds_min_free_percent)
const cacheDays = ref(props.auto.build_cache_days)
watch(
  () => [props.auto.min_free_percent, props.auto.builds_min_free_percent, props.auto.build_cache_days],
  ([p, b, d]) => {
    percent.value = p
    builds.value = b
    cacheDays.value = d
  },
)
const inRange = (v: number, min: number, max = MAX) => Number.isInteger(v) && v >= min && v <= max
const valid = computed(() => inRange(percent.value, MIN) && inRange(builds.value, BUILDS_MIN) && inRange(cacheDays.value, 0, CACHE_DAYS_MAX))
const dirty = computed(
  () =>
    percent.value !== props.auto.min_free_percent ||
    builds.value !== props.auto.builds_min_free_percent ||
    cacheDays.value !== props.auto.build_cache_days,
)

type Input = { enabled?: boolean; min_free_percent?: number; builds_min_free_percent?: number; other_images?: boolean; build_cache_days?: number }
const save = useMutation({
  mutationFn: (input: Input) => api.updateAdminDiskAuto(input),
  onSuccess: (auto, input) => {
    qc.setQueryData<DiskReport>(keys.adminDisk, (old) => (old ? { ...old, auto, builds_paused_percent: auto.builds_min_free_percent } : old))
    void qc.invalidateQueries({ queryKey: keys.adminServer })
    if (input.enabled !== undefined) toastOk(auto.enabled ? 'Automatic cleanup is on' : 'Automatic cleanup is off')
    else if (input.other_images !== undefined) toastOk(auto.other_images ? 'Images that are not bitploy’s may go too' : 'Images that are not bitploy’s stay')
    else toastOk('Disk settings saved')
  },
  onError: (e) => toastError(e),
})
function submit() {
  if (valid.value && dirty.value)
    save.mutate({ min_free_percent: percent.value, builds_min_free_percent: builds.value, build_cache_days: cacheDays.value })
}

const stepsText = computed(() =>
  (['build_cache', 'unused_images', 'stopped_containers', 'rollback_images', 'other_images'] as const)
    .filter((k) => k !== 'other_images' || props.auto.other_images)
    .map((k) => STEP_NAMES[k])
    .join(', '),
)
/** Builds pause before the full cleanup starts: in between only the cheap steps run. */
const gap = computed(() => props.auto.enabled && props.auto.min_free_percent < props.auto.builds_min_free_percent)

const RESULTS: Record<DiskCleanupRun['result'], { label: string; dot: string }> = {
  recovered: { label: 'Freed enough', dot: 'bg-success' },
  still_low: { label: 'Still low', dot: 'bg-warning' },
  failed: { label: 'Failed', dot: 'bg-destructive' },
}
function trigger(r: DiskCleanupRun): string {
  return r.trigger === 'build_refused' ? 'A build was refused for disk space' : `Free space fell to ${r.free_before_percent.toFixed(1)}%`
}
function stepLine(r: DiskCleanupRun): string {
  return r.steps
    .filter((s) => s.freed_bytes > 0 || s.removed > 0 || s.error)
    .map((s) => `${STEP_NAMES[s.kind]} ${s.error ? '(failed)' : formatBytes(s.freed_bytes)}`)
    .join(' · ')
}
</script>

<template>
  <Card>
    <CardHeader class="flex flex-row items-start justify-between gap-4">
      <div class="grid gap-1">
        <CardTitle class="flex items-center gap-2">
          Automatic cleanup
          <Badge v-if="auto.dev" variant="outline">Off in dev mode</Badge>
        </CardTitle>
        <p class="text-sm text-muted-foreground">
          Below {{ auto.min_free_percent }}% free, bitploy deletes {{ stepsText }}, in that order, until {{ auto.min_free_percent + MARGIN }}% is free. Volumes and running
          containers stay. Admins get a notification.
        </p>
      </div>
      <Switch
        :model-value="auto.enabled"
        :disabled="save.isPending.value"
        aria-label="Automatic cleanup"
        data-testid="disk-auto-switch"
        @update:model-value="(v: boolean) => save.mutate({ enabled: v })"
      />
    </CardHeader>
    <CardContent class="grid gap-6">
      <form class="grid gap-3" @submit.prevent="submit">
        <div class="flex flex-wrap items-center gap-3">
          <label for="disk-builds" class="w-56 text-sm">Pause builds, uploads and backups below</label>
          <div class="inline-flex items-center rounded-md border shadow-xs focus-within:ring-[3px] focus-within:ring-ring/50">
            <input
              id="disk-builds"
              v-model.number="builds"
              type="number"
              inputmode="numeric"
              :min="BUILDS_MIN"
              :max="MAX"
              data-testid="disk-builds-threshold"
              class="h-9 w-14 bg-transparent px-2 text-right text-sm tabular-nums outline-none [appearance:textfield] [&::-webkit-inner-spin-button]:appearance-none"
            />
            <span class="pr-3 text-sm text-muted-foreground">% free</span>
          </div>
          <span v-if="!inRange(builds, BUILDS_MIN)" class="text-xs text-destructive">Between {{ BUILDS_MIN }} and {{ MAX }}%.</span>
        </div>
        <div class="flex flex-wrap items-center gap-3">
          <label for="disk-threshold" class="w-56 text-sm">Clean up automatically below</label>
          <div class="inline-flex items-center rounded-md border shadow-xs focus-within:ring-[3px] focus-within:ring-ring/50">
            <input
              id="disk-threshold"
              v-model.number="percent"
              type="number"
              inputmode="numeric"
              :min="MIN"
              :max="MAX"
              class="h-9 w-14 bg-transparent px-2 text-right text-sm tabular-nums outline-none [appearance:textfield] [&::-webkit-inner-spin-button]:appearance-none"
            />
            <span class="pr-3 text-sm text-muted-foreground">% free</span>
          </div>
          <span v-if="!inRange(percent, MIN)" class="text-xs text-destructive">Between {{ MIN }} and {{ MAX }}%.</span>
        </div>
        <div class="flex flex-wrap items-center gap-3">
          <label for="disk-cache-days" class="w-56 text-sm">Delete build cache not used for</label>
          <div class="inline-flex items-center rounded-md border shadow-xs focus-within:ring-[3px] focus-within:ring-ring/50">
            <input
              id="disk-cache-days"
              v-model.number="cacheDays"
              type="number"
              inputmode="numeric"
              min="0"
              :max="CACHE_DAYS_MAX"
              data-testid="disk-cache-days"
              class="h-9 w-14 bg-transparent px-2 text-right text-sm tabular-nums outline-none [appearance:textfield] [&::-webkit-inner-spin-button]:appearance-none"
            />
            <span class="pr-3 text-sm text-muted-foreground">days</span>
          </div>
          <span v-if="!inRange(cacheDays, 0, CACHE_DAYS_MAX)" class="text-xs text-destructive">Between 0 and {{ CACHE_DAYS_MAX }} days.</span>
          <span v-else-if="cacheDays === 0" class="text-xs text-muted-foreground">Never; only when the disk runs low.</span>
        </div>
        <div v-if="dirty"><Button type="submit" size="sm" :disabled="!valid || save.isPending.value">Save</Button></div>
        <p v-if="gap" class="text-xs text-muted-foreground">
          Between {{ auto.min_free_percent }}% and {{ auto.builds_min_free_percent }}% free, builds are paused while bitploy only deletes build cache and unused
          images. Older app versions and other images go below {{ auto.min_free_percent }}%.
        </p>
        <p class="text-xs text-muted-foreground">On a large disk a lower percentage still leaves plenty of room: 5% of 2 TB is 100 GB.</p>
      </form>

      <label class="flex items-center justify-between gap-4 rounded-lg border px-3 py-2.5">
        <span class="grid gap-0.5">
          <span class="text-sm font-medium">Also delete images that are not bitploy’s</span>
          <span class="text-xs text-muted-foreground">The last step: unused images other software on this server pulled. Turn off if you run more than bitploy here.</span>
        </span>
        <Switch
          :model-value="auto.other_images"
          :disabled="save.isPending.value || !auto.enabled"
          aria-label="Also delete images that are not bitploy’s"
          data-testid="disk-auto-other-images"
          @update:model-value="(v: boolean) => save.mutate({ other_images: v })"
        />
      </label>

      <div class="grid gap-2">
        <p class="text-xs font-medium tracking-wide text-muted-foreground uppercase">Last runs</p>
        <p v-if="auto.runs.length === 0" class="text-sm text-muted-foreground">It has not run yet.</p>
        <ul v-else class="grid divide-y rounded-lg border">
          <li v-for="r in auto.runs" :key="r.started_at" class="grid gap-1 px-3 py-2.5 text-sm">
            <div class="flex flex-wrap items-center gap-x-3 gap-y-1">
              <span class="inline-flex items-center gap-1.5 font-medium">
                <span class="size-2 rounded-full" :class="RESULTS[r.result].dot" />
                {{ RESULTS[r.result].label }}
              </span>
              <span class="tabular-nums">Freed {{ formatBytes(r.freed_bytes) }}</span>
              <span class="text-muted-foreground tabular-nums">{{ r.free_before_percent.toFixed(1) }}% → {{ r.free_after_percent.toFixed(1) }}% free</span>
              <RelativeTime :date="r.finished_at" class="ml-auto text-xs text-muted-foreground" />
            </div>
            <p class="text-xs text-muted-foreground">
              {{ trigger(r) }}<template v-if="stepLine(r)"> · {{ stepLine(r) }}</template>
            </p>
          </li>
        </ul>
      </div>
    </CardContent>
  </Card>
</template>
