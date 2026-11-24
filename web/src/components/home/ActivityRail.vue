<script setup lang="ts">
// Right card of the Home "At a glance" row: the latest few events with semantic
// icon badges and right-aligned compact timestamps. The whole list is /activity.
import { computed } from 'vue'
import { RouterLink } from 'vue-router'
import { useQuery } from '@tanstack/vue-query'
import { Check, Database, Globe, KeyRound, Rocket, Sparkles, TriangleAlert, User } from 'lucide-vue-next'
import { Skeleton } from '@/components/ui/skeleton'
import { keys } from '@/composables/queries'
import { nowMs } from '@/composables/useNow'
import { api } from '@/lib/api'
import { activityRow, auditGroup, auditLabel } from '@/lib/audit'
import { channelLabel, exactTime } from '@/lib/format'
import { shortRelativeTime } from '@/lib/home'
import { cn } from '@/lib/utils'

const SHOWN = 4
// Under the same key as the Activity page, so a live update refreshes both.
const activity = useQuery({
  queryKey: [...keys.activity, 'recent'],
  queryFn: () => api.activity(),
  staleTime: 30_000,
})
const rows = computed(() => (activity.data.value?.items ?? []).slice(0, SHOWN).map(activityRow))

const ICONS = { apps: Globe, deploys: Rocket, data: Database, domains: Globe, access: KeyRound, github: Rocket, admin: User } as const

type RowTone = 'mcp' | 'ok' | 'bad' | 'muted'
const BAD_ACTIONS = new Set(['deployment.failed', 'app.crashed', 'app.container_died', 'app.oom', 'app.unhealthy', 'app.disk_stopped', 'backup.restore_failed', 'domain.failed'])
const OK_ACTIONS = new Set(['deployment.running', 'app.healthy', 'domain.active'])

function toneOf(r: { action: string; channel: string }): RowTone {
  if (BAD_ACTIONS.has(r.action)) return 'bad'
  if (r.channel === 'mcp') return 'mcp'
  if (OK_ACTIONS.has(r.action)) return 'ok'
  return 'muted'
}

function iconOf(r: { action: string; channel: string }) {
  const tone = toneOf(r)
  if (tone === 'bad') return TriangleAlert
  if (tone === 'mcp') return Sparkles
  if (tone === 'ok') return Check
  return ICONS[auditGroup(r.action)]
}

const ICON_BOX: Record<RowTone, string> = {
  mcp: 'border-primary/30 bg-primary/10 text-primary-text',
  ok: 'border-success/25 bg-success/10 text-success',
  bad: 'border-destructive/30 bg-destructive/10 text-destructive',
  muted: 'bg-muted/60 text-muted-foreground',
}
</script>

<template>
  <section class="flex h-full flex-col overflow-hidden rounded-xl border bg-card shadow-card" aria-label="Recent activity">
    <div class="flex items-center justify-between px-4 pt-3.5 pb-2.5">
      <h2 class="text-sm font-semibold">Recent activity</h2>
      <RouterLink to="/activity" class="text-xs text-muted-foreground transition-colors hover:text-foreground hover:underline">All →</RouterLink>
    </div>

    <div v-if="activity.isPending.value" class="grid gap-2.5 px-4 pb-4">
      <Skeleton v-for="i in 4" :key="i" class="h-10 w-full" />
    </div>

    <p v-else-if="!rows.length" class="px-4 pb-4 text-sm text-muted-foreground">
      Nothing yet. What you or your AI assistant do shows up here.
    </p>

    <ul v-else class="flex flex-1 flex-col text-sm">
      <li v-for="r in rows" :key="r.id" class="flex flex-1 items-center gap-3 border-t px-4 py-2.5">
        <span :class="cn('flex size-7 shrink-0 items-center justify-center rounded-md border', ICON_BOX[toneOf(r)])">
          <component :is="iconOf(r)" class="size-3.5" aria-hidden="true" />
        </span>
        <div class="min-w-0 flex-1">
          <div class="truncate text-[13px]">
            <template v-if="r.target">
              <RouterLink v-if="r.target.to" :to="r.target.to" class="font-medium text-foreground hover:underline">{{ r.target.text }}</RouterLink>
              <span v-else class="font-medium text-foreground">{{ r.target.text }}</span>
              <span class="text-muted-foreground">{{ ' ' }}{{ (r.label ?? auditLabel(r.action)).toLowerCase() }}</span>
            </template>
            <template v-else>
              <span class="font-medium text-foreground">{{ r.who }}</span>
              <span class="text-muted-foreground">{{ ' ' }}{{ (r.label ?? auditLabel(r.action)).toLowerCase() }}</span>
            </template>
          </div>
          <div class="truncate text-xs text-muted-foreground">
            {{ channelLabel(r.channel) }}<template v-if="r.target && r.who && r.who !== 'bitploy'"> · {{ r.who }}</template>
          </div>
        </div>
        <time
          :datetime="r.ts"
          :title="exactTime(r.ts)"
          class="shrink-0 font-mono text-xs text-muted-foreground/80 tabular-nums"
        >
          {{ shortRelativeTime(r.ts, nowMs) }}
        </time>
      </li>
    </ul>
  </section>
</template>
