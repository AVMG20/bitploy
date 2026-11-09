<script setup lang="ts">
import { computed, ref } from 'vue'
import { useMutation, useQueryClient } from '@tanstack/vue-query'
import { History, Play, TriangleAlert } from 'lucide-vue-next'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuTrigger } from '@/components/ui/dropdown-menu'
import { Textarea } from '@/components/ui/textarea'
import { ApiError } from '@/lib/api'
import type { DbSource } from '@/lib/dbSource'
import { pushHistory } from '@/lib/dbvalue'
import { formatCount } from '@/lib/format'
import { friendlyError } from '@/lib/errors'
import type { DbSqlErrorDetails, DbSqlResult, DbValue } from '@/lib/types'
import SqlText from '@/components/SqlText.vue'
import DataGrid from './DataGrid.vue'
import ValueDialog from './ValueDialog.vue'

// A place to type SQL. It runs read-only unless the page's "Allow changes" is on
// (the same switch as editing in the grid). The last queries are remembered for
// this browser tab (sessionStorage), per app, so they do not outlive a logout.
const props = defineProps<{ source: DbSource; /** A table name to start the first query with. */ startTable?: string; /** Allow changes is off on the page: the SQL runs read-only. */ readOnly: boolean }>()
const qc = useQueryClient()

const MAX_SQL_BYTES = 64 * 1024
const historyKey = computed(() => props.source.historyKey)
/** A database that can be written to at all (bitploy's own cannot). */
const writable = computed(() => !!props.source.write)

function loadHistory(): string[] {
  try {
    const raw = sessionStorage.getItem(historyKey.value)
    const list: unknown = raw ? JSON.parse(raw) : []
    return Array.isArray(list) ? list.filter((q): q is string => typeof q === 'string').slice(0, 20) : []
  } catch {
    return []
  }
}
function saveHistory(list: string[]) {
  try {
    sessionStorage.setItem(historyKey.value, JSON.stringify(list))
  } catch {
    // private window or storage blocked: the history just does not last
  }
}
const history = ref<string[]>(loadHistory())

const quoteId = (n: string) => `"${n.replace(/"/g, '""')}"`
const sql = ref(props.startTable ? `SELECT * FROM ${quoteId(props.startTable)} LIMIT 100;` : '')
const sqlBytes = computed(() => new TextEncoder().encode(sql.value).length)
const tooLong = computed(() => sqlBytes.value > MAX_SQL_BYTES)

// ---------------------------------------------------------------- writes
const write = computed(() => writable.value && !props.readOnly)

// ---------------------------------------------------------------- running
const results = ref<DbSqlResult[]>([])
const tookMs = ref<number | null>(null)
/** The statement that failed (1-based) and its message, when the server says which. */
const failure = ref<{ statement: number | null; error: unknown; code: string } | null>(null)

function refreshData() {
  void qc.invalidateQueries({ queryKey: props.source.rowsKey })
  void qc.invalidateQueries({ queryKey: props.source.schemaKey })
}

const run = useMutation({
  mutationFn: (v: { sql: string; write: boolean }) => props.source.sql(v.sql, v.write),
  onMutate: () => {
    failure.value = null
  },
  onSuccess: (res, v) => {
    results.value = res.results
    tookMs.value = res.took_ms
    if (v.write) refreshData()
  },
  onError: (e, v) => {
    const d = e instanceof ApiError && e.code === 'sql_error' ? (e.details as DbSqlErrorDetails | undefined) : undefined
    results.value = d?.results ?? []
    tookMs.value = null
    failure.value = { statement: typeof d?.statement === 'number' ? d.statement : null, error: e, code: e instanceof ApiError ? e.code : '' }
    // Statements before the failed one may have been applied.
    if (v.write) refreshData()
  },
  onSettled: (_r, _e, v) => {
    history.value = pushHistory(history.value, v.sql)
    saveHistory(history.value)
  },
})

const canRun = computed(() => !!sql.value.trim() && !tooLong.value && !run.isPending.value)
function execute() {
  if (canRun.value) run.mutate({ sql: sql.value, write: write.value })
}

const isMac = typeof navigator !== 'undefined' && /mac|iphone|ipad/i.test(navigator.platform)
const shortcut = isMac ? 'Cmd+Enter' : 'Ctrl+Enter'

function pickQuery(q: string) {
  sql.value = q
}
function clearHistory() {
  history.value = []
  saveHistory([])
}

const failedMessage = computed(() => (failure.value ? friendlyError(failure.value.error) : null))
/** A line for what a statement did: the rows it returned, or the rows it changed. */
function outcome(r: DbSqlResult): string {
  const n = (k: number, word: string) => `${formatCount(k)} ${word}${k === 1 ? '' : 's'}`
  const what = r.columns.length ? n(r.rows.length, 'row') : r.rows_affected !== null ? `${n(r.rows_affected, 'row')} changed` : 'Done'
  return `${what} · ${r.took_ms} ms`
}

// ---------------------------------------------------------------- viewing a value
const viewing = ref<{ value: DbValue; column: string } | null>(null)
const viewOpen = computed({
  get: () => viewing.value !== null,
  set: (v) => {
    if (!v) viewing.value = null
  },
})

const underlay = ref<HTMLElement | null>(null)
function syncScroll(e: Event) {
  const el = e.target as HTMLElement
  if (!underlay.value) return
  underlay.value.scrollTop = el.scrollTop
  underlay.value.scrollLeft = el.scrollLeft
}

