<script setup lang="ts">
import { computed } from 'vue'
import { useQuery } from '@tanstack/vue-query'
import QueryError from '@/components/QueryError.vue'
import StatCard from '@/components/StatCard.vue'
import { keys, REFRESH_MS } from '@/composables/queries'
import { api } from '@/lib/api'
import { formatGB, formatMB, formatPercent, pct } from '@/lib/format'

const server = useQuery({ queryKey: keys.adminServer, queryFn: api.adminServer, refetchInterval: REFRESH_MS })
const s = computed(() => server.data.value)
</script>

<template>
  <div>
    <QueryError v-if="server.isError.value" :error="server.error.value" @retry="server.refetch()" />
    <div v-else class="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
      <StatCard title="Processor" :loading="!s" :value="s ? formatPercent(s.cpu_percent) : ''" :sub="s ? `Load ${s.load1.toFixed(2)}` : ''" :progress="s?.cpu_percent" />
      <StatCard
        title="Memory"
        :loading="!s"
        :value="s ? `${formatMB(s.mem_used_mb)} of ${formatMB(s.mem_total_mb)}` : ''"
        :sub="s ? `${formatMB(s.mem_reserved_mb)} promised to apps` : ''"
        :progress="s ? pct(s.mem_used_mb, s.mem_total_mb) : undefined"
      />
      <StatCard
        title="Disk"
        :loading="!s"
        :value="s ? `${formatGB(s.disk_used_gb)} of ${formatGB(s.disk_total_gb)}` : ''"
        :progress="s ? pct(s.disk_used_gb, s.disk_total_gb) : undefined"
      />
      <StatCard
        title="Docker disk use"
        :loading="!s"
        :value="s ? formatGB(s.docker_images_gb + s.docker_build_cache_gb + s.docker_volumes_gb) : ''"
        :sub="s ? `Images ${formatGB(s.docker_images_gb)}, build cache ${formatGB(s.docker_build_cache_gb)}, volumes ${formatGB(s.docker_volumes_gb)}` : ''"
      />
      <StatCard title="Builds" :loading="!s" :value="s ? `${s.builds_running} running` : ''" :sub="s ? `${s.builds_queued} waiting in line` : ''" />
      <StatCard title="Apps" :loading="!s" :value="s ? `${s.apps_running} of ${s.apps_total} running` : ''" :progress="s ? pct(s.apps_running, s.apps_total) : undefined" />
    </div>
  </div>
</template>
