<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import type { Component } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import type { RouteLocationRaw } from 'vue-router'
import { Box, Boxes, CornerDownLeft, ExternalLink, Github, KeyRound, Link, Maximize2, Monitor, Moon, PanelRight, Plus, RefreshCw, RotateCw, Search, Settings, Settings2, ShieldCheck, Sparkles, Sun, Variable } from 'lucide-vue-next'
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/ui/dialog'
import { Kbd } from '@/components/ui/kbd'
import { useQueryClient } from '@tanstack/vue-query'
import { keys, useProjects, useServiceTemplates } from '@/composables/queries'
import { useAppActions } from '@/composables/useAppActions'
import { openAdd } from '@/composables/useAddToProject'
import { appRoute, projectRoute } from '@/lib/appLinks'
import { aliasOf, appRole, isPublicApp, projectIdOf, roleTitle, templateLabel } from '@/lib/projects'
import { api } from '@/lib/api'
import { copyText, toastError, toastOk } from '@/lib/notify'
import { paletteGroups, paletteMatches } from '@/lib/palette'
import { useSession } from '@/composables/useSession'
import { paletteOpen } from '@/composables/useShortcuts'
import { APP_TABS } from '@/lib/appTabs'
import { isMulti } from '@/lib/services'
import { appStatusInfo, hostOf } from '@/lib/format'
import { setTheme } from '@/lib/theme'
import { cn } from '@/lib/utils'

// The quick switcher: type a few letters of a project, an app, a page or an
// action and press Enter. "web logs" finds the Logs tab of the app called web;
// "restart" restarts the app you are on (or, on the canvas, one of the
// project's apps), "postgres" adds one to this project.
interface Item {
  id: string
  /** The heading shown above the group. */
  group: string
  /** Unique per group: two groups can have the same heading (an app and its project both called Shop). */
  groupKey: string
  label: string
  /** In the project you are in: listed before the same match elsewhere. */
  near?: boolean
  /** Shown on the right, muted. */
  hint?: string
  /** Extra words that find this item. */
  keywords?: string
  icon?: Component
  /** A coloured dot, for an app's status. */
  dot?: string
  to?: RouteLocationRaw
  run?: () => void
  /** Only listed once something is typed. */
  searchOnly?: boolean
}

const route = useRoute()
const router = useRouter()
const projects = useProjects()
const { isAdmin } = useSession()

const query = ref('')
const active = ref(0)
const listEl = ref<HTMLElement | null>(null)
const inputEl = ref<HTMLInputElement | null>(null)

const DOT: Record<string, string> = {
  success: 'bg-success',
  danger: 'bg-destructive',
  info: 'bg-info',
  warning: 'bg-warning',
  muted: 'bg-muted-foreground/50',
}

// The app you are on: its page, or its panel on the canvas (?app=).
const currentSlug = computed(() => (typeof route.params.slug === 'string' ? route.params.slug : typeof route.query.app === 'string' ? route.query.app : ''))
const templates = useServiceTemplates()
const actions = useAppActions(currentSlug)
const currentProject = computed(() => (typeof route.params.projectId === 'string' ? route.params.projectId : ''))

// Restart or deploy another app of the project, from the canvas.
const qc = useQueryClient()
async function runFor(slug: string, fn: (s: string) => Promise<unknown>, done: string, detail?: string) {
  try {
    await fn(slug)
    toastOk(done, detail)
  } catch (e) {
    toastError(e)
  } finally {
    void qc.invalidateQueries({ queryKey: keys.app(slug) })
    void qc.invalidateQueries({ queryKey: keys.deployments(slug) })
    void qc.invalidateQueries({ queryKey: keys.apps })
  }
}

