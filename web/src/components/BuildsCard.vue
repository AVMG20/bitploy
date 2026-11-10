<script setup lang="ts">
// The builds that wait or run, above the account menu in the sidebar
// (desktop only). Hidden while nothing builds; each row opens the app's
// Deployments tab. Only your own builds (and those of projects shared with
// you) show; a build that waits says how many of the server's builds, of
// anyone, go before it.
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'
import { Clock, Hammer, LoaderCircle } from 'lucide-vue-next'
import { useSidebar } from '@/components/ui/sidebar'
import { useBuilds } from '@/composables/queries'
import { deploymentStateInfo } from '@/lib/format'
import type { ActiveBuild } from '@/lib/types'
import { cn } from '@/lib/utils'

const MAX_ROWS = 4

const { isMobile, state, setOpenMobile } = useSidebar()
const builds = useBuilds(() => !isMobile.value)
const data = computed(() => builds.data.value)
const running = computed(() => data.value?.running ?? [])
const queued = computed(() => data.value?.queued ?? [])
const total = computed(() => running.value.length + queued.value.length)
const visible = computed(() => !isMobile.value && state.value === 'expanded' && total.value > 0)

// Running first, then the line; past MAX_ROWS the rest is "and N more".
const rows = computed(() => [...running.value, ...queued.value].slice(0, MAX_ROWS))
const hidden = computed(() => total.value - rows.value.length)

const summary = computed(() => {
  const parts: string[] = []
  if (running.value.length) parts.push(`${running.value.length} running`)
  if (queued.value.length) parts.push(`${queued.value.length} waiting`)
  return parts.join(' · ')
})

// A clock of its own, every second, only while the card shows.
const now = ref(Date.now())
let timer: number | null = null
watch(
  visible,
  (on) => {
    if (on && timer === null) {
      now.value = Date.now()
      timer = window.setInterval(() => (now.value = Date.now()), 1_000)
    } else if (!on && timer !== null) {
      window.clearInterval(timer)
      timer = null
    }
  },
  { immediate: true },
)
onBeforeUnmount(() => timer !== null && window.clearInterval(timer))

function elapsed(b: ActiveBuild): string {
  const from = Date.parse(b.started_at ?? b.queued_at)
  const s = Math.max(0, Math.floor((now.value - from) / 1000))
  const h = Math.floor(s / 3600)
  const m = Math.floor((s % 3600) / 60)
  const ss = String(s % 60).padStart(2, '0')
  return h ? `${h}:${String(m).padStart(2, '0')}:${ss}` : `${m}:${ss}`
}

function label(b: ActiveBuild): string {
  if (b.state !== 'queued') return deploymentStateInfo(b.state).label
  // Behind the app's own build: it starts when that one is done.
  if (running.value.some((r) => r.app.id === b.app.id)) return 'Up after this'
  if (!b.position) return 'Waiting'
  return b.position === 1 ? 'Next up' : `${b.position - 1} ahead`
}

function hint(b: ActiveBuild): string {
  const what = b.commit_message?.split('\n')[0] || `${b.app.name}: version ${b.seq}`
  if (b.state !== 'queued' || !b.position || running.value.some((r) => r.app.id === b.app.id)) return what
  const ahead = b.position - 1
  return `${what}\n${ahead ? `${ahead} ${ahead === 1 ? 'build waits' : 'builds wait'} before this one on the server` : 'Starts as soon as a build on the server finishes'}`
}

function link(b: ActiveBuild) {
  return { name: 'app-deployments', params: { projectId: b.app.project_id || b.app.id, slug: b.app.slug } }
}
</script>

<template>
  <Transition
    enter-active-class="transition duration-200 ease-out"
    enter-from-class="translate-y-1 opacity-0"
    leave-active-class="transition duration-150 ease-in"
    leave-to-class="translate-y-1 opacity-0"
  >
    <section v-if="visible" aria-label="Builds" class="rounded-lg border bg-card p-2.5 text-xs shadow-card" data-testid="builds-card">
      <header class="mb-2 flex items-center gap-2">
        <span :class="cn('grid size-5 shrink-0 place-items-center rounded-md', running.length ? 'bg-foreground/10 text-foreground' : 'bg-muted text-muted-foreground')">
          <Hammer :class="cn('size-3', running.length && 'builds-swing')" aria-hidden="true" />
        </span>
        <span class="min-w-0 flex-1 truncate font-medium text-foreground">Builds</span>
        <span class="sr-only">{{ summary }}</span>
        <span v-if="running.length" class="shrink-0 text-muted-foreground tabular-nums" aria-hidden="true">{{ running.length }}</span>
        <span
          v-if="queued.length"
          class="inline-flex shrink-0 items-center gap-1 rounded-full border border-dashed px-1.5 py-px text-[11px] text-muted-foreground tabular-nums"
          aria-hidden="true"
        >
          <Clock class="size-2.5" />+{{ queued.length }} in line
        </span>
      </header>
      <ul class="grid gap-0.5">
        <li v-for="b in rows" :key="b.id">
          <RouterLink
            :to="link(b)"
            class="-mx-1 flex items-center gap-2 rounded-md px-1 py-1 transition-colors hover:bg-accent focus-visible:bg-accent focus-visible:outline-none"
            :title="hint(b)"
            @click="setOpenMobile(false)"
          >
            <Clock v-if="b.state === 'queued'" class="size-3 shrink-0 text-muted-foreground" aria-hidden="true" />
            <LoaderCircle v-else class="size-3 shrink-0 animate-spin text-foreground/70 [animation-duration:1.4s] motion-reduce:animate-none" aria-hidden="true" />
            <span class="min-w-0 flex-1 truncate font-medium text-sidebar-accent-foreground">{{ b.app.name }}</span>
            <span :class="cn('max-w-[45%] truncate', b.state === 'queued' ? 'text-muted-foreground' : 'text-foreground/80')">{{ label(b) }}</span>
            <span class="min-w-8 shrink-0 text-right font-mono text-[11px] text-muted-foreground tabular-nums">{{ elapsed(b) }}</span>
          </RouterLink>
        </li>
      </ul>
      <p v-if="hidden" class="mt-1 text-muted-foreground">and {{ hidden }} more</p>
    </section>
  </Transition>
</template>

<style scoped>
/* The hammer taps while something builds; still when motion is reduced. */
@keyframes builds-swing {
  0%, 60%, 100% { transform: rotate(0deg); }
  20% { transform: rotate(-22deg); }
  40% { transform: rotate(8deg); }
}
.builds-swing {
  transform-origin: 80% 80%;
  animation: builds-swing 1.2s ease-in-out infinite;
}
@media (prefers-reduced-motion: reduce) {
  .builds-swing { animation: none; }
}
</style>
