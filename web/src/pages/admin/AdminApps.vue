<script setup lang="ts">
import { computed, ref } from 'vue'
import { RouterLink } from 'vue-router'
import { useMutation, useQuery, useQueryClient } from '@tanstack/vue-query'
import { SearchX, Square } from 'lucide-vue-next'
import AppStatusBadge from '@/components/AppStatusBadge.vue'
import ConfirmDialog from '@/components/ConfirmDialog.vue'
import EmptyState from '@/components/EmptyState.vue'
import ListSearch from '@/components/ListSearch.vue'
import Toolbar from '@/components/Toolbar.vue'
import QueryError from '@/components/QueryError.vue'
import SortableHead from '@/components/SortableHead.vue'
import StatCard from '@/components/StatCard.vue'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { Table, TableBody, TableCell, TableHeader, TableRow } from '@/components/ui/table'
import { keys, REFRESH_MS } from '@/composables/queries'
import { useQueryParam } from '@/composables/useQueryParam'
import { useTableSort } from '@/composables/useTableSort'
import { api } from '@/lib/api'
import { appMatches, sortApps } from '@/lib/appList'
import { appPath } from '@/lib/appLinks'
import { CPU_HINT, compareText, cpuFromPercent, formatCPU, formatMB, formatPercent, hostOf, pct } from '@/lib/format'
import { toastError, toastLoading } from '@/lib/notify'
import type { App } from '@/lib/types'

const qc = useQueryClient()
const apps = useQuery({ queryKey: keys.adminApps, queryFn: api.adminApps, refetchInterval: REFRESH_MS })
const users = useQuery({ queryKey: keys.adminUsers, queryFn: api.adminUsers })
const server = useQuery({ queryKey: keys.adminServer, queryFn: api.adminServer, refetchInterval: REFRESH_MS })

// Totals over every running app, from each app's latest 15-second sample.
const totals = computed(() => {
  const list = apps.data.value ?? []
  let cpu = 0
  let mem = 0
  let reserved = 0
  let running = 0
  for (const a of list) {
    if (a.desired_state !== 'running') continue
    reserved += a.memory_mb
    if (a.live) {
      running++
      cpu += a.live.cpu
      mem += a.live.memory_mb
    }
  }
  return { cpu, mem, reserved, running, total: list.length }
})

const owners = computed(() => new Map((users.data.value ?? []).map((u) => [u.id, u.name || u.email])))
const ownerOf = (a: App) => owners.value.get(a.owner_id) ?? a.owner_id

// ---------------------------------------------------------------- search and sort, kept in the address
const COLUMNS = ['name', 'owner', 'status', 'cpu', 'memory'] as const
type Column = (typeof COLUMNS)[number]
// Busiest first by default: this list is for finding what weighs on the server.
const sort = useTableSort<Column>(COLUMNS, 'cpu', ['cpu', 'memory'])
const search = useQueryParam('q', '')
const all = computed(() => apps.data.value ?? [])
const shown = computed(() => {
  const q = search.value.trim().toLowerCase()
  const found = all.value.filter((a) => appMatches(a, q) || (!!q && ownerOf(a).toLowerCase().includes(q)))
  if (sort.key.value === 'owner') {
    const d = sort.desc.value ? -1 : 1
    return [...found].sort((x, y) => d * compareText(ownerOf(x), ownerOf(y)) || compareText(x.name, y.name))
  }
  return sortApps(found, sort.key.value, sort.desc.value)
})
const searchEl = ref<InstanceType<typeof ListSearch> | null>(null)

// ---------------------------------------------------------------- stop someone's app
const target = ref<App | null>(null)
const open = computed({
  get: () => target.value !== null,
  set: (v) => {
    if (!v) target.value = null
  },
})
const stop = useMutation({
  mutationFn: (slug: string) => api.adminStopApp(slug),
  onMutate: (slug) => {
    target.value = null
    return toastLoading(`Stopping ${slug}...`)
  },
  onSuccess: (_r, slug, t) => {
    t.ok(`Stopped ${slug}`)
    void qc.invalidateQueries({ queryKey: keys.adminApps })
    void qc.invalidateQueries({ queryKey: keys.apps })
    void qc.invalidateQueries({ queryKey: keys.app(slug) })
  },
  onError: (e, _slug, t) => (t ? t.fail(e) : toastError(e)),
})
</script>

