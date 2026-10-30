<script setup lang="ts">
import { computed } from 'vue'
import { Rocket } from 'lucide-vue-next'
import { RouterLink } from 'vue-router'
import AppRow from '@/components/AppRow.vue'
import EmptyState from '@/components/EmptyState.vue'
import PageHeader from '@/components/PageHeader.vue'
import QueryError from '@/components/QueryError.vue'
import StatCard from '@/components/StatCard.vue'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { Table, TableBody, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { useApps, useMe } from '@/composables/queries'
import { formatCPUMillis, formatMB, formatPercent, pct } from '@/lib/format'

const me = useMe()
const apps = useApps()

const usage = computed(() => me.data.value?.usage)
const quota = computed(() => me.data.value?.quota)
</script>

<template>
  <div class="grid gap-6">
    <PageHeader title="Your apps" description="Everything you have put online, and how it is doing." />

    <div class="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
      <StatCard
        title="Apps running"
        :loading="me.isPending.value"
        :value="usage ? `${usage.apps_running} of ${usage.apps}` : ''"
        :sub="quota ? `You can have up to ${quota.apps} apps` : ''"
        :progress="usage && quota ? pct(usage.apps, quota.apps) : undefined"
      />
      <StatCard
        title="Memory in use"
        :loading="me.isPending.value"
        :value="usage ? formatMB(usage.memory_used_mb) : ''"
        :sub="usage && quota ? `${formatMB(usage.memory_reserved_mb)} reserved of ${formatMB(quota.memory_mb)}` : ''"
        :progress="usage && quota ? pct(usage.memory_reserved_mb, quota.memory_mb) : undefined"
      />
      <StatCard
        title="CPU right now"
        :loading="me.isPending.value"
        :value="usage ? formatPercent(usage.cpu_now_percent) : ''"
        :sub="usage && quota ? `${formatCPUMillis(usage.cpu_reserved_millis)} reserved of ${formatCPUMillis(quota.cpu_millis)}` : ''"
        :progress="usage && quota ? pct(usage.cpu_reserved_millis, quota.cpu_millis) : undefined"
      />
      <StatCard
        title="Deploys this week"
        :loading="me.isPending.value"
        :value="usage ? String(usage.deploys_week) : ''"
        :sub="usage ? `${usage.deploys_month} this month` : ''"
      />
    </div>

    <QueryError v-if="apps.isError.value" :error="apps.error.value" @retry="apps.refetch()" />

    <div v-else-if="apps.isPending.value" class="grid gap-2">
      <Skeleton v-for="i in 3" :key="i" class="h-14 w-full" />
    </div>

    <EmptyState
      v-else-if="!apps.data.value?.length"
      :icon="Rocket"
      title="No apps yet."
      description="Open Connect AI, then tell your assistant: deploy this folder."
    >
      <Button as-child><RouterLink to="/connect">Open Connect AI</RouterLink></Button>
    </EmptyState>

    <div v-else class="overflow-hidden rounded-xl border bg-card shadow-card">
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>App</TableHead>
            <TableHead>Status</TableHead>
            <TableHead class="hidden text-right sm:table-cell">CPU</TableHead>
            <TableHead class="hidden text-right sm:table-cell">Memory</TableHead>
            <TableHead class="hidden text-right md:table-cell">Running for</TableHead>
            <TableHead class="hidden text-right md:table-cell">Last deploy</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          <AppRow v-for="a in apps.data.value" :key="a.id" :app="a" />
        </TableBody>
      </Table>
    </div>
  </div>
</template>
