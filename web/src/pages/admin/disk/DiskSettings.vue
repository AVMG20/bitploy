<script setup lang="ts">
// The disk page's limits: when admins are warned, when builds pause, when the
// automatic cleanup starts, how long build cache is kept and how many versions
// every app keeps (PATCH /admin/disk/auto).
import { computed, reactive, watch } from 'vue'
import { useMutation, useQueryClient } from '@tanstack/vue-query'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { keys } from '@/composables/queries'
import { api } from '@/lib/api'
import { toastError, toastOk } from '@/lib/notify'
import type { DiskAutoCleanup, DiskReport } from '@/lib/types'

const props = defineProps<{ auto: DiskAutoCleanup }>()
const qc = useQueryClient()

type Key = 'warn_free_percent' | 'builds_min_free_percent' | 'min_free_percent' | 'build_cache_days' | 'rollback_versions'
const FIELDS: { key: Key; label: string; unit: string; min: number; max: number; hint: (v: number) => string }[] = [
  {
    key: 'warn_free_percent',
    label: 'Warn admins below',
    unit: '% free',
    min: 0,
    max: 80,
    hint: (v) => (v === 0 ? 'Off.' : 'One notification, before anything is deleted.'),
  },
  { key: 'builds_min_free_percent', label: 'Pause builds below', unit: '% free', min: 1, max: 50, hint: () => 'Uploads and backups pause too. Apps keep running.' },
  {
    key: 'min_free_percent',
    label: 'Clean up below',
    unit: '% free',
    min: 2,
    max: 50,
    hint: () => (props.auto.enabled ? 'The automatic cleanup starts.' : 'The automatic cleanup is off.'),
  },
  {
    key: 'build_cache_days',
    label: 'Delete build cache unused for',
    unit: 'days',
    min: 0,
    max: 90,
    hint: (v) => (v === 0 ? 'Never; only when the disk runs low.' : 'Checked every hour.'),
  },
  {
    key: 'rollback_versions',
    label: 'Keep versions per app',
    unit: 'versions',
    min: 1,
    max: 10,
    hint: (v) => (v === 1 ? 'Only the live one: no rollback.' : `The live one and ${v - 1} to roll back to.`),
  },
]

const form = reactive({} as Record<Key, number>)
function reset() {
  for (const f of FIELDS) form[f.key] = props.auto[f.key]
}
reset()
// Compared by value: another card saving (or a refetch) hands over a new
// auto object, which must not wipe what is being typed here.
watch(() => FIELDS.map((f) => props.auto[f.key]).join(','), reset)

const inRange = (k: Key) => {
  const f = FIELDS.find((x) => x.key === k)!
  return Number.isInteger(form[k]) && form[k] >= f.min && form[k] <= f.max
}
const warnBelowCleanup = computed(() => form.warn_free_percent !== 0 && form.warn_free_percent <= form.min_free_percent)
const valid = computed(() => FIELDS.every((f) => inRange(f.key)) && !warnBelowCleanup.value)
const changed = computed(() => FIELDS.filter((f) => form[f.key] !== props.auto[f.key]))
/** Builds pause before the full cleanup starts: in between only the cheap steps run. */
const gap = computed(() => props.auto.enabled && props.auto.min_free_percent < props.auto.builds_min_free_percent)

const save = useMutation({
  mutationFn: () => api.updateAdminDiskAuto(Object.fromEntries(changed.value.map((f) => [f.key, form[f.key]]))),
  onSuccess: (auto) => {
    qc.setQueryData<DiskReport>(keys.adminDisk, (old) => (old ? { ...old, auto, builds_paused_percent: auto.builds_min_free_percent } : old))
    void qc.invalidateQueries({ queryKey: keys.adminServer })
    toastOk('Limits saved')
  },
  onError: (e) => toastError(e),
})
</script>

<template>
  <Card>
    <CardHeader>
      <CardTitle>Limits</CardTitle>
      <CardDescription>When bitploy warns, pauses and cleans up, and what it keeps.</CardDescription>
    </CardHeader>
    <CardContent>
      <form class="grid gap-4" @submit.prevent="valid && changed.length && save.mutate()">
        <div v-for="f in FIELDS" :key="f.key" class="grid gap-1">
          <div class="flex items-center justify-between gap-3">
            <label :for="`disk-${f.key}`" class="text-sm">{{ f.label }}</label>
            <div class="inline-flex shrink-0 items-center rounded-md border shadow-xs focus-within:ring-[3px] focus-within:ring-ring/50">
              <input
                :id="`disk-${f.key}`"
                v-model.number="form[f.key]"
                type="number"
                inputmode="numeric"
                :min="f.min"
                :max="f.max"
                :data-testid="`disk-${f.key}`"
                class="h-8 w-12 bg-transparent px-2 text-right text-sm tabular-nums outline-none [appearance:textfield] [&::-webkit-inner-spin-button]:appearance-none"
              />
              <span class="w-16 pr-2.5 text-sm whitespace-nowrap text-muted-foreground">{{ f.unit }}</span>
            </div>
          </div>
          <span v-if="!inRange(f.key)" class="text-xs text-destructive">Between {{ f.min }} and {{ f.max }}.</span>
          <span v-else-if="f.key === 'warn_free_percent' && warnBelowCleanup" class="text-xs text-destructive">Must be above “Clean up below”.</span>
          <span v-else class="text-xs text-muted-foreground">{{ f.hint(form[f.key]) }}</span>
        </div>

        <div v-if="changed.length" class="flex gap-2">
          <Button type="submit" size="sm" :disabled="!valid || save.isPending.value">Save</Button>
          <Button type="button" size="sm" variant="ghost" :disabled="save.isPending.value" @click="reset">Undo</Button>
        </div>
        <p v-if="gap" class="rounded-md bg-muted/50 px-3 py-2 text-xs text-muted-foreground">
          Between {{ auto.min_free_percent }}% and {{ auto.builds_min_free_percent }}% free, builds are paused and bitploy deletes only build cache and unused
          images. The other steps start below {{ auto.min_free_percent }}%.
        </p>
      </form>
    </CardContent>
  </Card>
</template>
