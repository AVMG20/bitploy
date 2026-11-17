<script setup lang="ts">
import { computed } from 'vue'
import { RouterLink } from 'vue-router'
import { useQuery } from '@tanstack/vue-query'
import { ArrowUpRight, GitCommitHorizontal, Rocket, Sparkles, Users } from 'lucide-vue-next'
import RelativeTime from '@/components/RelativeTime.vue'
import ServiceIcon from '@/components/ServiceIcon.vue'
import StatusDot from '@/components/StatusDot.vue'
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip'
import { keys, useActiveBuilds } from '@/composables/queries'
import { useTapTooltips } from '@/composables/useTapTooltips'
import { api } from '@/lib/api'
import { projectRoute } from '@/lib/appLinks'
import { appStatusInfo, formatCount, hostOf, versionLabel } from '@/lib/format'
import type { Tone } from '@/lib/format'
import { aliasOf, appRole, isPublicApp, mainApp, ownerName, projectHealth, projectHealthText, sortProjectApps } from '@/lib/projects'
import type { App, Project, ServiceTemplate } from '@/lib/types'
import { cn } from '@/lib/utils'

// A project on the home page: its name, its address, whether everything runs,
// a chip per app (what it is and its short name), the visitors of its website
// today and the last deploy: who or what did it, which change, when.
const props = defineProps<{ project: Project; templates: readonly ServiceTemplate[] }>()

const apps = computed(() => sortProjectApps(props.project.apps, props.templates))
const main = computed(() => mainApp(props.project))
const health = computed(() => projectHealth(props.project.apps))
const { phaseOf } = useActiveBuilds()
const MAX_CHIPS = 6
const healthText = computed(() => projectHealthText(props.project.apps))

const PILL: Record<Tone, string> = {
  success: 'border-success/25 bg-success/10 text-success',
  warning: 'border-warning/30 bg-warning/10 text-warning-text',
  danger: 'border-destructive/25 bg-destructive/10 text-destructive',
  progress: 'border-border bg-muted text-foreground',
  muted: 'border-border bg-muted text-muted-foreground',
}
const pillClass = computed(() => PILL[appStatusInfo(health.value.status === 'empty' ? 'new' : health.value.status).tone])

// Visitors: one small request for the website of the project, only when it is
// public and running (the same request the app's own page makes, so it is shared).
const web = computed<App | undefined>(() => (main.value && isPublicApp(main.value) && main.value.status === 'running' ? main.value : undefined))
const traffic = useQuery({
  enabled: () => !!web.value,
  queryKey: computed(() => keys.traffic(web.value?.slug ?? '', 'today')),
  queryFn: () => api.traffic(web.value!.slug, 'today'),
  staleTime: 30_000,
})
const visitors = computed(() => traffic.data.value?.visitors_today)
const spark = computed(() => {
  const pts = traffic.data.value?.points ?? []
  if (pts.length < 2) return ''
  const v = pts.map((p) => p.visitors)
  const top = Math.max(1, ...v)
  const n = v.length - 1
  return v.map((x, i) => `${((i / n) * 120).toFixed(1)},${(30 - (x / top) * 26).toFixed(1)}`).join(' ')
})

// The newest deployment of any app in the project.
const last = computed(() => {
  let best: { app: App; at: string } | null = null
  for (const a of props.project.apps) {
    const d = a.current_deployment
    const at = d?.finished_at ?? d?.queued_at
    if (d && at && (!best || at > best.at)) best = { app: a, at }
  }
  if (!best) return null
  const d = best.app.current_deployment!
  const who: Record<string, string> = { mcp: 'AI assistant', github: 'Git push', dashboard: 'Dashboard', api: 'API', system: 'bitploy' }
  const what = d.commit_message?.split('\n')[0] || versionLabel(d.seq)
  return {
    who: who[d.channel] ?? d.channel,
    channel: d.channel,
    // Only name the app when the project has several.
    app: props.project.apps.length > 1 ? aliasOf(best.app) : '',
    what,
    at: best.at,
  }
})

// Who else is on the project, in full for the tooltip next to the short form.
const sharing = computed(() => {
  const p = props.project
  if (p.shared) return `Shared with you by ${ownerName(p)}`
  if (p.member_count) return `Shared with ${p.member_count === 1 ? '1 person' : `${p.member_count} people`}`
  return ''
})
const tips = useTapTooltips()
</script>

