<script setup lang="ts">
// The automatic cleanup: on or off, the steps it may take (in order, with
// what each would free now), running it by hand, and its last runs
// (PATCH /admin/disk/auto, POST /admin/disk/auto/run).
import { computed, ref } from 'vue'
import { useMutation, useQueryClient } from '@tanstack/vue-query'
import { Play } from 'lucide-vue-next'
import ConfirmDialog from '@/components/ConfirmDialog.vue'
import RelativeTime from '@/components/RelativeTime.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardAction, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Checkbox } from '@/components/ui/checkbox'
import { Switch } from '@/components/ui/switch'
import { keys } from '@/composables/queries'
import { api } from '@/lib/api'
import { CLEANUPS, STEP_NAMES } from '@/lib/disk'
import { formatBytes } from '@/lib/format'
import { toastError, toastOk } from '@/lib/notify'
import type { DiskAutoCleanup, DiskAutoInput, DiskAutoStep, DiskCleanupOption, DiskCleanupRun, DiskReport } from '@/lib/types'
import { cn } from '@/lib/utils'

const ORDER: DiskAutoStep[] = ['build_cache', 'unused_images', 'stopped_containers', 'rollback_images', 'other_images']
const MARGIN = 2

const props = defineProps<{ auto: DiskAutoCleanup; cleanups: DiskCleanupOption[]; freePercent: number }>()
const qc = useQueryClient()

const on = (k: DiskAutoStep) => props.auto.steps.includes(k)
const bytesOf = (k: DiskAutoStep) => Math.max(0, props.cleanups.find((c) => c.kind === k)?.bytes ?? 0)
const chosen = computed(() => ORDER.filter((k) => on(k) && !(k === 'other_images' && props.auto.dev)))
const chosenBytes = computed(() => chosen.value.reduce((n, k) => n + bytesOf(k), 0))

const save = useMutation({
  mutationFn: (input: DiskAutoInput) => api.updateAdminDiskAuto(input),
  onSuccess: (auto, input) => {
    qc.setQueryData<DiskReport>(keys.adminDisk, (old) => (old ? { ...old, auto } : old))
    if (input.enabled !== undefined) toastOk(auto.enabled ? 'Automatic cleanup is on' : 'Automatic cleanup is off')
  },
  onError: (e) => toastError(e),
})
function toggle(k: DiskAutoStep, v: boolean) {
  const next = ORDER.filter((x) => (x === k ? v : on(x)))
  if (next.length === 0) return
  save.mutate({ steps: next })
}

const confirmRun = ref(false)
const run = useMutation({
  mutationFn: () => api.runAdminDiskAuto(),
  onSuccess: async (r) => {
    confirmRun.value = false
    toastOk(r.freed_bytes > 0 ? `Freed ${formatBytes(r.freed_bytes)}` : 'Nothing to free', `${r.free_before_percent.toFixed(1)}% to ${r.free_after_percent.toFixed(1)}% free`)
    await qc.invalidateQueries({ queryKey: keys.adminDisk })
  },
  onError: (e) => toastError(e),
})
const runDestructive = computed(() => chosen.value.some((k) => CLEANUPS[k].destructive))

