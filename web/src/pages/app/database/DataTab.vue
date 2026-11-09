<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import Toolbar from '@/components/Toolbar.vue'
import { useMutation, useQueryClient } from '@tanstack/vue-query'
import { useDebounceFn, useMediaQuery } from '@vueuse/core'
import { useRoute, useRouter } from 'vue-router'
import { ChevronLeft, ChevronRight, Info, ListFilter, Loader2, Pencil, Plus, Search, Trash2, X } from 'lucide-vue-next'
import ConfirmDialog from '@/components/ConfirmDialog.vue'
import EmptyState from '@/components/EmptyState.vue'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Skeleton } from '@/components/ui/skeleton'
import { useDbRows } from '@/composables/queries'
import { toast } from 'vue-sonner'
import { ApiError } from '@/lib/api'
import type { DbSource } from '@/lib/dbSource'
import { cellView, filterValue, isBlob, isLongText, isTruncated, keyId, matchValues, rowsLabel } from '@/lib/dbvalue'
import { formatCount } from '@/lib/format'
import { toastError, toastOk } from '@/lib/notify'
import type { DbFilter, DbFilterOp, DbKey, DbRowsQuery, DbTable, DbValue } from '@/lib/types'
import BulkEditDialog from './BulkEditDialog.vue'
import CellEditor from './CellEditor.vue'
import DataGrid from './DataGrid.vue'
import DbError from './DbError.vue'
import InlineError from './InlineError.vue'
import RowDialog from './RowDialog.vue'
import ValueDialog from './ValueDialog.vue'

// The rows of one table or view. The parent gives this component a new key per
// table, so paging, sorting, filters and the selection start fresh for each.
const props = defineProps<{ source: DbSource; table: DbTable; /** Allow changes is off on the page: nothing can be changed from here. */ readOnly: boolean }>()
const qc = useQueryClient()

const PAGE_SIZES = [50, 100, 250, 500, 1000]
const OPS: { value: DbFilterOp; label: string }[] = [
  { value: '=', label: 'equals' },
  { value: '!=', label: 'does not equal' },
  { value: '<', label: 'less than' },
  { value: '<=', label: 'at most' },
  { value: '>', label: 'greater than' },
  { value: '>=', label: 'at least' },
  { value: 'like', label: 'matches (like)' },
  { value: 'not like', label: 'does not match' },
  { value: 'is null', label: 'is NULL' },
  { value: 'is not null', label: 'is not NULL' },
]
const noValue = (op: DbFilterOp) => op === 'is null' || op === 'is not null'

// ---------------------------------------------------------------- what to ask for
const limit = ref(100)
const offset = ref(0)
const orderBy = ref<string | undefined>()
const desc = ref(false)

interface Draft {
  id: number
  column: string
  op: DbFilterOp
  value: string
}
let draftId = 0
const drafts = ref<Draft[]>([])
const applied = ref<DbFilter[]>([])
const showFilters = ref(false)

// ---------------------------------------------------------------- search
// One box that looks in every column. It lives in the address (&q=...), so a reload keeps it.
const route = useRoute()
const router = useRouter()
const searchText = ref(typeof route.query.q === 'string' ? route.query.q : '')
const search = ref(searchText.value.trim())
const searchInput = ref<{ $el: HTMLInputElement } | null>(null)

function applySearch() {
  const q = searchText.value.trim()
  if (q === search.value) return
  search.value = q
  offset.value = 0
  void router.replace({ query: { ...route.query, q: q || undefined } })
}
const applySearchSoon = useDebounceFn(applySearch, 300)
watch(searchText, () => void applySearchSoon())
function clearSearch() {
  searchText.value = ''
  applySearch()
  searchInput.value?.$el.focus()
}
// On a narrow screen the placeholder has to fit.
const narrowScreen = useMediaQuery('(max-width: 639px)')

const columnNames = computed(() => props.table.columns.filter((c) => !c.hidden).map((c) => c.name))
// A column the server hides cannot be filtered or sorted on (it answers column_redacted).
const filterColumns = computed(() => props.table.columns.filter((c) => !c.hidden && !c.redacted).map((c) => c.name))

