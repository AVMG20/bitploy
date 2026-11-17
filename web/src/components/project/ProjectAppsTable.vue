<script setup lang="ts">
import { computed } from 'vue'
import SortableHead from '@/components/SortableHead.vue'
import ProjectAppRow from '@/components/project/ProjectAppRow.vue'
import { Table, TableBody, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { useTableSort } from '@/composables/useTableSort'
import { compareText } from '@/lib/format'
import { aliasOf, isPublicApp } from '@/lib/projects'
import type { AppRole } from '@/lib/projects'
import type { App } from '@/lib/types'

// The apps of a project as a table, for comparing them: status, where each is
// reached, visitors, and when it was last deployed. The order of the map is the
// start; a column heading sorts (kept in the address as ?sort=).
const props = defineProps<{ apps: readonly App[]; roleOf: (a: App) => AppRole }>()

const COLUMNS = ['map', 'name', 'status', 'deploy'] as const
type Column = (typeof COLUMNS)[number]
const sort = useTableSort<Column>(COLUMNS, 'map', ['deploy'])

// Visitors only make sense for apps on the internet; a project of databases skips the column.
const showVisitors = computed(() => props.apps.some(isPublicApp))

const deployedAt = (a: App) => a.current_deployment?.finished_at ?? a.current_deployment?.queued_at ?? ''
const rows = computed(() => {
  const k = sort.key.value
  if (k === 'map') return [...props.apps]
  const d = sort.desc.value ? -1 : 1
  const by: Record<Exclude<Column, 'map'>, (a: App) => number | string> = {
    name: (a) => aliasOf(a),
    status: (a) => a.status,
    deploy: deployedAt,
  }
  const f = by[k]
  return [...props.apps].sort((x, y) => {
    const a = f(x)
    const b = f(y)
    const c = typeof a === 'number' && typeof b === 'number' ? a - b : compareText(String(a), String(b))
    return d * c || compareText(aliasOf(x), aliasOf(y))
  })
})
</script>

<template>
  <!-- Wide as a table needs: a phone scrolls it sideways. -->
  <div class="overflow-x-auto rounded-xl border bg-card shadow-card" data-testid="project-apps-table">
    <Table :class="showVisitors ? 'min-w-[620px]' : 'min-w-[520px]'">
      <TableHeader>
        <TableRow class="hover:bg-transparent">
          <SortableHead label="App" :active="sort.key.value === 'name'" :desc="sort.desc.value" @sort="sort.toggle('name')" />
          <SortableHead label="Status" :active="sort.key.value === 'status'" :desc="sort.desc.value" @sort="sort.toggle('status')" />
          <TableHead class="uppercase">Reached at</TableHead>
          <TableHead v-if="showVisitors" class="text-right uppercase" title="People who opened the app today">Visitors today</TableHead>
          <SortableHead label="Last deploy" :active="sort.key.value === 'deploy'" :desc="sort.desc.value" @sort="sort.toggle('deploy')" />
        </TableRow>
      </TableHeader>
      <TableBody>
        <ProjectAppRow v-for="a in rows" :key="a.id" :app="a" :role="roleOf(a)" :visitors="showVisitors" />
      </TableBody>
    </Table>
  </div>
</template>