const RESULTS: Record<DiskCleanupRun['result'], { label: string; dot: string }> = {
  recovered: { label: 'Freed enough', dot: 'bg-success' },
  still_low: { label: 'Still low', dot: 'bg-warning' },
  failed: { label: 'Failed', dot: 'bg-destructive' },
}
function trigger(r: DiskCleanupRun): string {
  if (r.trigger === 'manual') return 'Run by an admin'
  if (r.trigger === 'build_refused') return 'A build was refused for disk space'
  return `Free space fell to ${r.free_before_percent.toFixed(1)}%`
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
    <CardHeader>
      <CardTitle class="flex items-center gap-2">
        Automatic cleanup
        <Badge v-if="auto.dev" variant="outline">Off in dev mode</Badge>
      </CardTitle>
      <CardDescription>
        Below {{ auto.min_free_percent }}% free, bitploy takes these steps in order and stops once {{ auto.min_free_percent + MARGIN }}% is free. Volumes and
        running containers are never touched.
      </CardDescription>
      <CardAction>
        <Switch
          :model-value="auto.enabled"
          :disabled="save.isPending.value"
          aria-label="Automatic cleanup"
          data-testid="disk-auto-switch"
          @update:model-value="(v: boolean) => save.mutate({ enabled: v })"
        />
      </CardAction>
    </CardHeader>
    <CardContent class="grid gap-6">
      <div class="grid gap-3">
        <ol class="grid divide-y rounded-lg border" :class="!auto.enabled && 'opacity-60'">
          <li v-for="(k, i) in ORDER" :key="k">
            <label class="flex cursor-pointer items-center gap-3 px-3 py-2.5" :data-testid="`disk-auto-step-${k}`">
              <span class="w-4 text-center text-xs text-muted-foreground tabular-nums">{{ i + 1 }}</span>
              <Checkbox
                :model-value="on(k)"
                :disabled="save.isPending.value || (on(k) && auto.steps.length === 1)"
                @update:model-value="(v) => toggle(k, v === true)"
              />
              <span class="grid min-w-0 flex-1 gap-0.5">
                <span :class="cn('text-sm font-medium', !on(k) && 'text-muted-foreground line-through decoration-muted-foreground/50')">
                  {{ CLEANUPS[k].title }}
                  <span v-if="CLEANUPS[k].destructive" class="ml-1 text-xs font-normal whitespace-nowrap text-muted-foreground">cannot be undone</span>
                </span>
                <span class="text-xs text-muted-foreground">{{ CLEANUPS[k].description }}</span>
              </span>
              <span v-if="k === 'other_images' && auto.dev" class="text-xs whitespace-nowrap text-muted-foreground">Off in dev mode</span>
              <span v-else class="text-sm whitespace-nowrap tabular-nums" :class="on(k) && bytesOf(k) > 0 ? 'font-medium' : 'text-muted-foreground'">{{ formatBytes(bytesOf(k)) }}</span>
            </label>
          </li>
        </ol>
        <div class="flex flex-wrap items-center justify-between gap-3">
          <p class="text-xs text-muted-foreground">
            The checked steps can free up to <span class="font-medium text-foreground tabular-nums">{{ formatBytes(chosenBytes) }}</span> now.
          </p>
          <Button size="sm" variant="outline" :disabled="run.isPending.value || chosen.length === 0" data-testid="disk-auto-run" @click="confirmRun = true">
            <Play /> {{ run.isPending.value ? 'Running...' : 'Run now' }}
          </Button>
        </div>
      </div>

      <div class="grid gap-2">
        <p class="text-xs font-medium tracking-wide text-muted-foreground uppercase">Last runs</p>
        <p v-if="auto.runs.length === 0" class="text-sm text-muted-foreground">No runs yet.</p>
        <ul v-else class="grid divide-y rounded-lg border">
          <li v-for="r in auto.runs" :key="r.started_at" class="grid gap-1 px-3 py-2.5 text-sm">
            <div class="flex flex-wrap items-center gap-x-3 gap-y-1">
              <span class="inline-flex items-center gap-1.5 font-medium">
                <span class="size-2 rounded-full" :class="RESULTS[r.result].dot" />
                {{ r.trigger === 'manual' ? 'Done' : RESULTS[r.result].label }}
              </span>
              <span class="tabular-nums">Freed {{ formatBytes(r.freed_bytes) }}</span>
              <span class="text-muted-foreground tabular-nums">{{ r.free_before_percent.toFixed(1) }}% to {{ r.free_after_percent.toFixed(1) }}% free</span>
              <RelativeTime :date="r.finished_at" class="ml-auto text-xs text-muted-foreground" />
            </div>
            <p class="text-xs text-muted-foreground">
              {{ trigger(r) }}<template v-if="stepLine(r)"> · {{ stepLine(r) }}</template>
            </p>
          </li>
        </ul>
      </div>
    </CardContent>

    <ConfirmDialog
      v-model:open="confirmRun"
      title="Run the automatic cleanup now?"
      :description="`Runs every checked step to the end, without stopping at ${auto.min_free_percent + MARGIN}% free. Now ${freePercent.toFixed(1)}% free.`"
      confirm-label="Run now"
      :destructive="runDestructive"
      :loading="run.isPending.value"
      @confirm="run.mutate()"
    >
      <ol class="grid gap-1 rounded-md border bg-muted/30 px-3 py-2 text-sm">
        <li v-for="(k, i) in chosen" :key="k" class="flex items-center gap-2">
          <span class="w-4 text-xs text-muted-foreground tabular-nums">{{ i + 1 }}</span>
          <span class="flex-1">{{ CLEANUPS[k].title }}</span>
          <span class="tabular-nums">{{ formatBytes(bytesOf(k)) }}</span>
        </li>
        <li class="mt-1 flex items-center justify-between border-t pt-1.5 font-medium">
          <span>About</span><span class="tabular-nums">{{ formatBytes(chosenBytes) }}</span>
        </li>
      </ol>
      <p v-if="on('rollback_images')" class="text-xs text-muted-foreground">Apps can no longer roll back to the versions this deletes.</p>
    </ConfirmDialog>
  </Card>
</template>
