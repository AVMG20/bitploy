<script setup lang="ts">
import { computed, ref } from 'vue'
import { RouterLink } from 'vue-router'
import { Play, RefreshCw, RotateCw, Square } from 'lucide-vue-next'
import AppStatusBadge from '@/components/AppStatusBadge.vue'
import ConfirmDialog from '@/components/ConfirmDialog.vue'
import CopyButton from '@/components/CopyButton.vue'
import MetricChart from '@/components/MetricChart.vue'
import QueryError from '@/components/QueryError.vue'
import RelativeTime from '@/components/RelativeTime.vue'
import StatusBadge from '@/components/StatusBadge.vue'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Progress } from '@/components/ui/progress'
import { Skeleton } from '@/components/ui/skeleton'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { useApp, useAppMetrics } from '@/composables/queries'
import { useAppActions } from '@/composables/useAppActions'
import { nowMs } from '@/composables/useNow'
import { channelLabel, deploymentStateInfo, describeSource, formatCPUMillis, formatDuration, formatMB, pct, shortSha, uptime } from '@/lib/format'
import type { MetricWindow } from '@/lib/types'

const props = defineProps<{ slug: string }>()
const app = useApp(() => props.slug)
const actions = useAppActions(() => props.slug)

const windowSel = ref<MetricWindow>('1h')
const windows: { value: MetricWindow; label: string }[] = [
  { value: '1h', label: '1 hour' },
  { value: '24h', label: '24 hours' },
  { value: '7d', label: '7 days' },
]
const metrics = useAppMetrics(() => props.slug, windowSel)
const points = computed(() => metrics.data.value?.points ?? [])

const confirmStop = ref(false)
const data = computed(() => app.data.value)
const canStart = computed(() => data.value && ['stopped', 'crashed', 'failed'].includes(data.value.status) && data.value.current_deployment)
const canStop = computed(() => data.value && ['running', 'deploying'].includes(data.value.status))
const dep = computed(() => data.value?.current_deployment ?? null)
const busy = computed(() => actions.redeploy.isPending.value || actions.restart.isPending.value || actions.start.isPending.value || actions.stop.isPending.value)

function stop() {
  actions.stop.mutate(undefined, { onSettled: () => (confirmStop.value = false) })
}
</script>

