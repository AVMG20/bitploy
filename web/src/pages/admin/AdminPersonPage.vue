<script setup lang="ts">
// Admin > People > a person: who they are, their limits and what they use,
// their apps, their builds and what they did lately. The actions of the
// People menu (limits, password reset link, suspend) are buttons here.
import { computed, ref } from 'vue'
import { RouterLink } from 'vue-router'
import { useQuery } from '@tanstack/vue-query'
import { ArrowLeft } from 'lucide-vue-next'
import AppStatusBadge from '@/components/AppStatusBadge.vue'
import EmptyState from '@/components/EmptyState.vue'
import PageHeader from '@/components/PageHeader.vue'
import QueryError from '@/components/QueryError.vue'
import RelativeTime from '@/components/RelativeTime.vue'
import StatCard from '@/components/StatCard.vue'
import UsageCard from '@/components/UsageCard.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { keys } from '@/composables/queries'
import { useQueryParam } from '@/composables/useQueryParam'
import { useSession } from '@/composables/useSession'
import { api } from '@/lib/api'
import { appPath } from '@/lib/appLinks'
import { auditLabel } from '@/lib/audit'
import { BUILD_WINDOWS, peakPercent } from '@/lib/builds'
import { CPU_HINT, channelLabel, cpuFromPercent, formatCPU, formatCPUMillis, formatDuration, formatMB } from '@/lib/format'
import { joinedText } from '@/lib/signups'
import type { AuditEntry, BuildWindow } from '@/lib/types'
import AdminTabBar from './AdminTabBar.vue'
import AdminUserDialogs from './AdminUserDialogs.vue'
import BuildList from './BuildList.vue'

const props = defineProps<{ id: string }>()
const { user: self } = useSession()

const users = useQuery({ queryKey: keys.adminUsers, queryFn: api.adminUsers, refetchInterval: 15_000 })
const person = computed(() => users.data.value?.find((u) => u.id === props.id) ?? null)
const missing = computed(() => !!users.data.value && !person.value)
const dialogs = ref<InstanceType<typeof AdminUserDialogs> | null>(null)

// ---------------------------------------------------------------- limits and what they use
const usage = computed(() => person.value?.usage)
const quota = computed(() => person.value?.quota)
const unlimited = computed(() => !!quota.value?.overallocate)
const storageTaken = computed(() => (usage.value ? usage.value.files_mb + usage.value.backups_mb + usage.value.volume_reserved_mb : 0))
const storageUsed = computed(() => (usage.value ? usage.value.files_mb + usage.value.backups_mb + usage.value.volume_used_mb : 0))

// ---------------------------------------------------------------- their apps
const apps = useQuery({ queryKey: keys.adminApps, queryFn: api.adminApps, refetchInterval: 15_000 })
const theirApps = computed(() => (apps.data.value ?? []).filter((a) => a.owner_id === props.id).sort((a, b) => a.name.localeCompare(b.name)))
const appsById = computed(() => new Map((apps.data.value ?? []).map((a) => [a.id, a])))
const appsBySlug = computed(() => new Set((apps.data.value ?? []).map((a) => a.slug)))

// ---------------------------------------------------------------- their builds
const windowSel = useQueryParam<BuildWindow>('builds', '7d', ['24h', '7d', '30d'])
const windowLabel = computed(() => BUILD_WINDOWS.find((w) => w.value === windowSel.value)?.label ?? windowSel.value)
const stats = useQuery({
  queryKey: computed(() => keys.adminBuildStats(windowSel.value)),
  queryFn: () => api.adminBuildStats(windowSel.value),
  refetchInterval: 60_000,
})
const mine = computed(() => stats.data.value?.people?.find((p) => p.user_id === props.id) ?? null)
const builds = useQuery({
  queryKey: computed(() => keys.adminBuilds(props.id, windowSel.value)),
  queryFn: () => api.adminBuilds(props.id, windowSel.value),
  refetchInterval: 60_000,
})

