<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'
import { useResizeObserver } from '@vueuse/core'
import { Play, RotateCw, SquareTerminal } from 'lucide-vue-next'
import type { FitAddon } from '@xterm/addon-fit'
import type { Terminal } from '@xterm/xterm'
import EmptyState from '@/components/EmptyState.vue'
import Toolbar from '@/components/Toolbar.vue'
import { Button } from '@/components/ui/button'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { useApp } from '@/composables/queries'
import { useAppActions } from '@/composables/useAppActions'
import { useQueryParam } from '@/composables/useQueryParam'
import { useSession } from '@/composables/useSession'
import { isMulti, publicService, serviceRole, sortServices } from '@/lib/services'
import {
  CONNECTING,
  nextTerminalState,
  parseServerMessage,
  resizeMessage,
  TERMINAL_THEME,
  terminalAbout,
  terminalOffered,
  terminalStatusText,
  terminalUrl,
} from '@/lib/terminal'
import type { TerminalEvent, TerminalState } from '@/lib/terminal'

const props = defineProps<{ slug: string }>()

const app = useApp(() => props.slug)
const data = computed(() => app.data.value ?? null)
const { user } = useSession()
const offered = computed(() => terminalOffered(data.value, user.value?.id ?? ''))
const actions = useAppActions(() => props.slug)

// An app with several services: whose container. The default is the one
// visitors reach. ?svc=, as on the Logs tab.
const multi = computed(() => isMulti(data.value))
const services = computed(() => sortServices(data.value?.services ?? []))
const picked = useQueryParam('svc', '')
const defaultService = computed(() => (data.value ? ((publicService(data.value) ?? services.value[0])?.name ?? '') : ''))
const service = computed(() => (multi.value ? picked.value || defaultService.value : ''))

// No container at all: never deployed, or stopped. Anything else (crashed,
// a redeploy under way) is for bitploy to say.
const noContainer = computed(() => !!data.value && (!data.value.current_deployment || data.value.status === 'stopped' || data.value.status === 'new'))
const canStart = computed(() => !!data.value?.current_deployment && ['stopped', 'crashed', 'failed'].includes(data.value.status))

const state = ref<TerminalState>(CONNECTING)
const phase = computed(() => (noContainer.value ? 'not_running' : state.value.phase))
const statusText = computed(() => terminalStatusText(state.value))
// Opened in this attempt: a failure before that shows on top of the empty terminal.
const opened = ref(false)
const loadError = ref('')

const about = computed(() => terminalAbout(data.value, service.value))
const notRunningText = computed(() => {
  if (!data.value?.current_deployment) {
    return data.value?.status === 'deploying'
      ? 'The first deploy is under way. The terminal opens here once the app runs.'
      : 'The terminal opens in the running container. Deploy the app first.'
  }
  if (canStart.value) return 'Start it to open a terminal in its container.'
  return state.value.message || 'The terminal opens in the running container.'
})

// ---------------------------------------------------------------- xterm.js, loaded with the tab
const host = ref<HTMLElement | null>(null)
let term: Terminal | null = null
let fit: FitAddon | null = null
let ws: WebSocket | null = null
let unmounted = false
const encoder = new TextEncoder()

const FONT = "'JetBrains Mono Variable', ui-monospace, SFMono-Regular, Menlo, Consolas, monospace"

async function setup() {
  let mods
  try {
    mods = await Promise.all([import('@xterm/xterm'), import('@xterm/addon-fit'), import('@xterm/xterm/css/xterm.css')])
    // xterm.js measures the font once; it has to be there first.
    await document.fonts?.load(`13px 'JetBrains Mono Variable'`).catch(() => undefined)
  } catch {
    loadError.value = 'The terminal could not load. Reload the page to try again.'
    return
  }
  if (unmounted || !host.value) return
  const [{ Terminal }, { FitAddon }] = mods
  term = new Terminal({ theme: TERMINAL_THEME, fontFamily: FONT, fontSize: 13, lineHeight: 1.2, cursorBlink: true, scrollback: 5000 })
  fit = new FitAddon()
  term.loadAddon(fit)
  term.open(host.value)
  fit.fit()
  term.onData((d) => sendInput(encoder.encode(d)))
  term.onBinary((d) => sendInput(Uint8Array.from(d, (c) => c.charCodeAt(0) & 0xff)))
  term.onResize(({ cols, rows }) => {
    if (ws?.readyState === WebSocket.OPEN && state.value.phase === 'open') ws.send(resizeMessage(cols, rows))
  })
  connect()
}

function sendInput(b: Uint8Array<ArrayBuffer>) {
  if (ws?.readyState === WebSocket.OPEN && state.value.phase === 'open') ws.send(b)
}

/** A grey line in the terminal after the shell's own output; the cursor goes (reset() brings it back). */
function note(text: string) {
  term?.write(`\r\n\x1b[90m${text}\x1b[0m\r\n\x1b[?25l`)
}

function apply(e: TerminalEvent) {
  const before = state.value.phase
  state.value = nextTerminalState(state.value, e)
  const now = state.value.phase
  if (before === 'open' && now !== 'open') note(now === 'not_running' ? state.value.message : terminalStatusText(state.value))
}

