<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { RouterLink, useRouter } from 'vue-router'
import { useMutation, useQueryClient } from '@tanstack/vue-query'
import { ArrowUpRight, FileText, MoreHorizontal, Pencil, Plus, ScrollText, Settings2, Trash2, Variable } from 'lucide-vue-next'
import ConfirmDialog from '@/components/ConfirmDialog.vue'
import CopyForAiButton from '@/components/CopyForAiButton.vue'
import EmptyState from '@/components/EmptyState.vue'
import QueryError from '@/components/QueryError.vue'
import ServiceIcon from '@/components/ServiceIcon.vue'
import StatusDot from '@/components/StatusDot.vue'
import ProjectAppCard from '@/components/project/ProjectAppCard.vue'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuTrigger } from '@/components/ui/dropdown-menu'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Skeleton } from '@/components/ui/skeleton'
import { keys, useProject, useProjectEnv, useServiceTemplates } from '@/composables/queries'
import { openAdd } from '@/composables/useAddToProject'
import { api } from '@/lib/api'
import { appRoute } from '@/lib/appLinks'
import { friendlyError } from '@/lib/errors'
import { appStatusInfo, formatMB, hostOf } from '@/lib/format'
import { toastError, toastOk } from '@/lib/notify'
import { aliasOf, appRole, isPublicApp, mainApp, projectHealth, projectLinks, projectUsage, sortProjectApps, talksTo, templateLabel, templateRole, usedBy } from '@/lib/projects'
import type { Lane } from '@/lib/projects'
import type { App } from '@/lib/types'

// A project: the apps it holds as cards on a board, in three lanes (what
// visitors open, what runs in the background, what keeps data), with lines
// from each app to the apps whose variables it uses.
const props = defineProps<{ projectId: string }>()
const router = useRouter()
const qc = useQueryClient()
const { list, project } = useProject(() => props.projectId)
const templates = useServiceTemplates()
const tpls = computed(() => templates.data.value ?? [])
const fallback = computed(() => !!list.data.value?.fallback)

const apps = computed(() => sortProjectApps(project.value?.apps ?? [], tpls.value))
const env = useProjectEnv(apps)
const links = computed(() => projectLinks(apps.value, env.value))
const health = computed(() => projectHealth(apps.value))
const usage = computed(() => projectUsage(apps.value))
const main = computed(() => (project.value ? mainApp(project.value) : undefined))
const roles = computed(() => new Map(apps.value.map((a) => [a.id, appRole(a, tpls.value)])))
const roleOf = (a: App) => roles.value.get(a.id) ?? appRole(a, tpls.value)

watch(
  () => project.value?.name,
  (n) => {
    if (n) document.title = `${n} - bitploy`
  },
  { immediate: true },
)

const LANES: { key: Lane; title: string; text: string }[] = [
  { key: 'public', title: 'Open to visitors', text: 'Has a web address' },
  { key: 'background', title: 'In the background', text: 'Reached only by the other apps' },
  { key: 'data', title: 'Data', text: 'Keeps what your apps store' },
]
const lanes = computed(() =>
  LANES.map((l) => ({ ...l, apps: apps.value.filter((a) => roleOf(a).lane === l.key) })).filter((l) => l.apps.length),
)

// ---------------------------------------------------------------- hover: light up an app and what it is linked to
const hovered = ref<string | null>(null)
const linkedTo = computed(() => {
  const h = hovered.value
  if (!h) return null
  const ids = new Set([h])
  for (const l of links.value) {
    if (l.from === h) ids.add(l.to)
    if (l.to === h) ids.add(l.from)
  }
  return ids
})

