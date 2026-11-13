<script setup lang="ts">
// Admin > Server: the builds behind the charts, opened from "Builds" on the
// Usage over time card. Latest lists everyone's builds newest first; By person
// sums them up per person (GET /admin/builds/stats), and a row there shows
// that person's builds in Latest.
import { computed, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'
import { useQuery } from '@tanstack/vue-query'
import { X } from 'lucide-vue-next'
import QueryError from '@/components/QueryError.vue'
import RelativeTime from '@/components/RelativeTime.vue'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Skeleton } from '@/components/ui/skeleton'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { keys } from '@/composables/queries'
import { api } from '@/lib/api'
import { BUILD_WINDOWS, peakPercent } from '@/lib/builds'
import { formatDuration, formatMB } from '@/lib/format'
import type { BuildPersonStats, BuildWindow } from '@/lib/types'
import BuildList from './BuildList.vue'

const open = defineModel<boolean>('open', { required: true })

const windowSel = ref<BuildWindow>('24h')
const view = ref<'latest' | 'people'>('latest')
// Latest shows only this person's builds while set (picked in By person).
const person = ref<BuildPersonStats | null>(null)

watch(open, (v) => {
  if (!v) person.value = null
})

const windowLabel = computed(() => BUILD_WINDOWS.find((w) => w.value === windowSel.value)?.label ?? windowSel.value)
const nameOf = (p: Pick<BuildPersonStats, 'name' | 'email'>) => p.name || p.email || 'A removed user'

const builds = useQuery({
  queryKey: computed(() => keys.adminBuilds(person.value?.user_id ?? '', windowSel.value)),
  queryFn: () => api.adminBuilds(person.value?.user_id ?? '', windowSel.value),
  enabled: computed(() => open.value && view.value === 'latest'),
  refetchInterval: 60_000,
})
const stats = useQuery({
  queryKey: computed(() => keys.adminBuildStats(windowSel.value)),
  queryFn: () => api.adminBuildStats(windowSel.value),
  enabled: computed(() => open.value && view.value === 'people'),
  refetchInterval: 60_000,
})
const people = computed(() => stats.data.value?.people ?? [])

// The name is a link to their page; a click anywhere else on the row shows their builds.
function onRow(e: MouseEvent, p: BuildPersonStats) {
  if ((e.target as HTMLElement).closest('a, button')) return
  person.value = p
  view.value = 'latest'
}
</script>

<template>
  <Dialog v-model:open="open">
    <DialogContent class="flex max-h-[85vh] flex-col sm:max-w-3xl" data-testid="admin-builds-dialog">
      <DialogHeader>
        <DialogTitle>Builds</DialogTitle>
        <DialogDescription>
          The builds shaded in the charts: who built what, how long it took and how busy the server was meanwhile. Kept 90 days.
        </DialogDescription>
      </DialogHeader>

      <div class="flex flex-wrap items-center justify-between gap-2">
        <Tabs v-model="view">
          <TabsList>
            <TabsTrigger value="latest">Latest</TabsTrigger>
            <TabsTrigger value="people">By person</TabsTrigger>
          </TabsList>
        </Tabs>
        <Tabs v-model="windowSel">
          <TabsList>
            <TabsTrigger v-for="w in BUILD_WINDOWS" :key="w.value" :value="w.value">{{ w.label }}</TabsTrigger>
          </TabsList>
        </Tabs>
      </div>

      <div class="min-h-0 overflow-y-auto">
        <template v-if="view === 'latest'">
          <p v-if="person" class="mb-2 flex flex-wrap items-center gap-2 text-sm">
            Only the builds of
            <RouterLink :to="{ name: 'admin-person', params: { id: person.user_id } }" class="font-medium hover:underline">{{ nameOf(person) }}</RouterLink>
            <Button variant="ghost" size="sm" class="h-7 px-2" @click="person = null"><X class="size-3.5" /> Show everyone</Button>
          </p>
          <QueryError v-if="builds.isError.value && !builds.data.value" :error="builds.error.value" @retry="builds.refetch()" />
          <Skeleton v-else-if="builds.isPending.value" class="h-32 w-full" />
          <p v-else-if="!builds.data.value?.length" class="py-8 text-center text-sm text-muted-foreground">No builds in the last {{ windowLabel }}.</p>
          <template v-else>
            <BuildList :builds="builds.data.value" :show-person="!person" />
            <p v-if="builds.data.value.length >= 100" class="mt-2 text-xs text-muted-foreground">The newest 100. Pick a shorter time or one person for the rest.</p>
          </template>
        </template>

        <template v-else>
          <QueryError v-if="stats.isError.value && !stats.data.value" :error="stats.error.value" @retry="stats.refetch()" />
          <Skeleton v-else-if="stats.isPending.value" class="h-32 w-full" />
          <p v-else-if="!people.length" class="py-8 text-center text-sm text-muted-foreground">No builds in the last {{ windowLabel }}.</p>
          <div v-else class="overflow-x-auto rounded-xl border bg-card">
            <Table>
              <TableHeader>
                <TableRow class="hover:bg-transparent">
                  <TableHead>Person</TableHead>
                  <TableHead class="text-right">Builds</TableHead>
                  <TableHead class="text-right">Total</TableHead>
                  <TableHead class="hidden text-right sm:table-cell">Average</TableHead>
                  <TableHead class="hidden md:table-cell">Longest</TableHead>
                  <TableHead class="hidden text-right md:table-cell" title="The server's highest CPU and memory while their builds ran">Peak server use</TableHead>
                  <TableHead class="hidden lg:table-cell">Last build</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                <TableRow v-for="p in people" :key="p.user_id" class="cursor-pointer" @click="onRow($event, p)">
                  <TableCell>
                    <RouterLink :to="{ name: 'admin-person', params: { id: p.user_id } }" class="font-medium hover:underline">{{ nameOf(p) }}</RouterLink>
                    <span v-if="p.name && p.email" class="block text-xs text-muted-foreground">{{ p.email }}</span>
                  </TableCell>
                  <TableCell class="text-right tabular-nums">
                    {{ p.builds }}
                    <span v-if="p.failed" class="block text-xs text-destructive">{{ p.failed }} failed</span>
                  </TableCell>
                  <TableCell class="text-right tabular-nums">{{ formatDuration(p.total_build_seconds) }}</TableCell>
                  <TableCell class="hidden text-right tabular-nums sm:table-cell">{{ formatDuration(p.avg_build_seconds) }}</TableCell>
                  <TableCell class="hidden md:table-cell">
                    <span class="tabular-nums">{{ formatDuration(p.longest_build_seconds) }}</span>
                    <span class="block text-xs text-muted-foreground">{{ p.longest_app }}</span>
                  </TableCell>
                  <TableCell class="hidden text-right tabular-nums md:table-cell">
                    {{ peakPercent(p.peak_cpu) }} CPU
                    <span class="block text-xs text-muted-foreground">{{ formatMB(p.peak_memory_mb) }} memory</span>
                  </TableCell>
                  <TableCell class="hidden text-sm lg:table-cell"><RelativeTime :date="p.last_build_at" /></TableCell>
                </TableRow>
              </TableBody>
            </Table>
          </div>
        </template>
      </div>

      <DialogFooter>
        <Button @click="open = false">Close</Button>
      </DialogFooter>
    </DialogContent>
  </Dialog>
</template>
