<script setup lang="ts">
import { computed, ref } from 'vue'
import { RouterLink } from 'vue-router'
import { ArrowRight, Rocket } from 'lucide-vue-next'
import EmptyState from '@/components/EmptyState.vue'
import MetricChart from '@/components/MetricChart.vue'
import QueryError from '@/components/QueryError.vue'
import RelativeTime from '@/components/RelativeTime.vue'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Progress } from '@/components/ui/progress'
import { Skeleton } from '@/components/ui/skeleton'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { useApp, useAppMetrics, useAppVisitorsToday } from '@/composables/queries'
import { nowMs } from '@/composables/useNow'
import { channelLabel, commitUrl, describeSource, formatCount, formatCPUMillis, formatDuration, formatMB, formatPercent, pct, shortSha, uptime } from '@/lib/format'
import type { MetricWindow } from '@/lib/types'
import DeployProgress from './DeployProgress.vue'
import UploadVersionButton from './UploadVersionButton.vue'

const props = defineProps<{ slug: string }>()
const app = useApp(() => props.slug)

const windowSel = ref<MetricWindow>('1h')
const windows: { value: MetricWindow; label: string }[] = [
  { value: '1h', label: '1 hour' },
  { value: '24h', label: '24 hours' },
  { value: '7d', label: '7 days' },
]
const metrics = useAppMetrics(() => props.slug, windowSel)
const points = computed(() => metrics.data.value?.points ?? [])

const data = computed(() => app.data.value)
const isWeb = computed(() => data.value?.type === 'web')
const traffic = useAppVisitorsToday(() => props.slug)
const dep = computed(() => data.value?.current_deployment ?? null)
const running = computed(() => data.value?.status === 'running')
// The newest 15-second sample, only while the app runs.
const live = computed(() => (running.value ? (data.value?.live ?? null) : null))
const cpuLimitPct = computed(() => (data.value?.cpu_millis ?? 0) / 10) // 1000 millis = 100% of one core
const memoryLimit = computed(() => metrics.data.value?.memory_limit_mb ?? data.value?.memory_mb ?? 0)
</script>