const oneLine = (q: string) => q.replace(/\s+/g, ' ').trim()
</script>

<template>
  <div class="grid min-w-0 gap-3">
    <div class="grid gap-2">
      <!-- The coloured copy sits under the textarea, which has transparent text: same font, padding, wrapping and scroll. -->
      <div class="relative rounded-lg bg-card dark:bg-input/30">
        <pre
          ref="underlay"
          aria-hidden="true"
          class="pointer-events-none absolute inset-0 overflow-hidden rounded-lg border border-transparent px-2.5 py-2 font-mono text-[13px] leading-relaxed break-words whitespace-pre-wrap [font-variant-ligatures:none] [scrollbar-width:none]"
        ><SqlText :sql="sql" />{{ sql.endsWith('\n') ? '\u200b' : '' }}</pre>
        <Textarea
          v-model="sql"
          class="relative max-h-96 min-h-32 bg-transparent font-mono text-[13px] leading-relaxed break-words whitespace-pre-wrap text-transparent caret-foreground selection:bg-primary/25 selection:text-transparent md:text-[13px] dark:bg-transparent [font-variant-ligatures:none] [scrollbar-width:none] [&::-webkit-scrollbar]:hidden"
          placeholder="SELECT * FROM my_table LIMIT 100;"
          aria-label="SQL"
          autocapitalize="off"
          autocomplete="off"
          spellcheck="false"
          @scroll="syncScroll"
          @keydown.ctrl.enter.prevent="execute"
          @keydown.meta.enter.prevent="execute"
        />
      </div>
      <div class="flex flex-wrap items-center justify-between gap-x-4 gap-y-2">
        <p v-if="!writable" class="text-xs text-muted-foreground">Read-only. Hidden (secret) columns come back as NULL here.</p>
        <p v-else-if="!write" class="text-xs text-muted-foreground">Runs read-only. Turn on Allow changes to run INSERT, UPDATE or DELETE.</p>
        <Badge v-else variant="destructive">Changes are allowed and saved at once</Badge>
        <div class="flex items-center gap-2">
          <span v-if="tooLong" class="text-xs text-destructive">Too long: SQL can be up to 64 KB.</span>
          <DropdownMenu>
            <DropdownMenuTrigger as-child>
              <Button variant="outline" size="sm" :disabled="!history.length"><History /> History</Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end" class="w-[min(28rem,calc(100vw-2rem))]">
              <DropdownMenuItem v-for="q in history" :key="q" class="font-mono text-xs" @select="pickQuery(q)">
                <span class="truncate"><SqlText :sql="oneLine(q)" /></span>
              </DropdownMenuItem>
              <DropdownMenuSeparator />
              <DropdownMenuItem @select="clearHistory">Clear history</DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenu>
          <Button :disabled="!canRun" @click="execute">
            <Play /> {{ run.isPending.value ? 'Running...' : 'Run' }}
            <kbd class="ml-1 hidden rounded border border-primary-foreground/30 px-1 font-sans text-[10px] font-normal opacity-80 sm:inline">{{ shortcut }}</kbd>
          </Button>
        </div>
      </div>
    </div>

    <div v-for="(r, i) in results" :key="i" class="grid gap-2">
      <div class="flex flex-wrap items-baseline justify-between gap-x-3 gap-y-0.5 text-xs">
        <span class="max-w-full min-w-0 truncate font-mono text-foreground/80" :title="r.sql">{{ results.length > 1 ? `${i + 1}. ` : '' }}<SqlText :sql="oneLine(r.sql)" /></span>
        <span class="text-muted-foreground tabular-nums">{{ outcome(r) }}</span>
      </div>
      <p v-if="r.truncated" class="text-[0.8rem] text-warning-text">Only the first {{ formatCount(r.rows.length) }} rows are shown. Add a LIMIT or a WHERE to see the rest.</p>
      <DataGrid v-if="r.columns.length" :columns="r.columns" :rows="r.rows" @view="(value, column) => (viewing = { value, column })" />
    </div>
    <p v-if="results.length > 1 && tookMs !== null" class="text-xs text-muted-foreground">{{ results.length }} statements, {{ tookMs }} ms.</p>

    <!-- After the results that did run: what stopped the run. -->
    <div v-if="failure && failedMessage" class="flex items-start gap-2 rounded-lg border border-destructive/30 bg-destructive/5 px-3 py-2 text-sm text-destructive" role="alert">
      <TriangleAlert class="mt-0.5 size-4 shrink-0" />
      <div class="min-w-0 flex-1">
        <template v-if="failure.code === 'db_read_only'">
          <span>This runs read-only, so nothing was changed.<template v-if="writable && readOnly"> Turn on Allow changes to run it.</template></span>
        </template>
        <template v-else>
          <span v-if="failure.statement && (failure.statement > 1 || results.length)" class="font-medium">Statement {{ failure.statement }}: </span>
          <span class="break-words">{{ failedMessage.message }}</span>
          <span v-if="failedMessage.hint && failure.code !== 'sql_error'" class="block text-destructive/80">{{ failedMessage.hint }}</span>
        </template>
      </div>
    </div>

    <ValueDialog v-if="viewing" v-model:open="viewOpen" :value="viewing.value" :column="viewing.column" />
  </div>
</template>
