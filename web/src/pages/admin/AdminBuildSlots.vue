<script setup lang="ts">
// How many builds may run at the same time on the whole server (PATCH
// /admin/settings max_parallel_builds). The deploy worker reads it on every
// pass, so a higher number starts what waits at once; a lower one lets the
// builds that run finish.
import { computed, ref, watch } from 'vue'
import { useMutation, useQuery, useQueryClient } from '@tanstack/vue-query'
import { Minus, Plus } from 'lucide-vue-next'
import QueryError from '@/components/QueryError.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardFooter, CardHeader, CardTitle } from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'
import { keys } from '@/composables/queries'
import { api } from '@/lib/api'
import { toastError, toastOk } from '@/lib/notify'
import type { ServerSettings } from '@/lib/types'
import { cn } from '@/lib/utils'

const MIN = 1
const MAX = 16

const qc = useQueryClient()
const settings = useQuery({ queryKey: keys.adminSettings, queryFn: api.adminSettings })
const st = computed(() => settings.data.value)

const value = ref(2)
const filled = ref(false)
watch(
  st,
  (s) => {
    if (!s || filled.value) return
    filled.value = true
    value.value = s.max_parallel_builds
  },
  { immediate: true },
)

const dirty = computed(() => !!st.value && value.value !== st.value.max_parallel_builds)
const valid = computed(() => Number.isInteger(value.value) && value.value >= MIN && value.value <= MAX)

function step(by: number) {
  value.value = Math.min(MAX, Math.max(MIN, (Number.isFinite(value.value) ? Math.round(value.value) : 2) + by))
}
function onInput(e: Event) {
  value.value = Number((e.target as HTMLInputElement).value)
}

const save = useMutation({
  mutationFn: () => api.updateAdminSettings({ max_parallel_builds: value.value }),
  onSuccess: (s: ServerSettings) => {
    qc.setQueryData(keys.adminSettings, s)
    value.value = s.max_parallel_builds
    void qc.invalidateQueries({ queryKey: keys.builds })
    void qc.invalidateQueries({ queryKey: keys.adminServer })
    toastOk('Build limit saved')
  },
  onError: (e) => toastError(e),
})
</script>

<template>
  <Card>
    <CardHeader>
      <CardTitle>Builds at the same time</CardTitle>
      <CardDescription>
        How many apps can build at once on this server, for every account together. More deployments wait in line. Each build shares the builder's CPU and memory, so more at once
        can make each one slower.
      </CardDescription>
    </CardHeader>
    <CardContent>
      <QueryError v-if="settings.isError.value" :error="settings.error.value" @retry="settings.refetch()" />
      <Skeleton v-else-if="!st" class="h-10 w-48" />
      <div v-else class="flex flex-wrap items-center gap-4">
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
            :aria-invalid="!valid"
            class="h-9 w-14 border-x bg-transparent text-center text-sm font-medium tabular-nums outline-none [appearance:textfield] focus-visible:bg-accent/50 [&::-webkit-inner-spin-button]:appearance-none [&::-webkit-outer-spin-button]:appearance-none"
            aria-label="Number of builds"
            @input="onInput"
            @keydown.enter.prevent="dirty && valid && save.mutate()"
          />
          <Button variant="ghost" size="icon" class="rounded-l-none" :disabled="value >= MAX" aria-label="One more" @click="step(1)"><Plus /></Button>
        </div>
        <div class="flex items-center gap-1" aria-hidden="true">
          <span
            v-for="i in MAX"
            :key="i"
            :class="cn('h-4 w-1.5 rounded-full transition-colors', valid && i <= value ? 'bg-primary' : 'bg-muted-foreground/20')"
          />
        </div>
        <Badge v-if="valid && value === st.default_max_parallel_builds" variant="secondary">Default</Badge>
      </div>
      <p v-if="st && !valid" class="mt-2 text-sm text-destructive">Use a whole number from {{ MIN }} to {{ MAX }}.</p>
    </CardContent>
    <CardFooter class="justify-end">
      <Button :disabled="!dirty || !valid || save.isPending.value" @click="save.mutate()">{{ save.isPending.value ? 'Saving...' : 'Save' }}</Button>
    </CardFooter>
  </Card>
</template>