const items = computed<Item[]>(() => {
  const plist = projects.data.value?.projects ?? []
  const fallback = !!projects.data.value?.fallback
  const list = plist.flatMap((p) => p.apps)
  const projectName = new Map(plist.flatMap((p) => p.apps.map((a) => [a.id, p.name] as const)))
  const out: Item[] = []
  const current = list.find((a) => a.slug === currentSlug.value)
  // The project you are in, on its pages or on the page of one of its apps.
  const nearId = currentProject.value || (current ? projectIdOf(current) : '')
  if (current) {
    const web = isPublicApp(current)
    const live = !!current.current_deployment
    const g = current.name
    const gk = `app:${current.id}`
    if (current.git_source) out.push({ id: 'act-deploy', group: g, groupKey: gk, label: 'Deploy latest', hint: current.git_source.branch, icon: RefreshCw, keywords: 'deploy build redeploy commit', run: () => actions.redeploy.mutate() })
    // A project with several services restarts all of them and lists them on its first tab.
    const multi = isMulti(current)
    if (live) out.push({ id: 'act-restart', group: g, groupKey: gk, label: `Restart ${current.name}`, hint: multi ? 'every service' : undefined, icon: RotateCw, keywords: 'restart reboot', run: () => actions.restart.mutate() })
    if (web && live) {
      out.push({ id: 'act-open', group: g, groupKey: gk, label: `Open ${hostOf(current.url)}`, icon: ExternalLink, keywords: 'open visit website app', run: () => window.open(current.url, '_blank', 'noopener') })
      out.push({ id: 'act-copy', group: g, groupKey: gk, label: 'Copy link', icon: Link, keywords: 'copy url address', run: () => void copyText(current.url, 'Link copied') })
    }
    if (route.params.slug && !fallback) out.push({ id: 'act-canvas', group: g, groupKey: gk, label: 'Show on the canvas', icon: PanelRight, keywords: 'project panel', to: projectRoute(projectIdOf(current), { app: current.slug }) })
    else if (!route.params.slug) out.push({ id: 'act-page', group: g, groupKey: gk, label: 'Open full page', icon: Maximize2, keywords: 'app page details', to: appRoute(current) })
    for (const t of APP_TABS) {
      if (t.database || (t.webOnly && !isPublicApp(current))) continue
      out.push({ id: `cur-${t.name}`, group: g, groupKey: gk, label: multi && t.name === 'app' ? 'Services' : t.label, keywords: current.slug, to: appRoute(current, t.name) })
    }
  }
  // The project you are in: add to it, and act on its other apps.
  const here = plist.find((p) => p.id === currentProject.value)
  if (here && !fallback) {
    const pk = `project:${here.id}`
    out.push({ id: 'add-here', group: here.name, groupKey: pk, label: `Add to ${here.name}`, icon: Plus, keywords: 'add database postgres redis service app github repository docker', run: () => openAdd(here.id) })
    for (const t of templates.data.value ?? []) {
      out.push({ id: `add-${t.id}`, group: here.name, groupKey: pk, label: `Add ${t.name}`, hint: templateLabel(t), icon: Plus, keywords: `add ${t.id} ${t.category} database cache`, run: () => openAdd(here.id, t.id), searchOnly: true })
    }
    for (const a of here.apps) {
      if (a.id === current?.id) continue
      if (a.current_deployment) {
        out.push({ id: `restart-${a.id}`, group: here.name, groupKey: pk, label: `Restart ${a.name}`, icon: RotateCw, keywords: `restart reboot ${a.slug} ${aliasOf(a)}`, run: () => void runFor(a.slug, api.restart, `${a.name} restarted`), searchOnly: true })
      }
      if (a.git_source) {
        out.push({
          id: `deploy-${a.id}`,
          group: here.name,
          groupKey: pk,
          label: `Deploy latest of ${a.name}`,
          hint: a.git_source.branch,
          icon: RefreshCw,
          keywords: `deploy build redeploy commit ${a.slug} ${aliasOf(a)}`,
          run: () => void runFor(a.slug, api.redeploy, 'Redeploy started'),
          searchOnly: true,
        })
      }
    }
    if (route.name !== 'project-variables') out.push({ id: 'proj-vars', group: here.name, groupKey: pk, label: 'Project variables', icon: Variable, keywords: 'env environment connections', to: { name: 'project-variables', params: { projectId: here.id } } })
    if (route.name !== 'project-settings') out.push({ id: 'proj-settings', group: here.name, groupKey: pk, label: 'Project settings', icon: Settings2, keywords: 'rename delete project', to: { name: 'project-settings', params: { projectId: here.id } } })
  }
  for (const p of plist) {
    if (fallback) break
    out.push({
      id: `project-${p.id}`,
      group: 'Projects',
      groupKey: 'projects',
      near: p.id === nearId,
      label: p.name,
      hint: p.apps.length === 1 ? '1 app' : `${p.apps.length} apps`,
      keywords: p.apps.map((a) => `${a.name} ${aliasOf(a)}`).join(' '),
      icon: Boxes,
      to: projectRoute(p.id),
    })
  }
  for (const a of list) {
    const pn = projectName.get(a.id)
    out.push({
      id: `app-${a.id}`,
      group: 'Apps',
      groupKey: 'apps',
      near: !!nearId && projectIdOf(a) === nearId,
      label: fallback || !pn || pn === a.name ? a.name : `${a.name} in ${pn}`,
      hint: isPublicApp(a) ? hostOf(a.url) : roleTitle(appRole(a)),
      keywords: `${a.slug} ${aliasOf(a)} ${a.url} ${pn ?? ''}`,
      dot: DOT[appStatusInfo(a.status).tone],
      to: appRoute(a),
    })
  }
  // Every tab of every app, for "shop logs". Only while searching, or the list would be endless.
  for (const a of list) {
    for (const t of APP_TABS) {
      if (t.name === 'app' || t.database || (t.webOnly && !isPublicApp(a))) continue
      out.push({ id: `tab-${a.id}-${t.name}`, group: 'Apps', groupKey: 'apps', near: !!nearId && projectIdOf(a) === nearId, label: `${a.name} › ${t.label}`, keywords: `${a.slug} ${projectName.get(a.id) ?? ''}`, to: appRoute(a, t.name), searchOnly: true })
    }
  }
  out.push(
    { id: 'p-apps', group: 'Go to', groupKey: 'goto', label: 'All projects', icon: Box, keywords: 'home overview apps', to: { name: 'overview' } },
    { id: 'p-new', group: 'Go to', groupKey: 'goto', label: 'New project', icon: Plus, keywords: 'create deploy upload app docker compose', to: { name: 'new-app' } },
    { id: 'p-new-git', group: 'Go to', groupKey: 'goto', label: 'New project from a GitHub repository', icon: Github, keywords: 'create deploy git repo', to: { name: 'new-app', query: { source: 'github' } } },
    { id: 'p-ready', group: 'Go to', groupKey: 'goto', label: 'Ready-made apps and databases', icon: Boxes, keywords: 'catalog postgres redis mysql template', to: { name: 'ready-made' } },
    { id: 'p-connect', group: 'Go to', groupKey: 'goto', label: 'Connect AI', icon: Sparkles, keywords: 'mcp assistant claude', to: '/connect' },
    { id: 'p-settings', group: 'Go to', groupKey: 'goto', label: 'Settings', icon: Settings, keywords: 'account profile password', to: { name: 'settings' } },
    { id: 'p-tokens', group: 'Go to', groupKey: 'goto', label: 'API tokens', icon: KeyRound, keywords: 'settings token', to: { name: 'settings-tokens' } },
    { id: 'p-github', group: 'Go to', groupKey: 'goto', label: 'GitHub connection', icon: Settings, keywords: 'settings git repository', to: { name: 'settings-github' } },
    { id: 'p-sessions', group: 'Go to', groupKey: 'goto', label: 'Devices you are logged in on', icon: Settings, keywords: 'settings sessions log out', to: { name: 'settings-sessions' } },
  )
  if (isAdmin.value) out.push({ id: 'p-admin', group: 'Go to', groupKey: 'goto', label: 'Admin', icon: ShieldCheck, keywords: 'people users invites server', to: { name: 'admin' } })
  out.push(
    { id: 't-light', group: 'Theme', groupKey: 'theme', label: 'Light theme', icon: Sun, keywords: 'theme appearance', run: () => setTheme('light'), searchOnly: true },
    { id: 't-dark', group: 'Theme', groupKey: 'theme', label: 'Dark theme', icon: Moon, keywords: 'theme appearance', run: () => setTheme('dark'), searchOnly: true },
    { id: 't-system', group: 'Theme', groupKey: 'theme', label: 'Theme: match my device', icon: Monitor, keywords: 'theme appearance system', run: () => setTheme('system'), searchOnly: true },
  )
  return out
})