// ---------------------------------------------------------------- the lines behind the cards
const board = ref<HTMLElement>()
interface LinkPath {
  key: string
  d: string
  from: string
  to: string
  label: string
  /** Where the line enters the app it reads from, and on which side. */
  ex: number
  ey: number
  side: 'l' | 'r'
}
const paths = ref<LinkPath[]>([])
const size = ref({ w: 0, h: 0 })
function measure() {
  const el = board.value
  if (!el) return
  const box = el.getBoundingClientRect()
  size.value = { w: box.width, h: box.height }
  const rects = new Map<string, { l: number; r: number; t: number; b: number }>()
  for (const card of el.querySelectorAll<HTMLElement>('[data-app]')) {
    const r = card.getBoundingClientRect()
    rects.set(card.dataset.app!, { l: r.left - box.left, r: r.right - box.left, t: r.top - box.top, b: r.bottom - box.top })
  }
  // Which side of each card a line leaves or enters: toward the other card, or the right for one in the same column.
  type End = { link: number; id: string; side: 'l' | 'r'; otherY: number; y: number }
  const ends: End[] = []
  const sides: { a: End; b: End }[] = []
  links.value.forEach((l, i) => {
    const a = rects.get(l.from)
    const b = rects.get(l.to)
    if (!a || !b) return
    const [sa, sb]: ['l' | 'r', 'l' | 'r'] = b.l >= a.r - 4 ? ['r', 'l'] : a.l >= b.r - 4 ? ['l', 'r'] : ['r', 'r']
    const ea = { link: i, id: l.from, side: sa, otherY: b.t, y: 0 }
    const eb = { link: i, id: l.to, side: sb, otherY: a.t, y: 0 }
    ends.push(ea, eb)
    sides.push({ a: ea, b: eb })
  })
  // Several lines on one side of a card spread out instead of meeting in one point.
  const bySide = new Map<string, End[]>()
  for (const e of ends) {
    const k = `${e.id}:${e.side}`
    bySide.set(k, [...(bySide.get(k) ?? []), e])
  }
  for (const list of bySide.values()) {
    const r = rects.get(list[0]!.id)!
    list.sort((x, y) => x.otherY - y.otherY)
    const step = Math.min(22, Math.max(10, (r.b - r.t - 60) / Math.max(1, list.length)))
    list.forEach((e, i) => (e.y = r.t + 34 + i * step))
  }
  const out: LinkPath[] = []
  for (const { a, b } of sides) {
    const ra = rects.get(a.id)!
    const rb = rects.get(b.id)!
    const ax = a.side === 'r' ? ra.r : ra.l
    const bx = b.side === 'r' ? rb.r : rb.l
    let d: string
    if (a.side !== b.side) {
      const dx = Math.max(28, Math.abs(bx - ax) / 2)
      const dir = a.side === 'r' ? 1 : -1
      d = `M${ax},${a.y} C${ax + dir * dx},${a.y} ${bx - dir * dx},${b.y} ${bx},${b.y}`
    } else {
      const right = Math.max(ra.r, rb.r) + 30
      d = `M${ax},${a.y} C${right},${a.y} ${right},${b.y} ${bx},${b.y}`
    }
    const l = links.value[a.link]!
    out.push({ key: `${l.from}>${l.to}`, d, from: l.from, to: l.to, label: l.vars.join(', '), ex: bx, ey: b.y, side: b.side })
  }
  paths.value = out
}
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

// ---------------------------------------------------------------- rename and delete
const renameOpen = ref(false)
const newName = ref('')
function startRename() {
  newName.value = project.value?.name ?? ''
  renameOpen.value = true
}
const rename = useMutation({
  mutationFn: () => api.updateProject(props.projectId, { name: newName.value.trim() }),
  onSuccess: () => {
    renameOpen.value = false
    void qc.invalidateQueries({ queryKey: keys.projects })
  },
  onError: (e) => toastError(e),
})
const renameError = computed(() => (rename.error.value ? friendlyError(rename.error.value).message : ''))

const deleteOpen = ref(false)
const remove = useMutation({
  mutationFn: () => api.deleteProject(props.projectId, project.value?.name ?? '', apps.value.length > 0),
  onSuccess: () => {
    toastOk(`Deleted ${project.value?.name ?? 'the project'}`)
    deleteOpen.value = false
    void qc.invalidateQueries({ queryKey: keys.apps })
    void router.push('/')
  },
  onError: (e) => toastError(e),
})
const deleteText = computed(() => {
  const n = apps.value.length
  if (!n) return 'The project is empty, so nothing else is deleted.'
  const names = apps.value.map((a) => a.name).join(', ')
  return `This deletes ${n === 1 ? 'its app' : `its ${n} apps`} (${names}) with their stored data, domains and backups. It cannot be undone.`
})

