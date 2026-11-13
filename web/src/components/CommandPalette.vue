<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import type { Component } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import type { RouteLocationRaw } from 'vue-router'
import { Bell, Box, Boxes, CornerDownLeft, Github, History, KeyRound, Monitor, Moon, Plus, Search, Settings, ShieldCheck, Sparkles, Sun } from 'lucide-vue-next'
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/ui/dialog'
import { Kbd } from '@/components/ui/kbd'
import { useProjects } from '@/composables/queries'
import { appRoute, projectRoute } from '@/lib/appLinks'
import { aliasOf, appRole, isPublicApp, projectHealth, projectIdOf, roleTitle } from '@/lib/projects'
import { paletteGroups, paletteMatches } from '@/lib/palette'
import { useSession } from '@/composables/useSession'
import { paletteOpen } from '@/composables/useShortcuts'
import { APP_TABS } from '@/lib/appTabs'
import { isMulti } from '@/lib/services'
import { terminalOffered } from '@/lib/terminal'
import { appStatusInfo, hostOf } from '@/lib/format'
import type { App } from '@/lib/types'
import { setTheme } from '@/lib/theme'
import { cn } from '@/lib/utils'

// The finder (Ctrl K): type a few letters of a project, an app or a page and
// press Enter. It only takes you somewhere; it never acts on an app. Inside a
// project it also lists that project's tabs, inside an app that app's tabs.
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
  /** A coloured dot, for a project's or an app's status. */
  dot?: string
  to?: RouteLocationRaw
  run?: () => void
  /** Only listed once something is typed. */
  searchOnly?: boolean
}

const route = useRoute()
const router = useRouter()
const projects = useProjects()
const { isAdmin, user } = useSession()

const query = ref('')
const active = ref(0)
const listEl = ref<HTMLElement | null>(null)
const inputEl = ref<HTMLInputElement | null>(null)

const DOT: Record<string, string> = {
  success: 'bg-success',
  danger: 'bg-destructive',
  progress: 'bg-foreground',
  warning: 'bg-warning',
  muted: 'bg-muted-foreground/50',
}

// The app you are on: its page, or its panel on the canvas (?app=).
const currentSlug = computed(() => (typeof route.params.slug === 'string' ? route.params.slug : typeof route.query.app === 'string' ? route.query.app : ''))
const currentProject = computed(() => (typeof route.params.projectId === 'string' ? route.params.projectId : ''))

const ADMIN_TABS: [string, string][] = [
  ['invites', 'Invites'],
  ['signups', 'Signups'],
  ['server', 'Server'],
  ['apps', 'All apps'],
  ['domains', 'Domains'],
  ['github', 'GitHub'],
  ['audit', 'Activity log'],
]

function twin(p: { name: string; apps: App[] }): boolean {
  return p.apps.length === 1 && p.apps[0]!.name.toLowerCase() === p.name.toLowerCase()
}

