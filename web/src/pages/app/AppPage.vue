<script setup lang="ts">
import { computed, ref } from 'vue'
import { RouterLink, RouterView, useRoute } from 'vue-router'
import { ArrowLeft, ExternalLink, Play, RefreshCw, RotateCw, Square } from 'lucide-vue-next'
import AppStatusBadge from '@/components/AppStatusBadge.vue'
import ConfirmDialog from '@/components/ConfirmDialog.vue'
import CopyButton from '@/components/CopyButton.vue'
import EmptyState from '@/components/EmptyState.vue'
import RelativeTime from '@/components/RelativeTime.vue'
import QueryError from '@/components/QueryError.vue'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { useApp } from '@/composables/queries'
import { useAppActions } from '@/composables/useAppActions'
import { ApiError } from '@/lib/api'
import { channelLabel, hostOf } from '@/lib/format'

const props = defineProps<{ slug: string }>()
const route = useRoute()
const app = useApp(() => props.slug)

const tabs = [
  { name: 'app', label: 'Overview' },
  { name: 'app-traffic', label: 'Traffic' },
  { name: 'app-deployments', label: 'Deployments' },
  { name: 'app-logs', label: 'Logs' },
  { name: 'app-domains', label: 'Domains' },
  { name: 'app-settings', label: 'Settings' },
]
// Background workers have no visitors.
const shownTabs = computed(() => tabs.filter((t) => t.name !== 'app-traffic' || app.data.value?.type !== 'worker'))
const current = computed(() => String(route.name))
const notFound = computed(() => app.error.value instanceof ApiError && app.error.value.status === 404)

// The actions live in the header so every tab has them.
const actions = useAppActions(() => props.slug)
const data = computed(() => app.data.value)
const dep = computed(() => data.value?.current_deployment ?? null)
const canStart = computed(() => !!data.value && ['stopped', 'crashed', 'failed'].includes(data.value.status) && !!dep.value)
const canStop = computed(() => !!data.value && ['running', 'deploying'].includes(data.value.status))
const busy = computed(() => actions.redeploy.isPending.value || actions.restart.isPending.value || actions.start.isPending.value || actions.stop.isPending.value)
const shownStatus = computed(() => (actions.restart.isPending.value ? 'restarting' : actions.start.isPending.value ? 'starting' : (data.value?.status ?? '')))

const confirmStop = ref(false)
function stop() {
  actions.stop.mutate(undefined, { onSettled: () => (confirmStop.value = false) })
}
</script>

<template>
  <div class="grid grid-cols-[minmax(0,1fr)] gap-5">
    <RouterLink to="/" class="inline-flex w-fit items-center gap-1 text-sm text-muted-foreground hover:text-foreground">
      <ArrowLeft class="size-4" /> All apps
    </RouterLink>

    <EmptyState v-if="notFound" title="We cannot find that app." description="It may have been deleted, or the address has a typo.">
      <Button as-child><RouterLink to="/">Back to your apps</RouterLink></Button>
    </EmptyState>
    <QueryError v-else-if="app.isError.value && !app.data.value" :error="app.error.value" @retry="app.refetch()" />

    <template v-else>
      <div v-if="data" class="flex flex-wrap items-start justify-between gap-x-6 gap-y-4">
        <div class="min-w-0">
          <div class="flex min-w-0 items-center gap-3">
            <h1 class="min-w-0 truncate text-2xl font-semibold tracking-tight">{{ data.name }}</h1>
            <AppStatusBadge :status="shownStatus" />
          </div>
          <div class="mt-1.5 flex flex-wrap items-center gap-x-2 gap-y-1 text-sm text-muted-foreground">
            <template v-if="data.type === 'web'">
              <a :href="data.url" target="_blank" rel="noopener noreferrer" class="truncate font-mono text-[13px] text-foreground hover:underline">{{ hostOf(data.url) }}</a>
              <CopyButton :value="data.url" what="Link copied" label="Copy link" icon class="-ml-1 size-6 text-muted-foreground" />
            </template>
            <span v-else>Background worker</span>
            <span class="size-1 rounded-full bg-muted-foreground/50" aria-hidden="true" />
            <span v-if="dep">Deployed <RelativeTime :date="dep.finished_at ?? dep.queued_at" /> by {{ channelLabel(dep.channel) }}</span>
            <span v-else>Not deployed yet</span>
          </div>
        </div>
        <div class="flex flex-wrap items-center gap-2">
          <Button v-if="data.type === 'web'" as-child variant="outline">
            <a :href="data.url" target="_blank" rel="noopener noreferrer">Open app <ExternalLink /></a>
          </Button>
          <Button variant="outline" :disabled="busy || !dep" @click="actions.restart.mutate()">
            <RotateCw :class="actions.restart.isPending.value && 'animate-spin'" /> {{ actions.restart.isPending.value ? 'Restarting...' : 'Restart' }}
          </Button>
          <Button v-if="canStart" variant="outline" :disabled="busy" @click="actions.start.mutate()">
            <Play /> {{ actions.start.isPending.value ? 'Starting...' : 'Start' }}
          </Button>
          <Button v-else-if="canStop" variant="outline" :disabled="busy" @click="confirmStop = true">
            <Square /> Stop
          </Button>
          <Button :disabled="busy || !dep" @click="actions.redeploy.mutate()">
            <RefreshCw :class="actions.redeploy.isPending.value && 'animate-spin'" /> Redeploy
          </Button>
        </div>
      </div>
      <div v-else class="grid gap-2">
        <Skeleton class="h-8 w-72" />
        <Skeleton class="h-4 w-96 max-w-full" />
      </div>

      <Tabs :model-value="current" class="min-w-0">
        <div class="overflow-x-auto overflow-y-hidden border-b">
          <TabsList variant="line" class="-ml-1.5">
            <TabsTrigger v-for="t in shownTabs" :key="t.name" :value="t.name" as-child>
              <RouterLink :to="{ name: t.name, params: { slug } }">{{ t.label }}</RouterLink>
            </TabsTrigger>
          </TabsList>
        </div>
      </Tabs>

      <RouterView />
    </template>

    <ConfirmDialog
      v-model:open="confirmStop"
      title="Stop this app?"
      description="People who open its address will see a page saying it is stopped. You can start it again at any time."
      confirm-label="Stop app"
      :loading="actions.stop.isPending.value"
      @confirm="stop"
    />
  </div>
</template>
