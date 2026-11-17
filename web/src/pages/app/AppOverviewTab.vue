<script setup lang="ts">
import { computed } from 'vue'
import { RouterLink } from 'vue-router'
import { useQuery } from '@tanstack/vue-query'
import { ArrowRight, Database, Lock, LockOpen, Plus, Rocket, Sparkles, Undo2 } from 'lucide-vue-next'
import ConfirmDialog from '@/components/ConfirmDialog.vue'
import EmptyState from '@/components/EmptyState.vue'
import LogLines from '@/components/LogLines.vue'
import RelativeTime from '@/components/RelativeTime.vue'
import ServiceIcon from '@/components/ServiceIcon.vue'
import Sparkline from '@/components/Sparkline.vue'
import StatCard from '@/components/StatCard.vue'
import StatusBadge from '@/components/StatusBadge.vue'
import StatusDot from '@/components/StatusDot.vue'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Progress } from '@/components/ui/progress'
import { Skeleton } from '@/components/ui/skeleton'
import CopyForAiButton from '@/components/CopyForAiButton.vue'
import { keys, livePoll, REFRESH_MS, useApp, useAppMetrics, useBackups, useDataRoot, useProject, useProjectEnv, useServiceTemplates } from '@/composables/queries'
import { nowMs } from '@/composables/useNow'
import { api, fetchLogLines } from '@/lib/api'
import { appStatusInfo, cpuFromPercent, formatBytes, deploymentStateInfo, describeSource, formatCPU, formatMB, hostOf, pct, uptime, versionLabel } from '@/lib/format'
import { engineName, isServerDb, pickDatabase } from '@/lib/dbvalue'
import { plainHint } from '@/lib/errors'
import { aliasOf, appRole, projectIdOf, projectLinks, talksTo } from '@/lib/projects'
import { aboutService, isMulti } from '@/lib/services'
import { change, formatSeconds, timeChange } from '@/lib/traffic'
import type { Deployment, Domain, DomainStatus } from '@/lib/types'
import ConnectCard from './ConnectCard.vue'
import DeploymentWarnings from './DeploymentWarnings.vue'
import DeployProgress from './DeployProgress.vue'
import FailedDeployCard from './FailedDeployCard.vue'
import FirstSteps from './FirstSteps.vue'
import ProjectServices from './ProjectServices.vue'
import UploadVersionButton from './UploadVersionButton.vue'
import { useRollback } from './useRollback'

const props = defineProps<{ slug: string }>()
const app = useApp(() => props.slug)

const data = computed(() => app.data.value)
const multi = computed(() => isMulti(data.value))
const isWeb = computed(() => data.value?.type === 'web')
const dep = computed(() => data.value?.current_deployment ?? null)
const running = computed(() => data.value?.status === 'running')
// The database card: a PostgreSQL, MariaDB or MySQL app always has one; any
// other app when its saved files hold a SQLite file at the top. A failed or
// empty look shows no card and no error.
const serverDb = computed(() => isServerDb(data.value))
const dataRoot = useDataRoot(() => props.slug, () => !!data.value?.volume && !serverDb.value)
const dbFile = computed(() => {
  const items = dataRoot.data.value?.items ?? []
  const name = pickDatabase(items, props.slug)
  return name ? { name, size: items.find((f) => f.name === name)?.size ?? 0 } : null
})
const dbEngine = computed(() => engineName(data.value?.template === 'pgvector' ? 'postgres' : (data.value?.template ?? undefined)))
// A project with several services has its own numbers per service (ProjectServices).
const single = computed(() => !!dep.value && !multi.value)

// ---------------------------------------------------------------- deployments
const deployments = useQuery({
  queryKey: computed(() => keys.deployments(props.slug)),
  queryFn: () => api.deployments(props.slug),
  refetchInterval: livePoll(REFRESH_MS),
})
const recent = computed(() => (deployments.data.value ?? []).slice(0, 3))
// The newest deploy failed while an older version is live.
const failedLatest = computed(() => {
  const d = deployments.data.value?.[0]
  return d && dep.value && d.state === 'failed' && d.seq > dep.value.seq ? d : null
})
const { target, confirmOpen, rollback, goBackText, canRollback } = useRollback(() => props.slug)

