<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'
import { useMutation, useQuery, useQueryClient } from '@tanstack/vue-query'
import { useEventListener } from '@vueuse/core'
import { Archive, ArrowUpCircle, ArrowUpRight, Cable, ExternalLink, Lock, Maximize2, Play, RefreshCw, RotateCw, Trash2, X } from 'lucide-vue-next'
import AppStatusBadge from '@/components/AppStatusBadge.vue'
import CopyButton from '@/components/CopyButton.vue'
import DeleteAppDialog from '@/components/DeleteAppDialog.vue'
import RelativeTime from '@/components/RelativeTime.vue'
import ServiceIcon from '@/components/ServiceIcon.vue'
import StatusBadge from '@/components/StatusBadge.vue'
import ConnectionCard from '@/components/project/ConnectionCard.vue'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { keys, liveMs, useApp } from '@/composables/queries'
import { liveBuildLogs } from '@/composables/useEvents'
import { useAppControls } from '@/composables/useAppControls'
import { isTyping } from '@/composables/useShortcuts'
import { useProjectData } from '@/composables/useProjectData'
import { api, fetchLogEntries } from '@/lib/api'
import { appRoute, projectRoute } from '@/lib/appLinks'
import { plainHint } from '@/lib/errors'
import { toastError } from '@/lib/notify'
import { deploymentStateInfo, deployedVia, hostOf, shortSha } from '@/lib/format'
import { aliasOf, envReferences, internalHostOf, roleTitle, talksTo, templateUpdate, usedBy } from '@/lib/projects'
import CatalogUpdateDialog from '@/pages/app/CatalogUpdateDialog.vue'
import PublicPort from '@/pages/app/PublicPort.vue'
import UploadVersionButton from '@/pages/app/UploadVersionButton.vue'

// One app of the canvas, in a panel over it (?app=<slug>): whether it runs and
// where, the buttons of its page, the latest deployment and the last lines of
// its log, its variables and who it talks to. The full page has the rest.
const props = defineProps<{ slug: string; projectId: string }>()
const emit = defineEmits<{ close: [] }>()

const app = useApp(() => props.slug)
const data = computed(() => app.data.value ?? null)
const { apps, links, env, roleOf, fallback } = useProjectData(() => props.projectId)
const role = computed(() => (data.value ? roleOf(data.value) : null))
const { actions, isWeb, multi, dep, canRebuild, canStart, busy, shownStatus, primary, pending, restart, rebuild } = useAppControls(() => props.slug, data)
const talks = computed(() => (data.value ? talksTo(data.value, apps.value, links.value) : []))
const users = computed(() => (data.value ? usedBy(data.value, apps.value, links.value) : []))
const vars = computed(() => env.value[props.slug])
const panelOf = (slug: string) => projectRoute(props.projectId, { app: slug })

// ---------------------------------------------------------------- the latest deployment
const deployments = useQuery({
  queryKey: computed(() => keys.deployments(props.slug)),
  queryFn: () => api.deployments(props.slug),
  refetchInterval: (q) => {
    const s = q.state.data?.[0]?.state
    return liveMs(s && !['running', 'failed', 'superseded', 'cancelled'].includes(s) ? 1500 : 10_000)
  },
})
const latest = computed(() => deployments.data.value?.[0] ?? null)
const building = computed(() => !!latest.value && !['running', 'failed', 'superseded', 'cancelled'].includes(latest.value.state))
const buildLog = computed(() => {
  const live = latest.value && building.value ? liveBuildLogs.value[latest.value.id] : undefined
  return (live?.length ? live : (latest.value?.log_tail ?? [])).slice(-8)
})

// ---------------------------------------------------------------- the last lines of the running app's log
const runtime = useQuery({
  queryKey: computed(() => ['panel-log', props.slug] as const),
  queryFn: ({ signal }) => fetchLogEntries({ slug: props.slug, kind: 'runtime' }, signal),
  enabled: computed(() => !!dep.value && !building.value),
  refetchInterval: () => liveMs(4000),
  retry: false,
})
const tail = computed(() => (runtime.data.value ?? []).slice(-20).map((e) => e.text))
const logEl = ref<HTMLElement | null>(null)
watch(tail, async () => {
  await nextTick()
  if (logEl.value) logEl.value.scrollTop = logEl.value.scrollHeight
})