function addFilter() {
  drafts.value.push({ id: ++draftId, column: filterColumns.value[0] ?? '', op: '=', value: '' })
  showFilters.value = true
}
function removeFilter(id: number) {
  drafts.value = drafts.value.filter((d) => d.id !== id)
  apply()
  if (!drafts.value.length) showFilters.value = false
}
function toFilter(d: Draft): DbFilter {
  if (noValue(d.op)) return { column: d.column, op: d.op }
  // A pattern stays text; a number in a comparison is sent as one.
  return { column: d.column, op: d.op, value: d.op === 'like' || d.op === 'not like' ? d.value : filterValue(d.value) }
}
function apply() {
  applied.value = drafts.value.filter((d) => d.column && (noValue(d.op) || d.value !== '')).map(toFilter)
  offset.value = 0
}
function clearFilters() {
  drafts.value = []
  showFilters.value = false
  apply()
}
const dirtyFilters = computed(() => JSON.stringify(drafts.value.filter((d) => d.column && (noValue(d.op) || d.value !== '')).map(toFilter)) !== JSON.stringify(applied.value))

function sortBy(column: string) {
  // ascending, then descending, then the table's own order again
  if (orderBy.value !== column) {
    orderBy.value = column
    desc.value = false
  } else if (!desc.value) {
    desc.value = true
  } else {
    orderBy.value = undefined
    desc.value = false
  }
  offset.value = 0
}

const query = computed<DbRowsQuery>(() => ({
  table: props.table.name,
  limit: limit.value,
  offset: offset.value,
  ...(orderBy.value ? { order_by: orderBy.value, desc: desc.value } : {}),
  ...(applied.value.length ? { filters: applied.value } : {}),
  ...(search.value ? { search: search.value } : {}),
}))
const rows = useDbRows(() => props.source, query)
const data = computed(() => rows.data.value)
const narrowed = computed(() => !!search.value || applied.value.length > 0)
const loading = computed(() => rows.isPlaceholderData.value || (rows.isFetching.value && !!data.value))

// A filter on a column that is gone (the schema changed under us) must not loop on errors.
watch(
  () => props.table.columns,
  () => {
    const names = new Set(columnNames.value)
    if (orderBy.value && !names.has(orderBy.value)) orderBy.value = undefined
  },
)

// ---------------------------------------------------------------- paging
const first = computed(() => (data.value?.rows.length ? data.value.offset + 1 : 0))
const last = computed(() => (data.value?.offset ?? 0) + (data.value?.rows.length ?? 0))
const hasPrev = computed(() => offset.value > 0)
const hasNext = computed(() => {
  const d = data.value
  if (!d) return false
  return d.total !== null ? d.offset + d.rows.length < d.total : d.rows.length === d.limit
})
function setLimit(v: unknown) {
  limit.value = Number(v)
  offset.value = 0
}
// The page after the last one disappeared (rows were deleted): go back.
watch(
  () => [data.value?.rows.length, data.value?.total] as const,
  ([n, total]) => {
    if (n === 0 && offset.value > 0 && !rows.isPlaceholderData.value) offset.value = total ? Math.max(0, Math.floor((total - 1) / limit.value) * limit.value) : 0
  },
)

// ---------------------------------------------------------------- selection
const selected = ref(new Set<string>())
const rowKeys = computed(() => (data.value?.rows ?? []).map((r) => r.key))
const editable = computed(() => (data.value?.editable ?? props.table.editable) && rowKeys.value.every((k) => k !== null))
const rowIds = computed(() => rowKeys.value.map((k) => (k ? keyId(k) : '')))
watch([() => props.table.name, query], () => {
  selected.value = new Set()
  editing.value = null
})
watch(
  () => props.readOnly,
  (ro) => {
    if (!ro) return
    selected.value = new Set()
    editing.value = null
  },
)

