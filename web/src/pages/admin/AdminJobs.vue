<script setup lang="ts">
// The Jobs tab: what bitploy runs by itself and what each job did last time
// (GET /admin/jobs). Runs are kept in memory, so they count from the moment
// bitploy started.
import { computed } from 'vue'
import { useQuery } from '@tanstack/vue-query'
import QueryError from '@/components/QueryError.vue'
import RelativeTime from '@/components/RelativeTime.vue'
import { Badge } from '@/components/ui/badge'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'
import { keys } from '@/composables/queries'
import { nowMs } from '@/composables/useNow'
import { api } from '@/lib/api'
import { formatCount, formatDuration } from '@/lib/format'
import type { Job, JobGroup } from '@/lib/types'

const jobs = useQuery({ queryKey: keys.adminJobs, queryFn: api.adminJobs, refetchInterval: 15_000 })
const data = computed(() => jobs.data.value)

const GROUPS: { key: JobGroup; title: string; description?: string }[] = [
  { key: 'disk', title: 'Disk' },
  { key: 'backups', title: 'Backups' },
  { key: 'monitoring', title: 'Monitoring' },
  { key: 'upkeep', title: 'Upkeep' },
  { key: 'nightly', title: 'Nightly', description: 'Run in this order, every night at 03:30 UTC.' },
]
const groups = computed(() =>
  GROUPS.map((g) => ({ ...g, jobs: (data.value?.jobs ?? []).filter((j) => j.group === g.key) })).filter((g) => g.jobs.length > 0),
)
const failed = computed(() => (data.value?.jobs ?? []).filter((j) => j.last_run && !j.last_run.ok).length)

function dot(j: Job): string {
  if (!j.last_run) return 'bg-muted-foreground/40'
  return j.last_run.ok ? 'bg-success' : 'bg-destructive'
}

function took(ms: number): string {
  if (ms < 1000) return `${ms} ms`
  if (ms < 60_000) return `${(ms / 1000).toFixed(1)} s`
  return formatDuration(ms / 1000)
}

function notRun(j: Job): string {
  if (j.kind === 'always_on') return 'Listens all the time'
  if (j.kind === 'triggered') return 'Not needed yet'
  return 'Not run yet'
}

/** The next run, or '' when the RelativeTime should show it. */
function nextText(at: string): string {
  const ms = new Date(at).getTime() - nowMs.value
  if (ms <= 0) return 'due now'
  if (ms < 60_000) return 'in under a minute'
  return ''
}
</script>

<template>
  <div class="grid gap-6">
    <div class="flex flex-wrap items-center gap-x-3 gap-y-1.5 text-sm text-muted-foreground">
      <p>
        What bitploy runs by itself.
        <template v-if="data?.since">Runs are counted since bitploy started <RelativeTime :date="data.since" />.</template>
        <template v-else-if="data">The background jobs do not run in this bitploy process.</template>
      </p>
      <Badge v-if="failed > 0" variant="destructive" data-testid="jobs-failed">{{ failed }} failed last time</Badge>
    </div>

    <QueryError v-if="jobs.isError.value && !data" :error="jobs.error.value" @retry="jobs.refetch()" />
    <div v-else-if="!data" class="grid gap-6">
      <Skeleton v-for="i in 3" :key="i" class="h-40 w-full rounded-xl" />
    </div>

    <Card v-for="g in groups" :key="g.key" :data-testid="`jobs-${g.key}`">
      <CardHeader>
        <CardTitle>{{ g.title }}</CardTitle>
        <CardDescription v-if="g.description">{{ g.description }}</CardDescription>
      </CardHeader>
      <CardContent class="px-0">
        <!-- Column labels from sm up; on a phone each row stacks and the cells say what they are. -->
        <div
          class="hidden grid-cols-[minmax(0,1fr)_minmax(0,16rem)_minmax(0,11rem)] gap-x-6 border-b px-4 pb-2 text-xs font-medium tracking-wide text-muted-foreground uppercase sm:grid"
        >
          <span>Job</span>
          <span>Last run</span>
          <span>Schedule</span>
        </div>
        <ul class="divide-y">
          <li
            v-for="j in g.jobs"
            :key="j.id"
            class="grid gap-x-6 gap-y-2 px-4 py-3 sm:grid-cols-[minmax(0,1fr)_minmax(0,16rem)_minmax(0,11rem)]"
            :data-testid="`job-${j.id}`"
          >
            <div class="min-w-0">
              <p class="flex items-center gap-2 font-medium">
                <span class="size-2 shrink-0 rounded-full" :class="dot(j)" />
                {{ j.title }}
              </p>
              <p class="mt-0.5 pl-4 text-xs text-muted-foreground">{{ j.description }}</p>
            </div>

            <div class="min-w-0 pl-4 sm:pl-0">
              <template v-if="j.last_run">
                <p class="break-words first-letter:uppercase" :class="j.last_run.ok ? '' : 'font-medium text-destructive'">
                  {{ j.last_run.ok ? j.last_run.summary || 'Done' : 'Failed' }}
                </p>
                <p class="text-xs text-muted-foreground tabular-nums">
                  <RelativeTime :date="j.last_run.started_at" /> · took {{ took(j.last_run.duration_ms) }}
                  <template v-if="j.runs > 1"> · {{ formatCount(j.runs) }} runs</template>
                </p>
              </template>
              <p v-else class="text-xs text-muted-foreground">{{ notRun(j) }}</p>
            </div>

            <div class="min-w-0 pl-4 text-xs sm:pl-0">
              <p>{{ j.schedule }}</p>
              <p v-if="j.next_run_at" class="text-muted-foreground tabular-nums">
                Next
                <template v-if="nextText(j.next_run_at)">{{ nextText(j.next_run_at) }}</template>
                <RelativeTime v-else :date="j.next_run_at" />
              </p>
            </div>

            <p
              v-if="j.last_run && !j.last_run.ok && j.last_run.error"
              class="min-w-0 rounded-md bg-destructive/10 px-2.5 py-1.5 font-mono text-xs break-words text-destructive sm:col-span-3 dark:bg-destructive/20"
            >
              {{ j.last_run.error }}
            </p>
          </li>
        </ul>
      </CardContent>
    </Card>
  </div>
</template>
