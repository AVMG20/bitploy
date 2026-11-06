<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useEventListener } from '@vueuse/core'
import { PanelLeftClose, Plus } from 'lucide-vue-next'
import ServiceIcon from '@/components/ServiceIcon.vue'
import AppPanel from '@/components/project/AppPanel.vue'
import ProjectAppCard from '@/components/project/ProjectAppCard.vue'
import { Button } from '@/components/ui/button'
import { useSidebar } from '@/components/ui/sidebar'
import { openAdd } from '@/composables/useAddToProject'
import { useProjectData } from '@/composables/useProjectData'
import { projectRoute } from '@/lib/appLinks'
import { labelSpots, laneLayout, laneWidthNeeded, placeLabels, routeLinks, routesBottom } from '@/lib/canvas'
import type { Box, RoutedLink } from '@/lib/canvas'
import { formatMB } from '@/lib/format'
import { aliasOf, projectHasRole, talksTo, templateLabel, templateRole, usedBy } from '@/lib/projects'
import type { Lane } from '@/lib/projects'

// The canvas: the apps of a project as cards in three lanes (what visitors
// open, what runs in the background, what keeps data), with lines from each
// app to the apps whose variables it reads. A card opens the app's page; its
// eye opens a quick look in a panel over the canvas (?app=<slug>).
const props = defineProps<{ projectId: string }>()
const route = useRoute()
const router = useRouter()
const { list, project, tpls, fallback, apps, links, roleOf } = useProjectData(() => props.projectId)

const LANES: { key: Lane; title: string; text: string }[] = [
  { key: 'public', title: 'Open to visitors', text: 'Has a web address' },
  { key: 'background', title: 'In the background', text: 'Reached only by the other apps' },
  { key: 'data', title: 'Data', text: 'Keeps what your apps store' },
]
const lanes = computed(() =>
  LANES.map((l) => ({ ...l, apps: apps.value.filter((a) => roleOf(a).lane === l.key) })).filter((l) => l.apps.length),
)
/** A line skips a lane: the cards in a lane keep room between them for lines to pass. */
const skips = computed(() => {
  const laneOf = new Map(lanes.value.flatMap((l, i) => l.apps.map((a) => [a.id, i] as const)))
  return links.value.some((l) => Math.abs((laneOf.get(l.from) ?? 0) - (laneOf.get(l.to) ?? 0)) > 1)
})

// ---------------------------------------------------------------- lanes side by side, or one under the other
const sidebar = useSidebar()
/** The board's width inside its padding. */
const inner = ref(0)
const layout = computed(() => (inner.value ? laneLayout(inner.value, lanes.value.length) : { side: lanes.value.length > 1, gap: 80 }))
/** Lanes one under the other only because the sidebar takes the room: offer to hide it. */
const sidebarHint = computed(
  () => !layout.value.side && lanes.value.length > 1 && !sidebar.isMobile.value && sidebar.open.value && inner.value + 208 >= laneWidthNeeded(lanes.value.length),
)

// ---------------------------------------------------------------- the open panel
const selected = computed(() => (typeof route.query.app === 'string' ? route.query.app : ''))
const selectedApp = computed(() => apps.value.find((a) => a.slug === selected.value) ?? null)
/** Closing the panel puts focus back on the eye that opened it, so Tab goes on from there. */
async function closePanel() {
  const id = selectedApp.value?.id
  await router.push({ query: { ...route.query, app: undefined } })
  await nextTick()
  if (id) document.querySelector<HTMLElement>(`[data-app="${CSS.escape(id)}"] a[aria-label^="Quick look"]`)?.focus()
}
// ?app= naming an app of another project opens it there; one that is gone is
// dropped from the address. A list still loading may not have a new app yet.
watch(
  () => [selected.value, project.value, list.data.value, list.isFetching.value] as const,
  ([slug, p, all, fetching]) => {
    if (!slug || !p || !all || fetching || p.apps.some((a) => a.slug === slug)) return
    const home = all.projects.find((x) => x.apps.some((a) => a.slug === slug))
    void router.replace(home ? projectRoute(home.id, { app: slug }) : { query: { ...route.query, app: undefined } })
  },
  { immediate: true },
)

/**
 * The panel opens on the right, over the canvas. When it would cover the
 * selected card there (a narrow screen, the card in the last lane) and the card
 * leaves room on its left, the panel opens on the left instead.
 */