const results = computed<Item[]>(() => (query.value.trim() ? paletteMatches(items.value, query.value) : items.value.filter((i) => !i.searchOnly)))
const grouped = computed(() => paletteGroups(results.value))

// Where the focus was before the palette opened, to put it back on close.
let focusBefore: HTMLElement | null = null
watch(query, () => (active.value = 0))
watch(
  paletteOpen,
  (o) => {
    if (o) {
      focusBefore = document.activeElement instanceof HTMLElement && document.activeElement !== document.body ? document.activeElement : null
      query.value = ''
      active.value = 0
    }
  },
  { flush: 'sync' },
)
function restoreFocus(e: Event) {
  const el = focusBefore
  focusBefore = null
  // Gone after choosing a page: the new page decides where the focus goes.
  if (!el?.isConnected) return
  e.preventDefault()
  el.focus({ preventScroll: true })
}

function move(step: number) {
  const n = results.value.length
  if (!n) return
  active.value = (active.value + step + n) % n
  void nextTick(() => listEl.value?.querySelector(`[data-index="${active.value}"]`)?.scrollIntoView({ block: 'nearest' }))
}

function choose(item: Item | undefined) {
  if (!item) return
  paletteOpen.value = false
  if (item.run) item.run()
  else if (item.to) void router.push(item.to)
}
</script>

