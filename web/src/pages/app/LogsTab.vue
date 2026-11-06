<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref, shallowRef, watch } from 'vue'
import { useQuery } from '@tanstack/vue-query'
import { ArrowDown, Clock, Download, Pause, Play } from 'lucide-vue-next'
import ListSearch from '@/components/ListSearch.vue'
import Toolbar from '@/components/Toolbar.vue'
import { Button } from '@/components/ui/button'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { keys, livePoll, REFRESH_MS, useApp } from '@/composables/queries'
import { useQueryParam } from '@/composables/useQueryParam'
import { api, fetchLogEntries, logStreamUrl, parseLogEntry } from '@/lib/api'
import type { LogEntry, LogKind, LogTarget } from '@/lib/api'
import { versionLabel } from '@/lib/format'
import { highlightEntry, LOG_TOKEN_CLASS, logRowClass } from '@/lib/logHighlight'
import { toastError } from '@/lib/notify'
import { cn } from '@/lib/utils'
import { isMulti, publicService, serviceRole, sortServices } from '@/lib/services'
import type { Ref } from 'vue'

const props = defineProps<{
  slug: string
  /** Only this service's log (the service panel); no picker then. */
  service?: string
  /** Inside the service panel: a shorter log, and the view is not kept in the address. */
  embedded?: boolean
}>()

const MAX_LINES = 5000

// What you look at lives in the address: ?kind=build opens the build log (the
// first-deploy card on the Overview links here), ?deployment=<id> that of one
// deployment (the Deployments tab), ?q= the filter.
const kind: Ref<LogKind> = props.embedded ? ref<LogKind>('runtime') : useQueryParam<LogKind>('kind', 'runtime', ['runtime', 'build'])
const live = ref(true)
const filter: Ref<string> = props.embedded ? ref('') : useQueryParam('q', '')
const chosen: Ref<string> = props.embedded ? ref('') : useQueryParam('deployment', '')

// A project with several services: whose log. The server shows the website's
// when none is asked for. ?svc= (?service= opens the service panel).
const app = useApp(() => props.slug)
const multi = computed(() => isMulti(app.data.value))
const services = computed(() => sortServices(app.data.value?.services ?? []))
const picked = useQueryParam('svc', '')
const defaultService = computed(() => (app.data.value ? (publicService(app.data.value) ?? services.value[0])?.name ?? '' : ''))
const logService = computed(() => props.service ?? (multi.value ? picked.value || defaultService.value : ''))
const chosenDeployment = computed(() => chosen.value || undefined)
const lines = shallowRef<LogEntry[]>([])

// Timestamps in front of each line: a preference kept in this browser.
const TIME_KEY = 'bitploy.logs.time'
function readTime(): boolean {
  try {
    return localStorage.getItem(TIME_KEY) === '1'
  } catch {
    return false
  }
}
const showTime = ref(readTime())
function toggleTime() {
  showTime.value = !showTime.value
  try {
    localStorage.setItem(TIME_KEY, showTime.value ? '1' : '0')
  } catch {
    // only for this visit
  }
}
const timeFmt = new Intl.DateTimeFormat(undefined, { hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false })
const dayFmt = new Intl.DateTimeFormat(undefined, { month: 'short', day: 'numeric' })
function stamp(t: string | undefined): string {
  if (!t) return ''
  const d = new Date(t)
  if (Number.isNaN(d.getTime())) return ''
  const today = new Date().toDateString() === d.toDateString()
  return today ? timeFmt.format(d) : `${dayFmt.format(d)} ${timeFmt.format(d)}`
}
const hasTimes = computed(() => lines.value.some((l) => !!l.time))
const status = ref<'idle' | 'connecting' | 'live' | 'reconnecting' | 'ended' | 'snapshot' | 'failed'>('idle')
const streamError = ref('')
const scroller = ref<HTMLElement | null>(null)

const deployments = useQuery({
  queryKey: computed(() => keys.deployments(props.slug)),
  queryFn: () => api.deployments(props.slug),
  refetchInterval: livePoll(REFRESH_MS),
})

const deploymentId = computed(() => chosenDeployment.value ?? deployments.data.value?.[0]?.id)
const selectedDeployment = computed(() => deployments.data.value?.find((d) => d.id === deploymentId.value))
const inProgress = computed(() => {
  const s = selectedDeployment.value?.state
  return s !== undefined && !['running', 'failed', 'superseded', 'cancelled'].includes(s)
})

const target = computed<LogTarget>(() => ({ slug: props.slug, kind: kind.value, deploymentId: deploymentId.value, service: logService.value || undefined }))
const noBuildYet = computed(() => kind.value === 'build' && !deploymentId.value && !deployments.isPending.value)
// Finished build logs are static, so there is nothing to follow.
const canFollow = computed(() => kind.value === 'runtime' || inProgress.value)