const panelSide = ref<'left' | 'right'>('right')
function placePanel() {
  const id = selectedApp.value?.id
  const card = id ? document.querySelector<HTMLElement>(`[data-app="${CSS.escape(id)}"]`) : null
  const vw = window.innerWidth
  if (!card || vw < 768) {
    panelSide.value = 'right'
    return
  }
  const w = Math.min(544, vw - 16) + 8 + 8
  const r = card.getBoundingClientRect()
  panelSide.value = r.right > vw - w && r.left >= w ? 'left' : 'right'
}
watch(selectedApp, () => void nextTick(placePanel), { immediate: true })
useEventListener(window, 'resize', placePanel)

// ---------------------------------------------------------------- hover: light up an app and what it is linked to
const hovered = ref<string | null>(null)
/** The app whose lines are lit and labelled: the hovered one, else the open one. */
const focus = computed(() => hovered.value ?? selectedApp.value?.id ?? null)
const linkedTo = computed(() => {
  const h = focus.value
  if (!h) return null
  const ids = new Set([h])
  for (const l of links.value) {
    if (l.from === h) ids.add(l.to)
    if (l.to === h) ids.add(l.from)
  }
  return ids
})
const onLine = (p: RoutedLink) => p.from === focus.value || p.to === focus.value

// ---------------------------------------------------------------- the lines behind the cards
const board = ref<HTMLElement>()
const paths = ref<RoutedLink[]>([])
/** The cards and the add button: lines and labels keep clear of them. */
const cards = ref<Box[]>([])
const size = ref({ w: 0, h: 0 })
/** Room added under the lanes when lines run below a lane. */
const below = ref(0)
/** Lines are drawn when the lanes sit side by side; stacked lanes show the names on the cards. */
const routable = ref(false)
function measure() {
  placePanel()
  const el = board.value
  if (!el) return
  const css = getComputedStyle(el)
  const w = el.clientWidth - parseFloat(css.paddingLeft) - parseFloat(css.paddingRight)
  if (w !== inner.value) {
    // The lanes change places first; the lines follow on the next frame.
    inner.value = w
    measureSoon()
    return
  }
  // Board coordinates start inside its border, where the lines' svg starts.
  const box = el.getBoundingClientRect()
  const left = box.left + el.clientLeft
  const top = box.top + el.clientTop
  const tops = new Set([...el.querySelectorAll<HTMLElement>('[data-lane]')].map((s) => Math.round(s.getBoundingClientRect().top)))
  routable.value = layout.value.side && tops.size === 1 && lanes.value.length > 1
  if (!routable.value) {
    paths.value = []
    below.value = 0
    size.value = { w: el.clientWidth, h: el.clientHeight }
    return
  }
  const boxes: Box[] = []
  for (const item of el.querySelectorAll<HTMLElement>('[data-app], [data-keep-clear]')) {
    const r = item.getBoundingClientRect()
    const col = Number(item.closest<HTMLElement>('[data-lane]')?.dataset.lane ?? 0)
    boxes.push({ id: item.dataset.app ?? 'add', col, l: r.left - left, r: r.right - left, t: r.top - top, b: r.bottom - top })
  }
  cards.value = boxes
  // A project with very many lines gets the best routes found in 120ms.
  paths.value = routeLinks(boxes, links.value, { width: el.clientWidth, budget: 120 })
  // The board without the added room, then the room the lowest line needs.
  const base = el.clientHeight - below.value
  below.value = Math.max(0, Math.ceil(routesBottom(paths.value) + 16 - base))
  size.value = { w: el.clientWidth, h: base + below.value }
}
/** Many lines: at rest they stay faint, and the hovered or open app's lines stand out. */
const dense = computed(() => paths.value.length > 10)
/** The lit lines last, so they run on top of the others. */
const drawn = computed(() => (focus.value ? [...paths.value].sort((a, b) => Number(onLine(a)) - Number(onLine(b))) : paths.value))
let frame = 0
function measureSoon() {
  cancelAnimationFrame(frame)
  frame = requestAnimationFrame(measure)
}
const labels = computed(() => {
  const f = focus.value
  if (!f) return []
  const spots = labelSpots(paths.value, f, (p) => links.value.find((x) => x.from === p.from && x.to === p.to)?.vars.join(', ') ?? '')
  // A label wraps where a card starts and is left out where there is no room; the card and the panel name every variable.
  return placeLabels(spots, cards.value, size.value)
})

