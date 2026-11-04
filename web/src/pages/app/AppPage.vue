<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { RouterLink, RouterView, useRoute, useRouter } from 'vue-router'
import { useQuery } from '@tanstack/vue-query'
import { ArrowUpCircle, ExternalLink, GitBranch, Hammer, Link, Lock, MoreHorizontal, Play, RefreshCw, RotateCw, Settings, Square } from 'lucide-vue-next'
import AppStatusBadge from '@/components/AppStatusBadge.vue'
import ConfirmDialog from '@/components/ConfirmDialog.vue'
import CopyButton from '@/components/CopyButton.vue'
import EmptyState from '@/components/EmptyState.vue'
import RelativeTime from '@/components/RelativeTime.vue'
import QueryError from '@/components/QueryError.vue'
import { Button } from '@/components/ui/button'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { Skeleton } from '@/components/ui/skeleton'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { keys, useApp, useDataRoot } from '@/composables/queries'
import { useAppActions } from '@/composables/useAppActions'
import { clearRestartNeeded, useRestartNeeded } from '@/composables/useRestartNeeded'
import { ApiError, api } from '@/lib/api'
import { APP_TABS } from '@/lib/appTabs'
import { pickDatabase } from '@/lib/dbvalue'
import { commitUrl, deployedVia, hostOf, shortSha } from '@/lib/format'
import { copyText } from '@/lib/notify'
import CatalogUpdateDialog from './CatalogUpdateDialog.vue'
import UploadVersionButton from './UploadVersionButton.vue'
import ServiceSheet from './ServiceSheet.vue'
import { isMulti } from '@/lib/services'

const props = defineProps<{ slug: string }>()
const route = useRoute()
const router = useRouter()
const app = useApp(() => props.slug)
const data = computed(() => app.data.value)
const isWeb = computed(() => data.value?.type === 'web')
// Several services: the overview is the service grid, actions act on all of them.
const multi = computed(() => isMulti(data.value))
const thing = computed(() => (multi.value ? 'project' : 'app'))

// The Database tab shows when the saved files hold a SQLite file at the top. A failed or empty look shows no tab and no error.
const root = useDataRoot(() => props.slug, () => !!data.value?.volume)
const hasDatabase = computed(() => !!pickDatabase(root.data.value?.items ?? [], props.slug))
// Background workers have no visitors and no web address.
const shownTabs = computed(() =>
  APP_TABS.filter((t) => (!t.webOnly || data.value?.type !== 'worker') && (!t.database || hasDatabase.value)).map((t) =>
    multi.value && t.name === 'app' ? { ...t, label: 'Services' } : t,
  ),
)
// A page below a tab (the database viewer) names the tab that stays highlighted.
const current = computed(() => String(route.meta.tab ?? route.name))
const notFound = computed(() => app.error.value instanceof ApiError && app.error.value.status === 404)

// The browser tab says which app this is.
const tabLabel = computed(() => (route.meta.title as string | undefined) ?? APP_TABS.find((t) => t.name === current.value && t.name !== 'app')?.label ?? '')
watch(
  () => [data.value?.name, tabLabel.value] as const,
  ([name, tab]) => {
    if (name) document.title = `${tab ? `${tab} - ` : ''}${name} - bitploy`
  },
  { immediate: true },
)

// ---------------------------------------------------------------- actions (in the header, so every tab has them)
const actions = useAppActions(() => props.slug)
const dep = computed(() => data.value?.current_deployment ?? null)
// A failed first deploy has no live version, but its source (or the linked repository) can be built again.
const canRebuild = computed(() => !!data.value && (!!dep.value || !!data.value.git_source || data.value.status !== 'new'))
const canStart = computed(() => !!data.value && ['stopped', 'crashed', 'failed'].includes(data.value.status) && !!dep.value)
const canStop = computed(() => !!data.value && ['running', 'deploying'].includes(data.value.status))
const busy = computed(() => actions.redeploy.isPending.value || actions.restart.isPending.value || actions.start.isPending.value || actions.stop.isPending.value)
const shownStatus = computed(() => (actions.restart.isPending.value ? 'restarting' : actions.start.isPending.value ? 'starting' : (data.value?.status ?? '')))

/**
 * The main button depends on where the code comes from: a linked repository
 * builds its newest commit, uploaded files get a new upload, a ready-made app
 * with a newer version gets that update. Anything else opens the app.
 */
