import { onBeforeUnmount, onMounted, readonly, ref } from 'vue'
import { useQueryClient } from '@tanstack/vue-query'
import type { QueryClient } from '@tanstack/vue-query'
import { effectsOf, EVENT_TYPES, keys, mergeLines, parseEvent } from '@/lib/live'
import type { LiveEvent } from '@/lib/live'
import type { Deployment } from '@/lib/types'

// One live stream per signed-in user (GET /api/v1/events). Each event is a
// small hint ("this changed"); we answer by refetching the matching queries.
// Polling stays as the fallback: see liveMs() in queries.ts.

const STREAM_URL = '/api/v1/events'
const MAX_LOG_LINES = 300
const KEEP_LOGS = 4

const connected = ref(false)
/** True while the event stream is open. */
export const eventsConnected = readonly(connected)

/** Live build log lines per deployment id, newest last (only while the stream is open). */
const logs = ref<Record<string, string[]>>({})
export const liveBuildLogs = readonly(logs)

function addBuildLog(qc: QueryClient, ev: Extract<LiveEvent, { type: 'build_log' }>) {
  let base = logs.value[ev.id]
  if (!base) {
    // First lines we see of this build: start from the log tail the page already has.
    const list = qc.getQueryData<Deployment[]>(keys.deployments(ev.app))
    base = list?.find((d) => d.id === ev.id)?.log_tail ?? []
  }
  const next = { ...logs.value, [ev.id]: mergeLines(base, ev.lines, MAX_LOG_LINES) }
  const ids = Object.keys(next)
  for (const id of ids.slice(0, Math.max(0, ids.length - KEEP_LOGS))) delete next[id]
  logs.value = next
}

function handle(qc: QueryClient, ev: LiveEvent) {
  if (ev.type === 'hello') return
  if (ev.type === 'build_log') {
    addBuildLog(qc, ev)
    return
  }
  const fx = effectsOf(ev)
  if (fx.all) {
    void qc.invalidateQueries()
    return
  }
  for (const key of fx.remove) qc.removeQueries({ queryKey: key })
  for (const key of fx.invalidate) void qc.invalidateQueries({ queryKey: key })
}

/**
 * Opens the event stream and keeps it open. Call it once, in the layout that
 * only signed-in users see: leaving the layout (logout) closes the stream.
 */
export function useEvents() {
  const qc = useQueryClient()
  let source: EventSource | null = null
  let retry: number | null = null
  let delay = 2_000
  let hadStream = false
  let stopped = false

  function open() {
    if (stopped || typeof EventSource === 'undefined') return
    source?.close()
    const es = new EventSource(STREAM_URL)
    source = es
    for (const type of EVENT_TYPES) {
      es.addEventListener(type, (e) => {
        const ev = parseEvent((e as MessageEvent<string>).data)
        if (!ev) return
        if (ev.type === 'hello') {
          delay = 2_000
          connected.value = true
          // Events may have been missed while we were away: refetch what is on screen once.
          if (hadStream) void qc.invalidateQueries()
          hadStream = true
        }
        handle(qc, ev)
      })
    }
    es.onerror = () => {
      if (connected.value) {
        connected.value = false
        // Back to fast polling at once, and catch up on what we missed.
        void qc.invalidateQueries()
      }
      // The browser reconnects by itself after a dropped connection, but gives up
      // on an HTTP error (a restart behind the proxy answers 502): retry ourselves.
      if (es.readyState === EventSource.CLOSED && !stopped) {
        retry = window.setTimeout(open, delay)
        delay = Math.min(delay * 2, 30_000)
      }
    }
  }

  function stop() {
    stopped = true
    if (retry !== null) window.clearTimeout(retry)
    source?.close()
    source = null
    connected.value = false
    logs.value = {}
  }

  onMounted(open)
  onBeforeUnmount(stop)
  return { connected: eventsConnected }
}