<template>
  <div>
    <QueryError v-if="apps.isError.value" :error="apps.error.value" @retry="apps.refetch()" />
    <div v-else-if="apps.isPending.value" class="grid gap-2">
      <Skeleton v-for="i in 3" :key="i" class="h-12 w-full" />
    </div>
    <EmptyState v-else-if="!all.length" title="No apps on this server yet." />
    <div v-else class="grid gap-4">
      <div class="grid gap-4 sm:grid-cols-3">
        <StatCard title="Apps running" :value="String(totals.running)" :suffix="`of ${totals.total}`" />
        <StatCard
          title="CPU used by apps"
          :value="formatCPU(cpuFromPercent(totals.cpu)).replace(' CPU', '')"
          suffix="CPU in use"
          :sub="CPU_HINT"
        />
        <StatCard
          title="Memory used by apps"
          :value="formatMB(totals.mem)"
          :suffix="server.data.value?.mem_total_mb ? `of ${formatMB(server.data.value.mem_total_mb)}` : ''"
          :sub="`${formatMB(totals.reserved)} reserved as limits`"
          :progress="server.data.value?.mem_total_mb ? pct(totals.mem, server.data.value.mem_total_mb) : undefined"
        />
      </div>

      <Toolbar class="flex-nowrap">
        <ListSearch ref="searchEl" v-model="search" class="flex-1 sm:max-w-sm" placeholder="Search by app, address, owner or status" label="Search all apps" />
        <p class="ml-auto hidden shrink-0 text-xs text-muted-foreground sm:block" aria-live="polite">
          <template v-if="search.trim()">{{ shown.length }} of {{ all.length }} apps</template>
          <template v-else>{{ all.length }} apps</template>
        </p>
      </Toolbar>

      <EmptyState v-if="!shown.length" :icon="SearchX" :title="`No apps match “${search.trim()}”.`">
        <Button variant="outline" @click="search = ''; searchEl?.focus()">Clear search</Button>
      </EmptyState>
      <div v-else class="overflow-x-auto rounded-xl border bg-card shadow-card">
        <Table>
          <TableHeader>
            <TableRow class="hover:bg-transparent">
              <SortableHead label="App" :active="sort.key.value === 'name'" :desc="sort.desc.value" @sort="sort.toggle('name')" />
              <SortableHead label="Owner" class="hidden sm:table-cell" :active="sort.key.value === 'owner'" :desc="sort.desc.value" @sort="sort.toggle('owner')" />
              <SortableHead label="Status" :active="sort.key.value === 'status'" :desc="sort.desc.value" @sort="sort.toggle('status')" />
              <SortableHead label="CPU" class="text-right" align="right" :active="sort.key.value === 'cpu'" :desc="sort.desc.value" @sort="sort.toggle('cpu')" />
              <SortableHead label="Memory used" class="text-right" align="right" :active="sort.key.value === 'memory'" :desc="sort.desc.value" @sort="sort.toggle('memory')" />
              <th class="w-0"><span class="sr-only">Actions</span></th>
            </TableRow>
          </TableHeader>
          <TableBody>
            <TableRow v-for="a in shown" :key="a.id">
              <TableCell>
                <RouterLink :to="appPath(a.slug)" class="font-medium hover:underline">{{ a.name }}</RouterLink>
                <p class="text-xs text-muted-foreground"><template v-if="a.project">{{ a.project.name }} &middot; </template>{{ a.type === 'web' ? hostOf(a.url) : 'No web address' }}</p>
              </TableCell>
              <TableCell class="hidden sm:table-cell">{{ ownerOf(a) }}</TableCell>
              <TableCell><AppStatusBadge :status="a.status" /></TableCell>
              <TableCell class="text-right tabular-nums">
                {{ a.live ? formatCPU(cpuFromPercent(a.live.cpu)) : '-' }}
                <span class="block text-xs text-muted-foreground">of {{ formatCPU(a.cpu_millis / 1000) }}</span>
              </TableCell>
              <TableCell class="text-right tabular-nums">
                {{ a.live ? formatMB(a.live.memory_mb) : '-' }}
                <span class="text-xs text-muted-foreground">/ {{ formatMB(a.memory_mb) }}</span>
                <p v-if="a.live" class="text-xs text-muted-foreground">{{ formatPercent(pct(a.live.memory_mb, a.memory_mb)) }} of limit</p>
              </TableCell>
              <TableCell>
                <Button v-if="a.status === 'running' || a.status === 'deploying'" size="sm" variant="outline" :aria-label="`Stop ${a.name}`" @click="target = a">
                  <Square /> Stop
                </Button>
              </TableCell>
            </TableRow>
          </TableBody>
        </Table>
      </div>
    </div>

    <ConfirmDialog
      v-model:open="open"
      :title="`Stop ${target?.name ?? 'this app'}?`"
      :description="`${target?.name ?? 'This app'} belongs to ${owners.get(target?.owner_id ?? '') ?? 'someone else'}. It goes offline until they start it again.`"
      confirm-label="Stop app"
      @confirm="target && stop.mutate(target.slug)"
    />
  </div>
</template>