function toggle(id: string) {
  const next = new Set(selected.value)
  if (!next.delete(id)) next.add(id)
  selected.value = next
}
function toggleAll(checked: boolean) {
  selected.value = checked ? new Set(rowIds.value.filter(Boolean)) : new Set()
}

const readOnlyReason = computed(() => {
  if (!props.source.write) return '' // bitploy's own database: the page already says it is read-only
  if (props.table.type === 'view') return 'This is a view, so its rows are read-only.'
  if (editable.value) return ''
  return 'This table has no primary key or rowid that identifies a row, so its rows are read-only here. Use the SQL tab to change them.'
})

// ---------------------------------------------------------------- viewing a value
const viewing = ref<{ value: DbValue; column: string; full?: () => Promise<DbValue> } | null>(null)
/** Show a value in full. A text the grid shows cut can be fetched whole (to copy it). */
function openView(value: DbValue, column: string, ri: number) {
  const row = data.value?.rows[ri]
  const cell = props.source.cell
  const key = row?.key
  viewing.value = { value, column, full: isLongText(value) && cell && key ? () => cell(props.table.name, key, column) : undefined }
}
const viewOpen = computed({
  get: () => viewing.value !== null,
  set: (v) => {
    if (!v) viewing.value = null
  },
})

// ---------------------------------------------------------------- insert
function refreshAll() {
  void qc.invalidateQueries({ queryKey: props.source.rowsKey })
  void qc.invalidateQueries({ queryKey: props.source.schemaKey })
}

/** What changes data. Nothing here calls it on a read-only database; this is the guard. */
function writer() {
  const w = props.source.write
  if (!w) throw new Error('This database is read-only.')
  return w
}

const valuesOf = (columns: string[], values: DbValue[]) => Object.fromEntries(columns.map((c, j) => [c, values[j] ?? null]))

const formOpen = ref(false)
function openInsert() {
  insert.reset()
  formOpen.value = true
}
const insert = useMutation({
  mutationFn: (values: Record<string, DbValue>) => writer().insert(props.table.name, values),
  onSuccess: () => {
    toastOk('Row added')
    formOpen.value = false
    refreshAll()
  },
  // The message shows in the form, so the person can fix the value.
})

// ---------------------------------------------------------------- edit in place
// A one-time hint per browser session (sessionStorage; try/catch: it may be blocked).
const HINT_KEY = 'bitploy:db-edit-hint'
function hintSeen(): boolean {
  try {
    return sessionStorage.getItem(HINT_KEY) === '1'
  } catch {
    return false
  }
}
const hintDismissed = ref(hintSeen())
// A double-click is a mouse thing: on a touch screen rows are ticked and then edited.
const touch = useMediaQuery('(pointer: coarse)')
function dismissHint() {
  hintDismissed.value = true
  try {
    sessionStorage.setItem(HINT_KEY, '1')
  } catch {
    // not remembered
  }
}

// Double-click a value to change it right there. Each save sends the row's key
// and the row as it was loaded (`match`), so a change made meanwhile is noticed.
const canWrite = computed(() => !!props.source.write && !props.readOnly && editable.value)
const editing = ref<{ row: number; col: number } | null>(null)
/** The cell whose new value is on its way: it shows that value, dimmed, until the rows are loaded again. */
const pending = ref<{ row: number; col: number; value: DbValue } | null>(null)
/** Tab moved on while a save was still running: that cell opens when the save is done. */
let queued: { row: number; col: number } | null = null

const columnAt = (ci: number) => props.table.columns.find((c) => c.name === data.value?.columns[ci])

// BLOBs cannot be typed here; generated and hidden columns cannot be written.
function cellEditable(ri: number, ci: number): boolean {
  if (!canWrite.value) return false
  const row = data.value?.rows[ri]
  const col = columnAt(ci)
  if (!row?.key || !col || col.hidden || col.generated) return false
  const v = row.values[ci] ?? null
  // A cut text can be edited once its whole value has been fetched; a cut BLOB or a BLOB never.
  if (isLongText(v)) return !!props.source.cell
  return !isBlob(v) && !isTruncated(v)
}

