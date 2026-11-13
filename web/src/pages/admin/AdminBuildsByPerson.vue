<script setup lang="ts">
// Admin > Server: who builds a lot, long or heavy (GET /admin/builds/stats).
// A row opens that person's recent builds; their name leads to their page.
import { computed, ref } from 'vue'
import { RouterLink } from 'vue-router'
import { useQuery } from '@tanstack/vue-query'
import QueryError from '@/components/QueryError.vue'
import RelativeTime from '@/components/RelativeTime.vue'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Skeleton } from '@/components/ui/skeleton'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { keys } from '@/composables/queries'
import { useQueryParam } from '@/composables/useQueryParam'
import { api } from '@/lib/api'
import { BUILD_WINDOWS, peakPercent } from '@/lib/builds'
import { formatDuration, formatMB } from '@/lib/format'
import type { BuildPersonStats, BuildWindow } from '@/lib/types'
import BuildList from './BuildList.vue'

const windowSel = useQueryParam<BuildWindow>('builds', '24h', ['24h', '7d', '30d'])
const stats = useQuery({
  queryKey: computed(() => keys.adminBuildStats(windowSel.value)),
  queryFn: () => api.adminBuildStats(windowSel.value),
  refetchInterval: 60_000,
})
const people = computed(() => stats.data.value?.people ?? [])
const windowLabel = computed(() => BUILD_WINDOWS.find((w) => w.value === windowSel.value)?.label ?? windowSel.value)

const nameOf = (p: Pick<BuildPersonStats, 'name' | 'email'>) => p.name || p.email || 'A removed user'

// The builds of the row that was clicked.
const picked = ref<BuildPersonStats | null>(null)
const open = computed({
  get: () => picked.value !== null,
  set: (v) => {
    if (!v) picked.value = null
  },
})
const builds = useQuery({
  queryKey: computed(() => keys.adminBuilds(picked.value?.user_id ?? '', windowSel.value)),
  queryFn: () => api.adminBuilds(picked.value!.user_id, windowSel.value),
  enabled: computed(() => !!picked.value),
})

// The name is a link of its own; a click anywhere else on the row opens the builds.
function onRow(e: MouseEvent, p: BuildPersonStats) {
  if ((e.target as HTMLElement).closest('a, button')) return
  picked.value = p
}
</script>

<template>
  <Card data-testid="admin-builds-by-person">
    <CardHeader class="flex flex-row flex-wrap items-start justify-between gap-3">
      <div class="grid gap-1.5">
        <CardTitle>Builds by person</CardTitle>
        <CardDescription>Who builds how often and how long, and how busy the server was while they built. Builds are kept 90 days.</CardDescription>
      </div>
      <Tabs v-model="windowSel">
        <TabsList>
          <TabsTrigger v-for="w in BUILD_WINDOWS" :key="w.value" :value="w.value">{{ w.label }}</TabsTrigger>
        </TabsList>
      </Tabs>
    </CardHeader>
    <CardContent>
      <QueryError v-if="stats.isError.value && !stats.data.value" :error="stats.error.value" @retry="stats.refetch()" />
      <Skeleton v-else-if="stats.isPending.value" class="h-32 w-full" />
      <p v-else-if="!people.length" class="py-8 text-center text-sm text-muted-foreground">No builds in the last {{ windowLabel }}.</p>
      <div v-else class="overflow-x-auto rounded-xl border">
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
    </CardContent>

    <Dialog v-model:open="open">
      <DialogContent class="flex max-h-[85vh] flex-col sm:max-w-2xl" data-testid="person-builds">
        <DialogHeader>
          <DialogTitle>Builds of {{ picked ? nameOf(picked) : '' }}</DialogTitle>
          <DialogDescription>The last {{ windowLabel }}, newest first (at most 100).</DialogDescription>
        </DialogHeader>
        <div class="min-h-0 overflow-y-auto">
          <QueryError v-if="builds.isError.value" :error="builds.error.value" @retry="builds.refetch()" />
          <Skeleton v-else-if="builds.isPending.value" class="h-32 w-full" />
          <BuildList v-else :builds="builds.data.value ?? []" />
        </div>
        <DialogFooter>
          <Button v-if="picked" as-child variant="outline">
            <RouterLink :to="{ name: 'admin-person', params: { id: picked.user_id } }">Open their page</RouterLink>
          </Button>
          <Button @click="open = false">Close</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  </Card>
</template>