function disconnect() {
  const s = ws
  ws = null
  if (s) {
    s.onmessage = null
    s.onclose = null
    s.close(1000)
  }
}

function connect() {
  disconnect()
  if (!term || !offered.value || noContainer.value) return
  state.value = CONNECTING
  opened.value = false
  term.reset()
  fit?.fit()
  const s = new WebSocket(terminalUrl(location, props.slug, { cols: term.cols, rows: term.rows, service: service.value }))
  s.binaryType = 'arraybuffer'
  ws = s
  s.onmessage = (e: MessageEvent<string | ArrayBuffer>) => {
    if (typeof e.data !== 'string') {
      term?.write(new Uint8Array(e.data))
      return
    }
    const m = parseServerMessage(e.data)
    if (!m) return
    apply({ kind: 'message', message: m })
    if (m.type === 'ready' && term) {
      opened.value = true
      // The size may have changed while it opened.
      s.send(resizeMessage(term.cols, term.rows))
      term.focus()
    }
  }
  s.onclose = () => {
    if (ws === s) ws = null
    apply({ kind: 'close' })
  }
}

onMounted(() => void setup())
onBeforeUnmount(() => {
  unmounted = true
  disconnect()
  term?.dispose()
  term = null
})

// The box follows the window; xterm.js then tells the shell its new size.
let frame = 0
useResizeObserver(host, () => {
  cancelAnimationFrame(frame)
  frame = requestAnimationFrame(() => fit?.fit())
})

// Another app or service opens a new shell. A stopped app closes it; once
// it runs again, one opens.
watch([() => props.slug, service, offered], () => connect())
watch(noContainer, (none) => {
  if (none) {
    disconnect()
    state.value = { phase: 'not_running', message: '', hint: '', code: null }
  } else connect()
})
watch(
  () => data.value?.status,
  (s) => {
    if (s === 'running' && state.value.phase === 'not_running') connect()
  },
)
</script>

<template>
  <div class="grid grid-cols-[minmax(0,1fr)] gap-3">
    <EmptyState
      v-if="data && !offered"
      :icon="SquareTerminal"
      :title="data.static ? 'A static site has no terminal.' : 'Only the app\'s owner and the project\'s members can open a terminal.'"
      :description="data.static ? 'bitploy serves its files itself; there is no container of its own to open a shell in.' : undefined"
    />
    <template v-else>
      <Toolbar>
        <Select v-if="multi && services.length" :model-value="service" @update:model-value="(v) => (picked = String(v) === defaultService ? '' : String(v))">
          <SelectTrigger class="w-48" aria-label="Service"><SelectValue placeholder="Pick a service" /></SelectTrigger>
          <SelectContent>
            <SelectItem v-for="s in services" :key="s.name" :value="s.name">
              {{ serviceRole(s).label }} <span class="font-mono text-xs text-muted-foreground">{{ s.name }}</span>
            </SelectItem>
          </SelectContent>
        </Select>
        <p class="min-w-0 flex-[1_1_16rem] text-sm text-muted-foreground">{{ about }}</p>
        <div v-if="phase !== 'not_running'" class="ml-auto flex min-h-8 flex-wrap items-center justify-end gap-2">
          <span class="text-xs text-muted-foreground" aria-live="polite" data-testid="terminal-status">
            <span v-if="phase === 'open'" class="mr-1 inline-block size-1.5 rounded-full bg-success align-middle" />{{ statusText }}
          </span>
          <Button v-if="phase === 'closed' || phase === 'exited' || phase === 'failed'" variant="outline" @click="connect">
            <RotateCw /> {{ phase === 'exited' ? 'Open a new shell' : 'Reconnect' }}
          </Button>
        </div>
      </Toolbar>

      <EmptyState v-if="phase === 'not_running'" :icon="SquareTerminal" :title="multi ? 'This service isn\'t running.' : 'The app isn\'t running.'" :description="notRunningText">
        <Button v-if="canStart" :disabled="actions.start.isPending.value" @click="actions.start.mutate()">
          <Play /> {{ actions.start.isPending.value ? 'Starting...' : 'Start app' }}
        </Button>
        <Button as-child variant="outline"><RouterLink :to="{ name: 'app', params: { slug } }">Go to Overview</RouterLink></Button>
      </EmptyState>

      <div
        v-show="phase !== 'not_running'"
        class="relative h-[60vh] min-h-64 overflow-hidden rounded-xl border p-2"
        :style="{ background: TERMINAL_THEME.background }"
        data-testid="terminal"
      >
        <div ref="host" class="size-full" />
        <div
          v-if="loadError || (!opened && (phase === 'failed' || phase === 'closed'))"
          class="absolute inset-0 flex flex-col items-center justify-center gap-1 px-6 text-center"
          role="alert"
        >
          <p class="text-sm font-medium text-[#e8e8e8]">{{ loadError || state.message }}</p>
          <p v-if="!loadError && state.hint" class="max-w-md text-sm text-[#8c8c8c]">{{ state.hint }}</p>
        </div>
      </div>
    </template>
  </div>
</template>

<style scoped>
/* xterm.js draws its own scrollbar area; keep it on the terminal's background. */
:deep(.xterm .xterm-viewport) {
  background-color: transparent !important;
}
</style>