/** The whole text of the cell being edited, when the grid only shows its beginning. */
const fullText = ref<string | null>(null)
/** A cut text is being fetched: the cell shows it as busy. */
const fetchingCell = ref<{ row: number; col: number } | null>(null)
watch(editing, (e) => {
  if (!e) fullText.value = null
})

async function onCellDblclick(ri: number, ci: number) {
  const d = data.value
  const row = d?.rows[ri]
  if (!d || !row || pending.value || fetchingCell.value) return
  const v = row.values[ci] ?? null
  if (!cellEditable(ri, ci)) {
    openView(v, d.columns[ci] ?? '', ri)
    return
  }
  if (!isLongText(v)) {
    editing.value = { row: ri, col: ci }
    return
  }
  // A cut text: fetch the whole of it, then edit that. The grid keeps showing the beginning.
  const col = columnAt(ci)
  if (!row.key || !col || !props.source.cell) return
  const asked = keyId(row.key)
  fetchingCell.value = { row: ri, col: ci }
  try {
    const full = await props.source.cell(props.table.name, row.key, col.name)
    const still = data.value?.rows[ri]?.key
    if (!still || keyId(still) !== asked) return // the page moved on meanwhile
    if (typeof full === 'string') {
      fullText.value = full
      editing.value = { row: ri, col: ci }
    } else {
      toast.info('This text is too long to edit here', { description: 'It opens for reading instead. Use the SQL tab to change it.' })
      viewing.value = { value: full, column: col.name }
    }
  } catch (e) {
    toastError(e)
  } finally {
    fetchingCell.value = null
  }
}

const flash = ref<{ row: number; col: number } | null>(null)

const inline = useMutation({
  mutationFn: (v: { key: DbKey; values: Record<string, DbValue>; match: Record<string, DbValue> }) =>
    writer().update(props.table.name, v.key, v.values, v.match),
  onSuccess: async () => {
    await qc.invalidateQueries({ queryKey: props.source.rowsKey })
    // A brief tint on the cell says it is saved; no toast.
    const p = pending.value
    if (p) {
      flash.value = { row: p.row, col: p.col }
      setTimeout(() => (flash.value = null), 1200)
    }
    dismissHint()
  },
  onError: (e) => {
    if (e instanceof ApiError && e.code === 'row_changed') toastError(new Error('This row changed since it was loaded, so your edit was not saved. Its latest values are shown now.'))
    else toastError(e)
    refreshAll()
  },
  onSettled: () => {
    pending.value = null
    if (queued) {
      editing.value = queued
      queued = null
    }
  },
})

function saveCell(ri: number, ci: number, value: DbValue) {
  const d = data.value
  const row = d?.rows[ri]
  const col = columnAt(ci)
  editing.value = null
  if (!d || !row?.key || !col) return
  pending.value = { row: ri, col: ci, value }
  inline.mutate({ key: row.key, values: { [col.name]: value }, match: matchValues(props.table.columns, valuesOf(d.columns, row.values)) })
}

/** Tab (or Shift+Tab): on to the next cell that can be edited. */
function moveEdit(ri: number, ci: number, back: boolean) {
  const n = data.value?.columns.length ?? 0
  const total = (data.value?.rows.length ?? 0) * n
  const step = back ? -1 : 1
  for (let pos = ri * n + ci + step; pos >= 0 && pos < total; pos += step) {
    const next = { row: Math.floor(pos / n), col: pos % n }
    // A cut text needs its whole value first (a double-click fetches it), so Tab skips it.
    if (!cellEditable(next.row, next.col) || isLongText(data.value?.rows[next.row]?.values[next.col] ?? null)) continue
    if (pending.value) queued = next
    else editing.value = next
    return
  }
}

const gridRows = computed(() =>
  (data.value?.rows ?? []).map((r, ri) => (pending.value?.row === ri ? r.values.map((v, ci) => (ci === pending.value?.col ? pending.value.value : v)) : r.values)),
)