// ---------------------------------------------------------------- one app: what to add next
const quickPicks = computed(() => {
  const want = ['postgres', 'redis', 'meilisearch']
  return want.map((id) => tpls.value.find((t) => t.id === id)).filter((t) => !!t)
})
const projectForAi = computed(() => {
  const p = project.value
  if (!p) return ''
  const lines = [`bitploy project "${p.name}" (id ${p.id}) with ${p.apps.length} app${p.apps.length === 1 ? '' : 's'}:`]
  for (const a of apps.value) {
    const r = roleOf(a)
    const where = isPublicApp(a) ? a.url : `private, at ${a.internal_host || `${aliasOf(a)}:${a.port ?? ''}`}`
    lines.push(`- ${aliasOf(a)} (slug ${a.slug}): ${r.product ? `${r.label}, ${r.product}` : r.label}, ${appStatusInfo(a.status).label.toLowerCase()}, ${where}`)
  }
  for (const l of links.value) {
    const from = apps.value.find((a) => a.id === l.from)
    const to = apps.value.find((a) => a.id === l.to)
    if (from && to) lines.push(`- ${aliasOf(from)} reads ${l.vars.join(', ')} from ${aliasOf(to)}`)
  }
  lines.push('Apps of a project reach each other by their short name; a variable can point at another app as ${{alias.VAR}}.')
  return lines.join('\n')
})
</script>

