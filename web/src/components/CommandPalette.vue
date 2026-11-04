<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import type { Component } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import type { RouteLocationRaw } from 'vue-router'
import { Box, CornerDownLeft, KeyRound, Monitor, Moon, Plus, Search, Settings, ShieldCheck, Sparkles, Sun } from 'lucide-vue-next'
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/ui/dialog'
import { Kbd } from '@/components/ui/kbd'
import { useApps } from '@/composables/queries'
import { useSession } from '@/composables/useSession'
import { paletteOpen } from '@/composables/useShortcuts'
import { APP_TABS } from '@/lib/appTabs'
import { appStatusInfo, hostOf } from '@/lib/format'
import { setTheme } from '@/lib/theme'
import { cn } from '@/lib/utils'

// The quick switcher: type a few letters of an app or a page and press Enter.
// "shop logs" finds the Logs tab of the app called shop.
interface Item {
  id: string
  group: string
  label: string
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
const apps = useApps()
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

const currentSlug = computed(() => (typeof route.params.slug === 'string' ? route.params.slug : ''))

const items = computed<Item[]>(() => {
  const list = apps.data.value ?? []
  const out: Item[] = []
  const current = list.find((a) => a.slug === currentSlug.value)
  if (current) {
    for (const t of APP_TABS) {
      if (t.database || (t.webOnly && current.type === 'worker')) continue
      out.push({ id: `cur-${t.name}`, group: current.name, label: t.label, keywords: current.slug, to: { name: t.name, params: { slug: current.slug } } })
    }
  }
  for (const a of list) {
    out.push({
      id: `app-${a.id}`,
      group: 'Projects',
      label: a.name,
      hint: a.type === 'web' ? hostOf(a.url) : 'Background worker',
      keywords: `${a.slug} ${a.url}`,
      dot: DOT[appStatusInfo(a.status).tone],
      to: { name: 'app', params: { slug: a.slug } },
    })
  }
  // Every tab of every app, for "shop logs". Only while searching, or the list would be endless.
  for (const a of list) {
    for (const t of APP_TABS) {
      if (t.name === 'app' || t.database || (t.webOnly && a.type === 'worker')) continue
      out.push({ id: `tab-${a.id}-${t.name}`, group: 'Projects', label: `${a.name} › ${t.label}`, keywords: a.slug, to: { name: t.name, params: { slug: a.slug } }, searchOnly: true })
    }
  }
  out.push(
    { id: 'p-apps', group: 'Go to', label: 'All projects', icon: Box, keywords: 'home overview apps', to: { name: 'overview' } },
    { id: 'p-new', group: 'Go to', label: 'New project', icon: Plus, keywords: 'create deploy upload app', to: { name: 'new-app' } },
    { id: 'p-connect', group: 'Go to', label: 'Connect AI', icon: Sparkles, keywords: 'mcp assistant claude', to: '/connect' },
    { id: 'p-settings', group: 'Go to', label: 'Settings', icon: Settings, keywords: 'account profile password', to: { name: 'settings' } },
    { id: 'p-tokens', group: 'Go to', label: 'API tokens', icon: KeyRound, keywords: 'settings token', to: { name: 'settings-tokens' } },
    { id: 'p-github', group: 'Go to', label: 'GitHub connection', icon: Settings, keywords: 'settings git repository', to: { name: 'settings-github' } },
    { id: 'p-sessions', group: 'Go to', label: 'Devices you are logged in on', icon: Settings, keywords: 'settings sessions log out', to: { name: 'settings-sessions' } },
  )
  if (isAdmin.value) out.push({ id: 'p-admin', group: 'Go to', label: 'Admin', icon: ShieldCheck, keywords: 'people users invites server', to: { name: 'admin' } })
  out.push(
    { id: 't-light', group: 'Theme', label: 'Light theme', icon: Sun, keywords: 'theme appearance', run: () => setTheme('light'), searchOnly: true },
    { id: 't-dark', group: 'Theme', label: 'Dark theme', icon: Moon, keywords: 'theme appearance', run: () => setTheme('dark'), searchOnly: true },
    { id: 't-system', group: 'Theme', label: 'Theme: match my device', icon: Monitor, keywords: 'theme appearance system', run: () => setTheme('system'), searchOnly: true },
  )
  return out
})

/** 0: the label starts with the search, 1: a word does, 2: it is in there somewhere. */
function score(item: Item, words: string[]): number | null {
  const label = item.label.toLowerCase()
  const all = `${label} ${item.group.toLowerCase()} ${(item.keywords ?? '').toLowerCase()}`
  if (!words.every((w) => all.includes(w))) return null
  const first = words[0]
  if (label.startsWith(first)) return 0
  if (label.split(/[\s›/-]+/).some((p) => p.startsWith(first))) return 1
  return 2
}

const results = computed<Item[]>(() => {
  const words = query.value.trim().toLowerCase().split(/\s+/).filter(Boolean)
  if (!words.length) return items.value.filter((i) => !i.searchOnly)
  const scored = items.value
    .map((item, i) => ({ item, i, s: score(item, words) }))
    .filter((x): x is { item: Item; i: number; s: number } => x.s !== null)
  // Best matches first, then the usual order; groups stay together.
  const groupRank = new Map<string, number>()
  for (const x of scored) groupRank.set(x.item.group, Math.min(groupRank.get(x.item.group) ?? 9, x.s))
  scored.sort((a, b) => (groupRank.get(a.item.group)! - groupRank.get(b.item.group)!) || a.s - b.s || a.i - b.i)
  return scored.slice(0, 60).map((x) => x.item)
})

const grouped = computed(() => {
  const out: { group: string; items: { item: Item; index: number }[] }[] = []
  results.value.forEach((item, index) => {
    const last = out[out.length - 1]
    if (last && last.group === item.group) last.items.push({ item, index })
    else out.push({ group: item.group, items: [{ item, index }] })
  })
  return out
})

watch(query, () => (active.value = 0))
watch(paletteOpen, (o) => {
  if (o) {
    query.value = ''
    active.value = 0
  }
})

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
    >
      <DialogTitle class="sr-only">Go to</DialogTitle>
      <DialogDescription class="sr-only">Type the name of an app or a page, then press Enter.</DialogDescription>
      <div class="flex items-center gap-2 border-b px-3">
        <Search class="size-4 shrink-0 text-muted-foreground" />
        <input
          ref="inputEl"
          v-model="query"
          class="h-12 min-w-0 flex-1 bg-transparent text-sm outline-none placeholder:text-muted-foreground"
          placeholder="Go to a project or a page..."
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
        <div v-for="g in grouped" :key="g.group" role="group" :aria-label="g.group" class="pb-1">
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