// ---------------------------------------------------------------- what they did lately
const activity = useQuery({
  queryKey: computed(() => keys.adminUserAudit(props.id)),
  queryFn: () => api.adminUserAudit(props.id, 15),
  refetchInterval: 60_000,
})
/** The app an entry is about: a link while the app exists. */
function entryApp(e: AuditEntry): { text: string; slug?: string } | null {
  if (e.target_type === 'app' && e.target_id) {
    const a = appsById.value.get(e.target_id)
    if (a) return { text: a.name, slug: a.slug }
  }
  const slug = e.details?.app ?? e.details?.slug
  return typeof slug === 'string' ? { text: slug, slug: appsBySlug.value.has(slug) ? slug : undefined } : null
}
const activityLink = computed(() => ({ name: 'admin', query: { tab: 'audit', q: person.value?.name || undefined } }))
</script>

<template>
  <div class="grid min-w-0 grid-cols-1 gap-5">
    <PageHeader title="Admin" />
    <AdminTabBar tab="users" page />

    <div class="flex flex-wrap items-start justify-between gap-3 pt-1">
      <div class="flex min-w-0 items-start gap-2">
        <Button as-child variant="ghost" size="icon" class="size-8 shrink-0" aria-label="Back to People">
          <RouterLink :to="{ name: 'admin' }"><ArrowLeft /></RouterLink>
        </Button>
        <div v-if="person" class="min-w-0">
          <h2 class="flex flex-wrap items-center gap-1.5 text-lg font-semibold tracking-tight">
            <span class="break-words">{{ person.name }}</span>
            <Badge v-if="person.role === 'admin'" variant="secondary">Admin</Badge>
            <Badge v-if="person.status === 'suspended'" variant="destructive">Suspended</Badge>
          </h2>
          <p class="text-sm break-all text-muted-foreground">{{ person.email }}</p>
          <p class="text-xs text-muted-foreground">
            <template v-if="joinedText(person.joined)">{{ joinedText(person.joined) }} &middot; </template>account made <RelativeTime :date="person.created_at" />
          </p>
        </div>
        <Skeleton v-else-if="users.isPending.value" class="h-12 w-56" />
      </div>
      <div v-if="person" class="flex flex-wrap gap-2">
        <Button variant="outline" size="sm" @click="dialogs?.edit(person)">Limits</Button>
        <Button variant="outline" size="sm" @click="dialogs?.resetPassword(person)">Reset password</Button>
        <Button v-if="person.id !== self?.id" :variant="person.status === 'suspended' ? 'outline' : 'destructive'" size="sm" @click="dialogs?.toggleStatus(person)">
          {{ person.status === 'suspended' ? 'Reactivate' : 'Suspend' }}
        </Button>
      </div>
    </div>

    <QueryError v-if="users.isError.value" :error="users.error.value" @retry="users.refetch()" />
    <EmptyState v-else-if="missing" title="There is no such person." description="The account may have been removed. Their builds stay in the history for 90 days.">
      <Button as-child variant="outline"><RouterLink :to="{ name: 'admin' }">Back to People</RouterLink></Button>
    </EmptyState>

    <template v-else>
      <!-- Limits and what they use -->
      <section class="grid grid-cols-1 gap-3">
        <h3 class="text-sm font-semibold">Limits and use</h3>
        <div class="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-4">
          <UsageCard
            title="Memory"
            :loading="!person"
            :value="usage ? formatMB(usage.memory_used_mb) : ''"
            :used="usage?.memory_used_mb ?? 0"
            :allocated="usage?.memory_reserved_mb ?? 0"
            :max="unlimited ? null : (quota?.memory_mb ?? null)"
            :allocated-text="usage ? formatMB(usage.memory_reserved_mb) : ''"
            :max-text="quota ? formatMB(quota.memory_mb) : ''"
            hint="In use: what their apps use right now. Allocated: the memory limits of all their apps added up."
          />
          <UsageCard
            title="CPU"
            :loading="!person"
            :value="usage ? formatCPU(cpuFromPercent(usage.cpu_now_percent)).replace(' CPU', '') : ''"
            value-label="CPU in use"
            :used="usage ? cpuFromPercent(usage.cpu_now_percent) * 1000 : 0"
            :allocated="usage?.cpu_reserved_millis ?? 0"
            :max="unlimited ? null : (quota?.cpu_millis ?? null)"
            :allocated-text="usage ? formatCPUMillis(usage.cpu_reserved_millis) : ''"
            :max-text="quota ? formatCPUMillis(quota.cpu_millis) : ''"
            :hint="`In use: what their apps use right now. Allocated: the CPU limits of all their apps added up. ${CPU_HINT}`"
          />
          <UsageCard
            title="Storage"
            :loading="!person"
            :value="usage ? formatMB(storageUsed) : ''"
            :used="storageUsed"
            :allocated="storageTaken"
            :max="quota?.volume_mb ?? null"
            :allocated-text="usage ? formatMB(storageTaken) : ''"
            :max-text="quota ? formatMB(quota.volume_mb) : ''"
            hint="App files, backups and saved files (/data). Allocated counts each saved-files volume at its full size."
          />
          <UsageCard
            title="Apps"
            :loading="!person"
            :value="usage ? String(usage.apps_running) : ''"
            value-label="running"
            :used="usage?.apps_running ?? 0"
            :allocated="person?.app_count ?? 0"
            :max="quota?.apps ?? null"
            :allocated-text="person ? String(person.app_count) : ''"
            :allocated-label="person?.app_count === 1 ? 'app' : 'apps'"
            :max-text="quota ? String(quota.apps) : ''"
            hint="Databases and caches count as apps."
          />
        </div>
        <p v-if="person" class="text-xs text-muted-foreground">
          This month: {{ person.usage.deploys_month }} deploys, {{ Math.round(person.usage.build_minutes_month) }} build minutes,
          {{ Math.round(person.usage.running_hours_month) }} hours of apps running. Up to {{ person.quota.domains_per_app }} custom domains per app.
          <template v-if="unlimited">Memory and CPU are not checked for this person.</template>
        </p>
      </section>

      <!-- Their apps -->
      <Card>
        <CardHeader>
          <CardTitle>Apps</CardTitle>
          <CardDescription>The apps they own. Apps in projects others shared with them are listed with those owners.</CardDescription>
        </CardHeader>
        <CardContent>
          <QueryError v-if="apps.isError.value" :error="apps.error.value" @retry="apps.refetch()" />
          <Skeleton v-else-if="apps.isPending.value" class="h-24 w-full" />
          <p v-else-if="!theirApps.length" class="py-6 text-center text-sm text-muted-foreground">No apps of their own.</p>
          <div v-else class="overflow-x-auto rounded-xl border">
            <Table>
              <TableHeader>
                <TableRow class="hover:bg-transparent">
                  <TableHead>App</TableHead>
                  <TableHead class="hidden sm:table-cell">Project</TableHead>
                  <TableHead>Status</TableHead>
                  <TableHead class="text-right">Memory</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                <TableRow v-for="a in theirApps" :key="a.id">
                  <TableCell>
                    <RouterLink :to="appPath(a.slug)" class="font-medium hover:underline">{{ a.name }}</RouterLink>
                    <span class="block font-mono text-xs text-muted-foreground">{{ a.slug }}</span>
                  </TableCell>
                  <TableCell class="hidden text-sm sm:table-cell">{{ a.project?.name ?? '-' }}</TableCell>
                  <TableCell><AppStatusBadge :status="a.status" /></TableCell>
                  <TableCell class="text-right text-sm whitespace-nowrap tabular-nums">
                    <template v-if="a.live">{{ formatMB(a.live.memory_mb) }} / </template>{{ formatMB(a.memory_mb) }}
                  </TableCell>
                </TableRow>
              </TableBody>
            </Table>
          </div>
        </CardContent>
      </Card>

      <!-- Their builds -->
      <Card>
        <CardHeader class="flex flex-row flex-wrap items-start justify-between gap-3">
          <div class="grid gap-1.5">
            <CardTitle>Builds</CardTitle>
            <CardDescription>The builds they started, and the pushes to their apps. Peaks are the whole server's while their builds ran.</CardDescription>
          </div>
          <Tabs v-model="windowSel">
            <TabsList>
              <TabsTrigger v-for="w in BUILD_WINDOWS" :key="w.value" :value="w.value">{{ w.label }}</TabsTrigger>
            </TabsList>
          </Tabs>
        </CardHeader>
        <CardContent class="grid gap-4">
          <QueryError v-if="stats.isError.value && !stats.data.value" :error="stats.error.value" @retry="stats.refetch()" />
          <div v-else class="grid grid-cols-2 gap-3 lg:grid-cols-4">
            <StatCard
              title="Builds"
              :loading="stats.isPending.value"
              :value="String(mine?.builds ?? 0)"
              :sub="mine?.failed ? `${mine.failed} failed` : 'none failed'"
            />
            <StatCard
              title="Build time"
              :loading="stats.isPending.value"
              :value="mine ? formatDuration(mine.total_build_seconds) : '-'"
              :sub="mine ? `${formatDuration(mine.avg_build_seconds)} on average` : ''"
            />
            <StatCard
              title="Longest"
              :loading="stats.isPending.value"
              :value="mine ? formatDuration(mine.longest_build_seconds) : '-'"
              :sub="mine?.longest_app ?? ''"
            />
            <StatCard
              title="Peak server use"
              hint="The server's highest CPU and memory while their builds ran"
              :loading="stats.isPending.value"
              :value="mine ? `${peakPercent(mine.peak_cpu)} CPU` : '-'"
              :sub="mine && mine.peak_memory_mb !== null ? `${formatMB(mine.peak_memory_mb)} memory` : ''"
            />
          </div>
          <QueryError v-if="builds.isError.value" :error="builds.error.value" @retry="builds.refetch()" />
          <Skeleton v-else-if="builds.isPending.value" class="h-24 w-full" />
          <p v-else-if="!builds.data.value?.length" class="py-6 text-center text-sm text-muted-foreground">No builds in the last {{ windowLabel }}.</p>
          <BuildList v-else :builds="builds.data.value" />
        </CardContent>
      </Card>

      <!-- What they did lately -->
      <Card>
        <CardHeader class="flex flex-row flex-wrap items-start justify-between gap-3">
          <div class="grid gap-1.5">
            <CardTitle>Recent activity</CardTitle>
            <CardDescription>What they did lately, newest first.</CardDescription>
          </div>
          <Button as-child variant="outline" size="sm">
            <RouterLink :to="activityLink">Open the activity log</RouterLink>
          </Button>
        </CardHeader>
        <CardContent>
          <QueryError v-if="activity.isError.value" :error="activity.error.value" @retry="activity.refetch()" />
          <Skeleton v-else-if="activity.isPending.value" class="h-24 w-full" />
          <p v-else-if="!activity.data.value?.length" class="py-6 text-center text-sm text-muted-foreground">Nothing in the last 90 days.</p>
          <ul v-else class="divide-y">
            <li v-for="e in activity.data.value" :key="e.id" class="flex flex-wrap items-baseline justify-between gap-x-3 gap-y-0.5 py-2 text-sm">
              <span class="min-w-0">
                <span :title="e.action">{{ auditLabel(e.action) }}</span>
                <template v-if="entryApp(e)">
                  <span class="text-muted-foreground">: </span>
                  <RouterLink v-if="entryApp(e)?.slug" :to="appPath(entryApp(e)!.slug!)" class="font-medium hover:underline">{{ entryApp(e)!.text }}</RouterLink>
                  <span v-else class="font-medium">{{ entryApp(e)!.text }}</span>
                </template>
              </span>
              <span class="text-xs whitespace-nowrap text-muted-foreground">{{ channelLabel(e.channel) }} &middot; <RelativeTime :date="e.ts" /></span>
            </li>
          </ul>
        </CardContent>
      </Card>
    </template>

    <AdminUserDialogs ref="dialogs" />
  </div>
</template>
