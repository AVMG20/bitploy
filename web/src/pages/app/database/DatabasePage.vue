<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useQueryClient } from '@tanstack/vue-query'
import { RouterLink, useRoute, useRouter } from 'vue-router'
import { ArrowLeft, Database, Download, Lock, RefreshCw } from 'lucide-vue-next'
import EmptyState from '@/components/EmptyState.vue'
import PageHeader from '@/components/PageHeader.vue'
import { Button } from '@/components/ui/button'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Skeleton } from '@/components/ui/skeleton'
import { Switch } from '@/components/ui/switch'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { useApp, useDataRoot, useDbSchema } from '@/composables/queries'
import { api } from '@/lib/api'
import { adminDbSource, appDbSource } from '@/lib/dbSource'
import { engineName, isDbFileName, isServerDb, pickDatabase } from '@/lib/dbvalue'
import { formatBytes } from '@/lib/format'
import DataTab from './DataTab.vue'
import DbError from './DbError.vue'
import InlineError from './InlineError.vue'
import SqlConsole from './SqlConsole.vue'
import StructureTab from './StructureTab.vue'
import TableSidebar from './TableSidebar.vue'

// The database viewer. It shows a SQLite file in an app's /data (the Database tab, or a file opened
// from Storage: ?path=app.db), the server of a PostgreSQL, MariaDB or MySQL app (the Database tab;
// ?db=name picks another database on it) or, with `admin`, bitploy's own database (read-only). Everything below
// talks to a DbSource, so both share this UI. The table and the tab live in the address too
// (&table=users&tab=structure), so a reload or a shared link comes back to the same place.
const props = defineProps<{ slug?: string; admin?: boolean }>()
const route = useRoute()
const router = useRouter()
const qc = useQueryClient()
const slug = computed(() => props.slug ?? '')
const app = useApp(slug, () => !props.admin)

type Tab = 'data' | 'structure' | 'sql'
const TABS: { value: Tab; label: string }[] = [
  { value: 'data', label: 'Data' },
  { value: 'structure', label: 'Structure' },
  { value: 'sql', label: 'SQL' },
]

const first = (v: unknown) => (typeof v === 'string' ? v : '')
// The Database tab (/apps/:slug/database) opens the app's main database from the top of /data;
// a database opened from Storage names its file in the address (?path=).
const isTab = computed(() => !props.admin && route.name === 'app-database-default')
const server = computed(() => !props.admin && isServerDb(app.data.value))
const root = useDataRoot(slug, () => isTab.value && !!app.data.value?.volume && !server.value)
const rootDatabases = computed(() => (root.data.value?.items ?? []).filter((f) => f.type === 'file' && isDbFileName(f.name)))
const picked = computed(() => pickDatabase(root.data.value?.items ?? [], slug.value))
const resolving = computed(
  () => isTab.value && (!app.data.value || (!server.value && !picked.value && !!app.data.value.volume && root.isPending.value)),
)
// For a database server the path is the name of a database on it; empty is the one the app was made with.
const path = computed(() => (props.admin ? '' : server.value ? first(route.query.db) : isTab.value ? (picked.value ?? '') : first(route.query.path)))
// Nothing to show here without a database at the top of /data: Storage says what is there.
watch(
  [isTab, resolving, picked],
  () => {
    if (isTab.value && !resolving.value && !server.value && !picked.value) void router.replace({ name: 'app-storage', params: { slug: slug.value } })
  },
  { immediate: true },
)

const fileName = computed(() => path.value.split('/').pop() || path.value)
const tab = computed<Tab>(() => TABS.find((t) => t.value === route.query.tab)?.value ?? 'data')
const source = computed(() => (props.admin ? adminDbSource : appDbSource(slug.value, path.value)))
// A source can be read-only for good (bitploy's own database); a file in /data has the page's Read-only switch.
const fixedReadOnly = computed(() => !source.value.write)

const schema = useDbSchema(source, () => props.admin || server.value || !!path.value)
const db = computed(() => schema.data.value)
const tables = computed(() => db.value?.tables ?? [])
// No (or an unknown) table in the address: the first one.
const table = computed(() => tables.value.find((t) => t.name === first(route.query.table)) ?? tables.value[0])

// Another database on the server: its first table, the Data tab.
function pickDb(name: string) {
  void router.replace({ query: { db: name } })
}

function go(patch: { tab?: Tab; table?: string; q?: string }) {
  void router.replace({ query: { ...route.query, ...patch } })
}

function selectTable(name: string) {
  go({ table: name, tab: tab.value === 'sql' ? 'data' : tab.value, ...(name !== table.value?.name ? { q: undefined } : {}) })
}