function pill(d: Deployment) {
  if (d.id === dep.value?.id) return deploymentStateInfo('running', data.value?.status)
  if (d.state === 'superseded' || d.state === 'running') return { label: 'Old', tone: 'muted' as const }
  return deploymentStateInfo(d.state)
}
const WHO: Record<string, string> = { mcp: 'AI assistant', dashboard: 'You', github: 'GitHub', api: 'API', system: 'bitploy' }
const who = (d: Deployment) => WHO[d.channel] ?? d.channel
const messageOf = (d: Deployment) => d.commit_message?.split('\n')[0] || describeSource(d, data.value)

// ---------------------------------------------------------------- the numbers
const metrics = useAppMetrics(() => props.slug, '1h', () => single.value)
const live = computed(() => (running.value ? (metrics.data.value?.latest ?? data.value?.live ?? null) : null))
const cpuLimit = computed(() => (data.value?.cpu_millis ?? 0) / 1000)
const memoryPct = computed(() => (live.value ? pct(live.value.memory_mb, data.value?.memory_mb ?? 0) : 0))
const cpuPct = computed(() => (live.value ? pct(cpuFromPercent(live.value.cpu), cpuLimit.value) : 0))
const OVER = 75

// Visitors, answer time and errors of the last 24 hours; only websites have them.
const traffic = useQuery({
  queryKey: computed(() => keys.traffic(props.slug, '24h')),
  queryFn: () => api.traffic(props.slug, '24h'),
  enabled: computed(() => single.value && isWeb.value),
  refetchInterval: 10_000,
})
const t = computed(() => traffic.data.value ?? null)
const visitorChange = computed(() => change(t.value?.totals.visitors ?? null, t.value?.previous?.visitors))
const timeDiff = computed(() => timeChange(t.value?.totals.median_ms ?? null, t.value?.previous?.median_ms))

// ---------------------------------------------------------------- latest logs
// The last few lines of the running app; the Logs tab has the rest.
const logs = useQuery({
  queryKey: computed(() => ['overview-logs', props.slug]),
  queryFn: () => fetchLogLines({ slug: props.slug, kind: 'runtime' }),
  enabled: computed(() => single.value && running.value),
  refetchInterval: livePoll(10_000),
})
const lastLines = computed(() => (logs.data.value ?? []).slice(-5))

// ---------------------------------------------------------------- the right column
const hostRows = computed(() => {
  const d = data.value
  if (!d || !isWeb.value) return []
  const own = { host: hostOf(d.url), secure: d.url.startsWith('https:'), note: d.domains?.length ? '' : 'main' }
  const extra = (d.domains ?? []).map((x: Domain) => ({ host: x.hostname, secure: x.status === 'active', note: domainNote(x.status) }))
  return [...extra, own]
})
function domainNote(s: DomainStatus): string {
  return s === 'active' ? '' : s === 'failed' ? 'Failed' : s === 'issuing_cert' ? 'Getting certificate' : 'Waiting for DNS'
}

const REFERENCE_EXAMPLE = '${{name.VAR}}'
// Who this app reads from, by the variables that point there.
const pid = computed(() => (data.value ? projectIdOf(data.value) : ''))
const { project } = useProject(pid)
const templates = useServiceTemplates()
const siblings = computed(() => project.value?.apps ?? [])
const env = useProjectEnv(siblings)
const links = computed(() => projectLinks(siblings.value, env.value))
const talks = computed(() =>
  data.value
    ? talksTo(data.value, siblings.value, links.value).map((a) => ({
        app: a,
        role: appRole(a, templates.data.value ?? []),
        vars: links.value.find((l) => l.from === data.value!.id && l.to === a.id)?.vars ?? [],
      }))
    : [],
)

const volumePct = computed(() => pct(data.value?.volume_used_mb ?? 0, data.value?.volume_mb ?? 0))
const mountPath = computed(() => data.value?.volumes?.[0]?.mount_path || '/data')
const backups = useBackups(() => props.slug, () => !!data.value?.volume && !!data.value?.backups_supported)
const lastBackup = computed(() => backups.data.value?.items.find((b) => b.status === 'ready') ?? null)
const SCHEDULE: Record<string, string> = { every_6h: 'every 6 hours', every_12h: 'every 12 hours', daily: 'every night', weekly: 'every week', monthly: 'every month' }
const schedule = computed(() => SCHEDULE[backups.data.value?.settings.schedule ?? ''] ?? '')
</script>

