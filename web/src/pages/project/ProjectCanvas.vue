<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { Plus } from 'lucide-vue-next'
import ServiceIcon from '@/components/ServiceIcon.vue'
import AppPanel from '@/components/project/AppPanel.vue'
import ProjectAppCard from '@/components/project/ProjectAppCard.vue'
import { Button } from '@/components/ui/button'
import { openAdd } from '@/composables/useAddToProject'
import { useProjectData } from '@/composables/useProjectData'
import { projectRoute } from '@/lib/appLinks'
import { labelRoom, routeLinks, spreadLabels } from '@/lib/canvas'
import type { Box, RoutedLink } from '@/lib/canvas'
import { formatMB } from '@/lib/format'
import { aliasOf, talksTo, templateLabel, templateRole, usedBy } from '@/lib/projects'
import type { Lane } from '@/lib/projects'

// The canvas: the apps of a project as cards in three lanes (what visitors
// open, what runs in the background, what keeps data), with lines from each
// app to the apps whose variables it reads. A card opens the app's panel over
// the canvas; the address keeps it (?app=<slug>).
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

// ---------------------------------------------------------------- the open panel
const selected = computed(() => (typeof route.query.app === 'string' ? route.query.app : ''))
const selectedApp = computed(() => apps.value.find((a) => a.slug === selected.value) ?? null)
function closePanel() {
  void router.push({ query: { ...route.query, app: undefined } })
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
const cards = ref<Box[]>([])
const size = ref({ w: 0, h: 0 })
/** Lines are drawn when the lanes sit side by side; stacked lanes show the names on the cards. */
const routable = ref(false)
function measure() {
  const el = board.value
  if (!el) return
  const box = el.getBoundingClientRect()
  size.value = { w: box.width, h: box.height }
  const tops = new Set([...el.querySelectorAll<HTMLElement>('[data-lane]')].map((s) => Math.round(s.getBoundingClientRect().top)))
  routable.value = tops.size === 1 && lanes.value.length > 1
  if (!routable.value) {
    paths.value = []
    return
  }
  const boxes: Box[] = []
  for (const card of el.querySelectorAll<HTMLElement>('[data-app]')) {
    const r = card.getBoundingClientRect()
    const col = Number(card.closest<HTMLElement>('[data-lane]')?.dataset.lane ?? 0)
    boxes.push({ id: card.dataset.app!, col, l: r.left - box.left, r: r.right - box.left, t: r.top - box.top, b: r.bottom - box.top })
  }
  cards.value = boxes
  paths.value = routeLinks(boxes, links.value)
}
const labels = computed(() => {
  const f = focus.value
  if (!f) return []
  const shown = paths.value.flatMap((p) => {
    if (!onLine(p)) return []
    const l = links.value.find((x) => x.from === p.from && x.to === p.to)
    return [{ key: p.key, x: p.endSide === 'l' ? p.end.x - 6 : p.end.x + 6, y: p.end.y, side: p.endSide, text: l?.vars.join(', ') ?? '' }]
  })
  // A label is cut short where a card starts, so it never covers one; the card and the panel name every variable.
  return spreadLabels(shown, 22).map((lb) => ({ ...lb, room: Math.max(32, labelRoom(lb.x, lb.y, lb.side, cards.value, size.value.w) - 4) }))
})

let ro: ResizeObserver | undefined
onMounted(() => {
  ro = new ResizeObserver(() => measure())
  if (board.value) ro.observe(board.value)
})
watch(board, (el, old) => {
  if (old) ro?.unobserve(old)
  if (el) ro?.observe(el)
})
onBeforeUnmount(() => ro?.disconnect())
watch([links, apps, lanes], () => void nextTick(measure), { deep: true })

// ---------------------------------------------------------------- one app: what to add next
const quickPicks = computed(() => {
  const want = ['postgres', 'valkey', 'meilisearch']
  return want.map((id) => tpls.value.find((t) => t.id === id)).filter((t) => !!t)
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
            A project holds the apps and databases of one product. Add a database and {{ aliasOf(apps[0]!) }} gets its address as a variable, or add an API or a worker from another repository.
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
    <div v-else ref="board" class="canvas-dots relative overflow-hidden rounded-xl border p-4 md:p-6 lg:p-8">
      <svg
        v-if="routable"
        class="pointer-events-none absolute inset-0"
        :width="size.w"
        :height="size.h"
        :viewBox="`0 0 ${size.w} ${size.h}`"
        aria-hidden="true"
        data-testid="canvas-lines"
      >
        <g v-for="p in paths" :key="p.key">
          <path
            :d="p.d"
            fill="none"
            :class="[
              'transition-[stroke,opacity] duration-150',
              linkedTo && onLine(p) ? 'link-flow stroke-primary opacity-100' : 'stroke-foreground/35',
              linkedTo && !onLine(p) && 'opacity-30',
            ]"
            stroke-width="1.5"
            stroke-linecap="round"
            stroke-linejoin="round"
          />
          <circle :cx="p.end.x" :cy="p.end.y" r="3" :class="linkedTo && onLine(p) ? 'fill-background stroke-primary' : 'fill-background stroke-foreground/40'" stroke-width="1.5" />
        </g>
      </svg>

      <div class="relative grid gap-6 lg:gap-x-20" :style="{ gridTemplateColumns: `repeat(auto-fit, minmax(min(100%, 14rem), 1fr))` }">
        <section v-for="(l, i) in lanes" :key="l.key" :data-lane="i" class="grid content-start gap-3" :aria-labelledby="`lane-${l.key}`">
          <div class="px-1">
            <h2 :id="`lane-${l.key}`" class="truncate text-xs font-medium text-foreground/80">{{ l.title }}</h2>
            <p class="truncate text-[11px] text-muted-foreground">{{ l.text }}</p>
          </div>
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
            class="flex items-center justify-center gap-2 rounded-xl border border-dashed bg-background/50 px-4 py-5 text-sm text-muted-foreground transition-colors hover:border-foreground/30 hover:text-foreground"
            @click="openAdd(project.id)"
          >
            <Plus class="size-4" /> Add an app or a database
          </button>
        </section>
      </div>
      <!-- The variables each line carries, on the lines of the hovered or open app. -->
      <div v-if="routable" class="pointer-events-none absolute inset-0 z-20" aria-hidden="true">
        <span
          v-for="lb in labels"
          :key="`${lb.key}-label`"
          class="absolute truncate rounded-md border border-primary/40 bg-card px-1.5 py-0.5 font-mono text-[10.5px] whitespace-nowrap text-foreground shadow-card"
          :style="{ left: `${lb.x}px`, top: `${lb.y}px`, maxWidth: `${lb.room}px`, transform: lb.side === 'l' ? 'translate(-100%, -50%)' : 'translate(0, -50%)' }"
        >{{ lb.text }}</span>
      </div>
    </div>

    <AppPanel v-if="selectedApp" :slug="selectedApp.slug" :project-id="project.id" @close="closePanel" />
  </div>
</template>