const primary = computed<'git' | 'upload' | 'update' | 'open' | 'none'>(() => {
  const a = data.value
  if (!a) return 'none'
  if (a.git_source) return 'git'
  if (a.catalog_id) return a.catalog_update ? 'update' : isWeb.value && dep.value ? 'open' : 'none'
  return 'upload'
})

// Show the new deployment's progress where it is listed.
function rebuild() {
  actions.redeploy.mutate(undefined, { onSuccess: () => void router.push({ name: 'app-deployments', params: { slug: props.slug } }) })
}
function restart() {
  actions.restart.mutate(undefined, { onSuccess: () => clearRestartNeeded(data.value?.id) })
}

const confirmStop = ref(false)
function stop() {
  actions.stop.mutate(undefined, { onSettled: () => (confirmStop.value = false) })
}

const updateOpen = ref(false)

// ---------------------------------------------------------------- changes that wait for a restart
const env = useQuery({
  queryKey: computed(() => keys.env(props.slug)),
  queryFn: () => api.env(props.slug),
  enabled: computed(() => data.value?.status === 'running'),
  staleTime: 30_000,
})
const pending = useRestartNeeded(data, () => env.data.value)
</script>

<template>
  <div class="grid grid-cols-[minmax(0,1fr)] gap-5">
    <EmptyState v-if="notFound" title="We cannot find that project." description="It may have been deleted, or the address has a typo.">
      <Button as-child><RouterLink to="/">Back to your projects</RouterLink></Button>
    </EmptyState>
    <QueryError v-else-if="app.isError.value && !data" :error="app.error.value" @retry="app.refetch()" />

    <template v-else>
      <div v-if="data" class="flex flex-wrap items-start justify-between gap-x-6 gap-y-4">
        <div class="min-w-0">
          <div class="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1.5">
            <h1 class="min-w-0 truncate text-2xl font-semibold tracking-tight">{{ data.name }}</h1>
            <AppStatusBadge :status="shownStatus" />
            <button
              v-if="data.catalog_update"
              type="button"
              class="inline-flex h-5.5 items-center gap-1 rounded-md border border-primary/25 bg-primary/10 px-2 text-xs font-medium text-primary transition-colors hover:bg-primary/15"
              @click="updateOpen = true"
            >
              <ArrowUpCircle class="size-3.5" aria-hidden="true" /> Update to {{ data.catalog_update }}
            </button>
          </div>
          <div class="mt-1.5 flex flex-wrap items-center gap-x-2 gap-y-1 text-sm text-muted-foreground">
            <template v-if="isWeb">
              <a :href="data.url" target="_blank" rel="noopener noreferrer" class="truncate font-mono text-[13px] text-foreground hover:underline">{{ hostOf(data.url) }}</a>
              <CopyButton :value="data.url" what="Link copied" label="Copy link" icon class="-ml-1 size-6 text-muted-foreground" />
              <span v-if="data.protected" class="inline-flex items-center gap-1 text-xs" :title="`Password-protected. Visitors sign in as ${data.protected_username}.`">
                <Lock class="size-3.5" aria-hidden="true" /> Private
              </span>
            </template>
            <span v-else>Background worker</span>
            <span class="size-1 rounded-full bg-muted-foreground/50" aria-hidden="true" />
            <span v-if="dep">Deployed <RelativeTime :date="dep.finished_at ?? dep.queued_at" /> {{ deployedVia(dep.channel) }}</span>
            <span v-else>Not deployed yet</span>
            <template v-if="data.git_source">
              <span class="size-1 rounded-full bg-muted-foreground/50" aria-hidden="true" />
              <span class="inline-flex items-center gap-1.5">
                <span class="inline-flex items-center gap-1"><GitBranch class="size-3.5" /> {{ data.git_source.branch }}</span>
                <template v-if="dep?.commit_sha">
                  <a v-if="commitUrl(data, dep.commit_sha)" :href="commitUrl(data, dep.commit_sha)" target="_blank" rel="noopener noreferrer" class="font-mono text-[13px] text-foreground hover:underline">{{ shortSha(dep.commit_sha) }}</a>
                  <span v-else class="font-mono text-[13px] text-foreground">{{ shortSha(dep.commit_sha) }}</span>
                </template>
              </span>
            </template>
          </div>
        </div>

        <div class="flex flex-wrap items-center gap-2">
          <template v-if="isWeb && primary !== 'open'">
            <Button v-if="dep" as-child variant="outline">
              <a :href="data.url" target="_blank" rel="noopener noreferrer">{{ multi ? 'Open website' : 'Open app' }} <ExternalLink /></a>
            </Button>
            <span v-else title="Available once the first deploy is live">
              <Button variant="outline" disabled>{{ multi ? 'Open website' : 'Open app' }} <ExternalLink /></Button>
            </span>
          </template>
          <Button variant="outline" :disabled="busy || !dep" :title="multi ? 'Restart every service of this project' : undefined" @click="restart">
            <RotateCw :class="actions.restart.isPending.value && 'animate-spin'" /> {{ actions.restart.isPending.value ? 'Restarting...' : multi ? 'Restart all' : 'Restart' }}
            <span v-if="pending.reasons.value.length" class="size-1.5 rounded-full bg-warning" aria-label="(changes waiting)" />
          </Button>
          <Button v-if="canStart" variant="outline" :disabled="busy" @click="actions.start.mutate()">
            <Play /> {{ actions.start.isPending.value ? 'Starting...' : 'Start' }}
          </Button>

          <Button v-if="primary === 'git'" :disabled="busy || !canRebuild" :title="`Build the newest commit of ${data.git_source?.branch} and put it online`" @click="rebuild">
            <RefreshCw :class="actions.redeploy.isPending.value && 'animate-spin'" /> Deploy latest
          </Button>
          <UploadVersionButton v-else-if="primary === 'upload'" :slug="slug" variant="default" :label="dep ? 'Upload new version' : 'Upload files'" />
          <Button v-else-if="primary === 'update'" @click="updateOpen = true"><ArrowUpCircle /> Update to {{ data.catalog_update }}</Button>
          <Button v-else-if="primary === 'open'" as-child>
            <a :href="data.url" target="_blank" rel="noopener noreferrer">Open app <ExternalLink /></a>
          </Button>

          <DropdownMenu>
            <DropdownMenuTrigger as-child>
              <Button variant="outline" size="icon" aria-label="More actions"><MoreHorizontal /></Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end" class="w-60">
              <DropdownMenuItem v-if="primary !== 'git'" :disabled="busy || !canRebuild" @click="rebuild">
                <Hammer /> Rebuild the current files
              </DropdownMenuItem>
              <DropdownMenuItem v-if="canStop" :disabled="busy" @click="confirmStop = true"><Square /> Stop {{ thing }}</DropdownMenuItem>
              <DropdownMenuItem v-if="isWeb" @click="copyText(data.url, 'Link copied')"><Link /> Copy link</DropdownMenuItem>
              <DropdownMenuSeparator />
              <DropdownMenuItem @click="router.push({ name: 'app-settings', params: { slug } })"><Settings /> Settings</DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenu>
        </div>
      </div>
      <div v-else class="grid gap-2">
        <Skeleton class="h-8 w-72 max-w-full" />
        <Skeleton class="h-4 w-96 max-w-full" />
      </div>

      <!-- Saved changes that the running app does not use yet. -->
      <div
        v-if="pending.reasons.value.length"
        role="status"
        class="flex flex-wrap items-center gap-x-4 gap-y-2 rounded-xl border border-warning/40 bg-warning/10 px-4 py-3 text-sm"
      >
        <RotateCw class="size-4 shrink-0 text-[color-mix(in_oklch,var(--warning)_75%,var(--foreground))]" aria-hidden="true" />
        <p class="min-w-0 flex-1">
          <span class="font-medium">Restart to apply your changes.</span>
          <span class="text-muted-foreground"> Your changes to {{ pending.text.value }} are saved, but the running {{ thing }} does not use them yet.</span>
        </p>
        <Button size="sm" :disabled="busy" @click="restart">
          <RotateCw :class="actions.restart.isPending.value && 'animate-spin'" /> {{ actions.restart.isPending.value ? 'Restarting...' : 'Restart now' }}
        </Button>
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
      :title="`Stop ${data?.name ?? 'this app'}?`"
      :description="multi ? 'Every service stops. People who open its address will see a page saying it is stopped. You can start it again at any time.' : 'People who open its address will see a page saying it is stopped. You can start it again at any time.'"
      :confirm-label="`Stop ${thing}`"
      :loading="actions.stop.isPending.value"
      @confirm="stop"
    />
    <ServiceSheet v-if="data && multi" :app="data" />
    <CatalogUpdateDialog v-if="data?.catalog_update && updateOpen" v-model:open="updateOpen" :app="data" />
  </div>
</template>