// ---------------------------------------------------------------- edit many, delete many
const chosenRows = () => (data.value?.rows ?? []).filter((r) => r.key && selected.value.has(keyId(r.key)))
const matchOf = (values: DbValue[]) => matchValues(props.table.columns, valuesOf(data.value?.columns ?? [], values))

const bulkOpen = ref(false)
function openBulk() {
  bulk.reset()
  bulkOpen.value = true
}
const bulk = useMutation({
  mutationFn: (v: { column: string; value: DbValue }) => {
    const chosen = chosenRows()
    return writer().updateMany(
      props.table.name,
      chosen.map((r) => r.key as DbKey),
      { [v.column]: v.value },
      chosen.map((r) => matchOf(r.values)),
    )
  },
  onSuccess: (res) => {
    toastOk(res.updated === 1 ? 'Updated 1 row' : `Updated ${res.updated} rows`)
    bulkOpen.value = false
    selected.value = new Set()
    refreshAll()
  },
  onError: (e) => {
    if (!(e instanceof ApiError && e.code === 'row_changed')) return // shown in the dialog
    bulkOpen.value = false
    toastError(new Error('A selected row changed since it was loaded, so nothing was saved. The list has been reloaded: check it and try again.'))
    selected.value = new Set()
    refreshAll()
  },
})

const confirmDelete = ref(false)
/** "Delete row id 13 from users?" for one row, "Delete 5 rows from users?" for several. */
const deleteTitle = computed(() => {
  const name = props.table.name
  const chosen = chosenRows()
  if (chosen.length !== 1) return `Delete ${selected.value.size} rows from ${name}?`
  const row = chosen[0]!
  const pks = props.table.columns.filter((c) => c.pk > 0)
  const cols = data.value?.columns ?? []
  const parts = pks.length && pks.every((c) => cols.includes(c.name))
    ? pks.map((c) => `${c.name} ${cellView(row.values[cols.indexOf(c.name)] ?? null).text}`)
    : Object.entries(row.key ?? {}).map(([k, v]) => `${k} ${cellView(v).text}`)
  const label = parts.join(', ').slice(0, 60)
  return label ? `Delete row ${label} from ${name}?` : `Delete 1 row from ${name}?`
})
const remove = useMutation({
  mutationFn: () => {
    const chosen = chosenRows()
    return writer().remove(
      props.table.name,
      chosen.map((r) => r.key as DbKey),
      chosen.map((r) => matchOf(r.values)),
    )
  },
  onSuccess: (res) => {
    toastOk(res.deleted === 1 ? 'Row deleted' : `${res.deleted} rows deleted`)
    confirmDelete.value = false
    selected.value = new Set()
    refreshAll()
  },
  onError: (e) => {
    confirmDelete.value = false
    if (e instanceof ApiError && e.code === 'row_changed') {
      toastError(new Error('A selected row changed since it was loaded, so nothing was deleted. The list has been reloaded: check it and try again.'))
      selected.value = new Set()
      refreshAll()
      return
    }
    toastError(e)
  },
})
</script>

