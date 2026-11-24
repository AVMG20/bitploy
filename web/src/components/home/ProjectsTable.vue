<script setup lang="ts">
// Compact table density for the Home page: scans 15-30+ projects on one screen
// with status, domain, services, the user's chosen metric, and pinned timestamps.
import { computed } from 'vue'
import { RouterLink, useRouter } from 'vue-router'
import { ArrowUpRight, GitCommitHorizontal, Rocket, Sparkles } from 'lucide-vue-next'
import Sparkline from '@/components/Sparkline.vue'
import StatusDot from '@/components/StatusDot.vue'
import { useActiveBuilds } from '@/composables/queries'
import { nowMs } from '@/composables/useNow'
import { projectRoute } from '@/lib/appLinks'
import { exactTime, hostOf } from '@/lib/format'
import { formatHomeMetricPoint, formatHourTick, projectLastDeploy, shortRelativeTime } from '@/lib/home'
import type { HomeMetricMeta, ProjectMetricSummary } from '@/lib/home'
import { aliasOf, appRole, isPublicApp, mainApp, projectHealth, roleTitle, sortProjectApps } from '@/lib/projects'
import type { Project, ServiceTemplate } from '@/lib/types'
import { cn } from '@/lib/utils'

const props = defineProps<{
  projects: readonly Project[]
  templates: readonly ServiceTemplate[]
  metricMeta: HomeMetricMeta
  metricsByProject: Readonly<Record<string, ProjectMetricSummary | undefined>>
}>()

const router = useRouter()
const { phaseOf } = useActiveBuilds()

function servicesText(p: Project, templates: readonly ServiceTemplate[]): string {
  const sorted = sortProjectApps(p.apps, templates)
  if (!sorted.length) return 'Empty'
  if (sorted.length === 1) return roleTitle(appRole(sorted[0]!, templates))
  const names = sorted.slice(0, 4).map(aliasOf).join(', ')
  return sorted.length > 4 ? `${names} +${sorted.length - 4}` : names
}

function sparkColor(m: ProjectMetricSummary): string {
  if (m.tone === 'danger') return 'var(--destructive)'
  return m.active ? 'var(--chart-1)' : 'color-mix(in oklch, var(--muted-foreground) 45%, transparent)'
}

function formatSparkPoint(v: number): string {
  return formatHomeMetricPoint(props.metricMeta.key, v)
}

const rows = computed(() =>
  props.projects.map((p) => {
    const main = mainApp(p)
    const publicMain = main && isPublicApp(main) ? main : null
    const metric = props.metricsByProject[p.id]
    const health = projectHealth(p.apps)
    return {
      project: p,
      publicMain,
      services: servicesText(p, props.templates),
      metric,
      last: projectLastDeploy(p),
      status: health.status === 'empty' ? 'new' : health.status,
      phase: phaseOf(p.apps),
    }
  }),
)

function openProject(id: string) {
  void router.push(projectRoute(id))
}
</script>

<template>
  <div class="overflow-x-auto rounded-xl border bg-card shadow-card">
    <table class="w-full border-collapse text-sm">
      <thead>
        <tr class="border-b text-left text-[11px] font-semibold tracking-wider text-muted-foreground/80 uppercase">
          <th class="px-4 py-2.5">Project</th>
          <th class="hidden px-3 py-2.5 md:table-cell">Services</th>
          <th class="px-3 py-2.5 text-right whitespace-nowrap">{{ metricMeta.label }}</th>
          <th class="hidden px-3 py-2.5 lg:table-cell">Last deploy</th>
          <th class="px-4 py-2.5 text-right whitespace-nowrap">Updated</th>
        </tr>
      </thead>
      <tbody class="divide-y">
        <tr
          v-for="r in rows"
          :key="r.project.id"
          class="cursor-pointer transition-colors hover:bg-muted/40"
          @click="openProject(r.project.id)"
        >
          <td class="px-4 py-2.5">
            <div class="flex min-w-0 items-center gap-2.5">
              <StatusDot :status="r.status" :building="r.phase" />
              <div class="min-w-0">
                <RouterLink :to="projectRoute(r.project.id)" class="block truncate font-medium text-foreground hover:underline" @click.stop>
                  {{ r.project.name }}
                </RouterLink>
                <a
                  v-if="r.publicMain"
                  :href="r.publicMain.url"
                  target="_blank"
                  rel="noopener noreferrer"
                  class="inline-flex max-w-52 items-center gap-1 font-mono text-xs text-muted-foreground hover:text-foreground hover:underline"
                  @click.stop
                >
                  <span class="truncate">{{ hostOf(r.publicMain.url) }}</span>
                  <ArrowUpRight class="size-3 shrink-0" />
                </a>
                <span v-else class="block text-xs text-muted-foreground/75">No web address</span>
              </div>
            </div>
          </td>

          <td class="hidden max-w-52 truncate px-3 py-2.5 font-mono text-xs text-muted-foreground md:table-cell">
            {{ r.services }}
          </td>

          <td class="px-3 py-2.5 text-right whitespace-nowrap">
            <div
              v-if="r.metric"
              :class="cn(
                'inline-flex items-center justify-end gap-2.5 font-mono text-xs tabular-nums',
                r.metric.tone === 'danger'
                  ? 'font-semibold text-destructive'
                  : r.metric.active
                    ? 'font-medium text-foreground'
                    : 'text-muted-foreground/70',
              )"
            >
              <Sparkline
                v-if="r.metric.points.length > 1"
                class="hidden w-16 shrink-0 sm:block"
                svg-class="h-5"
                :values="r.metric.points"
                :color="sparkColor(r.metric)"
                :format="formatSparkPoint"
                :label-at="(i) => formatHourTick(r.metric?.pointTimes?.[i])"
                :label="`${r.project.name} ${metricMeta.label}`"
                @click.stop
              />
              <span :title="r.metric.title">{{ r.metric.display }}</span>
            </div>
            <span v-else class="font-mono text-xs text-muted-foreground/60">–</span>
          </td>

          <td class="hidden max-w-64 px-3 py-2.5 text-xs text-muted-foreground lg:table-cell">
            <div v-if="r.last" class="flex min-w-0 items-center gap-1.5">
              <Sparkles v-if="r.last.channel === 'mcp'" class="size-3.5 shrink-0 text-primary-text" aria-hidden="true" />
              <GitCommitHorizontal v-else-if="r.last.channel === 'github'" class="size-3.5 shrink-0" aria-hidden="true" />
              <Rocket v-else class="size-3.5 shrink-0" aria-hidden="true" />
              <span class="truncate">{{ r.last.what }}</span>
            </div>
            <span v-else class="text-muted-foreground/60">Nothing deployed yet</span>
          </td>

          <td class="px-4 py-2.5 text-right font-mono text-xs whitespace-nowrap text-muted-foreground/80 tabular-nums">
            <time
              v-if="r.last"
              :datetime="r.last.at"
              :title="exactTime(r.last.at)"
            >
              {{ shortRelativeTime(r.last.at, nowMs) }}
            </time>
            <span v-else>—</span>
          </td>
        </tr>
      </tbody>
    </table>
  </div>
</template>
