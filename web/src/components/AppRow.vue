<script setup lang="ts">
import { computed } from 'vue'
import { RouterLink, useRouter } from 'vue-router'
import { ArrowUpCircle, ExternalLink } from 'lucide-vue-next'
import { TableCell, TableRow } from '@/components/ui/table'
import AppStatusBadge from './AppStatusBadge.vue'
import RelativeTime from './RelativeTime.vue'
import { nowMs } from '@/composables/useNow'
import { cpuFromPercent, formatCPU, formatMB, hostOf, uptime } from '@/lib/format'
import type { App } from '@/lib/types'
import { isMulti, serviceRole, sortServices } from '@/lib/services'

const props = defineProps<{ app: App }>()
const router = useRouter()

const running = computed(() => props.app.status === 'running')
// The list already carries the latest 15-second sample; no per-row metrics query.
const latest = computed(() => (running.value ? props.app.live : null))
// A project with several services says how many, and what they are on hover.
const serviceCount = computed(() => (isMulti(props.app) ? props.app.services.length : 0))
const serviceTitle = computed(() => sortServices(props.app.services ?? []).map((s) => `${serviceRole(s).label} (${s.name})`).join(', '))
const lastDeploy = computed(() => {
  const d = props.app.current_deployment
  return d?.finished_at ?? d?.queued_at ?? null
})
</script>

<template>
  <TableRow class="cursor-pointer" @click="router.push({ name: 'app', params: { slug: app.slug } })">
    <TableCell class="max-w-64">
      <div class="flex min-w-0 items-center gap-2">
        <RouterLink :to="{ name: 'app', params: { slug: app.slug } }" class="truncate font-medium hover:underline" @click.stop>
          {{ app.name }}
        </RouterLink>
        <span
          v-if="serviceCount"
          class="shrink-0 rounded-md border px-1.5 text-[11px] text-muted-foreground tabular-nums"
          :title="serviceTitle"
        >{{ serviceCount }} services</span>
        <span
          v-if="app.catalog_update"
          class="inline-flex shrink-0 items-center gap-1 rounded-md border border-primary/25 bg-primary/10 px-1.5 text-[11px] font-medium text-primary"
          :title="`Version ${app.catalog_update} is available. Update it in the app's Settings.`"
        >
          <ArrowUpCircle class="size-3" aria-hidden="true" /> Update
        </span>
      </div>
      <a
        v-if="app.type === 'web'"
        :href="app.url"
        target="_blank"
        rel="noopener noreferrer"
        class="inline-flex max-w-full items-center gap-1 truncate font-mono text-[11px] text-muted-foreground hover:text-foreground"
        @click.stop
      >
        <span class="truncate">{{ hostOf(app.url) }}</span>
        <ExternalLink class="size-3 shrink-0" />
      </a>
      <span v-else class="text-xs text-muted-foreground">Background worker</span>
    </TableCell>
    <TableCell><AppStatusBadge :status="app.status" /></TableCell>
    <TableCell class="hidden text-right tabular-nums sm:table-cell">{{ latest ? formatCPU(cpuFromPercent(latest.cpu)) : '-' }}</TableCell>
    <TableCell class="hidden text-right tabular-nums sm:table-cell">{{ latest ? formatMB(latest.memory_mb) : '-' }}</TableCell>
    <TableCell class="hidden text-right tabular-nums md:table-cell">
      {{ running ? uptime(app.started_at, nowMs) : '-' }}
    </TableCell>
    <TableCell class="hidden text-right md:table-cell"><RelativeTime :date="lastDeploy" empty="Not yet" /></TableCell>
  </TableRow>
</template>