// Each line gets a key of its own, so the list stays keyed when old lines drop off the top.
const ids = new WeakMap<LogEntry, number>()
let nextId = 0
function lineKey(l: LogEntry): number {
  let id = ids.get(l)
  if (id === undefined) ids.set(l, (id = nextId++))
  return id
}

const visible = computed(() => {
  const f = filter.value.trim().toLowerCase()
  return f ? lines.value.filter((l) => l.text.toLowerCase().includes(f)) : lines.value
})

let source: EventSource | null = null
let abort: AbortController | null = null
let pending: LogEntry[] = []
let flushTimer: number | null = null

function stickToBottom(): boolean {
  const el = scroller.value
  if (!el) return true
  return el.scrollHeight - el.scrollTop - el.clientHeight < 60
}

// Scrolled up to read: new lines do not pull you down, a button brings you back.
const atBottom = ref(true)
function onScroll() {
  atBottom.value = stickToBottom()
}

async function scrollDown() {
  await nextTick()
  const el = scroller.value
  if (el) el.scrollTop = el.scrollHeight
  atBottom.value = true
}

function flush() {
  flushTimer = null
  if (!pending.length) return
  const stick = stickToBottom()
  const next = lines.value.concat(pending)
  pending = []
  lines.value = next.length > MAX_LINES ? next.slice(next.length - MAX_LINES) : next
  if (stick) void scrollDown()
}

function push(text: string) {
  for (const l of text.split('\n')) pending.push(parseLogEntry(l))
  if (flushTimer === null) flushTimer = window.setTimeout(flush, 100)
}

function stop() {
  source?.close()
  source = null
  abort?.abort()
  abort = null
  pending = []
  if (flushTimer !== null) {
    clearTimeout(flushTimer)
    flushTimer = null
  }
}

async function snapshot() {
  status.value = 'snapshot'
  abort = new AbortController()
  try {
    lines.value = await fetchLogEntries(target.value, abort.signal)
    void scrollDown()
  } catch (e) {
    if (!(e instanceof DOMException)) toastError(e)
  }
}

async function start() {
  stop()
  lines.value = []
  streamError.value = ''
  if (noBuildYet.value) {
    status.value = 'idle'
    return
  }
  if (!(live.value && canFollow.value)) {
    await snapshot()
    return
  }
  status.value = 'connecting'
  const es = new EventSource(logStreamUrl(target.value))
  source = es
  let opened = false
  // The stream starts with the latest lines, so start from a clean slate on every (re)connect.
  es.onopen = () => {
    opened = true
    pending = []
    lines.value = []
    status.value = 'live'
  }
  es.onmessage = (e: MessageEvent<string>) => push(e.data)
  es.addEventListener('end', () => {
    es.close()
    status.value = 'ended'
  })
  es.onerror = (e: Event) => {
    // The server reports a failed stream with `event: error` and a JSON body, then hangs up.
    // Without this the browser would reconnect (and clear the log) forever.
    if (e instanceof MessageEvent && typeof e.data === 'string') {
      es.close()
      source = null
      try {
        streamError.value = (JSON.parse(e.data) as { error?: { message?: string } }).error?.message ?? ''
      } catch {
        streamError.value = ''
      }
      status.value = 'failed'
      return
    }
    if (!opened) {
      // Could not start streaming at all: show what is there instead.
      es.close()
      source = null
      void snapshot()
    } else if (es.readyState === EventSource.CLOSED) {
      status.value = 'ended'
    } else {
      status.value = 'reconnecting'
    }
  }
}

watch(
  // Only build logs depend on the chosen deployment; the runtime stream must not restart when the list loads.
  () => [props.slug, kind.value, kind.value === 'build' ? deploymentId.value : '', kind.value === 'runtime' ? logService.value : '', live.value, canFollow.value] as const,
  () => void start(),
  { immediate: true },
)
onBeforeUnmount(stop)

const statusText = computed(() => {
  switch (status.value) {
    case 'connecting':
      return 'Connecting...'
    case 'live':
      return 'Live'
    case 'reconnecting':
      return 'Connection lost, trying again...'
    case 'ended':
      return 'Stream ended'
    case 'snapshot':
      // A finished build log is complete; a runtime log you paused is not.
      // Live but a snapshot: the stream could not start, so these are the latest lines.
      if (!canFollow.value) return 'Complete log'
      return live.value ? 'Could not follow live; showing the latest lines' : 'Paused'
    case 'failed':
      return streamError.value || 'The live stream stopped.'
    default:
      return ''
  }
})

