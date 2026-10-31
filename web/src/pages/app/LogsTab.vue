<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref, shallowRef, watch } from 'vue'
import { useQuery } from '@tanstack/vue-query'
import { Download, Pause, Play, Search } from 'lucide-vue-next'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { keys, REFRESH_MS } from '@/composables/queries'
import { api, fetchLogLines, logStreamUrl, parseLogLine } from '@/lib/api'
import type { LogKind, LogTarget } from '@/lib/api'
import { toastError } from '@/lib/notify'

const props = defineProps<{ slug: string }>()

const MAX_LINES = 5000

const kind = ref<LogKind>('runtime')
const live = ref(true)
const filter = ref('')
const chosenDeployment = ref<string | undefined>(undefined)
const lines = shallowRef<string[]>([])
const status = ref<'idle' | 'connecting' | 'live' | 'reconnecting' | 'ended' | 'snapshot' | 'failed'>('idle')
const streamError = ref('')
const scroller = ref<HTMLElement | null>(null)

const deployments = useQuery({
  queryKey: computed(() => keys.deployments(props.slug)),
  queryFn: () => api.deployments(props.slug),
  refetchInterval: REFRESH_MS,
})

const deploymentId = computed(() => chosenDeployment.value ?? deployments.data.value?.[0]?.id)
const selectedDeployment = computed(() => deployments.data.value?.find((d) => d.id === deploymentId.value))
const inProgress = computed(() => {
  const s = selectedDeployment.value?.state
  return s !== undefined && !['running', 'failed', 'superseded', 'cancelled'].includes(s)
})

const target = computed<LogTarget>(() => ({ slug: props.slug, kind: kind.value, deploymentId: deploymentId.value }))
const noBuildYet = computed(() => kind.value === 'build' && !deploymentId.value && !deployments.isPending.value)
// Finished build logs are static, so there is nothing to follow.
const canFollow = computed(() => kind.value === 'runtime' || inProgress.value)

const visible = computed(() => {
  const f = filter.value.trim().toLowerCase()
  return f ? lines.value.filter((l) => l.toLowerCase().includes(f)) : lines.value
})

let source: EventSource | null = null
let abort: AbortController | null = null
let pending: string[] = []
let flushTimer: number | null = null

function stickToBottom(): boolean {
  const el = scroller.value
  if (!el) return true
  return el.scrollHeight - el.scrollTop - el.clientHeight < 60
}

async function scrollDown() {
  await nextTick()
  const el = scroller.value
  if (el) el.scrollTop = el.scrollHeight
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
  for (const l of text.split('\n')) pending.push(parseLogLine(l))
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
    lines.value = await fetchLogLines(target.value, abort.signal)
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
  () => [props.slug, kind.value, kind.value === 'build' ? deploymentId.value : '', live.value, canFollow.value] as const,
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
      return 'Paused'
    case 'failed':
      return streamError.value || 'The live stream stopped.'
    default:
      return ''
  }
})

function download() {
  const blob = new Blob([lines.value.join('\n') + '\n'], { type: 'text/plain' })
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = `${props.slug}-${kind.value}.log`
  document.body.appendChild(a)
  a.click()
  a.remove()
  setTimeout(() => URL.revokeObjectURL(url), 1000)
}
</script>

<template>
  <div class="grid gap-3">
    <div class="flex flex-wrap items-center gap-2">
      <Tabs v-model="kind">
        <TabsList>
          <TabsTrigger value="runtime">App log</TabsTrigger>
          <TabsTrigger value="build">Build log</TabsTrigger>
        </TabsList>
      </Tabs>

      <Select v-if="kind === 'build' && deployments.data.value?.length" :model-value="deploymentId" @update:model-value="(v) => (chosenDeployment = String(v))">
        <SelectTrigger class="w-44"><SelectValue placeholder="Pick a version" /></SelectTrigger>
        <SelectContent>
          <SelectItem v-for="d in deployments.data.value" :key="d.id" :value="d.id">Version #{{ d.seq }}</SelectItem>
        </SelectContent>
      </Select>

      <div class="relative min-w-40 flex-1 sm:max-w-xs">
        <Search class="pointer-events-none absolute top-2 left-2 size-4 text-muted-foreground" />
        <Input v-model="filter" class="pl-8" placeholder="Filter lines" aria-label="Filter log lines" />
      </div>

      <div class="ml-auto flex items-center gap-2">
        <span class="text-xs text-muted-foreground" aria-live="polite">
          <span v-if="status === 'live'" class="mr-1 inline-block size-1.5 animate-pulse rounded-full bg-success align-middle" />{{ statusText }}
        </span>
        <Button v-if="canFollow" variant="outline" size="sm" @click="live = !live">
          <component :is="live ? Pause : Play" /> {{ live ? 'Pause' : 'Follow live' }}
        </Button>
        <Button variant="outline" size="sm" :disabled="!lines.length" @click="download"><Download /> Download</Button>
      </div>
    </div>

    <div
      ref="scroller"
      class="h-[60vh] min-h-64 overflow-auto rounded-xl border bg-neutral-950 p-3 font-mono text-xs leading-relaxed text-neutral-100"
      role="log"
      tabindex="0"
      :aria-label="kind === 'build' ? 'Build log' : 'App log'"
    >
      <p v-if="noBuildYet" class="text-neutral-400">There is no build log yet. It appears after the first deploy.</p>
      <p v-else-if="!visible.length" class="text-neutral-400">
        {{ filter ? 'No lines match your filter.' : status === 'connecting' ? 'Connecting...' : kind === 'build' ? 'Nothing logged yet.' : 'Nothing logged yet. Anything your app prints shows up here.' }}
      </p>
      <div v-for="(l, i) in visible" :key="i" class="break-words whitespace-pre-wrap">{{ l }}</div>
    </div>
  </div>
</template>
