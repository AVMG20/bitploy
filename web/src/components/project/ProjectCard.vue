<script setup lang="ts">
import { computed } from 'vue'
import { RouterLink } from 'vue-router'
import { ArrowUpRight, GitCommitHorizontal, Rocket, Sparkles, Users } from 'lucide-vue-next'
import ServiceIcon from '@/components/ServiceIcon.vue'
import Sparkline from '@/components/Sparkline.vue'
import StatusDot from '@/components/StatusDot.vue'
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip'
import { useActiveBuilds } from '@/composables/queries'
import { nowMs } from '@/composables/useNow'
import { useTapTooltips } from '@/composables/useTapTooltips'
import { projectRoute } from '@/lib/appLinks'
import { appStatusInfo, exactTime, formatMB, hostOf } from '@/lib/format'
import type { Tone } from '@/lib/format'
import { formatHomeMetricPoint, formatHourTick, projectLastDeploy, shortRelativeTime } from '@/lib/home'
import type { ProjectMetricSummary } from '@/lib/home'
import { aliasOf, appRole, isPublicApp, mainApp, ownerName, projectHealth, projectHealthText, roleTitle, sortProjectApps } from '@/lib/projects'
import type { Project, ServiceTemplate } from '@/lib/types'
import { cn } from '@/lib/utils'

// A compact project card on Home: quiet green dot when healthy (loud badge only
// on errors/transitions), inline micro-sparkline for the user's chosen metric,
// service chips only when the project has several apps (otherwise a quiet stack
// line), and a pinned right-aligned deploy timestamp that never gets clipped.
const props = defineProps<{
  project: Project
  templates: readonly ServiceTemplate[]
  metric?: ProjectMetricSummary
}>()

const apps = computed(() => sortProjectApps(props.project.apps, props.templates))
const main = computed(() => mainApp(props.project))
const health = computed(() => projectHealth(props.project.apps))
const { phaseOf } = useActiveBuilds()
const MAX_CHIPS = 5
const healthText = computed(() => projectHealthText(props.project.apps))
const phase = computed(() => phaseOf(props.project.apps))

// Only show a loud status badge when attention is needed or the project isn't fully running.
const showStatusBadge = computed(() => {
  if (phase.value) return true
  const h = health.value
  return h.status !== 'running' || h.running < h.total
})

const PILL: Record<Tone, string> = {
  success: 'border-success/25 bg-success/10 text-success',
  warning: 'border-warning/30 bg-warning/10 text-warning-text',
  danger: 'border-destructive/30 bg-destructive/10 text-destructive',
  progress: 'border-border bg-muted text-foreground',
  muted: 'border-border bg-muted text-muted-foreground',
}
const pillClass = computed(() => PILL[appStatusInfo(health.value.status === 'empty' ? 'new' : health.value.status).tone])
const isFailed = computed(() => health.value.status === 'failed' || health.value.status === 'crashed')

const sparkColor = computed(() => {
  const m = props.metric
  if (!m) return 'var(--chart-1)'
  if (m.tone === 'danger') return 'var(--destructive)'
  return m.active ? 'var(--chart-1)' : 'color-mix(in oklch, var(--muted-foreground) 45%, transparent)'
})
function formatSparkPoint(v: number): string {
  return formatHomeMetricPoint(props.metric?.metric ?? 'requests', v)
}
function sparkTimeAt(index: number): string {
  return formatHourTick(props.metric?.pointTimes?.[index])
}
const last = computed(() => projectLastDeploy(props.project))

// Quiet single-app descriptor instead of repeating the project's name as a chip.
const singleAppSummary = computed(() => {
  if (apps.value.length !== 1) return ''
  const a = apps.value[0]!
  const parts = [roleTitle(appRole(a, props.templates))]
  if (a.static) parts.push('Static')
  else if (a.volume && a.volume_used_mb !== null && a.volume_used_mb > 0) parts.push(`${formatMB(a.volume_used_mb)} vol`)
  else if (a.git_source) parts.push('GitHub')
  return parts.join(' · ')
})

const sharing = computed(() => {
  const p = props.project
  if (p.shared) return `Shared with you by ${ownerName(p)}`
  if (p.member_count) return `Shared with ${p.member_count === 1 ? '1 person' : `${p.member_count} people`}`
  return ''
})
const tips = useTapTooltips()
</script>

