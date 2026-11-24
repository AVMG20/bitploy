<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'
import { Container, LayoutGrid, List, Plus, SlidersHorizontal, Sparkles } from 'lucide-vue-next'
import ActivityRail from '@/components/home/ActivityRail.vue'
import AttentionList from '@/components/home/AttentionList.vue'
import FirstRun from '@/components/home/FirstRun.vue'
import HomeMetricCard from '@/components/home/HomeMetricCard.vue'
import ProjectsTable from '@/components/home/ProjectsTable.vue'
import ServerCard from '@/components/home/ServerCard.vue'
import { useServerUsage } from '@/components/home/useServerUsage'
import QueryError from '@/components/QueryError.vue'
import StorageAppsDialog from '@/components/StorageAppsDialog.vue'
import ProjectCard from '@/components/project/ProjectCard.vue'
import { Button } from '@/components/ui/button'
import { Select, SelectContent, SelectItem, SelectSeparator, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Skeleton } from '@/components/ui/skeleton'
import { useAiConnected, useProjects, useServiceTemplates } from '@/composables/queries'
import { useHomeMetrics } from '@/composables/useHomeMetrics'
import type { CardMetricPref } from '@/composables/useHomeMetrics'
import { useSession } from '@/composables/useSession'
import { filterAndSortProjects, HOME_METRICS, homeMetricMeta, isHomeMetric, isHomeView } from '@/lib/home'
import type { HomeFilter, HomeSort, HomeView } from '@/lib/home'
import { cn } from '@/lib/utils'

// Home: operational header, attention banner (only when something needs you),
// server capacity strip, balanced 2-column pulse row (selectable 24h metric
// chart + recent activity), then the full-width filterable Projects workspace.
const projects = useProjects()
const templates = useServiceTemplates()
const { user } = useSession()
const { me, quota, attention, storageUsed } = useServerUsage()
const aiConnected = useAiConnected()

const all = computed(() => projects.data.value?.projects ?? [])
const apps = computed(() => all.value.flatMap((p) => p.apps))
const storageOpen = ref(false)

const { chartMetric, cardMetricPref, cardMetric, chartSummary, cardSummary, chartLoading } = useHomeMetrics(all, quota)

const VIEW_KEY = 'bitploy.home.view'
const SORT_KEY = 'bitploy.home.sort'
function readSavedView(): HomeView {
  try {
    const raw = window.localStorage.getItem(VIEW_KEY)
    if (isHomeView(raw)) return raw
  } catch {
    // ignore storage errors
  }
  return 'grid'
}
function readSavedSort(): HomeSort {
  try {
    const raw = window.localStorage.getItem(SORT_KEY)
    if (raw === 'order' || raw === 'metric' || raw === 'recent') return raw
  } catch {
    // ignore storage errors
  }
  return 'order'
}

const filter = ref<HomeFilter>('all')
const sort = ref<HomeSort>(readSavedSort())
const view = ref<HomeView>(readSavedView())

watch(view, (next) => {
  try {
    window.localStorage.setItem(VIEW_KEY, next)
  } catch {
    // ignore storage errors
  }
})
watch(sort, (next) => {
  try {
    window.localStorage.setItem(SORT_KEY, next)
  } catch {
    // ignore storage errors
  }
})

const hasMultiApp = computed(() => all.value.some((p) => p.apps.length > 1))
const runningApps = computed(() => apps.value.filter((a) => a.status === 'running').length)
const failedApps = computed(() => apps.value.filter((a) => a.status === 'failed' || a.status === 'crashed' || a.current_deployment?.state === 'failed').length)

const visibleProjects = computed(() =>
  filterAndSortProjects(all.value, {
    filter: filter.value,
    sort: sort.value,
    values: cardSummary.value.byProject,
  }),
)

const cardMeta = computed(() => homeMetricMeta(cardMetric.value))

function onCardMetricChange(v: unknown) {
  if (v === 'match' || isHomeMetric(v)) cardMetricPref.value = v as CardMetricPref
}

function onSortChange(v: unknown) {
  if (v === 'order' || v === 'metric' || v === 'recent') sort.value = v
}

const firstName = computed(() => (user.value?.name || user.value?.email.split('@')[0] || '').trim().split(/\s+/)[0] ?? '')
const greeting = computed(() => {
  const h = new Date().getHours()
  const part = h < 12 ? 'Good morning' : h < 18 ? 'Good afternoon' : 'Good evening'
  return firstName.value ? `${part}, ${firstName.value}` : part
})
</script>

