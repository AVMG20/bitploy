<script setup lang="ts">
// Builds on the whole server (PATCH /admin/settings): how many may run at the
// same time (max_parallel_builds), and the CPU and memory of the builder they
// share (builder_cpu_millis, builder_memory_mb). The deploy worker reads the
// slots on every pass, so a higher number starts what waits at once; a lower
// one lets the builds that run finish. The builder takes new limits at the
// next build that starts while no other build runs.
import { computed, ref, watch } from 'vue'
import { useMutation, useQuery, useQueryClient } from '@tanstack/vue-query'
import { Minus, Plus } from 'lucide-vue-next'
import QueryError from '@/components/QueryError.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardFooter, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Skeleton } from '@/components/ui/skeleton'
import { keys } from '@/composables/queries'
import { api } from '@/lib/api'
import { formatMB } from '@/lib/format'
import { toastError, toastOk } from '@/lib/notify'
import type { ServerSettings } from '@/lib/types'
import { cn } from '@/lib/utils'

const MIN = 1
const MAX = 16
const MIN_CPUS = 0.5
const MIN_MEMORY_GB = 0.5

const qc = useQueryClient()
const settings = useQuery({ queryKey: keys.adminSettings, queryFn: api.adminSettings })
const st = computed(() => settings.data.value)

const value = ref(2)
const cpus = ref<number | string>(2)
const memoryGB = ref<number | string>(6)
const toGB = (mb: number) => Math.round((mb / 1024) * 100) / 100

function fill(s: ServerSettings) {
  value.value = s.max_parallel_builds
  cpus.value = s.builder_cpu_millis / 1000
  memoryGB.value = toGB(s.builder_memory_mb)
}
const filled = ref(false)
watch(
  st,
  (s) => {
    if (!s || filled.value) return
    filled.value = true
    fill(s)
  },
  { immediate: true },
)

const cpuMillis = computed(() => Math.round(Number(cpus.value) * 1000))
const memoryMB = computed(() => Math.round(Number(memoryGB.value) * 1024))
const maxCPUs = computed(() => (st.value?.server.cpu_millis ? st.value.server.cpu_millis / 1000 : 256))
const maxMemoryGB = computed(() => (st.value?.server.memory_mb ? toGB(st.value.server.memory_mb) : 1024))

const slotsValid = computed(() => Number.isInteger(value.value) && value.value >= MIN && value.value <= MAX)
const cpuValid = computed(() => cpus.value !== '' && Number.isFinite(Number(cpus.value)) && Number(cpus.value) >= MIN_CPUS && cpuMillis.value <= maxCPUs.value * 1000)
const memoryValid = computed(
  () => memoryGB.value !== '' && Number.isFinite(Number(memoryGB.value)) && Number(memoryGB.value) >= MIN_MEMORY_GB && memoryMB.value <= (st.value?.server.memory_mb || Infinity),
)
const valid = computed(() => slotsValid.value && cpuValid.value && memoryValid.value)

const slotsDirty = computed(() => !!st.value && value.value !== st.value.max_parallel_builds)
const cpuDirty = computed(() => !!st.value && cpuMillis.value !== st.value.builder_cpu_millis)
const memoryDirty = computed(() => !!st.value && memoryMB.value !== st.value.builder_memory_mb)
const dirty = computed(() => slotsDirty.value || cpuDirty.value || memoryDirty.value)

// What one build gets when every slot builds at once.
const share = computed(() => {
  if (!valid.value || value.value < 2) return ''
  const c = Math.round((cpuMillis.value / value.value) / 10) / 100
  return `When ${value.value} build at once, each gets about ${c} CPU and ${formatMB(memoryMB.value / value.value)}.`
})

function step(by: number) {
  value.value = Math.min(MAX, Math.max(MIN, (Number.isFinite(value.value) ? Math.round(value.value) : 2) + by))
}
function onInput(e: Event) {
  value.value = Number((e.target as HTMLInputElement).value)
}

const save = useMutation({
  mutationFn: () =>
    api.updateAdminSettings({
      ...(slotsDirty.value ? { max_parallel_builds: value.value } : {}),
      ...(cpuDirty.value ? { builder_cpu_millis: cpuMillis.value } : {}),
      ...(memoryDirty.value ? { builder_memory_mb: memoryMB.value } : {}),
    }),
  onSuccess: (s: ServerSettings) => {
    qc.setQueryData(keys.adminSettings, s)
    fill(s)
    void qc.invalidateQueries({ queryKey: keys.builds })
    void qc.invalidateQueries({ queryKey: keys.adminServer })
    toastOk('Build settings saved')
  },
  onError: (e) => toastError(e),
})
function submit() {
  if (dirty.value && valid.value && !save.isPending.value) save.mutate()
}
</script>

