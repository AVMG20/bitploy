import { computed, ref, toValue, watch } from 'vue'
import type { MaybeRefOrGetter } from 'vue'
import { variableNeedsDeploy } from '@/lib/buildVars'
import type { App, EnvVar } from '@/lib/types'

/**
 * Changes that only apply when the app starts again: environment variables, its
 * size, turning on saved files. The server does not say whether the running
 * container is out of date, so this remembers, per app and in this browser,
 * which changes were saved while which container ran. A new container (a
 * restart, a deploy, a start) clears it, so there are no clocks to compare.
 * Variables changed elsewhere (an AI assistant, the API) are caught from their
 * own timestamps.
 */
// build: a variable the live version's build read changed, so it needs a new build (a deploy), not only a restart.
export type RestartReason = 'variables' | 'size' | 'storage' | 'build'

interface Entry {
  /** started_at of the container that was running when the change was saved. */
  started: string
  what: RestartReason[]
}

const KEY = 'bitploy.restart-needed'

function load(): Record<string, Entry> {
  try {
    const v = JSON.parse(localStorage.getItem(KEY) ?? '{}') as unknown
    return v && typeof v === 'object' ? (v as Record<string, Entry>) : {}
  } catch {
    return {}
  }
}

const store = ref<Record<string, Entry>>(load())

watch(
  store,
  (v) => {
    try {
      localStorage.setItem(KEY, JSON.stringify(v))
    } catch {
      // Not remembered across reloads; still shown on this page.
    }
  },
  { deep: true },
)

/** The app runs a container that a restart would replace. */
function live(a: App | null | undefined): a is App & { started_at: string } {
  return !!a && !!a.current_deployment && a.desired_state === 'running' && a.status === 'running' && !!a.started_at
}

/** Remember that a change of `what` waits for a restart. Does nothing when the app is not running: it applies when it starts. */
export function markRestartNeeded(a: App | null | undefined, what: RestartReason) {
  if (!live(a)) return
  const cur = store.value[a.id]
  const what0 = cur && cur.started === a.started_at ? cur.what : []
  store.value = { ...store.value, [a.id]: { started: a.started_at, what: what0.includes(what) ? what0 : [...what0, what] } }
}

export function clearRestartNeeded(appId: string | undefined) {
  if (!appId || !store.value[appId]) return
  const next = { ...store.value }
  delete next[appId]
  store.value = next
}

const LABELS: Record<RestartReason, string> = {
  variables: 'environment variables',
  size: 'size',
  storage: 'storage',
  build: 'build variables',
}

/** What waits for a restart of this app, in words; empty when nothing does. */
export function useRestartNeeded(app: MaybeRefOrGetter<App | null | undefined>, env?: MaybeRefOrGetter<EnvVar[] | undefined>) {
  const reasons = computed<RestartReason[]>(() => {
    const a = toValue(app)
    if (!live(a)) return []
    const out = new Set<RestartReason>()
    const e = store.value[a.id]
    if (e && e.started === a.started_at) e.what.forEach((w) => out.add(w))
    const started = new Date(a.started_at).getTime()
    for (const v of toValue(env) ?? []) {
      if (new Date(v.updated_at).getTime() <= started) continue
      out.add(variableNeedsDeploy(a, v.name) ? 'build' : 'variables')
    }
    return [...out]
  })

  // A new container took the changes: forget them.
  watch(
    () => toValue(app),
    (a) => {
      const e = a ? store.value[a.id] : undefined
      if (a && e && a.started_at && a.started_at !== e.started) clearRestartNeeded(a.id)
    },
    { immediate: true },
  )

  const text = computed(() => {
    const words = reasons.value.map((r) => LABELS[r])
    if (!words.length) return ''
    return words.length === 1 ? words[0] : `${words.slice(0, -1).join(', ')} and ${words[words.length - 1]}`
  })
  /** A new build is needed (a deploy), which also restarts: the banner offers that instead of a restart. */
  const needsBuild = computed(() => reasons.value.includes('build'))
  return { reasons, text, needsBuild }
}