<template>
  <div v-if="data" class="grid gap-4">
    <Alert v-if="data.status === 'failed' || data.status === 'crashed'" variant="destructive">
      <AlertTitle>{{ data.status === 'crashed' ? 'This app keeps crashing.' : 'The last deploy failed.' }}</AlertTitle>
      <AlertDescription>
        <p v-if="data.status_message">{{ data.status_message }}</p>
        <p v-if="dep?.error">{{ dep.error.message }} <span v-if="dep.error.hint" class="font-medium">{{ dep.error.hint }}</span></p>
        <RouterLink :to="{ name: 'app-logs', params: { slug } }" class="underline">Open the logs</RouterLink>
      </AlertDescription>
    </Alert>
    <Alert v-else-if="data.status_message">
      <AlertDescription>{{ data.status_message }}</AlertDescription>
    </Alert>

    <div class="grid gap-4 lg:grid-cols-3">
      <Card class="min-w-0 lg:col-span-2">
        <CardHeader>
          <CardTitle>Status</CardTitle>
          <CardDescription v-if="data.type === 'web'">Your app lives at this address.</CardDescription>
          <CardDescription v-else>This is a background worker. It has no web address.</CardDescription>
        </CardHeader>
        <CardContent class="grid min-w-0 gap-4">
          <div v-if="data.type === 'web'" class="flex min-w-0 items-center gap-1 rounded-lg border bg-muted/40 py-1 pr-1 pl-3">
            <a :href="data.url" target="_blank" rel="noopener noreferrer" class="min-w-0 flex-1 truncate font-mono text-sm hover:underline">{{ data.url }}</a>
            <CopyButton :value="data.url" what="Link copied" label="Copy link" />
          </div>
          <dl class="grid grid-cols-2 gap-x-4 gap-y-3 text-sm sm:grid-cols-4">
            <div>
              <dt class="text-muted-foreground">Status</dt>
              <dd class="mt-1"><AppStatusBadge :status="actions.restart.isPending.value ? 'restarting' : actions.start.isPending.value ? 'starting' : data.status" /></dd>
            </div>
            <div>
              <dt class="text-muted-foreground">Running for</dt>
              <dd class="mt-1 font-medium">{{ data.status === 'running' ? uptime(data.started_at, nowMs) : '-' }}</dd>
            </div>
            <div>
              <dt class="text-muted-foreground">Automatic restarts</dt>
              <dd class="mt-1 font-medium">{{ data.restart_count }}</dd>
            </div>
            <div>
              <dt class="text-muted-foreground">Size</dt>
              <dd class="mt-1 font-medium">{{ formatMB(data.memory_mb) }}, {{ formatCPUMillis(data.cpu_millis) }}</dd>
            </div>
          </dl>
          <div class="grid gap-2 text-sm">
            <div class="flex flex-wrap items-baseline justify-between gap-x-4 gap-y-1">
              <span class="text-muted-foreground">App files (code, image and saved versions)</span>
              <span class="font-medium tabular-nums">{{ data.files_mb !== null ? formatMB(data.files_mb) : 'Not measured yet' }}</span>
            </div>
            <template v-if="data.volume">
              <div class="flex flex-wrap items-baseline justify-between gap-x-4 gap-y-1">
                <span class="text-muted-foreground">Saved data (/data)</span>
                <span v-if="data.volume_used_mb !== null" class="font-medium tabular-nums">
                  {{ formatMB(data.volume_used_mb) }} of {{ formatMB(data.volume_mb) }}
                </span>
                <span v-else class="font-medium">Limit {{ formatMB(data.volume_mb) }}</span>
              </div>
              <Progress :model-value="pct(data.volume_used_mb ?? 0, data.volume_mb)" />
            </template>
            <p class="text-xs text-muted-foreground">
              <template v-if="data.disk_measured_at">Measured <RelativeTime :date="data.disk_measured_at" />. </template>
              Both count towards your storage. Bitploy measures every hour and after each deploy<template v-if="data.volume">, and stops the app when /data goes over its limit</template>.
            </p>
          </div>
          <div class="flex flex-wrap gap-2">
            <Button :disabled="busy || !dep" @click="actions.redeploy.mutate()">
              <RefreshCw :class="actions.redeploy.isPending.value && 'animate-spin'" /> Redeploy
            </Button>
            <Button variant="outline" :disabled="busy || !dep" @click="actions.restart.mutate()">
              <RotateCw :class="actions.restart.isPending.value && 'animate-spin'" /> {{ actions.restart.isPending.value ? 'Restarting...' : 'Restart' }}
            </Button>
            <Button v-if="canStart" variant="outline" :disabled="busy" @click="actions.start.mutate()">
              <Play /> {{ actions.start.isPending.value ? 'Starting...' : 'Start' }}
            </Button>
            <Button v-if="canStop" variant="outline" :disabled="busy" @click="confirmStop = true">
              <Square /> Stop
            </Button>
          </div>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Current version</CardTitle>
          <CardDescription v-if="dep">Deployment #{{ dep.seq }}</CardDescription>
        </CardHeader>
        <CardContent class="grid gap-2 text-sm">
          <template v-if="dep">
            <div><StatusBadge v-bind="deploymentStateInfo(dep.state)" /></div>
            <p><span class="text-muted-foreground">Source:</span> {{ describeSource(dep, data) }}</p>
            <p v-if="dep.commit_sha">
              <span class="text-muted-foreground">Commit:</span>
              <span class="font-mono">{{ shortSha(dep.commit_sha) }}</span>
              <span v-if="dep.commit_message" class="block truncate text-muted-foreground">{{ dep.commit_message }}</span>
            </p>
            <p><span class="text-muted-foreground">Started by:</span> {{ channelLabel(dep.channel) }}</p>
            <p><span class="text-muted-foreground">When:</span> <RelativeTime :date="dep.finished_at ?? dep.queued_at" /></p>
            <p v-if="dep.duration_seconds !== null"><span class="text-muted-foreground">Took:</span> {{ formatDuration(dep.duration_seconds) }}</p>
          </template>
          <p v-else class="text-muted-foreground">Nothing deployed yet. Ask your AI assistant to deploy to this app.</p>
        </CardContent>
      </Card>
    </div>

    <Card>
      <CardHeader class="flex flex-row flex-wrap items-center justify-between gap-2">
        <div>
          <CardTitle>Usage</CardTitle>
          <CardDescription>How much of the computer this app is using.</CardDescription>
        </div>
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
          <div>
            <p class="mb-2 text-sm font-medium">Processor (% of one core)</p>
            <MetricChart :points="points" field="cpu" label="Processor" unit="%" :window="windowSel" :limit="data.cpu_millis / 10" color="var(--chart-1)" />
          </div>
          <div>
            <p class="mb-2 text-sm font-medium">Memory (MB, limit {{ formatMB(metrics.data.value?.memory_limit_mb ?? data.memory_mb) }})</p>
            <MetricChart :points="points" field="memory_mb" label="Memory" unit="MB" :window="windowSel" :limit="metrics.data.value?.memory_limit_mb ?? data.memory_mb" color="var(--chart-2)" />
          </div>
        </div>
      </CardContent>
    </Card>

    <ConfirmDialog
      v-model:open="confirmStop"
      title="Stop this app?"
      description="People who open its address will see a page saying it is stopped. You can start it again at any time."
      confirm-label="Stop app"
      :loading="actions.stop.isPending.value"
      @confirm="stop"
    />
  </div>
  <div v-else class="grid gap-4">
    <Skeleton class="h-48 w-full" />
    <Skeleton class="h-64 w-full" />
  </div>
</template>
