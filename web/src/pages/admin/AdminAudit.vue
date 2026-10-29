<script setup lang="ts">
import { computed } from 'vue'
import { useInfiniteQuery, useQuery } from '@tanstack/vue-query'
import EmptyState from '@/components/EmptyState.vue'
import QueryError from '@/components/QueryError.vue'
import RelativeTime from '@/components/RelativeTime.vue'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { keys } from '@/composables/queries'
import { api } from '@/lib/api'
import { channelLabel } from '@/lib/format'

const audit = useInfiniteQuery({
  queryKey: keys.adminAudit,
  queryFn: ({ pageParam }) => api.audit(pageParam),
  initialPageParam: undefined as string | undefined,
  getNextPageParam: (last) => last.next_cursor || undefined,
})
const users = useQuery({ queryKey: keys.adminUsers, queryFn: api.adminUsers })
const names = computed(() => new Map((users.data.value ?? []).map((u) => [u.id, u.name || u.email])))
const entries = computed(() => audit.data.value?.pages.flatMap((p) => p.items ?? []) ?? [])

function details(d: Record<string, unknown> | null): string {
  if (!d || !Object.keys(d).length) return ''
  return JSON.stringify(d)
}
</script>

<template>
  <div class="grid gap-3">
    <div class="flex justify-end">
      <Button variant="outline" size="sm" :disabled="audit.isFetching.value" @click="audit.refetch()">Refresh</Button>
    </div>
    <QueryError v-if="audit.isError.value" :error="audit.error.value" @retry="audit.refetch()" />
    <div v-else-if="audit.isPending.value" class="grid gap-2"><Skeleton v-for="i in 5" :key="i" class="h-9 w-full" /></div>
    <EmptyState v-else-if="!entries.length" title="Nothing logged yet." description="Every change made in bitploy shows up here for 90 days." />
    <template v-else>
      <div class="overflow-x-auto rounded-xl border">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>When</TableHead>
              <TableHead>Who</TableHead>
              <TableHead>What</TableHead>
              <TableHead class="hidden md:table-cell">Where from</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            <TableRow v-for="e in entries" :key="e.id">
              <TableCell class="text-xs"><RelativeTime :date="e.ts" /></TableCell>
              <TableCell class="text-sm">
                {{ e.user_id ? (names.get(e.user_id) ?? e.user_id) : 'System' }}
                <span class="block text-xs text-muted-foreground">{{ channelLabel(e.channel) }}</span>
              </TableCell>
              <TableCell>
                <span class="font-mono text-xs">{{ e.action }}</span>
                <span v-if="e.target_id" class="block text-xs text-muted-foreground">{{ e.target_type }} {{ e.target_id }}</span>
                <span v-if="details(e.details)" class="block max-w-xs truncate font-mono text-[11px] text-muted-foreground" :title="details(e.details)">{{ details(e.details) }}</span>
              </TableCell>
              <TableCell class="hidden font-mono text-xs md:table-cell">{{ e.ip }}</TableCell>
            </TableRow>
          </TableBody>
        </Table>
      </div>
      <div v-if="audit.hasNextPage.value" class="flex justify-center">
        <Button variant="outline" :disabled="audit.isFetchingNextPage.value" @click="audit.fetchNextPage()">
          {{ audit.isFetchingNextPage.value ? 'Loading...' : 'Show older entries' }}
        </Button>
      </div>
    </template>
  </div>
</template>