<template>
  <div class="grid gap-5">
    <FirstRun v-if="!projects.isPending.value && !projects.isError.value && !all.length" :name="firstName" />

    <template v-else>
      <header class="flex flex-wrap items-center justify-between gap-4">
        <div class="min-w-0">
          <div class="flex flex-wrap items-center gap-2.5">
            <h1 class="text-2xl font-semibold tracking-tight">{{ greeting }}</h1>
            <span
              v-if="apps.length"
              :class="cn(
                'inline-flex items-center gap-1.5 rounded-full border px-2.5 py-0.5 text-xs font-medium',
                failedApps > 0
                  ? 'border-destructive/30 bg-destructive/10 text-destructive'
                  : 'border-border bg-card text-muted-foreground',
              )"
            >
              <span :class="cn('size-1.5 rounded-full', failedApps > 0 ? 'bg-destructive' : 'bg-success')" aria-hidden="true" />
              <template v-if="failedApps > 0">{{ failedApps }} {{ failedApps === 1 ? 'app needs' : 'apps need' }} attention</template>
              <template v-else-if="runningApps === apps.length">All {{ apps.length }} apps running</template>
              <template v-else>{{ runningApps }} of {{ apps.length }} apps running</template>
            </span>
          </div>
          <p v-if="!projects.isPending.value" class="mt-1 flex flex-wrap items-center gap-x-2 gap-y-0.5 text-xs text-muted-foreground">
            <span>{{ all.length }} {{ all.length === 1 ? 'project' : 'projects' }}</span>
            <span aria-hidden="true">·</span>
            <span>{{ runningApps }} running {{ runningApps === 1 ? 'app' : 'apps' }}</span>
            <template v-if="chartSummary.total !== null">
              <span aria-hidden="true">·</span>
              <span class="tabular-nums">
                <template v-if="chartSummary.meta.kind === 'resource'">{{ chartSummary.totalDisplay }} {{ chartSummary.meta.cardUnit }}</template>
                <template v-else>{{ chartSummary.totalDisplay }} {{ chartSummary.meta.heroUnit }} (24h)</template>
              </span>
            </template>
          </p>
        </div>

        <div class="flex items-center gap-2">
          <Button v-if="aiConnected === false" variant="outline" as-child>
            <RouterLink :to="{ name: 'connect' }"><Sparkles /> Connect AI</RouterLink>
          </Button>
          <Button as-child>
            <RouterLink :to="{ name: 'new-app' }"><Plus /> New project</RouterLink>
          </Button>
        </div>
      </header>

      <AttentionList :apps="apps" :resources="attention" @storage="storageOpen = true" />

      <ServerCard @storage="storageOpen = true" />

      <StorageAppsDialog v-if="me.data.value" v-model:open="storageOpen" :projects="all" :owner-id="me.data.value.user.id" :used-mb="storageUsed" />

      <div class="grid gap-4 lg:grid-cols-[minmax(0,1fr)_minmax(0,1.15fr)]">
        <HomeMetricCard v-model:metric="chartMetric" :summary="chartSummary" :loading="chartLoading" />
        <ActivityRail />
      </div>

      <QueryError v-if="projects.isError.value && !projects.data.value" :error="projects.error.value" @retry="projects.refetch()" />

      <section v-else class="grid gap-3 pt-1" aria-label="Projects">
        <div class="flex flex-wrap items-center justify-between gap-2.5">
          <div class="flex flex-wrap items-center gap-2">
            <h2 class="inline-flex items-center gap-2 text-sm font-semibold">
              Projects
              <span class="rounded-full bg-muted px-2 py-0.5 font-mono text-xs font-medium text-muted-foreground tabular-nums">
                {{ visibleProjects.length }}
              </span>
            </h2>

            <div class="flex items-center gap-1" role="group" aria-label="Filter projects">
              <button
                type="button"
                :class="cn('rounded-md border px-2.5 py-1 text-xs font-medium transition-colors', filter === 'all' ? 'border-border bg-card text-foreground shadow-card' : 'border-transparent text-muted-foreground hover:bg-muted/60 hover:text-foreground')"
                @click="filter = 'all'"
              >
                All
              </button>
              <button
                v-if="hasMultiApp"
                type="button"
                :class="cn('rounded-md border px-2.5 py-1 text-xs font-medium transition-colors', filter === 'multi' ? 'border-border bg-card text-foreground shadow-card' : 'border-transparent text-muted-foreground hover:bg-muted/60 hover:text-foreground')"
                @click="filter = 'multi'"
              >
                Multi-app
              </button>
              <button
                type="button"
                :class="cn('rounded-md border px-2.5 py-1 text-xs font-medium transition-colors', filter === 'active' ? 'border-border bg-card text-foreground shadow-card' : 'border-transparent text-muted-foreground hover:bg-muted/60 hover:text-foreground')"
                @click="filter = 'active'"
              >
                Active
              </button>
            </div>
          </div>

          <div class="flex flex-wrap items-center gap-2">
            <!-- Independent card metric preference (defaults to matching the top chart) -->
            <Select :model-value="cardMetricPref" @update:model-value="onCardMetricChange">
              <SelectTrigger size="sm" aria-label="Metric shown on project cards" class="h-7 gap-1.5 text-xs text-muted-foreground">
                <SlidersHorizontal class="size-3.5 shrink-0" aria-hidden="true" />
                <span class="text-foreground">{{ cardMeta.label }}</span>
              </SelectTrigger>
              <SelectContent position="popper" align="end">
                <SelectItem value="match">Match overview ({{ chartSummary.meta.label }})</SelectItem>
                <SelectSeparator />
                <SelectItem v-for="m in HOME_METRICS" :key="m.key" :value="m.key">{{ m.label }}</SelectItem>
              </SelectContent>
            </Select>

            <Select :model-value="sort" @update:model-value="onSortChange">
              <SelectTrigger size="sm" aria-label="Sort projects" class="h-7 text-xs">
                <SelectValue />
              </SelectTrigger>
              <SelectContent position="popper" align="end">
                <SelectItem value="order">Sidebar order</SelectItem>
                <SelectItem value="metric">{{ cardMeta.sortLabel }}</SelectItem>
                <SelectItem value="recent">Recent deploy</SelectItem>
              </SelectContent>
            </Select>

            <div class="inline-flex rounded-lg border bg-card p-0.5 shadow-card" role="group" aria-label="Layout view">
              <button
                type="button"
                title="Cards view"
                aria-label="Cards view"
                :class="cn('inline-flex size-6 items-center justify-center rounded-md transition-colors', view === 'grid' ? 'bg-muted text-foreground' : 'text-muted-foreground hover:text-foreground')"
                @click="view = 'grid'"
              >
                <LayoutGrid class="size-3.5" aria-hidden="true" />
              </button>
              <button
                type="button"
                title="Compact list view"
                aria-label="Compact list view"
                :class="cn('inline-flex size-6 items-center justify-center rounded-md transition-colors', view === 'list' ? 'bg-muted text-foreground' : 'text-muted-foreground hover:text-foreground')"
                @click="view = 'list'"
              >
                <List class="size-3.5" aria-hidden="true" />
              </button>
            </div>
          </div>
        </div>

        <div v-if="projects.isPending.value" class="grid gap-3 md:grid-cols-2 lg:grid-cols-3">
          <Skeleton v-for="i in 6" :key="i" class="h-36 rounded-xl" />
        </div>

        <template v-else>
          <div v-if="!visibleProjects.length" class="rounded-xl border border-dashed bg-card/50 px-6 py-10 text-center text-sm text-muted-foreground">
            No projects match this filter.
            <button type="button" class="ml-1 font-medium text-foreground underline underline-offset-4" @click="filter = 'all'">Show all</button>
          </div>

          <ProjectsTable
            v-else-if="view === 'list'"
            :projects="visibleProjects"
            :templates="templates.data.value ?? []"
            :metric-meta="cardMeta"
            :metrics-by-project="cardSummary.byProject"
          />

          <div v-else class="grid gap-3 md:grid-cols-2 lg:grid-cols-3">
            <ProjectCard
              v-for="p in visibleProjects"
              :key="p.id"
              :project="p"
              :templates="templates.data.value ?? []"
              :metric="cardSummary.byProject[p.id]"
            />
            <RouterLink
              v-if="filter === 'all'"
              :to="{ name: 'new-app' }"
              class="grid min-h-32 place-items-center rounded-xl border border-dashed p-4 text-sm text-muted-foreground transition-colors hover:border-foreground/30 hover:text-foreground"
            >
              <span class="grid justify-items-center gap-1.5 text-center">
                <span class="flex size-8 items-center justify-center rounded-lg border bg-card"><Plus class="size-4" /></span>
                <span class="font-medium">New project</span>
                <span class="text-xs">From GitHub, a folder, an image or your AI</span>
              </span>
            </RouterLink>
          </div>

          <p v-if="projects.data.value?.fallback" class="text-xs text-muted-foreground">
            <Container class="mr-1 inline size-3.5 align-[-2px]" />This server shows each app as its own project. It cannot put several apps in one project.
          </p>
        </template>
      </section>
    </template>
  </div>
</template>