function refresh() {
  void qc.invalidateQueries({ queryKey: source.value.schemaKey })
  void qc.invalidateQueries({ queryKey: source.value.rowsKey })
}

// Read-only: nothing on the page can change the database. Remembered per app and file in this browser.
const roKey = computed(() => `bitploy:db-readonly:${slug.value}:${path.value}`)
function loadReadOnly(): boolean {
  try {
    return localStorage.getItem(roKey.value) === '1'
  } catch {
    return false
  }
}
const switchOn = ref(loadReadOnly())
watch(roKey, () => (switchOn.value = loadReadOnly()))
const readOnly = computed(() => fixedReadOnly.value || switchOn.value)
function setReadOnly(on: boolean) {
  switchOn.value = on
  try {
    if (on) localStorage.setItem(roKey.value, '1')
    else localStorage.removeItem(roKey.value)
  } catch {
    // storage blocked: it just is not remembered
  }
}

const volumeOff = computed(() => !props.admin && !server.value && app.data.value && !app.data.value.volume)
const toStorage = computed(() => ({ name: 'app-storage', params: { slug: slug.value } }))
const back = computed(() => (props.admin ? { to: { path: '/admin' }, label: 'Admin' } : isTab.value ? null : { to: toStorage.value, label: 'Storage' }))
const meta = computed(() => {
  const d = db.value
  if (!d) return ''
  if (d.engine) return [`${engineName(d.engine)} ${(d.server_version ?? '').split(/[-\s]/)[0]}`.trim(), formatBytes(d.size)].join(' · ')
  return [formatBytes(d.size), `${d.journal_mode} journal`, `${formatBytes(d.page_size)} pages`, `SQLite ${d.sqlite_version}`].join(' · ')
})
const hasRedacted = computed(() => tables.value.some((t) => t.columns.some((c) => c.redacted)))
</script>

