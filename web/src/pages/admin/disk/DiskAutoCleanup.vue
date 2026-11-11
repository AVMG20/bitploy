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
const MARGIN = 2

const props = defineProps<{ auto: DiskAutoCleanup }>()
const qc = useQueryClient()

const percent = ref(props.auto.min_free_percent)
watch(
  () => props.auto.min_free_percent,
  (p) => (percent.value = p),
)
const valid = computed(() => Number.isInteger(percent.value) && percent.value >= MIN && percent.value <= MAX)
const dirty = computed(() => percent.value !== props.auto.min_free_percent)

const save = useMutation({
  mutationFn: (input: { enabled?: boolean; min_free_percent?: number }) => api.updateAdminDiskAuto(input),
  onSuccess: (auto, input) => {
    qc.setQueryData<DiskReport>(keys.adminDisk, (old) => (old ? { ...old, auto } : old))
    if (input.enabled === undefined) toastOk(`Cleanup starts below ${auto.min_free_percent}% free`)
    else toastOk(auto.enabled ? 'Automatic cleanup is on' : 'Automatic cleanup is off')
  },
  onError: (e) => toastError(e),
})

const order = ['build_cache', 'unused_images', 'stopped_containers', 'rollback_images', 'other_images'] as const
const stepsText = order.map((k) => STEP_NAMES[k]).join(', ')

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
      <form class="flex flex-wrap items-center gap-3" @submit.prevent="valid && dirty && save.mutate({ min_free_percent: percent })">
        <label for="disk-threshold" class="text-sm">Clean up below</label>
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
        <Button v-if="dirty" type="submit" size="sm" :disabled="!valid || save.isPending.value">Save</Button>
        <span v-if="dirty && !valid" class="text-xs text-destructive">Between {{ MIN }} and {{ MAX }}%.</span>
      </form>

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
