<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { RouterLink, useRoute, useRouter } from 'vue-router'
import { ArrowRight, ExternalLink, HardDrive, Lock, RotateCw } from 'lucide-vue-next'
import AppStatusBadge from '@/components/AppStatusBadge.vue'
import MetricChart from '@/components/MetricChart.vue'
import QueryError from '@/components/QueryError.vue'
import ServiceIcon from '@/components/ServiceIcon.vue'
import { Button } from '@/components/ui/button'
import { Progress } from '@/components/ui/progress'
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from '@/components/ui/sheet'
import { Skeleton } from '@/components/ui/skeleton'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { useAppMetrics } from '@/composables/queries'
import { useAppActions } from '@/composables/useAppActions'
import { CPU_HINT, cpuFromPercent, formatCPU, formatCPUMillis, formatMB, hostOf, pct } from '@/lib/format'
import { listWords, serviceRole, serviceSource, serviceStatus, usedBy } from '@/lib/services'
import type { App, MetricWindow } from '@/lib/types'
import LogsTab from './LogsTab.vue'
import VariablesTab from './VariablesTab.vue'

// The panel of one service of a project with several, opened by ?service=<name>
// on the project page (a service card links there). It holds what the project
// tabs hold, for this one service only.
const props = defineProps<{ app: App }>()
const route = useRoute()
const router = useRouter()

const name = computed(() => {
  const raw = route.query.service
  return (Array.isArray(raw) ? raw[0] : raw) ?? ''
})
const service = computed(() => props.app.services.find((s) => s.name === name.value))
const open = computed({
  get: () => !!service.value,
  set: (v) => {
    if (!v) void router.replace({ query: { ...route.query, service: undefined } })
  },
})

const tab = ref('usage')
watch(name, () => (tab.value = 'usage'))

const role = computed(() => (service.value ? serviceRole(service.value) : null))
const status = computed(() => (service.value ? serviceStatus(props.app, service.value) : props.app.status))
const running = computed(() => status.value === 'running')
const volumes = computed(() => props.app.volumes.filter((v) => service.value?.volumes?.includes(v.name)))
const talksTo = computed(() => service.value?.depends_on ?? [])
const users = computed(() => (service.value ? usedBy(props.app, service.value.name) : []))

const windowSel = ref<MetricWindow>('1h')
const windows: { value: MetricWindow; label: string }[] = [
  { value: '1h', label: '1 hour' },
  { value: '24h', label: '24 hours' },
  { value: '7d', label: '7 days' },
]
const metrics = useAppMetrics(() => props.app.slug, windowSel, () => open.value && tab.value === 'usage', name)
const points = computed(() => metrics.data.value?.points ?? [])
const live = computed(() => (running.value ? (metrics.data.value?.latest ?? service.value?.live ?? points.value[points.value.length - 1] ?? null) : null))
const cpuLimit = computed(() => (service.value?.cpu_millis ?? 0) / 1000)
const memoryLimit = computed(() => metrics.data.value?.memory_limit_mb ?? service.value?.memory_mb ?? 0)

const actions = useAppActions(() => props.app.slug)
const canRestart = computed(() => !!props.app.current_deployment && !actions.restartService.isPending.value)
</script>

