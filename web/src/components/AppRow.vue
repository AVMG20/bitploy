<script setup lang="ts">
import { computed } from 'vue'
import { RouterLink, useRouter } from 'vue-router'
import { ExternalLink } from 'lucide-vue-next'
import { TableCell, TableRow } from '@/components/ui/table'
import AppStatusBadge from './AppStatusBadge.vue'
import RelativeTime from './RelativeTime.vue'
import { useAppMetrics } from '@/composables/queries'
import { nowMs } from '@/composables/useNow'
import { formatMB, formatPercent, hostOf, uptime } from '@/lib/format'
import type { App } from '@/lib/types'

const props = defineProps<{ app: App }>()
const router = useRouter()

const running = computed(() => props.app.status === 'running')
const metrics = useAppMetrics(
  () => props.app.slug,
  () => '1h',
  running,
)
const latest = computed(() => (running.value ? metrics.data.value?.latest : null))
const lastDeploy = computed(() => {
  const d = props.app.current_deployment
  return d?.finished_at ?? d?.queued_at ?? props.app.updated_at
})
</script>

<template>
  <TableRow class="cursor-pointer" @click="router.push({ name: 'app', params: { slug: app.slug } })">
    <TableCell class="max-w-64">
      <RouterLink :to="{ name: 'app', params: { slug: app.slug } }" class="block truncate font-medium hover:underline" @click.stop>
        {{ app.name }}
      </RouterLink>
      <a
        v-if="app.type === 'web'"
        :href="app.url"
        target="_blank"
        rel="noopener noreferrer"
        class="inline-flex max-w-full items-center gap-1 truncate text-xs text-muted-foreground hover:underline"
        @click.stop
      >
        <span class="truncate">{{ hostOf(app.url) }}</span>
        <ExternalLink class="size-3 shrink-0" />
      </a>
      <span v-else class="text-xs text-muted-foreground">Background worker</span>
    </TableCell>
    <TableCell><AppStatusBadge :status="app.status" /></TableCell>
    <TableCell class="hidden text-right tabular-nums sm:table-cell">{{ latest ? formatPercent(latest.cpu) : '-' }}</TableCell>
    <TableCell class="hidden text-right tabular-nums sm:table-cell">{{ latest ? formatMB(latest.memory_mb) : '-' }}</TableCell>
    <TableCell class="hidden text-right tabular-nums md:table-cell">
      {{ running ? uptime(app.started_at, nowMs) : '-' }}
    </TableCell>
    <TableCell class="hidden text-right md:table-cell"><RelativeTime :date="lastDeploy" empty="Not yet" /></TableCell>
  </TableRow>
</template>