// The tabs of one app's page the viewer can open.
function tabsOf(app: App) {
  return APP_TABS.filter((t) => !t.database && !(t.webOnly && !isPublicApp(app)) && !(t.terminal && !terminalOffered(app, user.value?.id ?? '')))
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
  const here = fallback ? undefined : plist.find((p) => p.id === nearId)
  // The app you are on: its tabs, listed first.
  if (current) {
    const g = current.name
    const gk = `app:${current.id}`
    const multi = isMulti(current)
    for (const t of tabsOf(current)) {
      out.push({ id: `cur-${t.name}`, group: g, groupKey: gk, label: multi && t.name === 'app' ? 'Services' : t.label, keywords: current.slug, to: appRoute(current, t.name) })
    }
  }
  // The project you are in: its tabs, and the tabs of its other apps while searching.
  if (here) {
    const pk = `project:${here.id}`
    const tabs: [string, string, string][] = [
      ['project', 'Canvas', 'apps'],
      ['project-variables', 'Variables', 'env environment connections'],
      ['project-members', 'Members', 'people invite access'],
      ['project-settings', 'Settings', 'rename delete'],
    ]
    for (const [name, label, keywords] of tabs) {
      if (name === route.name && !current) continue
      out.push({ id: `proj-${name}`, group: here.name, groupKey: pk, label, keywords, to: { name, params: { projectId: here.id } } })
    }
    for (const a of here.apps) {
      if (a.id === current?.id) continue
      for (const t of tabsOf(a)) {
        if (t.name === 'app') continue
        out.push({ id: `tab-${a.id}-${t.name}`, group: here.name, groupKey: pk, label: `${a.name} › ${t.label}`, keywords: `${a.slug} ${aliasOf(a)}`, to: appRoute(a, t.name), searchOnly: true })
      }
    }
  }
  for (const p of plist) {
    if (fallback) break
    const health = projectHealth(p.apps).status
    out.push({
      id: `project-${p.id}`,
      group: 'Projects',
      groupKey: 'projects',
      near: p.id === nearId,
      label: p.name,
      hint: twin(p) ? (isPublicApp(p.apps[0]!) ? hostOf(p.apps[0]!.url) : roleTitle(appRole(p.apps[0]!))) : p.apps.length === 1 ? '1 app' : `${p.apps.length} apps`,
      keywords: p.apps.map((a) => `${a.name} ${aliasOf(a)}`).join(' '),
      dot: DOT[appStatusInfo(health === 'empty' ? 'new' : health).tone],
      to: projectRoute(p.id),
    })
  }
  // A project holding one app of the same name is listed once, as the project.
  const twins = new Set(fallback ? [] : plist.filter(twin).map((p) => p.apps[0]!.id))
  for (const a of list) {
    if (twins.has(a.id)) continue
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
  const page = (id: string, label: string, icon: Component, keywords: string, to: RouteLocationRaw, searchOnly = false): Item => ({ id, group: 'Pages', groupKey: 'pages', label, icon, keywords, to, searchOnly })
  out.push(
    page('p-apps', 'All projects', Box, 'home overview apps', { name: 'overview' }),
    page('p-new', 'New project', Plus, 'create deploy upload app docker compose github repository', { name: 'new-app' }),
    page('p-ready', 'Ready-made apps and databases', Boxes, 'catalog postgres redis mysql template', { name: 'ready-made' }),
    page('p-connect', 'Connect AI', Sparkles, 'mcp assistant claude', '/connect'),
    page('p-activity', 'Activity', History, 'log history audit who changed', { name: 'activity' }),
    page('p-settings', 'Settings', Settings, 'account profile password', { name: 'settings' }),
    page('p-tokens', 'API tokens', KeyRound, 'settings token', { name: 'settings-tokens' }),
    page('p-notifications', 'Notifications', Bell, 'settings alerts discord push', { name: 'settings-notifications' }),
    page('p-github', 'GitHub connection', Github, 'settings repository', { name: 'settings-github' }),
    page('p-sessions', 'Sessions', Settings, 'settings devices log out', { name: 'settings-sessions' }),
  )
  if (isAdmin.value) {
    out.push(page('p-admin', 'Admin', ShieldCheck, 'people users invites server', { name: 'admin' }))
    out.push(page('p-admin-users', 'Admin › People', ShieldCheck, 'users accounts', { name: 'admin' }, true))
    for (const [tab, label] of ADMIN_TABS) out.push(page(`p-admin-${tab}`, `Admin › ${label}`, ShieldCheck, 'admin', { name: 'admin', query: { tab } }, true))
    out.push(page('p-admin-database', 'Admin › Database', ShieldCheck, 'admin sqlite', { name: 'admin-database' }, true))
  }
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
      <DialogDescription class="sr-only">Type the name of a project, an app or a page, then press Enter.</DialogDescription>
      <div class="flex items-center gap-2 border-b px-3">
        <Search class="size-4 shrink-0 text-muted-foreground" />
        <input
          ref="inputEl"
          v-model="query"
          class="h-12 min-w-0 flex-1 bg-transparent text-sm outline-none placeholder:text-muted-foreground"
          placeholder="Find a project, an app or a page..."
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
        <span class="inline-flex items-center gap-1"><Kbd>Esc</Kbd> to close</span>
      </div>
    </DialogContent>
  </Dialog>
</template>
