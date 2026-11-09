<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import { RouterLink, RouterView, useRoute, useRouter } from 'vue-router'
import { useEventListener, useResizeObserver } from '@vueuse/core'
import { ArrowUpCircle, ExternalLink, GitBranch, Globe, Link, Lock, MoreHorizontal, PanelRight, Play, RefreshCw, RotateCw, Settings, Square, Trash2 } from 'lucide-vue-next'
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
import { useSession } from '@/composables/useSession'
import { nowMs } from '@/composables/useNow'
import { ApiError } from '@/lib/api'
import { APP_TABS } from '@/lib/appTabs'
import { isServerDb, pickDatabase } from '@/lib/dbvalue'
import { commitUrl, deployedVia, hostOf, shortSha } from '@/lib/format'
import { copyText } from '@/lib/notify'
import { terminalOffered } from '@/lib/terminal'
import CatalogUpdateDialog from './CatalogUpdateDialog.vue'
import UploadVersionButton from './UploadVersionButton.vue'
import ServiceSheet from './ServiceSheet.vue'
import ServiceIcon from '@/components/ServiceIcon.vue'
import { useProject, useServiceTemplates } from '@/composables/queries'
import { projectRoute } from '@/lib/appLinks'
import { appRole, internalHostOf, projectIdOf, publicPortText, roleTitle, templateUpdate } from '@/lib/projects'

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
// Several services (a compose file): the overview is the service grid, actions act on all of them.
// Before the tab bar below, which reads isWeb and multi at once.
const { actions, isWeb, multi, dep, canRebuild, canStart, canStop, busy, shownStatus, primary, pending, restart, rebuild: startRebuild } = useAppControls(() => props.slug, data)
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
// A PostgreSQL, MariaDB or MySQL app always has it: the tab opens its server.
const root = useDataRoot(() => props.slug, () => !!data.value?.volume && !isServerDb(data.value))
// On a database page the tab stays while the app or its files load, so the highlighted tab does not vanish.
const onDatabase = computed(() => String(route.name ?? '').startsWith('app-database') && route.meta.tab !== 'app-storage')
const hasDatabase = computed(() => onDatabase.value || isServerDb(data.value) || !!pickDatabase(root.data.value?.items ?? [], props.slug))
const openPort = computed(() => (data.value ? publicPortText(data.value, nowMs.value) : null))
// Background workers have no visitors and no web address. A static site has
// no container to open a terminal in, and an admin may not open one in
// another user's app.
const { user } = useSession()
const hasTerminal = computed(() => terminalOffered(data.value, user.value?.id ?? ''))
const shownTabs = computed(() =>
  APP_TABS.filter((t) => (!t.webOnly || isWeb.value) && (!t.database || hasDatabase.value) && (!t.terminal || hasTerminal.value)).map((t) =>
    multi.value && t.name === 'app' ? { ...t, label: 'Services' } : t,
  ),
)
// A page below a tab (the database viewer) names the tab that stays highlighted.
const current = computed(() => String(route.meta.tab ?? route.name))
const notFound = computed(() => app.error.value instanceof ApiError && app.error.value.status === 404)

// The browser tab says which app this is.
const tabLabel = computed(() => (route.meta.title as string | undefined) ?? APP_TABS.find((t) => t.name === current.value && t.name !== 'app')?.label ?? '')
// route.fullPath too: the router's own title (set after every navigation, also
// within a tab, like the Data and SQL views of the Database tab) would drop the app name.
watch(
  () => [data.value?.name, tabLabel.value, project.value?.name, route.fullPath] as const,
  ([name, tab, pname]) => {
    if (name) document.title = `${tab ? `${tab} - ` : ''}${name}${pname && pname !== name ? ` - ${pname}` : ''} - bitploy`
  },
  { immediate: true, flush: 'post' },
)

// ---------------------------------------------------------------- the tab bar on a narrow screen
// It scrolls sideways: the current tab is scrolled into view, and a faded edge
// shows there are more tabs that way.
const tabBar = ref<HTMLElement | null>(null)
const fade = ref({ left: false, right: false })
function updateFade() {
  const el = tabBar.value
  if (!el) return
  fade.value = { left: el.scrollLeft > 2, right: el.scrollLeft + el.clientWidth < el.scrollWidth - 2 }
}
function showCurrentTab() {
  const el = tabBar.value
  const active = el?.querySelector<HTMLElement>('[data-state="active"], [aria-current="page"]')
  if (el && active) {
    const left = active.offsetLeft - el.offsetLeft
    if (left < el.scrollLeft || left + active.offsetWidth > el.scrollLeft + el.clientWidth) {
      el.scrollTo({ left: Math.max(0, left - (el.clientWidth - active.offsetWidth) / 2) })
    }
  }
  updateFade()
}
watch([current, () => shownTabs.value.length, tabBar], () => void nextTick(showCurrentTab), { immediate: true })
useResizeObserver(tabBar, updateFade)
useEventListener(tabBar, 'scroll', updateFade, { passive: true })