// ---------------------------------------------------------------- backups of a database service
// Only a database service (PostgreSQL, MySQL, MariaDB: dump_tool is set) shows
// its last backup here; the Storage tab has the rest.
const qc = useQueryClient()
const backups = useQuery({
  queryKey: computed(() => keys.backups(props.slug)),
  queryFn: () => api.backups(props.slug),
  enabled: computed(() => !!data.value?.template && !!data.value?.volume && !!data.value?.backups_supported),
  refetchInterval: (q) => liveMs(q.state.data?.busy ? 2_000 : 30_000),
})
const dumpTool = computed(() => (data.value?.template ? backups.data.value?.dump_tool : undefined))
const lastBackup = computed(() => backups.data.value?.items.find((b) => b.status === 'ready' && b.kind !== 'before_restore') ?? null)
const backUp = useMutation({
  mutationFn: () => api.createBackup(props.slug),
  onSuccess: () => void qc.invalidateQueries({ queryKey: keys.backups(props.slug) }),
  onError: (e) => toastError(e),
})
const backingUp = computed(() => backUp.isPending.value || !!backups.data.value?.busy)

const TABS = computed(() =>
  [
    { name: 'app-deployments', label: 'Deployments' },
    { name: 'app-logs', label: 'Logs' },
    { name: 'app-variables', label: 'Variables' },
    { name: 'app-domains', label: 'Domains', web: true },
    { name: 'app-storage', label: 'Storage' },
    { name: 'app-settings', label: 'Settings' },
  ].filter((t) => !t.web || isWeb.value),
)

const updateOpen = ref(false)
const deleteOpen = ref(false)

// Esc closes the panel, unless a dialog or a menu above it takes the key.
useEventListener(document, 'keydown', (e: KeyboardEvent) => {
  if (e.key !== 'Escape' || e.defaultPrevented || isTyping(e)) return
  if (document.querySelector('[role="dialog"], [role="alertdialog"], [role="menu"], [role="listbox"]')) return
  emit('close')
})
const root = ref<HTMLElement | null>(null)
watch(
  () => props.slug,
  () => void nextTick(() => root.value?.focus({ preventScroll: true })),
  { immediate: true },
)
</script>

