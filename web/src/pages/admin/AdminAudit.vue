<script setup lang="ts">
import { appPath } from '@/lib/appLinks'
import { computed } from 'vue'
import { RouterLink } from 'vue-router'
import { useInfiniteQuery, useQuery } from '@tanstack/vue-query'
import { RefreshCw, SearchX } from 'lucide-vue-next'
import EmptyState from '@/components/EmptyState.vue'
import ListSearch from '@/components/ListSearch.vue'
import Toolbar from '@/components/Toolbar.vue'
import QueryError from '@/components/QueryError.vue'
import RelativeTime from '@/components/RelativeTime.vue'
import { Button } from '@/components/ui/button'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Skeleton } from '@/components/ui/skeleton'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { keys } from '@/composables/queries'
import { useQueryParam } from '@/composables/useQueryParam'
import { api } from '@/lib/api'
import { AUDIT_GROUPS, auditDetails, auditGroup, auditLabel } from '@/lib/audit'
import { channelLabel } from '@/lib/format'
import type { AuditEntry } from '@/lib/types'

const audit = useInfiniteQuery({
  queryKey: keys.adminAudit,
  queryFn: ({ pageParam }) => api.audit(pageParam),
  initialPageParam: undefined as string | undefined,
  getNextPageParam: (last) => last.next_cursor || undefined,
})
const users = useQuery({ queryKey: keys.adminUsers, queryFn: api.adminUsers })
const apps = useQuery({ queryKey: keys.adminApps, queryFn: api.adminApps })
const names = computed(() => new Map((users.data.value ?? []).map((u) => [u.id, u.name || u.email])))
const appsById = computed(() => new Map((apps.data.value ?? []).map((a) => [a.id, a])))
const entries = computed(() => audit.data.value?.pages.flatMap((p) => p.items ?? []) ?? [])

function who(e: AuditEntry): string {
  return e.user_id ? (names.value.get(e.user_id) ?? 'A removed user') : 'bitploy'
}
/** What the entry is about, in words, and where to go for it. */
function target(e: AuditEntry): { text: string; slug?: string } | null {
  if (!e.target_id) return null
  if (e.target_type === 'app') {
    const a = appsById.value.get(e.target_id)
    if (a) return { text: a.name, slug: a.slug }
    const slug = e.details?.slug ?? e.details?.old_slug
    return { text: typeof slug === 'string' ? slug : 'a deleted app' }
  }
  if (e.target_type === 'user') return { text: names.value.get(e.target_id) ?? 'a user' }
  if (e.target_type === 'domain' && typeof e.details?.hostname === 'string') return { text: e.details.hostname }
  return null
}

// ---------------------------------------------------------------- filters, kept in the address
const kind = useQueryParam('kind', 'all', ['all', ...AUDIT_GROUPS.map((g) => g.value)])
const search = useQueryParam('q', '')
const shown = computed(() => {
  const words = search.value.trim().toLowerCase().split(/\s+/).filter(Boolean)
  return entries.value.filter((e) => {
    if (kind.value !== 'all' && auditGroup(e.action) !== kind.value) return false
    if (!words.length) return true
    const hay = [auditLabel(e.action), e.action, who(e), target(e)?.text ?? '', channelLabel(e.channel), e.ip, ...auditDetails(e.details)].join(' ').toLowerCase()
    return words.every((w) => hay.includes(w))
  })
})
const filtering = computed(() => kind.value !== 'all' || !!search.value.trim())
</script>

<template>
  <div class="grid gap-3">
    <Toolbar>
      <ListSearch v-model="search" class="min-w-0 flex-[1_1_12rem] sm:max-w-xs" placeholder="Search who, what or which app" label="Search the activity log" />
      <Select v-model="kind">
        <SelectTrigger class="w-40" aria-label="Kind of activity"><SelectValue /></SelectTrigger>
        <SelectContent>
          <SelectItem value="all">Everything</SelectItem>
          <SelectItem v-for="g in AUDIT_GROUPS" :key="g.value" :value="g.value">{{ g.label }}</SelectItem>
        </SelectContent>
      </Select>
      <Button variant="outline" class="ml-auto" :disabled="audit.isFetching.value" @click="audit.refetch()">
        <RefreshCw :class="audit.isFetching.value && 'animate-spin'" /> Refresh
      </Button>
    </Toolbar>
    <QueryError v-if="audit.isError.value" :error="audit.error.value" @retry="audit.refetch()" />
    <div v-else-if="audit.isPending.value" class="grid gap-2"><Skeleton v-for="i in 5" :key="i" class="h-9 w-full" /></div>
    <EmptyState v-else-if="!entries.length" title="Nothing logged yet." description="Every change made in bitploy shows up here for 90 days." />
    <template v-else>
      <EmptyState
        v-if="!shown.length"
        :icon="SearchX"
        title="Nothing matches."
        :description="audit.hasNextPage.value ? 'Only the entries loaded so far are searched. Load older ones below, or change the filter.' : 'Change the search or the kind of activity.'"
      >
        <Button variant="outline" @click="search = ''; kind = 'all'">Show everything</Button>
      </EmptyState>
      <div v-else class="overflow-x-auto rounded-xl border bg-card shadow-card">
        <Table>
          <TableHeader>
            <TableRow class="hover:bg-transparent">
              <TableHead>When</TableHead>
              <TableHead>Who</TableHead>
              <TableHead>What</TableHead>
              <TableHead class="hidden md:table-cell">Where from</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            <TableRow v-for="e in shown" :key="e.id">
              <TableCell class="align-top text-xs"><RelativeTime :date="e.ts" /></TableCell>
              <TableCell class="align-top text-sm">
                {{ who(e) }}
                <span class="block text-xs text-muted-foreground">{{ channelLabel(e.channel) }}</span>
              </TableCell>
              <TableCell class="align-top">
                <span class="text-sm">{{ auditLabel(e.action) }}</span>
                <template v-if="target(e)">
                  <span class="text-sm text-muted-foreground">: </span>
                  <RouterLink v-if="target(e)?.slug" :to="appPath(target(e)!.slug!)" class="text-sm font-medium hover:underline">{{ target(e)!.text }}</RouterLink>
                  <span v-else class="text-sm font-medium">{{ target(e)!.text }}</span>
                </template>
                <span v-if="auditDetails(e.details).length" class="block max-w-md truncate text-xs text-muted-foreground" :title="auditDetails(e.details).join('\n')">
                  {{ auditDetails(e.details).join(' · ') }}
                </span>
                <span class="block font-mono text-[11px] text-muted-foreground/70">{{ e.action }}</span>
              </TableCell>
              <TableCell class="hidden align-top font-mono text-xs md:table-cell">{{ e.ip }}</TableCell>
            </TableRow>
          </TableBody>
        </Table>
      </div>
      <div v-if="audit.hasNextPage.value" class="flex flex-wrap items-center justify-center gap-3">
        <Button variant="outline" :disabled="audit.isFetchingNextPage.value" @click="audit.fetchNextPage()">
          {{ audit.isFetchingNextPage.value ? 'Loading...' : 'Show older entries' }}
        </Button>
        <span v-if="filtering" class="text-xs text-muted-foreground">The filter looks at the entries loaded so far.</span>
      </div>
    </template>
  </div>
</template>