function download() {
  const blob = new Blob([lines.value.map((l) => (l.time ? `${l.time} ${l.text}` : l.text)).join('\n') + '\n'], { type: 'text/plain' })
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = `${props.slug}${logService.value && kind.value === 'runtime' ? `-${logService.value}` : ''}-${kind.value}.log`
  document.body.appendChild(a)
  a.click()
  a.remove()
  setTimeout(() => URL.revokeObjectURL(url), 1000)
}
</script>

<template>
  <div class="grid grid-cols-[minmax(0,1fr)] gap-3">
    <Toolbar>
      <Tabs v-model="kind">
        <TabsList>
          <TabsTrigger value="runtime">{{ multi ? 'Service log' : 'App log' }}</TabsTrigger>
          <TabsTrigger value="build">Build log</TabsTrigger>
        </TabsList>
      </Tabs>

      <Select v-if="multi && !service && kind === 'runtime' && services.length" :model-value="logService" @update:model-value="(v) => (picked = String(v) === defaultService ? '' : String(v))">
        <SelectTrigger class="w-48" aria-label="Service"><SelectValue placeholder="Pick a service" /></SelectTrigger>
        <SelectContent>
          <SelectItem v-for="s in services" :key="s.name" :value="s.name">
            {{ serviceRole(s).label }} <span class="font-mono text-xs text-muted-foreground">{{ s.name }}</span>
          </SelectItem>
        </SelectContent>
      </Select>

      <Select v-if="kind === 'build' && deployments.data.value?.length" :model-value="deploymentId" @update:model-value="(v) => (chosen = String(v))">
        <SelectTrigger class="w-44" aria-label="Version"><SelectValue placeholder="Pick a version" /></SelectTrigger>
        <SelectContent>
          <SelectItem v-for="d in deployments.data.value" :key="d.id" :value="d.id">{{ versionLabel(d.seq) }}</SelectItem>
        </SelectContent>
      </Select>

      <ListSearch v-model="filter" class="min-w-0 flex-[1_1_10rem] sm:max-w-xs" placeholder="Filter lines" label="Filter log lines" />

      <div class="ml-auto flex flex-wrap items-center justify-end gap-2">
        <span class="text-xs text-muted-foreground" aria-live="polite">
          <span v-if="status === 'live'" class="mr-1 inline-block size-1.5 animate-pulse rounded-full bg-success align-middle" />{{ statusText }}
        </span>
        <Button
          v-if="hasTimes"
          variant="outline"
          :aria-pressed="showTime"
          :class="showTime && 'bg-accent'"
          title="Show when each line was written"
          @click="toggleTime"
        >
          <Clock /> Times
        </Button>
        <Button v-if="canFollow" variant="outline" @click="live = !live">
          <component :is="live ? Pause : Play" /> {{ live ? 'Pause' : 'Follow live' }}
        </Button>
        <Button variant="outline" :disabled="!lines.length" @click="download"><Download /> Download</Button>
      </div>
    </Toolbar>

    <div class="relative">
    <div
      ref="scroller"
      :class="cn('min-h-64 overflow-auto', embedded ? 'h-[calc(100vh-22rem)]' : 'h-[60vh]')"
      class="rounded-xl border bg-card p-3 font-mono text-xs leading-relaxed text-card-foreground"
      role="log"
      tabindex="0"
      :aria-label="kind === 'build' ? 'Build log' : logService ? `Log of ${logService}` : 'App log'"
      @scroll.passive="onScroll"
    >
      <p v-if="noBuildYet" class="text-muted-foreground">There is no build log yet. It appears after the first deploy.</p>
      <p v-else-if="!visible.length" class="text-muted-foreground">
        {{ filter ? 'No lines match your filter.' : status === 'connecting' ? 'Connecting...' : 'Nothing logged yet.' }}
      </p>
      <!-- Keyed by the line itself and memoised: a new batch of lines renders only the new ones. -->
      <div
        v-for="l in visible"
        :key="lineKey(l)"
        v-memo="[l, showTime]"
        :class="cn('-mx-1.5 rounded-sm px-1.5 break-words whitespace-pre-wrap', logRowClass(highlightEntry(l)), showTime && l.time && 'flex gap-3')"
      >
        <span v-if="showTime && l.time" class="shrink-0 font-normal text-muted-foreground select-none" :title="l.time">{{ stamp(l.time) }}</span>
        <span class="min-w-0"><template v-for="(t, j) in highlightEntry(l).tokens" :key="j"><span v-if="LOG_TOKEN_CLASS[t.type]" :class="LOG_TOKEN_CLASS[t.type]">{{ t.text }}</span><template v-else>{{ t.text }}</template></template><template v-if="!l.text">&#8203;</template></span>
      </div>
    </div>
    <Button
      v-if="!atBottom && visible.length"
      size="sm"
      class="absolute right-4 bottom-4 shadow-lg"
      @click="scrollDown"
    >
      <ArrowDown /> Jump to latest
    </Button>
    </div>
  </div>
</template>
