<script setup lang="ts">
import { computed } from 'vue'
import { RouterLink, useRouter } from 'vue-router'
import { ArrowUpRight, Lock } from 'lucide-vue-next'
import RelativeTime from '@/components/RelativeTime.vue'
import ServiceIcon from '@/components/ServiceIcon.vue'
import AppStatusBadge from '@/components/AppStatusBadge.vue'
import { TableCell, TableRow } from '@/components/ui/table'
import AppVisitorsToday from '@/components/project/AppVisitorsToday.vue'
import { appRoute } from '@/lib/appLinks'
import { channelLabel, hostOf } from '@/lib/format'
import { aliasOf, internalHostOf, isPublicApp } from '@/lib/projects'
import type { AppRole } from '@/lib/projects'
import type { App } from '@/lib/types'

// One row of the table under the project map; CPU and memory are on the map's cards.
const props = defineProps<{ app: App; role: AppRole; visitors?: boolean }>()
const router = useRouter()

const publicApp = computed(() => isPublicApp(props.app))
const host = computed(() => internalHostOf(props.app))
const dep = computed(() => props.app.current_deployment)

// The whole row opens the app; links and buttons inside keep their own click.
function open(e: MouseEvent) {
  if ((e.target as HTMLElement).closest('a, button')) return
  void router.push(appRoute(props.app))
}
</script>

<template>
  <TableRow class="cursor-pointer" :data-app-row="app.id" @click="open">
    <TableCell>
      <RouterLink :to="appRoute(app)" class="flex min-w-0 items-center gap-2.5 hover:underline">
        <ServiceIcon :role="role.key" size="sm" />
        <span class="min-w-0">
          <span class="block truncate font-medium">{{ aliasOf(app) }}</span>
          <span v-if="app.name !== aliasOf(app)" class="block truncate text-xs text-muted-foreground">{{ app.name }}</span>
        </span>
      </RouterLink>
    </TableCell>
    <TableCell><AppStatusBadge :status="app.status" /></TableCell>
    <TableCell class="text-[12.5px]">
      <a v-if="publicApp" :href="app.url" target="_blank" rel="noopener noreferrer" class="inline-flex items-center gap-1 font-mono hover:underline">
        {{ hostOf(app.url) }} <ArrowUpRight class="size-3 text-muted-foreground" />
      </a>
      <span v-else-if="host" class="inline-flex items-center gap-1.5 font-mono text-muted-foreground"><Lock class="size-3" /> {{ host }}</span>
      <span v-else class="text-muted-foreground">No address</span>
    </TableCell>
    <TableCell v-if="visitors" class="text-right font-mono text-xs tabular-nums">
      <AppVisitorsToday v-if="publicApp" :slug="app.slug" />
      <span v-else class="text-muted-foreground">-</span>
    </TableCell>
    <TableCell class="text-[12.5px] whitespace-nowrap text-muted-foreground">
      <template v-if="dep">
        <RelativeTime :date="dep.finished_at ?? dep.queued_at" class="block text-foreground" />
        <span class="block text-xs">{{ channelLabel(dep.channel) }}</span>
      </template>
      <template v-else>Not deployed yet</template>
    </TableCell>
  </TableRow>
</template>
