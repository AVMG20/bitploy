<script setup lang="ts">
import { computed, ref } from 'vue'
import { ArrowDownUp, Plus, Rocket, SearchX } from 'lucide-vue-next'
import { RouterLink, useRouter } from 'vue-router'
import AppRow from '@/components/AppRow.vue'
import EmptyState from '@/components/EmptyState.vue'
import ListSearch from '@/components/ListSearch.vue'
import Toolbar from '@/components/Toolbar.vue'
import PageHeader from '@/components/PageHeader.vue'
import QueryError from '@/components/QueryError.vue'
import SortableHead from '@/components/SortableHead.vue'
import StatCard from '@/components/StatCard.vue'
import { Button } from '@/components/ui/button'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuLabel,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { Skeleton } from '@/components/ui/skeleton'
import { Table, TableBody, TableHeader, TableRow } from '@/components/ui/table'
import { useApps, useMe } from '@/composables/queries'
import { nowMs } from '@/composables/useNow'
import { useQueryParam } from '@/composables/useQueryParam'
import { useTableSort } from '@/composables/useTableSort'
import { APP_SORTS, APP_SORTS_DESC, appMatches, sortApps } from '@/lib/appList'
import type { AppSort } from '@/lib/appList'
import { CPU_HINT, cpuFromPercent, formatCPU, formatCPUMillis, formatMB, pct } from '@/lib/format'

const router = useRouter()
const me = useMe()
const apps = useApps()

const usage = computed(() => me.data.value?.usage)
const quota = computed(() => me.data.value?.quota)
// Storage that counts against the allowance: app files, backups and the room set aside for saved files.
const storageTaken = computed(() => (usage.value ? usage.value.files_mb + usage.value.backups_mb + usage.value.volume_reserved_mb : 0))

// ---------------------------------------------------------------- search and sort, kept in the address
const search = useQueryParam('q', '')
const sort = useTableSort<AppSort>(APP_SORTS, 'name', APP_SORTS_DESC)
const all = computed(() => apps.data.value ?? [])
const shown = computed(() =>
  sortApps(
    all.value.filter((a) => appMatches(a, search.value)),
    sort.key.value,
    sort.desc.value,
    nowMs.value,
  ),
)

const SORT_LABELS: Record<AppSort, string> = {
  name: 'Name',
  status: 'Status',
  cpu: 'CPU',
  memory: 'Memory',
  uptime: 'Running for',
  deployed: 'Last deploy',
}
const mobileSort = computed({
  get: () => `${sort.desc.value ? '-' : ''}${sort.key.value}`,
  set: (v: string) => sort.set(v.replace(/^-/, '') as AppSort, v.startsWith('-')),
})
const searchEl = ref<InstanceType<typeof ListSearch> | null>(null)

// Enter in the search opens the app when only one is left.
function openOnly() {
  if (shown.value.length === 1) void router.push({ name: 'app', params: { slug: shown.value[0].slug } })
}
</script>

