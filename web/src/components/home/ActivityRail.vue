<script setup lang="ts">
// The latest few events on Home: most changes are made by an AI assistant, and
// this is where you see what it did. The whole list is the Activity page.
import { computed } from 'vue'
import { RouterLink } from 'vue-router'
import { useQuery } from '@tanstack/vue-query'
import { Database, Globe, KeyRound, Rocket, Sparkles, User } from 'lucide-vue-next'
import RelativeTime from '@/components/RelativeTime.vue'
import { Skeleton } from '@/components/ui/skeleton'
import { keys } from '@/composables/queries'
import { api } from '@/lib/api'
import { activityRow, auditGroup, auditLabel } from '@/lib/audit'
import { channelLabel } from '@/lib/format'

const SHOWN = 5
// Under the same key as the Activity page, so a live update refreshes both.
const activity = useQuery({
  queryKey: [...keys.activity, 'recent'],
  queryFn: () => api.activity(),
  staleTime: 30_000,
})
const rows = computed(() => (activity.data.value?.items ?? []).slice(0, SHOWN).map(activityRow))

const ICONS = { apps: Globe, deploys: Rocket, data: Database, domains: Globe, access: KeyRound, github: Rocket, admin: User } as const
function icon(r: { action: string; channel: string }) {
  return r.channel === 'mcp' ? Sparkles : ICONS[auditGroup(r.action)]
}
</script>

<template>
  <div class="rounded-xl border bg-card shadow-card">
    <div class="flex items-center justify-between px-4 pt-4 pb-2">
      <h2 class="text-sm font-semibold">Activity</h2>
      <RouterLink to="/activity" class="text-xs text-muted-foreground hover:text-foreground hover:underline">All</RouterLink>
    </div>
    <div v-if="activity.isPending.value" class="grid gap-2 px-4 pb-4"><Skeleton v-for="i in 3" :key="i" class="h-9 w-full" /></div>
    <p v-else-if="!rows.length" class="px-4 pb-4 text-sm text-muted-foreground">Nothing yet. What you or your AI assistant do shows up here.</p>
    <ul v-else class="text-sm">
      <li v-for="r in rows" :key="r.id" class="flex gap-3 border-t px-4 py-2.5">
        <span :class="['mt-0.5 flex size-7 shrink-0 items-center justify-center rounded-md border', r.channel === 'mcp' ? 'border-primary/30 bg-primary/10 text-primary-text' : 'bg-muted/60 text-muted-foreground']">
          <component :is="icon(r)" class="size-3.5" aria-hidden="true" />
        </span>
        <div class="min-w-0">
          <div class="line-clamp-2">
            <b class="font-medium">{{ r.who }}</b> <span class="text-muted-foreground">{{ (r.label ?? auditLabel(r.action)).toLowerCase() }}</span>
            <template v-if="r.target">
              {{ ' ' }}<RouterLink v-if="r.target.to" :to="r.target.to" class="hover:underline">{{ r.target.text }}</RouterLink>
              <template v-else>{{ r.target.text }}</template>
            </template>
          </div>
          <div class="text-xs text-muted-foreground"><RelativeTime :date="r.ts" /> · {{ channelLabel(r.channel) }}</div>
        </div>
      </li>
    </ul>
  </div>
</template>
