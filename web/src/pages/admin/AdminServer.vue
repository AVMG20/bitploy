<script setup lang="ts">
import { computed } from 'vue'
import { useQuery } from '@tanstack/vue-query'
import MetricChart from '@/components/MetricChart.vue'
import QueryError from '@/components/QueryError.vue'
import StatCard from '@/components/StatCard.vue'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import AdminBuildSlots from './AdminBuildSlots.vue'
import AdminPublicPorts from './AdminPublicPorts.vue'
import { keys, REFRESH_MS, useServerMetrics } from '@/composables/queries'
import { useQueryParam } from '@/composables/useQueryParam'
import { api } from '@/lib/api'
import { formatGB, formatMB, formatPercent, pct } from '@/lib/format'
import type { MetricWindow } from '@/lib/types'

const server = useQuery({ queryKey: keys.adminServer, queryFn: api.adminServer, refetchInterval: REFRESH_MS })
const s = computed(() => server.data.value)

const windowSel = useQueryParam<MetricWindow>('window', '1h', ['1h', '24h', '7d'])
const windows: { value: MetricWindow; label: string }[] = [
  { value: '1h', label: '1 hour' },
  { value: '24h', label: '24 hours' },
  { value: '7d', label: '7 days' },
]
const history = useServerMetrics(windowSel)
const points = computed(() => history.data.value?.points ?? [])
const memTotalMB = computed(() => history.data.value?.mem_total_mb || s.value?.mem_total_mb || 0)

// Free disk in %, and whether it is low enough to warn about (below 20%).
const DISK_WARN_PERCENT = 20
const diskFreeGB = computed(() => (s.value ? Math.max(0, s.value.disk_total_gb - s.value.disk_used_gb) : 0))
const diskFreePercent = computed(() => (s.value && s.value.disk_total_gb > 0 ? (diskFreeGB.value / s.value.disk_total_gb) * 100 : 100))
const diskLow = computed(() => diskFreePercent.value < DISK_WARN_PERCENT)
const diskFull = computed(() => !!s.value && diskFreePercent.value < s.value.disk_min_free_percent)
</script>

<template>
  <div>
    <QueryError v-if="server.isError.value" :error="server.error.value" @retry="server.refetch()" />
    <Alert v-if="s && diskLow" variant="destructive" class="mb-4" data-testid="disk-low">
      <AlertTitle>{{ diskFull ? 'The server disk is full' : 'The server disk is almost full' }}</AlertTitle>
      <AlertDescription>
        <p>
          Only {{ formatGB(diskFreeGB) }} ({{ Math.floor(diskFreePercent) }}%) is free.
          <template v-if="diskFull">
            Below {{ s.disk_min_free_percent }}% free, bitploy refuses new builds, uploads and backups, and skips automatic backups. Apps that run keep running.
          </template>
          <template v-else>
            Below {{ s.disk_min_free_percent }}% free, bitploy stops accepting new builds, uploads and backups, and skips automatic backups.
          </template>
        </p>
        <p>
          Make room: <RouterLink :to="{ name: 'admin-disk' }" class="font-medium underline underline-offset-4">clean up Docker on the disk page</RouterLink>, delete apps or old
          backups nobody needs, lower storage limits, or give the server a bigger disk.
        </p>
      </AlertDescription>
    </Alert>
    <div v-if="!server.isError.value" class="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
      <StatCard title="CPU" :loading="!s" :value="s ? formatPercent(s.cpu_percent) : ''" suffix="of the whole server" :sub="s ? `Load ${s.load1.toFixed(2)}` : ''" :progress="s?.cpu_percent" />
      <StatCard
        title="Memory"
        :loading="!s"
        :value="s ? `${formatMB(s.mem_used_mb)} of ${formatMB(s.mem_total_mb)}` : ''"
        :sub="s ? `${formatMB(s.mem_reserved_mb)} reserved for apps` : ''"
        :progress="s ? pct(s.mem_used_mb, s.mem_total_mb) : undefined"
      />
      <StatCard
        title="Disk"
        :to="{ name: 'admin-disk' }"
        :loading="!s"
        :value="s ? `${formatGB(s.disk_used_gb)} of ${formatGB(s.disk_total_gb)}` : ''"
        :sub="s ? `${formatGB(Math.max(0, s.disk_total_gb - s.disk_used_gb))} free` : ''"
        :progress="s ? pct(s.disk_used_gb, s.disk_total_gb) : undefined"
      />
      <StatCard
        title="Docker disk use"
        :to="{ name: 'admin-disk' }"
        :loading="!s"
        :value="s ? formatGB(s.docker_images_gb + s.docker_build_cache_gb + s.docker_volumes_gb) : ''"
        :sub="s ? `Images ${formatGB(s.docker_images_gb)}, build cache ${formatGB(s.docker_build_cache_gb)}, saved files (/data) ${formatGB(s.docker_volumes_gb)}` : ''"
      />
      <StatCard title="Builds" :loading="!s" :value="s ? `${s.builds_running} running` : ''" :sub="s ? `${s.builds_queued} waiting in line` : ''" />
      <StatCard
        title="Apps"
        :loading="!s"
        :value="s ? `${s.apps_running} of ${s.apps_total} running` : ''"
        :sub="s ? `${s.apps_total} of ${s.apps_max} apps allowed on this server` : ''"
        :progress="s ? pct(s.apps_total, s.apps_max) : undefined"
      />
    </div>

    <Card class="mt-6">
      <CardHeader class="flex flex-row flex-wrap items-center justify-between gap-2">
        <CardTitle>Usage over time</CardTitle>
        <Tabs v-model="windowSel">
          <TabsList>
            <TabsTrigger v-for="w in windows" :key="w.value" :value="w.value">{{ w.label }}</TabsTrigger>
          </TabsList>
        </Tabs>
      </CardHeader>
      <CardContent>
        <QueryError v-if="history.isError.value && !history.data.value" :error="history.error.value" @retry="history.refetch()" />
        <Skeleton v-else-if="history.isPending.value" class="h-48 w-full" />
        <p v-else-if="points.length < 2" class="py-12 text-center text-sm text-muted-foreground">
          No data yet. The charts fill in about a minute after bitploy starts.
        </p>
        <div v-else class="grid gap-6 md:grid-cols-2">
          <div class="min-w-0">
            <p class="mb-2 flex items-baseline justify-between gap-2 text-sm">
              <span class="font-medium">CPU</span>
              <span class="text-xs text-muted-foreground">% of the whole server</span>
            </p>
            <MetricChart :points="points" field="cpu" label="CPU" unit="%" :window="windowSel" :limit="100" color="var(--chart-1)" />
          </div>
          <div class="min-w-0">
            <p class="mb-2 flex items-baseline justify-between gap-2 text-sm">
              <span class="font-medium">Memory</span>
              <span class="text-xs text-muted-foreground">GB in use, of {{ formatMB(memTotalMB) }}</span>
            </p>
            <MetricChart :points="points" field="memory_mb" label="Memory" unit="GB" :scale="1 / 1024" :window="windowSel" :limit="memTotalMB / 1024" color="var(--chart-2)" />
          </div>
        </div>
      </CardContent>
    </Card>

    <AdminBuildSlots class="mt-6" />

    <AdminPublicPorts class="mt-6" />

    <p v-if="s" class="mt-6 text-xs text-muted-foreground" data-testid="bitploy-version">
      bitploy version <span class="font-mono">{{ s.version }}</span>
    </p>
  </div>
</template>