<template>
  <div class="grid gap-6">
    <PageHeader title="Your projects" description="Everything you have put online, and how it is doing.">
      <Button v-if="apps.data.value?.length" as-child><RouterLink :to="{ name: 'new-app' }"><Plus /> New project</RouterLink></Button>
    </PageHeader>

    <!-- Each card: one number, what it is out of, and a bar for that same share. -->
    <div class="grid grid-cols-2 gap-3 sm:gap-4 lg:grid-cols-4">
      <StatCard
        title="Memory"
        :loading="me.isPending.value"
        :value="usage ? formatMB(usage.memory_reserved_mb) : ''"
        :suffix="quota ? `of ${formatMB(quota.memory_mb)} reserved` : ''"
        :progress="usage && quota ? pct(usage.memory_reserved_mb, quota.memory_mb) : undefined"
        :sub="usage ? `Your projects use ${formatMB(usage.memory_used_mb)} right now.` : ''"
        hint="Each running project reserves its own memory limit: the sum of its services."
      />
      <StatCard
        title="CPU"
        :loading="me.isPending.value"
        :value="usage ? formatCPUMillis(usage.cpu_reserved_millis).replace(' CPU', '') : ''"
        :suffix="quota ? `of ${formatCPUMillis(quota.cpu_millis)} reserved` : ''"
        :progress="usage && quota ? pct(usage.cpu_reserved_millis, quota.cpu_millis) : undefined"
        :sub="usage ? `Your projects use ${formatCPU(cpuFromPercent(usage.cpu_now_percent))} right now.` : ''"
        :hint="`Each running project reserves its own CPU limit. ${CPU_HINT}`"
      />
      <StatCard
        title="Storage"
        :loading="me.isPending.value"
        :value="usage ? formatMB(storageTaken) : ''"
        :suffix="quota ? `of ${formatMB(quota.volume_mb)} taken` : ''"
        :progress="usage && quota ? pct(storageTaken, quota.volume_mb) : undefined"
        :sub="usage ? (usage.volume_reserved_mb ? `Includes ${formatMB(usage.volume_reserved_mb)} set aside for saved files.` : 'App files and backups.') : ''"
      />
      <StatCard
        title="Deploys"
        :loading="me.isPending.value"
        :value="usage ? String(usage.deploys_week) : ''"
        suffix="this week"
        :sub="usage ? `${usage.deploys_month} this month` : ''"
      />
    </div>

    <QueryError v-if="apps.isError.value" :error="apps.error.value" @retry="apps.refetch()" />

    <div v-else-if="apps.isPending.value" class="grid gap-2">
      <Skeleton class="h-9 w-full max-w-sm" />
      <Skeleton v-for="i in 3" :key="i" class="h-14 w-full" />
    </div>

    <EmptyState
      v-else-if="!all.length"
      :icon="Rocket"
      title="No projects yet."
      description="Upload a folder, pick a GitHub repository or a ready-made app. Or open Connect AI and tell your assistant: deploy this folder."
    >
      <Button as-child><RouterLink :to="{ name: 'new-app' }"><Plus /> New project</RouterLink></Button>
      <Button as-child variant="outline"><RouterLink to="/connect">Open Connect AI</RouterLink></Button>
    </EmptyState>

    <section v-else class="grid gap-3" aria-label="Projects">
      <Toolbar class="flex-nowrap">
        <ListSearch ref="searchEl" v-model="search" class="flex-1 sm:max-w-sm" placeholder="Search by name, address or status" label="Search projects" @enter="openOnly" />
        <!-- On a phone the columns that sort are hidden, so the order is picked here. -->
        <DropdownMenu>
          <DropdownMenuTrigger as-child>
            <Button variant="outline" size="icon" class="shrink-0 md:hidden" aria-label="Sort projects"><ArrowDownUp /></Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end" class="w-52">
            <DropdownMenuLabel class="text-xs font-normal text-muted-foreground">Sort by</DropdownMenuLabel>
            <DropdownMenuRadioGroup v-model="mobileSort">
              <DropdownMenuRadioItem value="name">Name, A to Z</DropdownMenuRadioItem>
              <DropdownMenuRadioItem value="status">Status, problems first</DropdownMenuRadioItem>
              <DropdownMenuRadioItem value="-deployed">Last deploy, newest first</DropdownMenuRadioItem>
              <DropdownMenuRadioItem value="-memory">Memory, most first</DropdownMenuRadioItem>
              <DropdownMenuRadioItem value="-cpu">CPU, most first</DropdownMenuRadioItem>
            </DropdownMenuRadioGroup>
          </DropdownMenuContent>
        </DropdownMenu>
        <p class="ml-auto hidden shrink-0 text-xs text-muted-foreground sm:block" aria-live="polite">
          <template v-if="search.trim()">{{ shown.length }} of {{ all.length }} projects</template>
          <template v-else>{{ all.length }} {{ all.length === 1 ? 'project' : 'projects' }}<template v-if="quota"> of {{ quota.apps }} allowed</template></template>
        </p>
      </Toolbar>

      <EmptyState v-if="!shown.length" :icon="SearchX" :title="`No projects match “${search.trim()}”.`" description="Search looks at names, addresses, the type and the status.">
        <Button variant="outline" @click="search = ''; searchEl?.focus()">Clear search</Button>
      </EmptyState>

      <div v-else class="overflow-hidden rounded-xl border bg-card shadow-card">
        <Table>
          <TableHeader>
            <TableRow class="hover:bg-transparent">
              <SortableHead :label="SORT_LABELS.name" :active="sort.key.value === 'name'" :desc="sort.desc.value" @sort="sort.toggle('name')" />
              <SortableHead :label="SORT_LABELS.status" :active="sort.key.value === 'status'" :desc="sort.desc.value" @sort="sort.toggle('status')" />
              <SortableHead :label="SORT_LABELS.cpu" class="hidden text-right sm:table-cell" align="right" :active="sort.key.value === 'cpu'" :desc="sort.desc.value" @sort="sort.toggle('cpu')" />
              <SortableHead :label="SORT_LABELS.memory" class="hidden text-right sm:table-cell" align="right" :active="sort.key.value === 'memory'" :desc="sort.desc.value" @sort="sort.toggle('memory')" />
              <SortableHead :label="SORT_LABELS.uptime" class="hidden text-right md:table-cell" align="right" :active="sort.key.value === 'uptime'" :desc="sort.desc.value" @sort="sort.toggle('uptime')" />
              <SortableHead :label="SORT_LABELS.deployed" class="hidden text-right md:table-cell" align="right" :active="sort.key.value === 'deployed'" :desc="sort.desc.value" @sort="sort.toggle('deployed')" />
            </TableRow>
          </TableHeader>
          <TableBody>
            <AppRow v-for="a in shown" :key="a.id" :app="a" />
          </TableBody>
        </Table>
      </div>
    </section>
  </div>
</template>
