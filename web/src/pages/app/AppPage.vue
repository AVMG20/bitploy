<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { RouterLink, RouterView, useRoute, useRouter } from 'vue-router'
import { ArrowUpCircle, ExternalLink, GitBranch, Hammer, Link, Lock, MoreHorizontal, PanelRight, Play, RefreshCw, RotateCw, Settings, Square, Trash2 } from 'lucide-vue-next'
import AppStatusBadge from '@/components/AppStatusBadge.vue'
import ConfirmDialog from '@/components/ConfirmDialog.vue'
import CopyButton from '@/components/CopyButton.vue'
import DeleteAppDialog from '@/components/DeleteAppDialog.vue'
import EmptyState from '@/components/EmptyState.vue'
import PageHeader from '@/components/PageHeader.vue'
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
import { useApp, useDataRoot } from '@/composables/queries'
import { useAppControls } from '@/composables/useAppControls'
import { ApiError } from '@/lib/api'
import { APP_TABS } from '@/lib/appTabs'
import { pickDatabase } from '@/lib/dbvalue'
import { commitUrl, deployedVia, hostOf, shortSha } from '@/lib/format'
import { copyText } from '@/lib/notify'
import CatalogUpdateDialog from './CatalogUpdateDialog.vue'
import UploadVersionButton from './UploadVersionButton.vue'
import ServiceSheet from './ServiceSheet.vue'
import ServiceIcon from '@/components/ServiceIcon.vue'
import { useProject, useServiceTemplates } from '@/composables/queries'
import { projectRoute } from '@/lib/appLinks'
import { appRole, internalHostOf, projectIdOf, roleTitle, templateUpdate } from '@/lib/projects'

const props = defineProps<{ slug: string; projectId?: string }>()
const route = useRoute()
const router = useRouter()
const app = useApp(() => props.slug)
const data = computed(() => app.data.value)
const thing = 'app'

// The project around the app: its other apps, one click away.
const pid = computed(() => props.projectId || (data.value ? projectIdOf(data.value) : ''))
const { list: projectList, project } = useProject(pid)
const templates = useServiceTemplates()
const role = computed(() => (data.value ? appRole(data.value, templates.data.value ?? []) : null))
// The address names the project; an old link with the wrong one is put right.
watch(
  () => data.value,
  (a) => {
    if (a && props.projectId && props.projectId !== projectIdOf(a) && project.value === null && projectList.data.value) {
      void router.replace({ name: route.name ?? 'app', params: { ...route.params, projectId: projectIdOf(a) }, query: route.query, hash: route.hash })
    }
  },
)

// The Database tab shows when the saved files hold a SQLite file at the top. A failed or empty look shows no tab and no error.
const root = useDataRoot(() => props.slug, () => !!data.value?.volume)
const hasDatabase = computed(() => !!pickDatabase(root.data.value?.items ?? [], props.slug))
// Background workers have no visitors and no web address.
const shownTabs = computed(() =>
  APP_TABS.filter((t) => (!t.webOnly || isWeb.value) && (!t.database || hasDatabase.value)).map((t) =>
    multi.value && t.name === 'app' ? { ...t, label: 'Services' } : t,
  ),
)
// A page below a tab (the database viewer) names the tab that stays highlighted.
const current = computed(() => String(route.meta.tab ?? route.name))
const notFound = computed(() => app.error.value instanceof ApiError && app.error.value.status === 404)

// The browser tab says which app this is.
const tabLabel = computed(() => (route.meta.title as string | undefined) ?? APP_TABS.find((t) => t.name === current.value && t.name !== 'app')?.label ?? '')
watch(
  () => [data.value?.name, tabLabel.value, project.value?.name] as const,
  ([name, tab, pname]) => {
    if (name) document.title = `${tab ? `${tab} - ` : ''}${name}${pname && pname !== name ? ` - ${pname}` : ''} - bitploy`
  },
  { immediate: true },
)

// ---------------------------------------------------------------- actions (in the header, so every tab has them)
// Several services (a compose file): the overview is the service grid, actions act on all of them.
const { actions, isWeb, multi, dep, canRebuild, canStart, canStop, busy, shownStatus, primary, pending, restart, rebuild: startRebuild } = useAppControls(() => props.slug, data)
// Show the new deployment's progress where it is listed.
function rebuild() {
  startRebuild(() => void router.push({ name: 'app-deployments', params: { slug: props.slug } }))
}

const confirmStop = ref(false)
function stop() {
  actions.stop.mutate(undefined, { onSettled: () => (confirmStop.value = false) })
}

const updateOpen = ref(false)
const deleteOpen = ref(false)
</script>