// ---------------------------------------------------------------- actions (in the header, so every tab has them)
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
          <span v-else-if="openPort" class="inline-flex items-center gap-1 font-medium text-primary" :title="openPort.title" data-testid="header-public-port">
            <Globe class="size-3.5" aria-hidden="true" /> {{ openPort.text }}
          </span>
          <span v-else-if="internalHostOf(data)" class="inline-flex items-center gap-1" :title="`The other apps of the project reach it at ${internalHostOf(data)}. The internet cannot.`">
            <Lock class="size-3.5" aria-hidden="true" /> Private, at <span class="font-mono text-[13px] text-foreground">{{ internalHostOf(data) }}</span>
          </span>
          <span v-else>Runs in the background, no web address</span>
          <span v-if="dep">Deployed <RelativeTime :date="dep.finished_at ?? dep.queued_at" /> {{ deployedVia(dep.channel) }}</span>
          <span v-else-if="data.status !== 'deploying'">Not deployed yet</span>
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
            <!-- On a phone it is the icon alone, so the header's buttons fit on one row. -->
            <Button v-if="dep" as-child variant="outline" class="max-sm:px-2.5">
              <a :href="data.url" target="_blank" rel="noopener noreferrer" aria-label="Open website" title="Open website"><span class="max-sm:sr-only">Open website</span> <ExternalLink /></a>
            </Button>
            <span v-else title="Available once the first deploy is live">
              <Button variant="outline" disabled class="max-sm:px-2.5" aria-label="Open website"><span class="max-sm:sr-only">Open website</span> <ExternalLink /></Button>
            </span>
          </template>
          <Button v-if="!canStart" variant="outline" :disabled="busy || !dep" :title="multi ? 'Restart every service of this app' : undefined" @click="restart">
            <RotateCw :class="actions.restart.isPending.value && 'animate-spin'" /> {{ actions.restart.isPending.value ? 'Restarting...' : 'Restart' }}
            <span v-if="pending.reasons.value.length && !pending.needsBuild.value" class="size-1.5 rounded-full bg-warning" aria-label="(changes waiting)" />
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
            <a :href="data.url" target="_blank" rel="noopener noreferrer">Open website <ExternalLink /></a>
          </Button>

          <DropdownMenu>
            <DropdownMenuTrigger as-child>
              <Button variant="outline" size="icon" aria-label="More actions"><MoreHorizontal /></Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end" class="w-60">
              <DropdownMenuItem v-if="primary !== 'git'" :disabled="busy || !canRebuild" @click="rebuild">
                <RefreshCw /> Deploy again
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
          <span class="font-medium">{{ pending.needsBuild.value ? 'Deploy again to apply your changes.' : 'Restart to apply your changes.' }}</span>
          <span class="text-muted-foreground">
            Your changes to {{ pending.text.value }} are saved, but the running {{ thing }} does not use them yet.
          </span>
        </p>
        <Button v-if="pending.needsBuild.value" size="sm" :disabled="busy || !canRebuild" @click="rebuild">
          <RefreshCw :class="actions.redeploy.isPending.value && 'animate-spin'" /> Deploy again
        </Button>
        <Button v-else size="sm" :disabled="busy" @click="restart">
          <RotateCw :class="actions.restart.isPending.value && 'animate-spin'" /> {{ actions.restart.isPending.value ? 'Restarting...' : 'Restart now' }}
        </Button>
      </div>

      <Tabs :model-value="current" class="min-w-0">
        <div
          ref="tabBar"
          :class="[
            'scrollbar-none overflow-x-auto overflow-y-hidden border-b',
            fade.left && fade.right ? 'tabs-fade-both' : fade.right ? 'tabs-fade-right' : fade.left ? 'tabs-fade-left' : '',
          ]"
          data-testid="app-tabs"
        >
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

<style scoped>
.scrollbar-none {
  scrollbar-width: none;
}
.scrollbar-none::-webkit-scrollbar {
  display: none;
}
/* More tabs past the edge: the edge fades out. */
.tabs-fade-right {
  mask-image: linear-gradient(to right, #000 calc(100% - 2.5rem), transparent);
}
.tabs-fade-left {
  mask-image: linear-gradient(to left, #000 calc(100% - 2.5rem), transparent);
}
.tabs-fade-both {
  mask-image: linear-gradient(to right, transparent, #000 2.5rem, #000 calc(100% - 2.5rem), transparent);
}
</style>