<template>
  <div class="grid min-w-0 gap-4">
    <!-- bitploy's own database is a page of its own (the crumbs lead back to Admin); a file in /data sits in its app's page. -->
    <PageHeader v-if="admin" title="bitploy database" class="mb-1">
      <template #meta>
        <span v-if="meta">{{ meta }}</span>
        <span><Lock class="mr-1 inline size-3.5 align-[-2px]" />Read-only. Secrets are hidden<template v-if="hasRedacted"> (shown as <span class="font-medium">hidden</span>)</template>.</span>
      </template>
      <Button v-if="db" variant="outline" size="sm" @click="refresh"><RefreshCw :class="schema.isFetching.value && 'animate-spin'" /> Refresh</Button>
    </PageHeader>
    <div v-else class="flex flex-wrap items-center justify-between gap-x-4 gap-y-3">
      <div class="min-w-0">
        <RouterLink v-if="back" :to="back.to" class="inline-flex items-center gap-1 text-sm text-muted-foreground hover:text-foreground">
          <ArrowLeft class="size-4" /> {{ back.label }}
        </RouterLink>
        <div class="flex min-w-0 items-center gap-2">
          <Database class="size-5 shrink-0 text-muted-foreground" />
          <h2 v-if="server" class="min-w-0 truncate font-mono text-lg font-semibold tracking-tight">{{ db?.database || path || 'Database' }}</h2>
          <h2 v-else class="min-w-0 truncate font-mono text-lg font-semibold tracking-tight" :title="`/data/${path}`">{{ fileName || 'Database' }}</h2>
        </div>
        <p v-if="!server && path && path !== fileName" class="mt-0.5 truncate font-mono text-xs text-muted-foreground">/data/{{ path }}</p>
        <p v-if="meta" class="mt-0.5 text-xs text-muted-foreground">{{ meta }}</p>
        <p v-if="isTab && rootDatabases.length > 1" class="mt-0.5 text-xs text-muted-foreground">
          Other databases are in <RouterLink :to="toStorage" class="underline hover:text-foreground">Storage</RouterLink>.
        </p>
      </div>
      <div v-if="db" class="flex flex-wrap items-center gap-3">
        <Select v-if="server && (db.databases?.length ?? 0) > 1" :model-value="db.database" @update:model-value="(v) => pickDb(String(v))">
          <SelectTrigger size="sm" class="font-mono text-[13px]" aria-label="Database on this server"><SelectValue /></SelectTrigger>
          <SelectContent>
            <SelectItem v-for="name in db.databases" :key="name" :value="name" class="font-mono text-[13px]">{{ name }}</SelectItem>
          </SelectContent>
        </Select>
        <label v-if="!fixedReadOnly" class="inline-flex cursor-pointer items-center gap-1.5 text-sm text-muted-foreground" title="Hides everything that edits data, and keeps the SQL console from writing.">
          <Lock class="size-3.5" /> Read-only
          <Switch size="sm" :model-value="readOnly" aria-label="Read-only" @update:model-value="(v) => setReadOnly(v === true)" />
        </label>
        <Button variant="outline" size="sm" @click="refresh"><RefreshCw :class="schema.isFetching.value && 'animate-spin'" /> Refresh</Button>
        <Button v-if="!server" as-child variant="outline" size="sm">
          <a :href="api.dataFileDownloadUrl(slug, path)" download><Download /> Download</a>
        </Button>
      </div>
    </div>

    <div v-if="resolving" class="grid grid-cols-[minmax(0,1fr)] gap-4 md:grid-cols-[13rem_minmax(0,1fr)] xl:grid-cols-[15rem_minmax(0,1fr)]">
      <Skeleton class="h-48 w-full" />
      <Skeleton class="h-80 w-full" />
    </div>
    <EmptyState v-else-if="!admin && !server && (!path || !isDbFileName(fileName))" title="No database chosen." description="Open a SQLite file from the Storage tab.">
      <Button as-child variant="outline"><RouterLink :to="toStorage">Back to Storage</RouterLink></Button>
    </EmptyState>
    <DbError v-else-if="volumeOff" :error="null" code="volume_off" :back-to="toStorage" />
        <DbError v-else-if="schema.isError.value && !db" :server="server" :error="schema.error.value" :back-to="back?.to ?? toStorage" :back-label="back?.label" :download-href="admin ? undefined : api.dataFileDownloadUrl(slug, path)" @retry="schema.refetch()" />
    <div v-else-if="!db" class="grid grid-cols-[minmax(0,1fr)] gap-4 md:grid-cols-[13rem_minmax(0,1fr)] xl:grid-cols-[15rem_minmax(0,1fr)]">
      <Skeleton class="h-48 w-full" />
      <Skeleton class="h-80 w-full" />
    </div>

    <div v-else class="grid min-w-0 grid-cols-[minmax(0,1fr)] items-start gap-4 md:grid-cols-[13rem_minmax(0,1fr)] xl:grid-cols-[15rem_minmax(0,1fr)]">
      <InlineError v-if="schema.isError.value" class="md:col-span-2" :error="schema.error.value" @retry="schema.refetch()" />
      <div class="hidden md:sticky md:top-16 md:block">
        <TableSidebar :tables="tables" :selected="tab === 'sql' ? undefined : table?.name" @select="selectTable" />
      </div>

      <div class="grid min-w-0 gap-4">
        <!-- Below md the list of tables is a plain select. -->
        <Select v-if="tables.length" :model-value="tab === 'sql' ? undefined : table?.name" @update:model-value="(v) => selectTable(String(v))">
          <SelectTrigger class="w-full font-mono text-[13px] md:hidden" aria-label="Table"><SelectValue placeholder="Pick a table" /></SelectTrigger>
          <SelectContent>
            <SelectItem v-for="t in tables" :key="t.name" :value="t.name" class="font-mono text-[13px]">{{ t.name }}<span v-if="t.type === 'view'" class="ml-1 font-sans text-xs text-muted-foreground">view</span></SelectItem>
          </SelectContent>
        </Select>

        <Tabs :model-value="tab" @update:model-value="(v) => go({ tab: v as Tab })">
          <div class="flex items-center justify-between gap-3 overflow-x-auto overflow-y-hidden border-b">
            <TabsList variant="line" class="-ml-1.5">
              <TabsTrigger v-for="t in TABS" :key="t.value" :value="t.value">{{ t.label }}</TabsTrigger>
            </TabsList>
            <p v-if="table && tab !== 'sql'" class="truncate pr-1 font-mono text-xs text-muted-foreground">{{ table.name }}</p>
          </div>
        </Tabs>

        <template v-if="tab !== 'sql'">
          <EmptyState v-if="!table" :icon="Database" title="This database has no tables." :description="fixedReadOnly ? undefined : 'Create one in the SQL tab.'" />
          <DataTab v-else-if="tab === 'data'" :key="`${path}:${table.name}`" :source="source" :table="table" :read-only="readOnly" />
          <StructureTab v-else :table="table" />
        </template>
        <SqlConsole v-show="tab === 'sql'" :source="source" :start-table="table?.name" :read-only="readOnly" />
      </div>
    </div>
  </div>
</template>