let ro: ResizeObserver | undefined
onMounted(() => {
  ro = new ResizeObserver(measureSoon)
  if (board.value) ro.observe(board.value)
})
watch(board, (el, old) => {
  if (old) ro?.unobserve(old)
  if (el) ro?.observe(el)
})
onBeforeUnmount(() => {
  ro?.disconnect()
  cancelAnimationFrame(frame)
})
watch([links, apps, lanes, layout], () => void nextTick(measureSoon), { deep: true })

// ---------------------------------------------------------------- one app: what to add next
// A compose file that brings its own database (or cache, or search) gets no suggestion to add another.
const quickPicks = computed(() => {
  const want = ['postgres', 'valkey', 'meilisearch']
  return want
    .map((id) => tpls.value.find((t) => t.id === id))
    .filter((t) => !!t)
    .filter((t) => !projectHasRole(apps.value, templateRole(t), tpls.value))
})
</script>

<template>
  <div v-if="project" class="grid grid-cols-[minmax(0,1fr)] gap-6">
    <!-- An empty project -->
    <div v-if="!apps.length" class="canvas-dots grid place-items-center rounded-xl border px-6 py-16">
      <div class="grid max-w-md justify-items-center gap-3 text-center">
        <span class="flex size-11 items-center justify-center rounded-xl border bg-card text-muted-foreground shadow-card"><Plus class="size-5" /></span>
        <h2 class="text-lg font-semibold">Add the first app</h2>
        <p class="text-sm text-muted-foreground">Bring your code from GitHub, an upload or a Dockerfile, or start with a database.</p>
        <Button @click="openAdd(project.id)"><Plus /> Add to {{ project.name }}</Button>
      </div>
    </div>

    <!-- One app: the app, and what a project can hold besides it -->
    <div v-else-if="apps.length === 1" class="canvas-dots grid gap-5 rounded-xl border p-4 md:grid-cols-[minmax(0,26rem)_minmax(0,1fr)] md:p-8">
      <div class="grid content-start gap-3">
        <ProjectAppCard :app="apps[0]!" :role="roleOf(apps[0]!)" :templates="tpls" :talks-to="[]" :used-by="[]" :selected="selected === apps[0]!.slug" />
      </div>

      <div v-if="!fallback" class="grid content-start gap-3 rounded-xl border border-dashed bg-background/60 p-5">
        <div>
          <h2 class="font-medium">Add to {{ project.name }}</h2>
          <p class="mt-1 text-sm text-muted-foreground">
            <template v-if="quickPicks.some((t) => templateRole(t) === 'database')">Add a database and {{ aliasOf(apps[0]!) }} gets its address as a variable, or add</template><template v-else>Add</template>
            an API or a worker from another repository.
          </p>
        </div>
        <div v-if="quickPicks.length" class="grid gap-2 sm:grid-cols-3">
          <button
            v-for="t in quickPicks"
            :key="t.id"
            type="button"
            class="group grid content-start gap-1.5 rounded-lg border bg-card p-2.5 text-left text-sm transition-colors hover:border-foreground/25"
            @click="openAdd(project.id, t.id)"
          >
            <ServiceIcon :role="templateRole(t)" size="sm" />
            <span class="min-w-0">
              <span class="block font-medium break-words">{{ t.name }}</span>
              <span class="block text-xs text-muted-foreground">{{ templateLabel(t) }}, {{ formatMB(t.memory_mb) }}</span>
            </span>
          </button>
        </div>
        <div>
          <Button variant="outline" @click="openAdd(project.id)"><Plus /> Add an app or a database</Button>
        </div>
      </div>
    </div>

    <!-- Several apps: the board -->
    <div v-else ref="board" class="canvas-dots relative overflow-hidden rounded-xl border p-4 md:p-6 xl:p-8">
      <svg
        v-if="routable"
        class="pointer-events-none absolute inset-0"
        :width="size.w"
        :height="size.h"
        :viewBox="`0 0 ${size.w} ${size.h}`"
        aria-hidden="true"
        data-testid="canvas-lines"
      >
        <g v-for="p in drawn" :key="p.key">
          <path
            :d="p.d"
            fill="none"
            :class="[
              'transition-[stroke,opacity] duration-150',
              linkedTo && onLine(p) ? 'link-flow stroke-primary opacity-100' : dense ? 'stroke-foreground/20' : 'stroke-foreground/35',
              linkedTo && !onLine(p) && 'opacity-30',
            ]"
            stroke-width="1.5"
            stroke-linecap="round"
            stroke-linejoin="round"
          />
          <circle
            :cx="p.end.x"
            :cy="p.end.y"
            r="3"
            :class="linkedTo && onLine(p) ? 'fill-background stroke-primary' : ['fill-background', dense ? 'stroke-foreground/25' : 'stroke-foreground/40', linkedTo && 'opacity-30']"
            stroke-width="1.5"
          />
        </g>
      </svg>

      <!-- Lanes too narrow to sit side by side: one under the other, and no lines. -->
      <p v-if="sidebarHint" class="relative mb-5 flex flex-wrap items-center justify-between gap-x-4 gap-y-2 rounded-lg border bg-card/80 px-3 py-2 text-sm text-muted-foreground">
        The lines between the apps show when the lanes fit side by side.
        <Button variant="outline" size="sm" @click="sidebar.setOpen(false)"><PanelLeftClose /> Hide the sidebar</Button>
      </p>
      <div
        class="relative grid gap-6"
        :style="layout.side ? { gridTemplateColumns: `repeat(${lanes.length}, minmax(0, 1fr))`, columnGap: `${layout.gap}px` } : { gridTemplateColumns: 'minmax(0, 1fr)' }"
      >
        <section v-for="(l, i) in lanes" :key="l.key" :data-lane="i" :class="['grid content-start', layout.side && skips ? 'gap-8' : 'gap-3']" :aria-labelledby="`lane-${l.key}`">
          <div class="px-1">
            <h2 :id="`lane-${l.key}`" class="truncate text-xs font-medium text-foreground/80">{{ l.title }}</h2>
            <p class="truncate text-[11px] text-muted-foreground">{{ l.text }}</p>
          </div>
          <!-- One under the other, a lane's cards fill the row. -->
          <div :class="layout.side ? 'contents' : 'grid grid-cols-[repeat(auto-fill,minmax(min(100%,16rem),1fr))] gap-3'">
          <ProjectAppCard
            v-for="a in l.apps"
            :key="a.id"
            :app="a"
            :role="roleOf(a)"
            :templates="tpls"
            :talks-to="talksTo(a, apps, links)"
            :used-by="usedBy(a, apps, links)"
            :selected="a.slug === selected"
            :lit="!!linkedTo && linkedTo.has(a.id) && focus !== a.id"
            :dim="!!linkedTo && !linkedTo.has(a.id)"
            @hover="(id) => (hovered = id)"
          />
          <button
            v-if="i === lanes.length - 1 && !fallback"
            type="button"
            data-keep-clear
            class="flex items-center justify-center gap-2 rounded-xl border border-dashed bg-background/50 px-4 py-5 text-sm text-muted-foreground transition-colors hover:border-foreground/30 hover:text-foreground"
            @click="openAdd(project.id)"
          >
            <Plus class="size-4" /> Add an app or a database
          </button>
          </div>
        </section>
      </div>
      <!-- Room for the lines that run below a lane. -->
      <div v-if="below" :style="{ height: `${below}px` }" />
      <!-- The variables each line carries, on the lines of the hovered or open app. -->
      <div v-if="routable" class="pointer-events-none absolute inset-0 z-20" aria-hidden="true">
        <span
          v-for="lb in labels"
          :key="`${lb.key}-label`"
          class="absolute overflow-hidden rounded-md border border-primary/40 bg-card px-1 py-px font-mono text-[10.5px] leading-4 text-foreground shadow-card"
          :style="{ left: `${lb.left}px`, top: `${lb.top}px`, width: `${lb.width}px`, height: `${lb.height}px` }"
        ><span v-for="(t, k) in lb.lines" :key="k" class="block whitespace-nowrap">{{ t }}</span></span>
      </div>
    </div>

    <AppPanel v-if="selectedApp" :slug="selectedApp.slug" :project-id="project.id" :side="panelSide" @close="closePanel" />
  </div>
</template>