<template>
  <div class="grid grid-cols-[minmax(0,1fr)] gap-6">
    <QueryError v-if="list.isError.value && !list.data.value" :error="list.error.value" @retry="list.refetch()" />
    <div v-else-if="list.isPending.value" class="grid gap-4">
      <Skeleton class="h-8 w-64" />
      <Skeleton class="h-96 w-full rounded-xl" />
    </div>
    <EmptyState v-else-if="!project" title="We cannot find that project." description="It may have been deleted, or the address has a typo.">
      <Button as-child><RouterLink to="/">Back to your projects</RouterLink></Button>
    </EmptyState>

    <template v-else>
      <!-- Header -->
      <div class="flex flex-wrap items-start justify-between gap-x-6 gap-y-4">
        <div class="min-w-0">
          <div class="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1.5">
            <h1 class="min-w-0 truncate text-2xl font-semibold tracking-tight">{{ project.name }}</h1>
            <span v-if="health.total" class="inline-flex items-center gap-1.5 rounded-md border px-2 py-0.5 text-xs text-muted-foreground">
              <StatusDot :status="health.status === 'empty' ? 'new' : health.status" />
              {{ health.running === health.total ? (health.total === 1 ? 'Running' : `All ${health.total} running`) : `${health.running} of ${health.total} running` }}
            </span>
          </div>
          <div class="mt-1.5 flex flex-wrap items-center gap-x-2 gap-y-1 text-sm text-muted-foreground">
            <a v-if="main && isPublicApp(main)" :href="main.url" target="_blank" rel="noopener noreferrer" class="inline-flex items-center gap-1 font-mono text-[13px] text-foreground hover:underline">
              {{ hostOf(main.url) }} <ArrowUpRight class="size-3 text-muted-foreground" />
            </a>
            <span v-else-if="apps.length">No web address yet</span>
            <template v-if="apps.length">
              <span class="size-1 rounded-full bg-muted-foreground/50" aria-hidden="true" />
              <span>{{ apps.length === 1 ? '1 app' : `${apps.length} apps` }}<template v-if="usage.memoryMB">, {{ formatMB(usage.memoryMB) }} memory reserved</template></span>
            </template>
          </div>
        </div>
        <div class="flex flex-wrap items-center gap-2">
          <CopyForAiButton v-if="apps.length" :text="projectForAi" />
          <Button :disabled="fallback" :title="fallback ? 'This server keeps one app per project.' : undefined" @click="openAdd(project.id)"><Plus /> Add</Button>
          <DropdownMenu v-if="!fallback">
            <DropdownMenuTrigger as-child>
              <Button variant="outline" size="icon" aria-label="More actions for this project"><MoreHorizontal /></Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end" class="w-52">
              <DropdownMenuItem @click="startRename"><Pencil /> Rename project</DropdownMenuItem>
              <DropdownMenuSeparator />
              <DropdownMenuItem variant="destructive" @click="deleteOpen = true"><Trash2 /> Delete project</DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenu>
          <Button v-else-if="main" variant="outline" as-child><RouterLink :to="appRoute(main, 'app-settings')"><Settings2 /> Settings</RouterLink></Button>
        </div>
      </div>

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
          <ProjectAppCard :app="apps[0]!" :role="roleOf(apps[0]!)" :templates="tpls" :talks-to="[]" :used-by="[]" />
          <div class="flex flex-wrap gap-2">
            <Button variant="outline" size="sm" as-child><RouterLink :to="appRoute(apps[0]!, 'app-logs')"><ScrollText /> Logs</RouterLink></Button>
            <Button variant="outline" size="sm" as-child><RouterLink :to="appRoute(apps[0]!, 'app-variables')"><Variable /> Variables</RouterLink></Button>
            <Button variant="outline" size="sm" as-child><RouterLink :to="appRoute(apps[0]!, 'app-deployments')"><FileText /> Deployments</RouterLink></Button>
            <Button variant="outline" size="sm" as-child><RouterLink :to="appRoute(apps[0]!, 'app-settings')"><Settings2 /> Settings</RouterLink></Button>
          </div>
        </div>

        <div v-if="!fallback" class="grid content-start gap-3 rounded-xl border border-dashed bg-background/60 p-5">
          <div>
            <h2 class="font-medium">Add to {{ project.name }}</h2>
            <p class="mt-1 text-sm text-muted-foreground">
              A project holds everything one product needs. Add a database and {{ aliasOf(apps[0]!) }} gets its address as a variable; add an API or a worker from another repository.
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
          class="pointer-events-none absolute inset-0 hidden lg:block"
          :width="size.w"
          :height="size.h"
          :viewBox="`0 0 ${size.w} ${size.h}`"
          aria-hidden="true"
        >
          <g v-for="p in paths" :key="p.key">
            <path
              :d="p.d"
              fill="none"
              :class="[
                'transition-[stroke,opacity] duration-150',
                linkedTo && (p.from === hovered || p.to === hovered) ? 'link-flow stroke-primary opacity-100' : 'stroke-foreground/25',
                linkedTo && !(p.from === hovered || p.to === hovered) && 'opacity-20',
              ]"
              stroke-width="1.5"
              stroke-linecap="round"
            />
            <circle :cx="p.ex" :cy="p.ey" r="3" class="fill-background stroke-foreground/40" stroke-width="1.5" />
          </g>
        </svg>

        <div class="relative grid gap-6 lg:gap-16" :style="{ gridTemplateColumns: `repeat(auto-fit, minmax(min(100%, 17rem), 1fr))` }">
          <section v-for="l in lanes" :key="l.key" class="grid content-start gap-3" :aria-labelledby="`lane-${l.key}`">
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
              :lit="!!linkedTo && linkedTo.has(a.id) && hovered !== a.id"
              :dim="!!linkedTo && !linkedTo.has(a.id)"
              @hover="(id) => (hovered = id)"
            />
            <button
              v-if="l.key === lanes.at(-1)!.key && !fallback"
              type="button"
              class="flex items-center justify-center gap-2 rounded-xl border border-dashed bg-background/50 px-4 py-5 text-sm text-muted-foreground transition-colors hover:border-foreground/30 hover:text-foreground"
              @click="openAdd(project.id)"
            >
              <Plus class="size-4" /> Add an app or a database
            </button>
          </section>
        </div>
        <!-- The variables each line carries, on the lines of the hovered app. -->
        <div class="pointer-events-none absolute inset-0 z-20 hidden lg:block" aria-hidden="true">
          <template v-for="p in paths" :key="`${p.key}-label`">
            <span
              v-if="hovered && (p.from === hovered || p.to === hovered)"
              class="absolute rounded-md border border-primary/40 bg-card px-1.5 py-0.5 font-mono text-[10.5px] whitespace-nowrap text-foreground shadow-card"
              :style="{ left: `${p.side === 'l' ? p.ex - 8 : p.ex + 8}px`, top: `${p.ey}px`, transform: p.side === 'l' ? 'translate(-100%, -50%)' : 'translate(0, -50%)' }"
            >{{ p.label }}</span>
          </template>
        </div>
      </div>
    </template>

    <Dialog v-model:open="renameOpen">
      <DialogContent class="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>Rename project</DialogTitle>
          <DialogDescription>Only the name changes. The apps keep their addresses and short names.</DialogDescription>
        </DialogHeader>
        <form class="grid gap-2" @submit.prevent="newName.trim() && rename.mutate()">
          <Label for="project-name">Name</Label>
          <Input id="project-name" v-model="newName" autocomplete="off" />
          <p v-if="renameError" class="text-[0.8rem] text-destructive">{{ renameError }}</p>
          <DialogFooter class="mt-2">
            <Button type="button" variant="ghost" @click="renameOpen = false">Cancel</Button>
            <Button type="submit" :disabled="!newName.trim() || rename.isPending.value">{{ rename.isPending.value ? 'Saving...' : 'Save' }}</Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>

    <ConfirmDialog
      v-model:open="deleteOpen"
      :title="`Delete ${project?.name ?? 'this project'}?`"
      :description="deleteText"
      confirm-label="Delete project"
      :type-to-confirm="apps.length ? project?.name : undefined"
      :loading="remove.isPending.value"
      @confirm="remove.mutate()"
    />
  </div>
</template>