<template>
  <Dialog v-model:open="paletteOpen">
    <DialogContent
      class="top-[15%] max-h-[70vh] translate-y-0 gap-0 overflow-hidden p-0 sm:max-w-lg"
      :show-close-button="false"
      @open-auto-focus.prevent="inputEl?.focus()"
      @close-auto-focus="restoreFocus"
    >
      <DialogTitle class="sr-only">Go to</DialogTitle>
      <DialogDescription class="sr-only">Type the name of an app, a page or an action, then press Enter.</DialogDescription>
      <div class="flex items-center gap-2 border-b px-3">
        <Search class="size-4 shrink-0 text-muted-foreground" />
        <input
          ref="inputEl"
          v-model="query"
          class="h-12 min-w-0 flex-1 bg-transparent text-sm outline-none placeholder:text-muted-foreground"
          placeholder="Go to a project, an app or an action..."
          role="combobox"
          aria-expanded="true"
          aria-controls="palette-list"
          :aria-activedescendant="results.length ? `palette-${active}` : undefined"
          autocomplete="off"
          spellcheck="false"
          @keydown.down.prevent="move(1)"
          @keydown.up.prevent="move(-1)"
          @keydown.enter.prevent="choose(results[active])"
        />
        <Kbd>Esc</Kbd>
      </div>
      <div id="palette-list" ref="listEl" role="listbox" aria-label="Results" class="max-h-[min(26rem,55vh)] overflow-y-auto p-1.5">
        <p v-if="!results.length" class="px-3 py-8 text-center text-sm text-muted-foreground">Nothing matches "{{ query.trim() }}".</p>
        <div v-for="g in grouped" :key="`${g.key}:${g.items[0]!.index}`" role="group" :aria-label="g.group" class="pb-1">
          <p class="truncate px-2 pt-2 pb-1 text-xs font-medium text-muted-foreground">{{ g.group }}</p>
          <div
            v-for="{ item, index } in g.items"
            :id="`palette-${index}`"
            :key="item.id"
            role="option"
            :aria-selected="index === active"
            :data-index="index"
            :class="cn('flex cursor-pointer items-center gap-2.5 rounded-md px-2 py-2 text-sm', index === active && 'bg-accent text-accent-foreground')"
            @mousemove="active = index"
            @click="choose(item)"
          >
            <span v-if="item.dot" class="flex size-4 shrink-0 items-center justify-center"><span :class="cn('size-2 rounded-full', item.dot)" /></span>
            <component :is="item.icon" v-else-if="item.icon" class="size-4 shrink-0 text-muted-foreground" />
            <span v-else class="size-4 shrink-0" />
            <span class="min-w-0 flex-1 truncate">{{ item.label }}</span>
            <span v-if="item.hint" class="hidden max-w-[45%] truncate font-mono text-xs text-muted-foreground sm:inline">{{ item.hint }}</span>
            <CornerDownLeft v-if="index === active" class="size-3.5 shrink-0 text-muted-foreground" />
          </div>
        </div>
      </div>
      <div class="hidden items-center gap-3 border-t px-3 py-2 text-xs text-muted-foreground sm:flex">
        <span class="inline-flex items-center gap-1"><Kbd>↑</Kbd><Kbd>↓</Kbd> to move</span>
        <span class="inline-flex items-center gap-1"><Kbd>Enter</Kbd> to open</span>
        <span class="ml-auto inline-flex items-center gap-1"><Kbd>/</Kbd> searches the list you are on</span>
      </div>
    </DialogContent>
  </Dialog>
</template>