<template>
  <div class="grid grid-cols-[minmax(0,1fr)] gap-5">
    <EmptyState v-if="notFound" title="App not found." description="It may have been deleted, or the address has a typo.">
      <Button as-child><RouterLink to="/">Back to your projects</RouterLink></Button>
    </EmptyState>
    <QueryError v-else-if="app.isError.value && !data" :error="app.error.value" @retry="app.refetch()" />

    <template v-else>
      <PageHeader v-if="data" :title="data.name">
        <template v-if="role" #icon><ServiceIcon :role="role.key" /></template>
        <template #badges>
          <span v-if="role" class="text-sm text-muted-foreground">{{ roleTitle(role) }}</span>
          <AppStatusBadge :status="shownStatus" />
          <span v-if="templateUpdate(data)" class="text-xs text-muted-foreground" :title="templateUpdate(data)!.title" data-testid="template-update">{{ templateUpdate(data)!.text }}</span>
          <button
            v-if="data.catalog_update"
            type="button"
            class="inline-flex h-5.5 items-center gap-1 rounded-md border border-primary/25 bg-primary/10 px-2 text-xs font-medium text-primary transition-colors hover:bg-primary/15"
            @click="updateOpen = true"
          >
            <ArrowUpCircle class="size-3.5" aria-hidden="true" /> Update to {{ data.catalog_update }}
          </button>
        </template>
        <template #meta>
          <span v-if="isWeb" class="inline-flex max-w-full items-center gap-1">
            <a :href="data.url" target="_blank" rel="noopener noreferrer" class="truncate font-mono text-[13px] text-foreground hover:underline">{{ hostOf(data.url) }}</a>
            <CopyButton :value="data.url" what="Link copied" label="Copy link" icon class="size-6 shrink-0 text-muted-foreground" />
            <span v-if="data.protected" class="inline-flex shrink-0 items-center gap-1 text-xs" :title="`Password-protected. Visitors sign in as ${data.protected_username}.`">
              <Lock class="size-3.5" aria-hidden="true" /> Private
            </span>
          </span>
          <span v-else-if="internalHostOf(data)" class="inline-flex items-center gap-1" :title="`The other apps of the project reach it at ${internalHostOf(data)}. The internet cannot.`">
            <Lock class="size-3.5" aria-hidden="true" /> Private, at <span class="font-mono text-[13px] text-foreground">{{ internalHostOf(data) }}</span>
          </span>
          <span v-else>Runs in the background, no web address</span>
          <span v-if="dep">Deployed <RelativeTime :date="dep.finished_at ?? dep.queued_at" /> {{ deployedVia(dep.channel) }}</span>
          <span v-else>Not deployed yet</span>
          <template v-if="data.git_source">
            <span class="inline-flex items-center gap-1.5">
              <span class="inline-flex items-center gap-1"><GitBranch class="size-3.5" /> {{ data.git_source.branch }}</span>
              <template v-if="dep?.commit_sha">
                <a v-if="commitUrl(data, dep.commit_sha)" :href="commitUrl(data, dep.commit_sha)" target="_blank" rel="noopener noreferrer" class="font-mono text-[13px] text-foreground hover:underline">{{ shortSha(dep.commit_sha) }}</a>
                <span v-else class="font-mono text-[13px] text-foreground">{{ shortSha(dep.commit_sha) }}</span>
              </template>
            </span>
          </template>
        </template>

          <template v-if="isWeb && primary !== 'open'">
            <Button v-if="dep" as-child variant="outline">
              <a :href="data.url" target="_blank" rel="noopener noreferrer">{{ multi ? 'Open website' : 'Open app' }} <ExternalLink /></a>
            </Button>
            <span v-else title="Available once the first deploy is live">
              <Button variant="outline" disabled>{{ multi ? 'Open website' : 'Open app' }} <ExternalLink /></Button>
            </span>
          </template>
          <Button v-if="!canStart" variant="outline" :disabled="busy || !dep" :title="multi ? 'Restart every service of this app' : undefined" @click="restart">
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
              <DropdownMenuItem v-if="project" @click="router.push(projectRoute(project.id, { app: slug }))"><PanelRight /> Show on the canvas</DropdownMenuItem>
              <DropdownMenuSeparator />
              <DropdownMenuItem @click="router.push({ name: 'app-settings', params: { slug } })"><Settings /> Settings</DropdownMenuItem>
              <DropdownMenuItem variant="destructive" @click="deleteOpen = true"><Trash2 /> Delete app</DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenu>
      </PageHeader>
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
      :description="multi ? 'Every service stops. Visitors see a page that says the app is stopped until you start it again.' : 'Visitors see a page that says the app is stopped until you start it again.'"
      :confirm-label="`Stop ${thing}`"
      :loading="actions.stop.isPending.value"
      @confirm="stop"
    />
    <ServiceSheet v-if="data && multi" :app="data" />
    <DeleteAppDialog v-if="data" v-model:open="deleteOpen" :app="data" />
    <CatalogUpdateDialog v-if="data?.catalog_update && updateOpen" v-model:open="updateOpen" :app="data" />
  </div>
</template>