<template>
  <article
    :class="cn(
      'group relative flex min-w-0 flex-col overflow-hidden rounded-xl border bg-card shadow-card transition-colors hover:border-foreground/25 hover:bg-card/85',
      isFailed && 'border-destructive/40',
    )"
  >
    <!-- Header: status dot + name/domain on left; chosen metric or status pill on right -->
    <div class="flex min-w-0 items-start justify-between gap-3 px-4 pt-3.5 pb-2.5">
      <div class="min-w-0 flex-1">
        <div class="flex min-w-0 items-center gap-2">
          <StatusDot :status="health.status === 'empty' ? 'new' : health.status" :building="phase" />
          <h3 class="min-w-0 truncate text-[15px] leading-snug font-semibold">
            <RouterLink :to="projectRoute(project.id)" class="after:absolute after:inset-0 after:rounded-xl focus-visible:outline-none">
              {{ project.name }}
            </RouterLink>
          </h3>
        </div>
        <a
          v-if="main && isPublicApp(main)"
          :href="main.url"
          target="_blank"
          rel="noopener noreferrer"
          class="relative z-10 mt-0.5 inline-flex max-w-full items-center gap-1 pl-4 font-mono text-xs text-muted-foreground hover:text-foreground hover:underline"
        >
          <span class="truncate">{{ hostOf(main.url) }}</span>
          <ArrowUpRight class="size-3 shrink-0" />
        </a>
        <p v-else class="mt-0.5 pl-4 text-xs text-muted-foreground">{{ apps.length ? 'No web address' : 'No apps yet' }}</p>
      </div>

      <span
        v-if="showStatusBadge"
        :class="cn('inline-flex h-5.5 shrink-0 items-center gap-1.5 rounded-md border px-2 text-[11px] font-medium whitespace-nowrap', pillClass)"
      >
        {{ phase === 'running' ? 'Deploying' : phase === 'queued' ? 'Queued' : healthText }}
      </span>

      <div
        v-else-if="metric && metric.value !== null"
        class="flex shrink-0 items-center gap-2.5"
      >
        <Sparkline
          v-if="metric.points.length > 1"
          class="relative z-10 w-16 shrink-0"
          svg-class="h-6"
          :values="metric.points"
          :color="sparkColor"
          :format="formatSparkPoint"
          :label-at="sparkTimeAt"
          :label="`${project.name} ${metric.unit}`"
        />
        <div class="flex shrink-0 flex-col items-end gap-0.5" :title="metric.title">
          <span
            :class="cn(
              'font-mono text-xs leading-none font-semibold tabular-nums',
              metric.tone === 'danger' ? 'text-destructive' : metric.active ? 'text-foreground' : 'font-normal text-muted-foreground/70',
            )"
          >
            {{ metric.display }}
          </span>
          <span class="text-[10.5px] leading-none text-muted-foreground/75">{{ metric.unit }}</span>
        </div>
      </div>
    </div>

    <!-- Middle row: multi-app chips OR quiet single-app metadata + shared indicator -->
    <div class="flex min-h-7 flex-wrap items-center gap-1.5 px-4 pb-3">
      <template v-if="apps.length > 1">
        <span
          v-for="a in apps.slice(0, MAX_CHIPS)"
          :key="a.id"
          :class="cn(
            'inline-flex max-w-full min-w-0 items-center gap-1.5 rounded-md border py-0.5 pr-2 pl-0.5 text-xs',
            a.status === 'failed' || a.status === 'crashed'
              ? 'border-destructive/35 bg-destructive/10 text-destructive'
              : 'bg-muted/40 text-foreground',
          )"
          :title="`${a.name}: ${appRole(a, templates).label}`"
        >
          <ServiceIcon :role="appRole(a, templates).key" size="sm" class="size-4.5 rounded [&>svg]:size-2.5" />
          <span class="truncate font-mono text-[11px]">{{ aliasOf(a) }}</span>
        </span>
        <span v-if="apps.length > MAX_CHIPS" class="px-1 text-xs text-muted-foreground">+{{ apps.length - MAX_CHIPS }}</span>
      </template>
      <span v-else-if="apps.length === 1" class="text-xs text-muted-foreground/85">{{ singleAppSummary }}</span>
      <span v-else class="text-xs text-muted-foreground">Open it to add an app or a database.</span>

      <Tooltip v-if="sharing" :open="tips.open('shared')" disable-closing-trigger @update:open="tips.onOpen('shared', $event)">
        <TooltipTrigger as-child>
          <span
            data-tap-tooltip="shared"
            class="relative z-10 ml-auto inline-flex shrink-0 cursor-default items-center gap-1 rounded-md bg-muted/60 px-1.5 py-0.5 font-mono text-[11px] text-muted-foreground tabular-nums hover:text-foreground"
            :aria-label="sharing"
            @pointerup="tips.onTap('shared', $event)"
          >
            <Users class="size-3 shrink-0" aria-hidden="true" />
            <span class="max-w-20 truncate">{{ project.shared ? ownerName(project) : project.member_count }}</span>
          </span>
        </TooltipTrigger>
        <TooltipContent side="top" @pointer-down-outside="tips.onOutside('shared', $event)" @escape-key-down="tips.close">{{ sharing }}</TooltipContent>
      </Tooltip>
    </div>

    <!-- Footer: source icon + commit message + pinned right-aligned relative time -->
    <div class="mt-auto flex min-w-0 items-center gap-2 border-t px-4 py-2.5 text-xs text-muted-foreground">
      <template v-if="last">
        <Sparkles v-if="last.channel === 'mcp'" class="size-3.5 shrink-0 text-primary-text" aria-hidden="true" />
        <GitCommitHorizontal v-else-if="last.channel === 'github'" class="size-3.5 shrink-0" aria-hidden="true" />
        <Rocket v-else class="size-3.5 shrink-0" aria-hidden="true" />
        <span class="min-w-0 flex-1 truncate">
          <span class="font-medium text-foreground">{{ last.who }}</span>
          <template v-if="last.app"> · {{ last.app }}</template>
          · {{ last.what }}
        </span>
        <time
          :datetime="last.at"
          :title="exactTime(last.at)"
          class="shrink-0 font-mono text-[11px] text-muted-foreground/80 tabular-nums"
        >
          {{ shortRelativeTime(last.at, nowMs) }}
        </time>
      </template>
      <span v-else class="min-w-0 truncate">Nothing deployed yet</span>
    </div>
  </article>
</template>
