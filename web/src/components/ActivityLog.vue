<script setup lang="ts">
// An activity list with its search, filter and refresh: Admin > Activity log
// (every row) and the Activity page (a user's own). The page fetches the
// entries and turns them into rows (lib/audit.ts); this shows them the same
// way on both.
import { computed } from 'vue'
import { RouterLink } from 'vue-router'
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
import { useQueryParam } from '@/composables/useQueryParam'
import { AUDIT_GROUPS, auditLabel, filterAuditRows } from '@/lib/audit'
import type { AuditGroup, AuditRow } from '@/lib/audit'
import { channelLabel } from '@/lib/format'

const props = withDefaults(
  defineProps<{
    rows: AuditRow[]
    pending: boolean
    error: unknown
    fetching: boolean
    hasMore: boolean
    fetchingMore: boolean
    /** A "Where from" column with the IP address (admins). Else the address, when known, goes under the channel. */
    whereColumn?: boolean
    /** The app or project in a column of its own, else after what happened. */
    targetColumn?: boolean
    /** Other words for the kinds in the filter. */
    groupLabels?: Partial<Record<AuditGroup, string>>
    emptyTitle?: string
    emptyDescription?: string
  }>(),
  {
    whereColumn: false,
    targetColumn: false,
    groupLabels: () => ({}),
    emptyTitle: 'Nothing logged yet.',
    emptyDescription: 'Every change shows up here for 90 days.',
  },
)
const emit = defineEmits<{ refresh: []; more: [] }>()

const groups = computed(() => AUDIT_GROUPS.map((g) => ({ ...g, label: props.groupLabels[g.value] ?? g.label })))

// ---------------------------------------------------------------- filters, kept in the address
const kind = useQueryParam('kind', 'all', ['all', ...AUDIT_GROUPS.map((g) => g.value)])
const search = useQueryParam('q', '')
const shown = computed(() => filterAuditRows(props.rows, kind.value, search.value))
const filtering = computed(() => kind.value !== 'all' || !!search.value.trim())
</script>

<template>
  <!-- min-w-0: a wide table scrolls in its box instead of widening the page. -->
  <div class="grid min-w-0 gap-3">
    <Toolbar>
      <ListSearch v-model="search" class="min-w-0 flex-[1_1_12rem] sm:max-w-xs" placeholder="Search who, what or which app" label="Search the activity" />
      <Select v-model="kind">
        <SelectTrigger class="w-40" aria-label="Kind of activity"><SelectValue /></SelectTrigger>
        <SelectContent>
          <SelectItem value="all">Everything</SelectItem>
          <SelectItem v-for="g in groups" :key="g.value" :value="g.value">{{ g.label }}</SelectItem>
        </SelectContent>
      </Select>
      <Button variant="outline" class="ml-auto" :disabled="fetching" @click="emit('refresh')">
        <RefreshCw :class="fetching && 'animate-spin'" /> Refresh
      </Button>
    </Toolbar>
    <QueryError v-if="error" :error="error" @retry="emit('refresh')" />
    <div v-else-if="pending" class="grid gap-2"><Skeleton v-for="i in 5" :key="i" class="h-9 w-full" /></div>
    <EmptyState v-else-if="!rows.length" :title="emptyTitle" :description="emptyDescription" />
    <template v-else>
      <EmptyState
        v-if="!shown.length"
        :icon="SearchX"
        title="Nothing matches."
        :description="hasMore ? 'Only the entries loaded so far are searched. Load older ones below, or change the filter.' : 'Change the search or the kind of activity.'"
      >
        <Button variant="outline" @click="search = ''; kind = 'all'">Show everything</Button>
      </EmptyState>
      <div v-else class="min-w-0 overflow-x-auto rounded-xl border bg-card shadow-card">
        <Table>
          <TableHeader>
            <TableRow class="hover:bg-transparent">
              <TableHead class="hidden sm:table-cell">When</TableHead>
              <TableHead>Who</TableHead>
              <TableHead>What</TableHead>
              <TableHead v-if="targetColumn" class="hidden md:table-cell">App</TableHead>
              <TableHead v-if="whereColumn" class="hidden md:table-cell">Where from</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            <TableRow v-for="r in shown" :key="r.id">
              <TableCell class="hidden align-top text-xs sm:table-cell"><RelativeTime :date="r.ts" /></TableCell>
              <!-- On a phone the cells wrap and the time goes under what happened, so a row fits the screen. -->
              <TableCell class="align-top text-sm max-sm:whitespace-normal">
                {{ r.who }}
                <span class="block text-xs text-muted-foreground">
                  {{ channelLabel(r.channel) }}<span v-if="!whereColumn && r.ip" class="font-mono"> · {{ r.ip }}</span>
                </span>
              </TableCell>
              <TableCell class="align-top max-sm:whitespace-normal">
                <!-- The action's code (deployment.queue) is on hover and in the search, not in the way. -->
                <span class="text-sm" :title="r.action">{{ r.label ?? auditLabel(r.action) }}</span>
                <!-- With a column for it, the app still shows here on a smaller screen, where that column is hidden. -->
                <span v-if="r.target" :class="targetColumn && 'md:hidden'">
                  <span class="text-sm text-muted-foreground">: </span>
                  <RouterLink v-if="r.target.to" :to="r.target.to" class="text-sm font-medium hover:underline">{{ r.target.text }}</RouterLink>
                  <span v-else class="text-sm font-medium">{{ r.target.text }}</span>
                </span>
                <span v-if="r.details.length" class="block text-xs text-muted-foreground max-sm:line-clamp-2 max-sm:break-words sm:max-w-md sm:truncate" :title="r.details.join('\n')">
                  {{ r.details.join(' · ') }}
                </span>
                <RelativeTime :date="r.ts" class="block text-xs text-muted-foreground sm:hidden" />
              </TableCell>
              <TableCell v-if="targetColumn" class="hidden align-top text-sm md:table-cell">
                <template v-if="r.target">
                  <RouterLink v-if="r.target.to" :to="r.target.to" class="font-medium hover:underline">{{ r.target.text }}</RouterLink>
                  <span v-else class="text-muted-foreground">{{ r.target.text }}</span>
                  <span v-if="r.target.sub" class="block text-xs text-muted-foreground">{{ r.target.sub }}</span>
                </template>
              </TableCell>
              <TableCell v-if="whereColumn" class="hidden align-top font-mono text-xs md:table-cell">{{ r.ip }}</TableCell>
            </TableRow>
          </TableBody>
        </Table>
      </div>
      <div v-if="hasMore" class="flex flex-wrap items-center justify-center gap-3">
        <Button variant="outline" :disabled="fetchingMore" @click="emit('more')">
          {{ fetchingMore ? 'Loading...' : 'Show older entries' }}
        </Button>
        <span v-if="filtering" class="text-xs text-muted-foreground">The filter looks at the entries loaded so far.</span>
      </div>
    </template>
  </div>
</template>