<template>
  <Sheet v-model:open="open">
    <SheetContent class="w-full gap-0 p-0 data-[side=right]:sm:max-w-2xl" @open-auto-focus.prevent>
      <template v-if="service && role">
        <SheetHeader class="gap-3 border-b p-5 pr-12">
          <div class="flex min-w-0 items-start gap-3">
            <ServiceIcon :role="role.key" />
            <div class="min-w-0 flex-1">
              <SheetTitle class="flex min-w-0 flex-wrap items-center gap-x-2.5 gap-y-1 text-base">
                <span class="font-mono">{{ service.name }}</span> <span class="text-sm font-normal text-muted-foreground">{{ role.label }}</span>
                <AppStatusBadge :status="actions.restartService.isPending.value ? 'restarting' : status" />
              </SheetTitle>
              <SheetDescription class="mt-1">{{ role.blurb }}</SheetDescription>
            </div>
          </div>
          <div class="flex flex-wrap items-center gap-2">
            <Button size="sm" variant="outline" :disabled="!canRestart" @click="actions.restartService.mutate(service.name)">
              <RotateCw :class="actions.restartService.isPending.value && 'animate-spin'" />
              {{ actions.restartService.isPending.value ? 'Restarting...' : 'Restart this service' }}
            </Button>
            <Button v-if="service.public && app.type === 'web' && app.current_deployment" size="sm" variant="outline" as-child>
              <a :href="app.url" target="_blank" rel="noopener noreferrer">Open {{ hostOf(app.url) }} <ExternalLink /></a>
            </Button>
            <span v-else-if="service.port" class="inline-flex items-center gap-1.5 text-xs text-muted-foreground">
              <Lock class="size-3.5" /> Other services reach it at <span class="font-mono text-foreground/80">{{ service.name }}:{{ service.port }}</span>
            </span>
          </div>
        </SheetHeader>

        <Tabs v-model="tab" class="min-h-0 flex-1 gap-0">
          <div class="border-b px-5">
            <TabsList variant="line" class="-ml-1.5">
              <TabsTrigger value="usage">Usage</TabsTrigger>
              <TabsTrigger value="logs">Logs</TabsTrigger>
              <TabsTrigger value="variables">Variables</TabsTrigger>
              <TabsTrigger value="storage">Storage<span v-if="volumes.length" class="ml-1 text-muted-foreground tabular-nums">{{ volumes.length }}</span></TabsTrigger>
              <TabsTrigger value="settings">Settings</TabsTrigger>
            </TabsList>
          </div>

          <div class="min-h-0 flex-1 overflow-y-auto p-5">
            <TabsContent value="usage" class="grid gap-5">
              <div class="grid grid-cols-2 gap-3">
                <div class="rounded-lg border p-3">
                  <p class="text-xs text-muted-foreground">Memory now</p>
                  <p class="mt-0.5 text-lg font-semibold tabular-nums">
                    {{ live ? formatMB(live.memory_mb) : '-' }} <span class="text-xs font-normal text-muted-foreground">of {{ formatMB(service.memory_mb) }}</span>
                  </p>
                  <Progress class="mt-2 [&>[data-slot=progress-indicator]]:bg-success" :model-value="live ? pct(live.memory_mb, service.memory_mb) : 0" />
                </div>
                <div class="rounded-lg border p-3">
                  <p class="text-xs text-muted-foreground">CPU now</p>
                  <p class="mt-0.5 text-lg font-semibold tabular-nums">
                    {{ live ? formatCPU(cpuFromPercent(live.cpu)).replace(' CPU', '') : '-' }} <span class="text-xs font-normal text-muted-foreground" :title="CPU_HINT">of {{ formatCPU(cpuLimit) }}</span>
                  </p>
                  <Progress class="mt-2" :model-value="live ? pct(cpuFromPercent(live.cpu), cpuLimit) : 0" />
                </div>
              </div>
              <Tabs v-model="windowSel">
                <TabsList>
                  <TabsTrigger v-for="w in windows" :key="w.value" :value="w.value">{{ w.label }}</TabsTrigger>
                </TabsList>
              </Tabs>
              <QueryError v-if="metrics.isError.value && !metrics.data.value" :error="metrics.error.value" @retry="metrics.refetch()" />
              <Skeleton v-else-if="metrics.isPending.value && running" class="h-40 w-full" />
              <p v-else-if="points.length < 2" class="py-10 text-center text-sm text-muted-foreground">
                No data yet. Charts fill in about a minute after the service starts running.
              </p>
              <template v-else>
                <div class="min-w-0">
                  <p class="mb-2 flex items-baseline justify-between gap-2 text-sm">
                    <span class="font-medium">CPU</span>
                    <span class="text-xs text-muted-foreground" :title="CPU_HINT">limit {{ formatCPU(cpuLimit) }}</span>
                  </p>
                  <MetricChart :points="points" field="cpu" label="CPU" unit="CPU" :scale="0.01" :window="windowSel" :limit="cpuLimit" color="var(--chart-1)" />
                </div>
                <div class="min-w-0">
                  <p class="mb-2 flex items-baseline justify-between gap-2 text-sm">
                    <span class="font-medium">Memory</span>
                    <span class="text-xs text-muted-foreground">MB, limit {{ formatMB(memoryLimit) }}</span>
                  </p>
                  <MetricChart :points="points" field="memory_mb" label="Memory" unit="MB" :window="windowSel" :limit="memoryLimit" color="var(--chart-2)" />
                </div>
              </template>
            </TabsContent>

            <TabsContent value="logs">
              <LogsTab :slug="app.slug" :service="service.name" embedded />
            </TabsContent>

            <TabsContent value="variables">
              <VariablesTab :slug="app.slug" :service="service.name" />
            </TabsContent>

            <TabsContent value="storage" class="grid gap-3">
              <p v-if="!volumes.length" class="rounded-lg border border-dashed p-6 text-center text-sm text-muted-foreground">
                {{ service.name }} has no saved files: what it writes is lost on every restart and deploy.
                To keep files, give it a volume in your compose file.
              </p>
              <div v-for="v in volumes" :key="v.name" class="grid gap-2 rounded-lg border p-4">
                <div class="flex flex-wrap items-baseline justify-between gap-x-4 gap-y-1">
                  <span class="inline-flex items-center gap-2 text-sm font-medium"><HardDrive class="size-4 text-muted-foreground" /> <span class="font-mono">{{ v.name }}</span></span>
                  <span class="text-sm tabular-nums">
                    <template v-if="v.used_mb !== null">{{ formatMB(v.used_mb) }} <span class="text-muted-foreground">of {{ formatMB(v.size_mb) }}</span></template>
                    <template v-else><span class="text-muted-foreground">Limit</span> {{ formatMB(v.size_mb) }}</template>
                  </span>
                </div>
                <Progress class="[&>[data-slot=progress-indicator]]:bg-success" :model-value="pct(v.used_mb ?? 0, v.size_mb)" />
                <div class="flex flex-wrap items-center justify-between gap-2 text-xs text-muted-foreground">
                  <span>{{ service.name }} sees it at <span class="font-mono text-foreground/80">{{ v.mount_path }}</span>. Kept between deploys.</span>
                  <RouterLink :to="{ name: 'app-storage', params: { slug: app.slug }, query: { volume: v.name } }" class="inline-flex items-center gap-1 font-medium text-foreground hover:underline">
                    Files and backups <ArrowRight class="size-3.5" />
                  </RouterLink>
                </div>
              </div>
            </TabsContent>

            <TabsContent value="settings" class="grid gap-4">
              <dl class="grid grid-cols-[9rem_1fr] gap-x-4 gap-y-2.5 text-sm">
                <dt class="text-muted-foreground">Runs</dt>
                <dd class="min-w-0 break-all" :class="service.kind === 'image' && 'font-mono text-[13px]'">{{ serviceSource(service) }}</dd>
                <dt class="text-muted-foreground">Address</dt>
                <dd class="min-w-0">
                  <template v-if="service.public && app.type === 'web'">Public, at <span class="font-mono text-[13px]">{{ hostOf(app.url) }}</span><template v-if="service.port"> (port {{ service.port }})</template></template>
                  <template v-else-if="service.port">Private, at <span class="font-mono text-[13px]">{{ service.name }}:{{ service.port }}</span></template>
                  <template v-else>None, it does not listen for requests</template>
                </dd>
                <template v-if="service.health_path && service.port">
                  <dt class="text-muted-foreground">Health check</dt>
                  <dd class="font-mono text-[13px]">{{ service.health_path }}</dd>
                </template>
                <dt class="text-muted-foreground">Size</dt>
                <dd>{{ formatMB(service.memory_mb) }} memory, {{ formatCPUMillis(service.cpu_millis) }}</dd>
                <dt class="text-muted-foreground">Starts after</dt>
                <dd>
                  <span v-if="talksTo.length" class="font-mono text-[13px]">{{ listWords(talksTo) }}</span>
                  <span v-else class="text-muted-foreground">Nothing, it starts first</span>
                </dd>
                <dt class="text-muted-foreground">Used by</dt>
                <dd>
                  <span v-if="users.length" class="font-mono text-[13px]">{{ listWords(users) }}</span>
                  <span v-else class="text-muted-foreground">No other service</span>
                </dd>
                <dt class="text-muted-foreground">Saved files</dt>
                <dd>
                  <span v-if="volumes.length" class="font-mono text-[13px]">{{ volumes.map((v) => `${v.name} at ${v.mount_path}`).join(', ') }}</span>
                  <span v-else class="text-muted-foreground">None</span>
                </dd>
              </dl>
              <p class="rounded-lg border bg-muted/30 p-3 text-xs text-muted-foreground">
                These come from your compose file<template v-if="app.compose_path"> (<span class="font-mono">{{ app.compose_path }}</span>)</template>. To change them, edit it there and deploy again.
              </p>
            </TabsContent>
          </div>
        </Tabs>
      </template>
    </SheetContent>
  </Sheet>
</template>