<template>
  <aside
    ref="root"
    tabindex="-1"
    :aria-label="data ? `${data.name}, on the canvas` : 'App'"
    data-testid="app-panel"
    class="fixed inset-0 z-40 flex flex-col overflow-hidden bg-background outline-none md:inset-y-2 md:right-2 md:left-auto md:w-[min(34rem,calc(100vw-1rem))] md:rounded-xl md:border md:shadow-2xl"
  >
    <!-- Header -->
    <div class="flex shrink-0 items-start gap-3 border-b px-4 py-3">
      <ServiceIcon v-if="role" :role="role.key" />
      <div class="min-w-0 flex-1">
        <div class="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1">
          <h2 class="truncate text-base font-semibold">{{ data?.name ?? slug }}</h2>
          <span v-if="data && aliasOf(data) !== data.name" class="truncate font-mono text-xs text-muted-foreground">{{ aliasOf(data) }}</span>
          <AppStatusBadge v-if="data" :status="shownStatus" />
        </div>
        <p v-if="role" class="truncate text-xs text-muted-foreground">{{ roleTitle(role) }}</p>
      </div>
      <Button v-if="data" variant="outline" size="sm" as-child>
        <RouterLink :to="appRoute(data)" aria-label="Open full page"><Maximize2 /> <span class="hidden sm:inline">Open full page</span></RouterLink>
      </Button>
      <Button variant="ghost" size="icon" class="size-8" aria-label="Close" title="Close (Esc)" @click="emit('close')"><X /></Button>
    </div>

    <div v-if="app.isError.value && !data" class="p-4 text-sm text-muted-foreground">
      This app is not in the project any more.
      <button type="button" class="font-medium text-foreground underline-offset-4 hover:underline" @click="emit('close')">Back to the canvas</button>
    </div>
    <div v-else-if="!data" class="grid gap-3 p-4">
      <Skeleton class="h-5 w-2/3" />
      <Skeleton class="h-9 w-full" />
      <Skeleton class="h-40 w-full" />
    </div>

    <div v-else class="min-h-0 flex-1 overflow-y-auto">
      <div class="grid gap-5 p-4">
        <!-- Where it is reached, and the buttons of its page. -->
        <section class="grid gap-3" aria-label="Address and actions">
          <div class="flex min-w-0 items-center gap-1 text-sm">
            <template v-if="isWeb">
              <a :href="data.url" target="_blank" rel="noopener noreferrer" class="truncate font-mono text-[13px] hover:underline">{{ hostOf(data.url) }}</a>
              <CopyButton :value="data.url" what="Link copied" label="Copy link" icon class="size-7 text-muted-foreground" />
              <span v-if="data.protected" class="inline-flex items-center gap-1 text-xs text-muted-foreground"><Lock class="size-3.5" /> Private</span>
            </template>
            <span v-else-if="internalHostOf(data)" class="inline-flex min-w-0 items-center gap-1.5 text-muted-foreground">
              <Lock class="size-3.5 shrink-0" /> Private, at <span class="truncate font-mono text-[13px] text-foreground">{{ internalHostOf(data) }}</span>
            </span>
            <span v-else class="text-muted-foreground">Runs in the background, no web address</span>
          </div>
          <div class="flex flex-wrap items-center gap-2">
            <Button v-if="primary === 'git'" size="sm" :disabled="busy || !canRebuild" @click="rebuild()">
              <RefreshCw :class="actions.redeploy.isPending.value && 'animate-spin'" /> Deploy latest
            </Button>
            <UploadVersionButton v-else-if="primary === 'upload'" :slug="slug" variant="default" :label="dep ? 'Upload new version' : 'Upload files'" />
            <Button v-else-if="primary === 'update'" size="sm" @click="updateOpen = true"><ArrowUpCircle /> Update to {{ data.catalog_update }}</Button>
            <Button v-if="isWeb && dep" size="sm" :variant="primary === 'open' ? 'default' : 'outline'" as-child>
              <a :href="data.url" target="_blank" rel="noopener noreferrer">{{ multi ? 'Open website' : 'Open app' }} <ExternalLink /></a>
            </Button>
            <Button size="sm" variant="outline" :disabled="busy || !dep" @click="restart">
              <RotateCw :class="actions.restart.isPending.value && 'animate-spin'" /> {{ actions.restart.isPending.value ? 'Restarting...' : multi ? 'Restart all' : 'Restart' }}
            </Button>
            <Button v-if="canStart" size="sm" variant="outline" :disabled="busy" @click="actions.start.mutate()">
              <Play /> {{ actions.start.isPending.value ? 'Starting...' : 'Start' }}
            </Button>
          </div>
          <p v-if="templateUpdate(data)" class="text-xs text-muted-foreground" data-testid="template-update">{{ templateUpdate(data)!.title }}</p>
          <div v-if="pending.reasons.value.length" role="status" class="flex items-center gap-2 rounded-lg border border-warning/40 bg-warning/10 px-3 py-2 text-xs">
            <RotateCw class="size-3.5 shrink-0" aria-hidden="true" />
            <span class="min-w-0 flex-1">Your changes to {{ pending.text.value }} apply after a restart.</span>
            <button type="button" class="shrink-0 font-medium underline-offset-4 hover:underline" :disabled="busy" @click="restart">Restart now</button>
          </div>
        </section>

        <!-- The latest deployment. -->
        <section class="grid gap-2" aria-labelledby="panel-deploy">
          <div class="flex items-center justify-between gap-2">
            <h3 id="panel-deploy" class="text-xs font-medium text-muted-foreground">Latest deployment</h3>
            <RouterLink :to="appRoute(data, 'app-deployments')" class="text-xs font-medium hover:underline">All deployments</RouterLink>
          </div>
          <Skeleton v-if="deployments.isPending.value" class="h-16 w-full" />
          <p v-else-if="!latest" class="rounded-lg border border-dashed px-3 py-3 text-sm text-muted-foreground">Not deployed yet.</p>
          <div v-else class="grid gap-2 rounded-lg border bg-card p-3">
            <div class="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1 text-sm">
              <StatusBadge v-bind="deploymentStateInfo(latest.state)" :pulse="building" />
              <span class="text-muted-foreground">Version {{ latest.seq }}</span>
              <span v-if="latest.commit_sha" class="font-mono text-xs">{{ shortSha(latest.commit_sha) }}</span>
              <span class="ml-auto text-xs text-muted-foreground"><RelativeTime :date="latest.finished_at ?? latest.started_at ?? latest.queued_at" /> {{ deployedVia(latest.channel) }}</span>
            </div>
            <p v-if="latest.commit_message" class="truncate text-xs text-muted-foreground">{{ latest.commit_message }}</p>
            <div v-if="latest.error && latest.state === 'failed'" class="rounded-md bg-destructive/10 p-2 text-xs text-destructive" role="alert">
              <p class="font-medium">{{ latest.error.message }}</p>
              <p v-if="plainHint(latest.error.code, latest.error.hint)" class="mt-0.5">{{ plainHint(latest.error.code, latest.error.hint) }}</p>
            </div>
            <pre v-if="(building || latest.state === 'failed') && buildLog.length" class="max-h-40 overflow-auto rounded-md border bg-background/60 p-2 font-mono text-[11px] whitespace-pre-wrap">{{ buildLog.join('\n') }}</pre>
          </div>
        </section>

        <!-- The running app's log. -->
        <section v-if="dep && !building" class="grid gap-2" aria-labelledby="panel-log">
          <div class="flex items-center justify-between gap-2">
            <h3 id="panel-log" class="inline-flex items-center gap-1.5 text-xs font-medium text-muted-foreground">
              <span v-if="data.status === 'running'" class="size-1.5 animate-pulse rounded-full bg-success" aria-hidden="true" /> Log
            </h3>
            <RouterLink :to="appRoute(data, 'app-logs')" class="text-xs font-medium hover:underline">Open logs</RouterLink>
          </div>
          <pre
            ref="logEl"
            class="h-44 overflow-auto rounded-lg border bg-card p-2.5 font-mono text-[11px] leading-relaxed whitespace-pre-wrap text-foreground/85"
            data-testid="panel-log"
          >{{ tail.length ? tail.join('\n') : runtime.isPending.value ? 'Loading...' : 'No lines yet.' }}</pre>
        </section>

        <!-- Who it talks to. -->
        <!-- A database or cache: the card below says who uses it, and connects more. -->
        <section v-if="(talks.length || users.length) && !(data.template && !fallback)" class="grid gap-2 text-sm" aria-labelledby="panel-links">
          <h3 id="panel-links" class="text-xs font-medium text-muted-foreground">Connections</h3>
          <p v-if="talks.length" class="flex flex-wrap items-center gap-1.5">
            <Cable class="size-3.5 text-muted-foreground" /> <span class="text-muted-foreground">Talks to</span>
            <RouterLink v-for="t in talks" :key="t.id" :to="panelOf(t.slug)" class="rounded-md border bg-muted/50 px-1.5 py-0.5 font-mono text-xs hover:border-foreground/30">{{ aliasOf(t) }}</RouterLink>
          </p>
          <p v-if="users.length" class="flex flex-wrap items-center gap-1.5">
            <Cable class="size-3.5 text-muted-foreground" /> <span class="text-muted-foreground">Used by</span>
            <RouterLink v-for="t in users" :key="t.id" :to="panelOf(t.slug)" class="rounded-md border bg-muted/50 px-1.5 py-0.5 font-mono text-xs hover:border-foreground/30">{{ aliasOf(t) }}</RouterLink>
          </p>
        </section>
        <ConnectionCard v-if="data.template && !fallback" :app="data" />
        <PublicPort :app="data" compact />

        <!-- The last backup of a database service. -->
        <section v-if="dumpTool" class="grid gap-2" aria-labelledby="panel-backups">
          <div class="flex items-center justify-between gap-2">
            <h3 id="panel-backups" class="text-xs font-medium text-muted-foreground">Backups</h3>
            <RouterLink :to="appRoute(data, 'app-storage')" class="text-xs font-medium hover:underline">All backups</RouterLink>
          </div>
          <p class="flex flex-wrap items-center gap-x-1.5 gap-y-1 text-sm" data-testid="panel-backup">
            <Archive class="size-3.5 text-muted-foreground" aria-hidden="true" />
            <span v-if="lastBackup" class="text-muted-foreground">Last backup <RelativeTime :date="lastBackup.created_at" /></span>
            <span v-else class="text-muted-foreground">No backups yet</span>
            <span class="text-muted-foreground" aria-hidden="true">·</span>
            <button type="button" class="font-medium underline-offset-4 hover:underline disabled:opacity-50" :disabled="backingUp" :title="data.status === 'running' ? `A dump made with ${dumpTool}` : 'A copy of the database files, as it is stopped'" @click="backUp.mutate()">
              {{ backingUp ? 'Backing up...' : 'Back up now' }}
            </button>
          </p>
        </section>

        <!-- Its variables. -->
        <section class="grid gap-2" aria-labelledby="panel-vars">
          <div class="flex items-center justify-between gap-2">
            <h3 id="panel-vars" class="text-xs font-medium text-muted-foreground">Variables</h3>
            <RouterLink :to="appRoute(data, 'app-variables')" class="text-xs font-medium hover:underline">Edit variables</RouterLink>
          </div>
          <Skeleton v-if="!vars" class="h-16 w-full" />
          <p v-else-if="!vars.length" class="rounded-lg border border-dashed px-3 py-3 text-sm text-muted-foreground">No variables.</p>
          <ul v-else class="divide-y rounded-lg border text-xs">
            <li v-for="v in vars.slice(0, 10)" :key="`${v.service}:${v.name}`" class="flex min-w-0 items-center gap-2 px-3 py-1.5">
              <span class="truncate font-mono font-medium">{{ v.name }}</span>
              <span class="ml-auto flex min-w-0 shrink items-center justify-end gap-1">
                <template v-if="envReferences(v).length">
                  <span v-for="r in envReferences(v)" :key="r.raw" class="inline-flex items-center gap-1 truncate rounded border border-primary/25 bg-primary/10 px-1 font-mono text-[11px]">
                    <Cable class="size-3 shrink-0 text-primary" /> {{ r.app }}.{{ r.variable }}
                  </span>
                </template>
                <span v-else class="truncate font-mono text-muted-foreground">{{ v.masked }}</span>
              </span>
            </li>
            <li v-if="vars.length > 10" class="px-3 py-1.5 text-muted-foreground">and {{ vars.length - 10 }} more</li>
          </ul>
        </section>

        <!-- The rest of its page. -->
        <nav class="grid gap-2" aria-label="Pages of this app">
          <h3 class="text-xs font-medium text-muted-foreground">More</h3>
          <div class="grid grid-cols-2 gap-1.5 sm:grid-cols-3">
            <RouterLink
              v-for="t in TABS"
              :key="t.name"
              :to="appRoute(data, t.name)"
              class="inline-flex items-center justify-between gap-1 rounded-md border bg-card px-2.5 py-1.5 text-xs transition-colors hover:border-foreground/25"
            >{{ t.label }} <ArrowUpRight class="size-3 text-muted-foreground" /></RouterLink>
          </div>
        </nav>

        <div class="border-t pt-4">
          <Button variant="ghost" size="sm" class="text-destructive hover:bg-destructive/10 hover:text-destructive" @click="deleteOpen = true"><Trash2 /> Delete app</Button>
        </div>
      </div>
    </div>

    <DeleteAppDialog v-if="data" v-model:open="deleteOpen" :app="data" />
    <CatalogUpdateDialog v-if="data?.catalog_update && updateOpen" v-model:open="updateOpen" :app="data" />
  </aside>
</template>