<template>
  <div class="grid min-w-0 gap-3">
    <Toolbar class="gap-x-3">
      <form class="relative min-w-48 flex-1 sm:max-w-xl" role="search" @submit.prevent="applySearch">
        <Search class="pointer-events-none absolute top-2 left-2.5 size-4 text-muted-foreground" />
        <Input
          ref="searchInput"
          v-model="searchText"
          type="search"
          maxlength="200"
          class="pr-8 pl-8 [&::-webkit-search-cancel-button]:hidden"
          :placeholder="narrowScreen ? 'Search…' : 'Search all columns'"
          aria-label="Search all columns"
          title="Text columns: contains. Numbers: starts with. BLOBs are not searched."
          autocomplete="off"
          spellcheck="false"
          @keydown.esc="searchText && clearSearch()"
        />
        <button
          v-if="searchText"
          type="button"
          class="absolute top-1.5 right-1.5 inline-flex size-5 items-center justify-center rounded text-muted-foreground hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring/50 focus-visible:outline-none"
          aria-label="Clear search"
          @click="clearSearch"
        >
          <X class="size-3.5" />
        </button>
      </form>
      <div class="ml-auto flex flex-wrap items-center justify-end gap-1.5">
        <template v-if="selected.size && canWrite">
          <Button variant="outline" @click="openBulk"><Pencil /> Edit {{ selected.size }}</Button>
          <Button variant="destructive" :disabled="remove.isPending.value" @click="confirmDelete = true"><Trash2 /> Delete {{ selected.size }}</Button>
        </template>
        <Button variant="ghost" class="text-muted-foreground" :aria-expanded="showFilters" @click="drafts.length ? (showFilters = !showFilters) : addFilter()">
          <ListFilter /> Filters
          <span v-if="applied.length" class="ml-0.5 rounded-full bg-primary px-1.5 text-[11px] leading-4 font-medium text-primary-foreground">{{ applied.length }}</span>
        </Button>
        <Button v-if="canWrite" @click="openInsert"><Plus /> New row</Button>
      </div>
    </Toolbar>

    <p v-if="data" class="-mt-1 flex items-center gap-2 text-[0.8rem] text-muted-foreground tabular-nums">
      <span v-if="data.total !== null">{{ rowsLabel(data.total, narrowed) }}</span>
      <span v-else>Count not available</span>
      <Loader2 v-if="loading" class="size-3.5 animate-spin" />
      <span v-if="canWrite && !hintDismissed && data.rows.length" class="ml-auto inline-flex items-center gap-1">
        {{ touch ? 'Tick rows, then Edit' : 'Double-click a cell to edit' }}
        <button type="button" class="inline-flex size-4 items-center justify-center rounded hover:text-foreground" aria-label="Dismiss" @click="dismissHint"><X class="size-3" /></button>
      </span>
    </p>

    <form v-if="showFilters" class="grid gap-2 rounded-lg border bg-card p-2.5" @submit.prevent="(apply(), (showFilters = false))">
      <Toolbar v-for="d in drafts" :key="d.id">
        <Select v-model="d.column">
          <SelectTrigger class="w-40" aria-label="Column"><SelectValue /></SelectTrigger>
          <SelectContent>
            <SelectItem v-for="c in filterColumns" :key="c" :value="c" class="font-mono text-[13px]">{{ c }}</SelectItem>
          </SelectContent>
        </Select>
        <Select v-model="d.op">
          <SelectTrigger class="w-40" aria-label="Condition"><SelectValue /></SelectTrigger>
          <SelectContent>
            <SelectItem v-for="o in OPS" :key="o.value" :value="o.value">{{ o.label }}</SelectItem>
          </SelectContent>
        </Select>
        <Input v-if="!noValue(d.op)" v-model="d.value" class="min-w-32 flex-1 font-mono text-[13px] sm:max-w-xs" :placeholder="d.op === 'like' || d.op === 'not like' ? 'Use % as wildcard' : 'Value'" aria-label="Value" autocomplete="off" spellcheck="false" />
        <Button type="button" variant="ghost" size="icon" title="Remove filter" aria-label="Remove filter" @click="removeFilter(d.id)"><X /></Button>
      </Toolbar>
      <Toolbar>
        <Button type="button" variant="ghost" @click="addFilter"><Plus /> Add filter</Button>
        <span class="flex-1" />
        <Button type="button" variant="ghost" @click="clearFilters">Clear</Button>
        <Button type="submit" :disabled="!dirtyFilters">Apply</Button>
      </Toolbar>
    </form>

    <p v-if="readOnlyReason && data" class="flex items-start gap-1.5 text-[0.8rem] text-muted-foreground">
      <Info class="mt-0.5 size-3.5 shrink-0" /> {{ readOnlyReason }}
    </p>

    <InlineError v-if="rows.isError.value && data" :error="rows.error.value" @retry="rows.refetch()" />
    <DbError v-if="rows.isError.value && !data" :error="rows.error.value" @retry="rows.refetch()" />
    <Skeleton v-else-if="!data" class="h-64 w-full" />
    <template v-else>
      <EmptyState
        v-if="!data.rows.length"
        :title="search ? `No rows match “${search}”` : applied.length ? 'No rows match these filters.' : 'This table is empty.'"
        :description="narrowed ? 'Change or clear the search and filters to see more.' : canWrite ? 'Add the first row with New row.' : undefined"
      >
        <Button v-if="search" variant="outline" size="sm" @click="clearSearch">Clear search</Button>
        <Button v-if="applied.length" variant="outline" size="sm" @click="clearFilters">Clear filters</Button>
      </EmptyState>
      <DataGrid
        v-else
        :columns="data.columns"
        :rows="gridRows"
        :row-ids="rowIds"
        :selectable="canWrite"
        :selected="selected"
        :cell-editable="cellEditable"
        :editing="editing"
        :saving-cell="pending ?? fetchingCell"
        :redacted="table.columns.filter((c) => c.redacted).map((c) => c.name)"
        sortable
        :order-by="orderBy"
        :desc="desc"
        :key-columns="table.columns.filter((c) => c.pk > 0).map((c) => c.name)"
        :dim="loading"
        @sort="sortBy"
        @toggle="toggle"
        @toggle-all="toggleAll"
        @cell-dblclick="onCellDblclick"
        :flash-cell="flash"
        @view="openView"
      >
        <template #editor="{ row, col }">
          <CellEditor
            v-if="columnAt(col)"
            :column="columnAt(col)!"
            :value="fullText ?? data.rows[row]?.values[col] ?? null"
            @save="(v) => saveCell(row, col, v)"
            @cancel="editing = null"
            @next="(back) => moveEdit(row, col, back)"
          />
        </template>
      </DataGrid>

      <div class="flex flex-wrap items-center justify-between gap-x-4 gap-y-2 text-sm text-muted-foreground">
        <div class="flex items-center gap-2">
          <Select :model-value="String(limit)" @update:model-value="setLimit">
            <SelectTrigger class="w-[4.5rem] border-transparent bg-transparent shadow-none dark:bg-transparent" size="sm" aria-label="Rows per page"><SelectValue /></SelectTrigger>
            <SelectContent>
              <SelectItem v-for="n in PAGE_SIZES" :key="n" :value="String(n)">{{ n }}</SelectItem>
            </SelectContent>
          </Select>
          <span class="-ml-1.5">per page</span>
        </div>
        <div class="flex items-center gap-2">
          <span class="tabular-nums">
            {{ formatCount(first) }}-{{ formatCount(last) }}<template v-if="data.total !== null"> of {{ formatCount(data.total) }}</template>
          </span>
          <Button variant="outline" size="icon-sm" :disabled="!hasPrev || loading" aria-label="Previous page" @click="offset = Math.max(0, offset - limit)"><ChevronLeft /></Button>
          <Button variant="outline" size="icon-sm" :disabled="!hasNext || loading" aria-label="Next page" @click="offset += limit"><ChevronRight /></Button>
        </div>
      </div>
    </template>

    <RowDialog v-model:open="formOpen" :table="table" :saving="insert.isPending.value" :error="insert.error.value" @submit="(v) => insert.mutate(v)" />
    <BulkEditDialog v-model:open="bulkOpen" :table="table" :count="selected.size" :saving="bulk.isPending.value" :error="bulk.error.value" @submit="(column, value) => bulk.mutate({ column, value })" />
    <ValueDialog v-if="viewing" v-model:open="viewOpen" :value="viewing.value" :column="viewing.column" :full="viewing.full" />
    <ConfirmDialog
      v-model:open="confirmDelete"
      :title="deleteTitle"
      :description="`${selected.size === 1 ? 'This row is' : 'These rows are'} deleted from ${table.name}. This cannot be undone.`"
      confirm-label="Delete"
      :loading="remove.isPending.value"
      @confirm="remove.mutate()"
    />
  </div>
</template>