<template>
  <div v-if="data" class="grid gap-4">
    <!-- Without a running version the deploy card below shows the failure. -->
    <Alert v-if="(data.status === 'failed' && dep) || data.status === 'crashed'" variant="destructive">
      <AlertTitle>{{ data.status === 'crashed' ? 'This app keeps crashing.' : 'This app stopped working.' }}</AlertTitle>
      <AlertDescription>
        <p v-if="data.status_message">{{ data.status_message }}</p>
        <p v-if="dep?.error">{{ dep.error.message }} <span v-if="dep.error.hint" class="font-medium">{{ dep.error.hint }}</span></p>
        <RouterLink :to="{ name: 'app-logs', params: { slug } }" class="underline">Open the logs</RouterLink>
      </AlertDescription>
    </Alert>
    <Alert v-else-if="data.status_message && data.status !== 'failed'">
      <AlertDescription>{{ data.status_message }}</AlertDescription>
    </Alert>

    <DeployProgress v-if="!dep" :slug="slug">
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
      <!-- Live numbers: what the app uses right now, against its limits. -->
      <div class="grid grid-cols-2 overflow-hidden rounded-xl border bg-card shadow-card lg:grid-cols-4">
        <div class="border-b p-4 lg:border-b-0">
          <p class="text-xs font-medium tracking-wide text-muted-foreground uppercase">Running for</p>
          <p class="mt-1 text-xl font-semibold tracking-tight tabular-nums">{{ running ? uptime(data.started_at, nowMs) : '-' }}</p>
          <p v-if="isWeb && data.restart_count > 0" class="mt-1 text-xs text-muted-foreground">
            Restarted {{ data.restart_count === 1 ? 'once' : data.restart_count + ' times' }} by itself
          </p>
        </div>
        <div class="border-b border-l p-4 lg:border-b-0">
          <p class="text-xs font-medium tracking-wide text-muted-foreground uppercase">Memory now</p>
          <p class="mt-1 text-xl font-semibold tracking-tight tabular-nums">
            {{ live ? formatMB(live.memory_mb) : '-' }}
            <span class="text-sm font-normal tracking-normal text-muted-foreground">of {{ formatMB(data.memory_mb) }}</span>
          </p>
          <Progress class="mt-2 [&>[data-slot=progress-indicator]]:bg-success" :model-value="live ? pct(live.memory_mb, data.memory_mb) : 0" />
        </div>
        <div class="p-4 lg:border-l">
          <p class="text-xs font-medium tracking-wide text-muted-foreground uppercase">Processor now</p>
          <p class="mt-1 text-xl font-semibold tracking-tight tabular-nums">
            {{ live ? formatPercent(live.cpu) : '-' }}
            <span class="text-sm font-normal tracking-normal text-muted-foreground">of {{ formatCPUMillis(data.cpu_millis) }}</span>
          </p>
          <Progress class="mt-2" :model-value="live ? pct(live.cpu, cpuLimitPct) : 0" />
        </div>
        <RouterLink
          v-if="isWeb"
          :to="{ name: 'app-traffic', params: { slug } }"
          class="border-l p-4 transition-colors hover:bg-muted/40"
        >
          <p class="text-xs font-medium tracking-wide text-muted-foreground uppercase">Visitors today</p>
          <p class="mt-1 text-xl font-semibold tracking-tight tabular-nums">{{ traffic.data.value ? formatCount(traffic.data.value.visitors_today) : '-' }}</p>
        </RouterLink>
        <div v-else class="border-l p-4">
          <p class="text-xs font-medium tracking-wide text-muted-foreground uppercase">Automatic restarts</p>
          <p class="mt-1 text-xl font-semibold tracking-tight tabular-nums">{{ data.restart_count }}</p>
        </div>
      </div>

      <Card>
        <CardHeader class="flex flex-row flex-wrap items-center justify-between gap-2">
          <CardTitle>Usage</CardTitle>
          <Tabs v-model="windowSel">
            <TabsList>
              <TabsTrigger v-for="w in windows" :key="w.value" :value="w.value">{{ w.label }}</TabsTrigger>
            </TabsList>
          </Tabs>
        </CardHeader>
        <CardContent>
          <QueryError v-if="metrics.isError.value && !metrics.data.value" :error="metrics.error.value" @retry="metrics.refetch()" />
          <Skeleton v-else-if="metrics.isPending.value" class="h-48 w-full" />
          <p v-else-if="points.length < 2" class="py-12 text-center text-sm text-muted-foreground">
            No data yet. Charts fill in about a minute after the app starts running.
          </p>
          <div v-else class="grid gap-6 md:grid-cols-2">
            <div class="min-w-0">
              <p class="mb-2 flex items-baseline justify-between gap-2 text-sm">
                <span class="font-medium">Processor</span>
                <span class="text-xs text-muted-foreground">% of one core</span>
              </p>
              <MetricChart :points="points" field="cpu" label="Processor" unit="%" :window="windowSel" :limit="cpuLimitPct" color="var(--chart-1)" />
            </div>
            <div class="min-w-0">
              <p class="mb-2 flex items-baseline justify-between gap-2 text-sm">
                <span class="font-medium">Memory</span>
                <span class="text-xs text-muted-foreground">MB, limit {{ formatMB(memoryLimit) }}</span>
              </p>
              <MetricChart :points="points" field="memory_mb" label="Memory" unit="MB" :window="windowSel" :limit="memoryLimit" color="var(--chart-2)" />
            </div>
          </div>
        </CardContent>
      </Card>

      <div class="grid gap-4 lg:grid-cols-2">
        <Card>
          <CardHeader class="flex flex-row items-center justify-between gap-2">
            <CardTitle>Current version</CardTitle>
            <span class="font-mono text-xs text-muted-foreground">#{{ dep.seq }}</span>
          </CardHeader>
          <CardContent class="flex flex-1 flex-col">
            <dl class="mb-5 grid grid-cols-[8rem_1fr] gap-x-4 gap-y-2.5 text-sm">
              <dt class="text-muted-foreground">Source</dt>
              <dd class="min-w-0">{{ describeSource(dep, data) }}</dd>
              <template v-if="dep.commit_sha">
                <dt class="text-muted-foreground">Commit</dt>
                <dd class="min-w-0">
                  <a v-if="commitUrl(data, dep.commit_sha)" :href="commitUrl(data, dep.commit_sha)" target="_blank" rel="noopener noreferrer" class="font-mono hover:underline">{{ shortSha(dep.commit_sha) }}</a>
                  <span v-else class="font-mono">{{ shortSha(dep.commit_sha) }}</span>
                  <span v-if="dep.commit_message" class="block truncate text-muted-foreground">{{ dep.commit_message }}</span>
                </dd>
              </template>
              <dt class="text-muted-foreground">Started by</dt>
              <dd>{{ channelLabel(dep.channel) }}</dd>
              <dt class="text-muted-foreground">Deployed</dt>
              <dd><RelativeTime :date="dep.finished_at ?? dep.queued_at" /></dd>
              <template v-if="dep.duration_seconds !== null">
                <dt class="text-muted-foreground">Build took</dt>
                <dd>{{ formatDuration(dep.duration_seconds) }}</dd>
              </template>
              <dt class="text-muted-foreground">Size</dt>
              <dd>{{ formatMB(data.memory_mb) }} memory, {{ formatCPUMillis(data.cpu_millis) }}</dd>
            </dl>
            <div class="mt-auto flex items-center justify-between gap-3 border-t pt-3 text-xs text-muted-foreground">
              <span>You can go back to an earlier version.</span>
              <RouterLink :to="{ name: 'app-deployments', params: { slug } }" class="inline-flex items-center gap-1 font-medium text-foreground hover:underline">
                All deployments <ArrowRight class="size-3.5" />
              </RouterLink>
            </div>
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle>Storage</CardTitle>
          </CardHeader>
          <CardContent class="flex flex-1 flex-col gap-4 pb-1 text-sm">
            <div class="flex items-baseline justify-between gap-4">
              <span class="text-muted-foreground">App files <span class="text-muted-foreground/70">(code, image, saved versions)</span></span>
              <span class="shrink-0 font-semibold tabular-nums">{{ data.files_mb !== null ? formatMB(data.files_mb) : 'Not measured yet' }}</span>
            </div>
            <div v-if="data.volume" class="grid gap-2">
              <div class="flex items-baseline justify-between gap-4">
                <span class="text-muted-foreground">Saved data <span class="font-mono text-xs">/data</span></span>
                <span class="shrink-0 font-semibold tabular-nums">
                  <template v-if="data.volume_used_mb !== null">{{ formatMB(data.volume_used_mb) }} <span class="font-normal text-muted-foreground">of {{ formatMB(data.volume_mb) }}</span></template>
                  <template v-else><span class="font-normal text-muted-foreground">Limit</span> {{ formatMB(data.volume_mb) }}</template>
                </span>
              </div>
              <Progress class="[&>[data-slot=progress-indicator]]:bg-success" :model-value="pct(data.volume_used_mb ?? 0, data.volume_mb)" />
            </div>
            <div v-else class="flex items-baseline justify-between gap-4">
              <span class="text-muted-foreground">Saved data <span class="font-mono text-xs">/data</span></span>
              <span class="text-muted-foreground">Off</span>
            </div>
            <div class="flex items-baseline justify-between gap-4">
              <span class="text-muted-foreground">Backups</span>
              <span class="shrink-0 font-semibold tabular-nums" :class="!data.backups_mb && 'font-normal text-muted-foreground'">{{ data.backups_mb ? formatMB(data.backups_mb) : 'None' }}</span>
            </div>
            <div class="mt-auto flex items-center justify-between gap-3 border-t pt-3 text-xs text-muted-foreground">
              <span>
                <template v-if="data.disk_measured_at">Measured <RelativeTime :date="data.disk_measured_at" /> · </template>counts towards your storage<template v-if="data.volume">; the app stops if /data goes over its limit</template>
              </span>
              <RouterLink :to="{ name: 'app-storage', params: { slug } }" class="inline-flex shrink-0 items-center gap-1 font-medium text-foreground hover:underline">
                Storage <ArrowRight class="size-3.5" />
              </RouterLink>
            </div>
          </CardContent>
        </Card>
      </div>
    </template>
  </div>
  <div v-else class="grid gap-4">
    <Skeleton class="h-20 w-full" />
    <Skeleton class="h-64 w-full" />
  </div>
</template>