<template>
  <article class="group relative flex min-w-0 flex-col overflow-hidden rounded-xl border bg-card shadow-card transition-colors hover:border-foreground/25">
    <div class="flex min-w-0 items-start justify-between gap-3 px-4 pt-4">
      <div class="min-w-0">
        <h3 class="truncate font-semibold">
          <RouterLink :to="projectRoute(project.id)" class="after:absolute after:inset-0 after:rounded-xl focus-visible:outline-none">{{ project.name }}</RouterLink>
        </h3>
        <a
          v-if="main && isPublicApp(main)"
          :href="main.url"
          target="_blank"
          rel="noopener noreferrer"
          class="relative z-10 mt-0.5 inline-flex max-w-full items-center gap-1 font-mono text-xs text-muted-foreground hover:text-foreground hover:underline"
        >
          <span class="truncate">{{ hostOf(main.url) }}</span><ArrowUpRight class="size-3 shrink-0" />
        </a>
        <p v-else class="mt-0.5 text-xs text-muted-foreground">{{ apps.length ? 'No web address' : 'No apps yet' }}</p>
      </div>
      <span :class="cn('inline-flex h-5.5 shrink-0 items-center gap-1.5 rounded-md border px-2 text-xs font-medium whitespace-nowrap', pillClass)">
        <StatusDot :status="health.status === 'empty' ? 'new' : health.status" :building="phaseOf(project.apps)" aria-hidden="true" />
        <span>{{ healthText }}</span>
      </span>
    </div>

    <!-- The apps, one chip each. -->
    <div class="mx-4 mt-3.5 mb-4 flex flex-wrap items-center gap-1.5">
      <span
        v-for="a in apps.slice(0, MAX_CHIPS)"
        :key="a.id"
        class="inline-flex max-w-full min-w-0 items-center gap-1.5 rounded-md border bg-muted/40 py-0.5 pr-2 pl-0.5 text-xs"
        :title="`${a.name}: ${appRole(a, templates).label}`"
      >
        <ServiceIcon :role="appRole(a, templates).key" size="sm" class="size-5 rounded [&>svg]:size-3" />
        <span class="truncate font-mono">{{ aliasOf(a) }}</span>
      </span>
      <span v-if="apps.length > MAX_CHIPS" class="px-1 text-xs text-muted-foreground">and {{ apps.length - MAX_CHIPS }} more</span>
      <span v-if="!apps.length" class="text-xs text-muted-foreground">Open it to add an app or a database.</span>
    </div>

    <!-- Visitors of the website today; nothing here for a project without a running website. -->
    <div v-if="web" class="mb-4 flex items-end justify-between gap-3 px-4">
      <div>
        <div class="text-xl font-semibold tracking-tight tabular-nums">{{ visitors === undefined ? '–' : formatCount(visitors) }}</div>
        <div class="text-xs text-muted-foreground">visitors today</div>
      </div>
      <svg v-if="spark" viewBox="0 0 120 32" class="h-8 w-30 shrink-0 text-primary" role="img" aria-label="Visitors per hour today">
        <polyline fill="none" stroke="currentColor" stroke-width="1.6" stroke-linejoin="round" stroke-linecap="round" :points="spark" />
      </svg>
    </div>

    <!-- The last deploy, and who else is on the project. -->
    <div class="mt-auto flex min-w-0 items-center gap-2 border-t px-4 py-3 text-xs text-muted-foreground">
      <template v-if="last">
        <Sparkles v-if="last.channel === 'mcp'" class="size-3.5 shrink-0 text-primary-text" aria-hidden="true" />
        <GitCommitHorizontal v-else-if="last.channel === 'github'" class="size-3.5 shrink-0" aria-hidden="true" />
        <Rocket v-else class="size-3.5 shrink-0" aria-hidden="true" />
        <span class="min-w-0 truncate">
          <span class="text-foreground">{{ last.who }}</span>
          <template v-if="last.app"> · {{ last.app }}</template>
          · {{ last.what }} · <RelativeTime :date="last.at" />
        </span>
      </template>
      <span v-else class="min-w-0 truncate">Nothing deployed yet</span>
      <Tooltip v-if="sharing" :open="tips.open('shared')" disable-closing-trigger @update:open="tips.onOpen('shared', $event)">
        <TooltipTrigger as-child>
          <span
            data-tap-tooltip="shared"
            class="relative z-10 -m-1 ml-auto inline-flex shrink-0 cursor-default items-center gap-1 rounded-sm p-1 tabular-nums hover:text-foreground"
            :aria-label="sharing"
            @pointerup="tips.onTap('shared', $event)"
          >
            <Users class="size-3 shrink-0" aria-hidden="true" /><span class="max-w-24 truncate">{{ project.shared ? ownerName(project) : project.member_count }}</span>
          </span>
        </TooltipTrigger>
        <TooltipContent side="top" @pointer-down-outside="tips.onOutside('shared', $event)" @escape-key-down="tips.close">{{ sharing }}</TooltipContent>
      </Tooltip>
    </div>
  </article>
</template>