<template>
  <div v-if="data" class="grid gap-4">
    <FailedDeployCard v-if="failedLatest && dep" :slug="slug" :app="data" :failed="failedLatest" :live="dep" />

    <!-- Without a running version the deploy card below shows the failure. -->
    <Alert v-if="(data.status === 'failed' && dep) || data.status === 'crashed'" variant="destructive">
      <AlertTitle>{{ data.status === 'crashed' ? `This app keeps crashing.` : `This app stopped working.` }}</AlertTitle>
      <AlertDescription>
        <p v-if="data.status_message">{{ data.status_message }}</p>
        <p v-if="dep?.error">{{ multi ? aboutService(dep.error.message, dep.error.service) : dep.error.message }} <span v-if="plainHint(dep.error.code, dep.error.hint)" class="font-medium">{{ plainHint(dep.error.code, dep.error.hint) }}</span></p>
        <div class="mt-1 flex flex-wrap items-center gap-x-4 gap-y-2">
          <CopyForAiButton :app="data" />
          <RouterLink :to="{ name: 'app-logs', params: { slug } }" class="underline">Open the logs</RouterLink>
        </div>
      </AlertDescription>
    </Alert>
    <Alert v-else-if="data.status_message && data.status !== 'failed'">
      <AlertDescription>{{ data.status_message }}</AlertDescription>
    </Alert>

    <FirstSteps v-if="data.catalog_id && running" :app="data" />

    <DeployProgress v-if="!dep" :slug="slug" :app="data">
      <EmptyState
        :icon="Rocket"
        title="Nothing deployed yet."
        :description="data.git_source ? 'Push to the linked branch to build it and put it online.' : 'Upload your files, or ask your AI assistant to deploy to this app.'"
      >
        <UploadVersionButton v-if="!data.git_source" :slug="slug" label="Upload files" />
        <Button as-child variant="outline"><RouterLink to="/connect">Connect your AI assistant</RouterLink></Button>
      </EmptyState>
    </DeployProgress>

    <template v-else>
      <DeploymentWarnings v-if="dep.warnings?.length" :warnings="dep.warnings" :slug="slug" />

      <ProjectServices v-if="multi" :app="data" />

      <template v-else>
        <!-- Four numbers: how the site does for a website, how the process does for a worker or database. -->
        <div class="grid grid-cols-2 gap-3 lg:grid-cols-4">
          <template v-if="isWeb">
            <StatCard
              compact
              title="Visitors, 24 h"
              :to="{ name: 'app-traffic', params: { slug } }"
              :loading="traffic.isPending.value"
              :value="t ? t.totals.visitors.toLocaleString() : '-'"
              :change="visitorChange"
            >
              <template #aside>
                <Sparkline v-if="t && t.points.length > 1" :values="t.points.map((p) => p.visitors)" color="var(--chart-1)" :format="(v) => `${Math.round(v)}`" label="Visitors per hour" />
              </template>
            </StatCard>
            <StatCard
              compact
              title="Response time"
              :to="{ name: 'app-traffic', params: { slug } }"
              :loading="traffic.isPending.value"
              :value="t?.totals.median_ms != null ? formatSeconds(t.totals.median_ms).replace(' s', '') : '-'"
              :suffix="t?.totals.median_ms != null ? 's, typical' : undefined"
              :change="timeDiff"
              hint="How long your app took to answer people. Half of the answers were faster than this."
            >
              <template #aside>
                <Sparkline v-if="t && t.points.length > 1" :values="t.points.map((p) => p.median_ms ?? 0)" color="var(--muted-foreground)" :format="(v) => `${Math.round(v)} ms`" label="Answer time per hour" />
              </template>
            </StatCard>
          </template>
          <template v-else>
            <StatCard
              compact
              title="CPU now"
              :value="live ? formatCPU(cpuFromPercent(live.cpu)).replace(' CPU', '') : running ? '...' : '0'"
              :suffix="`of ${formatCPU(cpuLimit)}`"
              :progress="cpuPct"
              :to="{ name: 'app-traffic', params: { slug } }"
            />
          </template>

          <StatCard
            compact
            title="Memory now"
            :value="live ? formatMB(live.memory_mb) : running ? '...' : '0 MB'"
            :suffix="`of ${formatMB(data.memory_mb)}`"
            :progress="memoryPct"
            :to="{ name: 'app-traffic', params: { slug } }"
          />

          <StatCard
            v-if="isWeb"
            compact
            title="Errors, 24 h"
            :loading="traffic.isPending.value"
            :value="t ? t.totals.errors.toLocaleString() : '-'"
            suffix="server errors"
            :to="{ name: 'app-logs', params: { slug } }"
          />
          <template v-else>
            <StatCard compact title="Running for" :value="running ? uptime(data.started_at, nowMs) : appStatusInfo(data.status).label" />
            <StatCard compact title="Automatic restarts" :value="String(data.restart_count)" :sub="data.restart_count ? 'The app stopped by itself and bitploy started it again.' : undefined" />
          </template>
        </div>

        <div class="grid items-start gap-4 lg:grid-cols-[minmax(0,1fr)_21rem]">
          <div class="grid min-w-0 content-start gap-4">
            <Card>
              <CardHeader class="flex flex-row items-center justify-between gap-2">
                <CardTitle>Recent deploys</CardTitle>
                <RouterLink :to="{ name: 'app-deployments', params: { slug } }" class="inline-flex items-center gap-1 text-xs text-muted-foreground hover:text-foreground">All deployments <ArrowRight class="size-3.5" /></RouterLink>
              </CardHeader>
              <CardContent class="px-0">
                <Skeleton v-if="deployments.isPending.value" class="mx-4 h-24" />
                <ul v-else class="divide-y border-t text-sm">
                  <li v-for="d in recent" :key="d.id" class="flex flex-wrap items-center gap-x-3 gap-y-1 px-4 py-2.5">
                    <StatusBadge :label="pill(d).label" :tone="pill(d).tone" />
                    <span class="w-7 shrink-0 font-mono text-xs text-muted-foreground">v{{ d.seq }}</span>
                    <span class="min-w-0 flex-1 truncate max-sm:basis-full max-sm:order-last" :title="messageOf(d)">{{ messageOf(d) }}</span>
                    <span class="inline-flex items-center gap-1 text-xs text-muted-foreground"><Sparkles v-if="d.channel === 'mcp'" class="size-3.5 text-primary-text" aria-hidden="true" />{{ who(d) }}</span>
                    <span class="w-20 text-right text-xs text-muted-foreground"><RelativeTime :date="d.finished_at ?? d.queued_at" /></span>
                    <Button v-if="canRollback(d)" size="xs" variant="ghost" class="h-6 px-2 text-xs" @click="target = d"><Undo2 /> Roll back</Button>
                  </li>
                </ul>
              </CardContent>
            </Card>

            <Card v-if="running">
              <CardHeader class="flex flex-row items-center justify-between gap-2">
                <CardTitle class="inline-flex items-center gap-2">Latest logs <span class="size-1.5 rounded-full bg-success status-pulse" aria-hidden="true" /></CardTitle>
                <RouterLink :to="{ name: 'app-logs', params: { slug } }" class="inline-flex items-center gap-1 text-xs text-muted-foreground hover:text-foreground">Open logs <ArrowRight class="size-3.5" /></RouterLink>
              </CardHeader>
              <CardContent>
                <Skeleton v-if="logs.isPending.value" class="h-24" />
                <div v-else-if="lastLines.length" class="overflow-x-auto font-mono text-xs leading-relaxed"><LogLines :lines="lastLines" /></div>
                <p v-else class="text-sm text-muted-foreground">Nothing printed yet.</p>
              </CardContent>
            </Card>
          </div>

          <div class="grid min-w-0 content-start gap-4">
            <ConnectCard v-if="data.template" :app="data" />

            <Card v-if="serverDb || dbFile" data-testid="database-card">
              <CardContent class="flex items-center gap-3">
                <span class="flex size-9 shrink-0 items-center justify-center rounded-lg border bg-muted/40"><Database class="size-4" aria-hidden="true" /></span>
                <span class="min-w-0 flex-1">
                  <span class="block text-sm font-medium">Database</span>
                  <span class="block truncate font-mono text-xs text-muted-foreground">{{ dbFile ? `${dbFile.name} · ${formatBytes(dbFile.size)}` : dbEngine }}</span>
                </span>
                <Button as-child size="sm" variant="outline"><RouterLink :to="{ name: 'app-database-default', params: { slug } }">Open</RouterLink></Button>
              </CardContent>
            </Card>

            <Card v-if="isWeb">
              <CardHeader class="flex flex-row items-center justify-between gap-2">
                <CardTitle>Domains</CardTitle>
                <Button as-child size="sm" variant="ghost" class="-my-1 -mr-2"><RouterLink :to="{ name: 'app-domains', params: { slug } }"><Plus /> Add</RouterLink></Button>
              </CardHeader>
              <CardContent>
                <ul class="grid gap-2 text-sm">
                  <li v-for="h in hostRows" :key="h.host" class="flex items-center gap-2">
                    <Lock v-if="h.secure" class="size-3.5 shrink-0 text-success" aria-label="Secure (HTTPS)" />
                    <LockOpen v-else class="size-3.5 shrink-0 text-muted-foreground" aria-label="Not secure yet" />
                    <span class="min-w-0 flex-1 truncate font-mono text-[13px]">{{ h.host }}</span>
                    <span v-if="h.note" class="shrink-0 text-xs text-muted-foreground">{{ h.note }}</span>
                  </li>
                </ul>
              </CardContent>
            </Card>

            <Card v-if="!data.template && siblings.length > 1">
              <CardHeader><CardTitle>Talks to</CardTitle></CardHeader>
              <CardContent>
                <ul v-if="talks.length" class="grid gap-2.5 text-sm">
                  <li v-for="x in talks" :key="x.app.id">
                    <RouterLink :to="{ name: 'app', params: { slug: x.app.slug } }" class="flex items-center gap-2.5 rounded-md hover:bg-muted/40">
                      <ServiceIcon :role="x.role.key" size="sm" />
                      <span class="min-w-0 flex-1">
                        <span class="block truncate font-mono text-[13px]">{{ aliasOf(x.app) }}</span>
                        <span v-if="x.vars.length" class="block truncate text-xs text-muted-foreground">through {{ x.vars.join(', ') }}</span>
                      </span>
                      <StatusDot :status="x.app.status" />
                    </RouterLink>
                  </li>
                </ul>
                <p v-else class="text-sm text-muted-foreground">Not connected to another app yet. Point a variable at one with <span class="font-mono text-xs">{{ REFERENCE_EXAMPLE }}</span>.</p>
              </CardContent>
            </Card>

            <Card>
              <CardHeader class="flex flex-row items-center justify-between gap-2">
                <CardTitle>Saved files</CardTitle>
                <span class="font-mono text-xs text-muted-foreground">{{ mountPath }}</span>
              </CardHeader>
              <CardContent class="grid gap-2.5 text-sm">
                <template v-if="data.volume">
                  <div class="flex items-baseline justify-between gap-3">
                    <span class="text-muted-foreground">Used</span>
                    <span class="font-medium tabular-nums">
                      <template v-if="data.volume_used_mb !== null">{{ formatMB(data.volume_used_mb) }} <span class="font-normal text-muted-foreground">of {{ formatMB(data.volume_mb) }}</span></template>
                      <template v-else><span class="font-normal text-muted-foreground">Limit</span> {{ formatMB(data.volume_mb) }}</template>
                    </span>
                  </div>
                  <Progress class="[&>[data-slot=progress-indicator]]:bg-success" :class="volumePct > OVER && '[&>[data-slot=progress-indicator]]:bg-destructive'" :model-value="volumePct" />
                  <p v-if="!data.backups_supported" class="text-xs text-muted-foreground">Kept between deploys. Not backed up: it can be rebuilt.</p>
                  <p v-else-if="lastBackup" class="flex items-center gap-1.5 text-xs text-muted-foreground">
                    <span class="size-1.5 rounded-full bg-success" aria-hidden="true" />
                    <span v-if="schedule">Backed up {{ schedule }}, </span><span v-else>No schedule, </span>last <RelativeTime :date="lastBackup.created_at" />
                  </p>
                  <p v-else-if="!backups.isPending.value" class="text-xs text-muted-foreground">No backup yet.</p>
                </template>
                <p v-else class="text-muted-foreground">Off. Files the app writes are gone after a deploy.</p>
                <RouterLink :to="{ name: 'app-storage', params: { slug } }" class="inline-flex items-center gap-1 text-xs font-medium hover:underline">
                  Storage &amp; backups <ArrowRight class="size-3.5" />
                </RouterLink>
              </CardContent>
            </Card>
          </div>
        </div>
      </template>
    </template>

    <ConfirmDialog
      v-model:open="confirmOpen"
      :title="`Go back to ${versionLabel(target?.seq).toLowerCase()}?`"
      :description="goBackText"
      confirm-label="Go back"
      @confirm="target && rollback.mutate(target)"
    />
  </div>
  <div v-else class="grid gap-4">
    <Skeleton class="h-20 w-full" />
    <Skeleton class="h-64 w-full" />
  </div>
</template>