<template>
  <Card data-testid="admin-builds">
    <CardHeader>
      <CardTitle>Builds</CardTitle>
      <CardDescription>
        How many apps can build at once on this server, for every account together, and the CPU and memory the builder has for them. More deployments wait in line. Builds
        that run at the same time share the builder's CPU and memory, so more at once can make each one slower.
      </CardDescription>
    </CardHeader>
    <CardContent>
      <QueryError v-if="settings.isError.value" :error="settings.error.value" @retry="settings.refetch()" />
      <Skeleton v-else-if="!st" class="h-40 w-full" />
      <div v-else class="grid gap-6">
        <div class="grid gap-2">
          <Label for="max-parallel-builds">Builds at the same time</Label>
          <div class="flex flex-wrap items-center gap-4">
            <div class="inline-flex items-center rounded-md border shadow-xs" role="group" aria-label="Builds at the same time">
              <Button variant="ghost" size="icon" class="rounded-r-none" :disabled="value <= MIN" aria-label="One fewer" @click="step(-1)"><Minus /></Button>
              <input
                id="max-parallel-builds"
                type="number"
                inputmode="numeric"
                :min="MIN"
                :max="MAX"
                step="1"
                :value="value"
                :aria-invalid="!slotsValid"
                class="h-9 w-14 border-x bg-transparent text-center text-sm font-medium tabular-nums outline-none [appearance:textfield] focus-visible:bg-accent/50 [&::-webkit-inner-spin-button]:appearance-none [&::-webkit-outer-spin-button]:appearance-none"
                aria-label="Number of builds"
                @input="onInput"
                @keydown.enter.prevent="submit"
              />
              <Button variant="ghost" size="icon" class="rounded-l-none" :disabled="value >= MAX" aria-label="One more" @click="step(1)"><Plus /></Button>
            </div>
            <div class="flex items-center gap-1" aria-hidden="true">
              <span
                v-for="i in MAX"
                :key="i"
                :class="cn('h-4 w-1.5 rounded-full transition-colors', slotsValid && i <= value ? 'bg-primary' : 'bg-muted-foreground/20')"
              />
            </div>
            <Badge v-if="slotsValid && value === st.default_max_parallel_builds" variant="secondary">Default</Badge>
          </div>
          <p v-if="!slotsValid" class="text-sm text-destructive">Use a whole number from {{ MIN }} to {{ MAX }}.</p>
        </div>

        <div class="grid gap-4 sm:grid-cols-2">
          <div class="grid content-start gap-2">
            <div class="flex items-center gap-2">
              <Label for="builder-cpus">CPU cores for builds</Label>
              <Badge v-if="cpuValid && cpuMillis === st.default_builder_cpu_millis" variant="secondary">Default</Badge>
            </div>
            <Input
              id="builder-cpus"
              v-model="cpus"
              type="number"
              inputmode="decimal"
              :min="MIN_CPUS"
              :max="maxCPUs"
              step="0.5"
              class="h-9 max-w-40 tabular-nums"
              :aria-invalid="!cpuValid"
              @keydown.enter.prevent="submit"
            />
            <p v-if="!cpuValid" class="text-sm text-destructive">Use {{ MIN_CPUS }} to {{ maxCPUs }} cores.</p>
            <p v-else class="text-xs text-muted-foreground">
              <template v-if="st.server.cpu_millis">This server has {{ st.server.cpu_millis / 1000 }} cores. </template>Default {{ st.default_builder_cpu_millis / 1000 }}.
            </p>
          </div>
          <div class="grid content-start gap-2">
            <div class="flex items-center gap-2">
              <Label for="builder-memory">Memory for builds (GB)</Label>
              <Badge v-if="memoryValid && memoryMB === st.default_builder_memory_mb" variant="secondary">Default</Badge>
            </div>
            <Input
              id="builder-memory"
              v-model="memoryGB"
              type="number"
              inputmode="decimal"
              :min="MIN_MEMORY_GB"
              :max="maxMemoryGB"
              step="0.5"
              class="h-9 max-w-40 tabular-nums"
              :aria-invalid="!memoryValid"
              @keydown.enter.prevent="submit"
            />
            <p v-if="!memoryValid" class="text-sm text-destructive">Use {{ MIN_MEMORY_GB }} to {{ maxMemoryGB }} GB.</p>
            <p v-else class="text-xs text-muted-foreground">
              <template v-if="st.server.memory_mb">This server has {{ formatMB(st.server.memory_mb) }}. </template>Default {{ formatMB(st.default_builder_memory_mb) }}. A build
              that needs more fails with out of memory.
            </p>
          </div>
        </div>

        <p class="text-sm text-muted-foreground">
          <template v-if="share">{{ share }} </template>New CPU and memory apply to the next build that starts while no other build runs; the build cache is kept.
        </p>
      </div>
    </CardContent>
    <CardFooter class="justify-end">
      <Button :disabled="!dirty || !valid || save.isPending.value" @click="save.mutate()">{{ save.isPending.value ? 'Saving...' : 'Save' }}</Button>
    </CardFooter>
  </Card>
</template>
