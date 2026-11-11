<script setup lang="ts">
import { computed, ref } from 'vue'
import { RouterLink } from 'vue-router'
import { useMutation, useQuery, useQueryClient } from '@tanstack/vue-query'
import { ChevronRight, History, Loader2, ScrollText, Undo2 } from 'lucide-vue-next'
import ConfirmDialog from '@/components/ConfirmDialog.vue'
import LogLines from '@/components/LogLines.vue'
import CopyForAiButton from '@/components/CopyForAiButton.vue'
import { plainHint } from '@/lib/errors'
import EmptyState from '@/components/EmptyState.vue'
import QueryError from '@/components/QueryError.vue'
import RelativeTime from '@/components/RelativeTime.vue'
import StatusBadge from '@/components/StatusBadge.vue'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { keys, livePoll, REFRESH_MS, useApp } from '@/composables/queries'
import { api } from '@/lib/api'
import { commitUrl, deployedVia, deploymentStateInfo, describeSource, formatDuration, shortSha, versionLabel } from '@/lib/format'
import { toastError, toastOk } from '@/lib/notify'
import type { Deployment, DeploymentService } from '@/lib/types'
import ServiceIcon from '@/components/ServiceIcon.vue'
import { isMulti, serviceRole } from '@/lib/services'
import CancelBuildButton from './CancelBuildButton.vue'
import DeployFailure from './DeployFailure.vue'
import UploadVersionButton from './UploadVersionButton.vue'

const props = defineProps<{ slug: string }>()
const qc = useQueryClient()
const app = useApp(() => props.slug)
const list = useQuery({
  queryKey: computed(() => keys.deployments(props.slug)),
  queryFn: () => api.deployments(props.slug),
  refetchInterval: livePoll(REFRESH_MS),
})

const target = ref<Deployment | null>(null)
const confirmOpen = computed({
  get: () => target.value !== null,
  set: (v) => {
    if (!v) target.value = null
  },
})

const rollback = useMutation({
  mutationFn: (d: Deployment) => api.rollback(props.slug, d.id),
  onSuccess: (_r, d) => {
    toastOk(
      `Going back to ${versionLabel(d.seq).toLowerCase()}`,
      app.data.value?.volume ? 'The app is offline for a few seconds while it switches.' : undefined,
    )
    target.value = null
    void qc.invalidateQueries({ queryKey: keys.deployments(props.slug) })
    void qc.invalidateQueries({ queryKey: keys.app(props.slug) })
    void qc.invalidateQueries({ queryKey: keys.apps })
  },
  onError: (e) => toastError(e),
})

// What going back does, and that a push to the linked branch builds the newest code again.
const goBackText = computed(() => {
  const gs = app.data.value?.git_source
  let text = `Your app runs ${versionLabel(target.value?.seq).toLowerCase()} again.`
  // Migrations are not undone: the older version must work with the database as it is now.
  if (app.data.value?.before_deploy_command || app.data.value?.after_deploy_command)
    text += ' Your deploy commands do not run when you go back, so changes they made, like database migrations, stay.'
  if (gs?.auto_deploy) text += ` The next push to ${gs.branch} on GitHub builds and deploys your newest code again.`
  return text
})

// Every new version shows up here; how you send one depends on where the code is.
const emptyText = computed(() => {
  const a = app.data.value
  if (a?.git_source) return `Push to ${a.git_source.branch} to build the first version.`
  if (a?.catalog_id) return 'The first version is being prepared.'
  return 'Upload your files, or ask your AI assistant to deploy to this app.'
})

function canRollback(d: Deployment): boolean {
  const built = !!d.image || !!d.services?.some((s) => !!s.image)
  return d.state === 'superseded' && built && !d.image_removed && d.id !== app.data.value?.current_deployment?.id
}

// ---------------------------------------------------------------- the services of one deployment (projects with several)
const multi = computed(() => isMulti(app.data.value))
function roleOf(name: string) {
  const s = app.data.value?.services.find((x) => x.name === name)
  return serviceRole(s ?? { name, kind: 'build', public: false, port: null })
}
/** The failing service first, then the project's order. */
function rowsOf(d: Deployment): DeploymentService[] {
  return [...(d.services ?? [])].sort((a, b) => Number(!!b.error) - Number(!!a.error))
}
function summary(d: Deployment): string {
  const list = d.services ?? []
  const failed = list.filter((s) => s.error || s.state === 'failed').length
  const reused = list.filter((s) => s.reused).length
  const parts = [`${list.length} services`]
  if (failed) parts.push(`${failed} failed`)
  if (reused) parts.push(`${reused} unchanged`)
  return parts.join(', ')
}
// The version the app runs says Live (or Current while the app is stopped);
// a finished one the app no longer runs says Replaced.
function stateOf(d: Deployment) {
  const current = app.data.value?.current_deployment?.id
  if (d.state === 'running' && current && d.id !== current) return deploymentStateInfo('superseded')
  return deploymentStateInfo(d.state, app.data.value?.status)
}
// A failed deploy newer than the version the app runs: that version is still live.
function liveKept(d: Deployment) {
  const a = app.data.value
  return d.state === 'failed' && a?.status === 'running' && !!a.current_deployment && d.seq > a.current_deployment.seq
}
function serviceState(d: Deployment, s: DeploymentService) {
  if (s.reused) return { label: 'Unchanged', tone: 'muted' as const }
  return deploymentStateInfo(s.state ?? (s.error ? 'failed' : d.state))
}
</script>

<template>
  <div class="grid gap-3">
    <QueryError v-if="list.isError.value" :error="list.error.value" @retry="list.refetch()" />
    <div v-else-if="list.isPending.value" class="grid gap-2">
      <Skeleton v-for="i in 3" :key="i" class="h-20 w-full" />
    </div>
    <EmptyState
      v-else-if="!list.data.value?.length"
      :icon="History"
      title="No deployments yet."
      :description="emptyText"
    >
      <UploadVersionButton v-if="app.data.value && !app.data.value.git_source && !app.data.value.catalog_id" :slug="slug" variant="default" label="Upload files" />
      <Button as-child variant="outline"><RouterLink to="/connect">Connect your AI assistant</RouterLink></Button>
    </EmptyState>
    <template v-else>
      <article v-for="d in list.data.value" :key="d.id" class="grid gap-2 rounded-xl border bg-card p-4 shadow-card">
        <div class="flex flex-wrap items-center gap-x-3 gap-y-2">
          <span class="font-semibold">{{ versionLabel(d.seq) }}</span>
          <StatusBadge v-bind="stateOf(d)" />
          <span class="text-sm text-muted-foreground"><RelativeTime :date="d.queued_at" /> {{ deployedVia(d.channel) }}</span>
          <span v-if="d.duration_seconds !== null" class="text-sm text-muted-foreground">Took {{ formatDuration(d.duration_seconds) }}</span>
          <div class="ml-auto flex flex-wrap gap-2">
            <Button as-child size="sm" variant="ghost">
              <RouterLink :to="{ name: 'app-logs', params: { slug }, query: { kind: 'build', deployment: d.id } }"><ScrollText /> Build log</RouterLink>
            </Button>
            <CancelBuildButton :slug="slug" :deployment="d" />
            <Button v-if="canRollback(d)" size="sm" variant="outline" @click="target = d">
              <Undo2 /> Go back to this version
            </Button>
            <span v-else-if="d.state === 'superseded' && d.image_removed" class="inline-flex h-8 items-center text-xs text-muted-foreground">
              Its image was deleted, so it cannot be restored
            </span>
          </div>
        </div>
        <div class="flex flex-wrap gap-x-4 gap-y-1 text-sm text-muted-foreground">
          <span>{{ describeSource(d, app.data.value, list.data.value) }}</span>
          <span v-if="d.commit_sha">
            <a v-if="commitUrl(app.data.value, d.commit_sha)" :href="commitUrl(app.data.value, d.commit_sha)" target="_blank" rel="noopener noreferrer" class="font-mono text-foreground hover:underline">{{ shortSha(d.commit_sha) }}</a>
            <span v-else class="font-mono">{{ shortSha(d.commit_sha) }}</span>
            <span v-if="d.commit_message" class="ml-1.5">{{ d.commit_message }}</span>
          </span>
        </div>
        <DeployFailure v-if="d.error && d.state !== 'cancelled'" :error="d.error" :log-tail="d.log_tail" :multi="multi" :slug="slug" :live-kept="liveKept(d)">
          <template #actions>
            <CopyForAiButton v-if="d.state === 'failed' && app.data.value" :app="app.data.value" :deployment="d" />
          </template>
          <details v-if="d.log_tail?.length" class="mt-3">
            <summary class="cursor-pointer text-xs text-muted-foreground hover:text-foreground">Show the last log lines</summary>
            <div class="mt-2 max-h-60 overflow-auto rounded bg-background/60 p-2 font-mono text-xs leading-relaxed text-foreground"><LogLines :lines="d.log_tail" /></div>
          </details>
        </DeployFailure>
        <details v-if="multi && d.services?.length" class="group rounded-lg border" :open="d.services.some((s) => !!s.error) || undefined">
          <summary class="flex cursor-pointer list-none items-center gap-2 px-3 py-2 text-sm text-muted-foreground select-none hover:text-foreground [&::-webkit-details-marker]:hidden">
            <ChevronRight class="size-4 transition-transform group-open:rotate-90" aria-hidden="true" />
            {{ summary(d) }}
          </summary>
          <ul class="divide-y border-t">
            <li v-for="s in rowsOf(d)" :key="s.service" class="grid gap-1 px-3 py-2">
              <div class="flex flex-wrap items-center gap-x-3 gap-y-1">
                <ServiceIcon :role="roleOf(s.service).key" size="sm" />
                <span class="text-sm font-medium">{{ roleOf(s.service).label }}</span>
                <span class="font-mono text-xs text-muted-foreground">{{ s.service }}</span>
                <StatusBadge v-bind="serviceState(d, s)" />
                <span v-if="s.image" class="ml-auto max-w-[50%] truncate font-mono text-xs text-muted-foreground" :title="s.image">{{ s.image }}</span>
              </div>
              <p v-if="s.reused" class="pl-10 text-xs text-muted-foreground">Nothing changed, so the running copy was kept.</p>
              <p v-if="s.error" class="pl-10 text-sm text-destructive">
                {{ s.error.message }} <span v-if="plainHint(s.error.code, s.error.hint)">{{ plainHint(s.error.code, s.error.hint) }}</span>
              </p>
            </li>
          </ul>
        </details>
        <ul v-if="d.warnings?.length" class="grid gap-1 text-sm">
          <li
            v-for="(w, i) in d.warnings"
            :key="i"
            :class="w.severity === 'info' ? 'text-muted-foreground' : 'text-warning-text'"
          >
            {{ w.message }}<template v-if="plainHint(w.code, w.hint)">&nbsp;{{ plainHint(w.code, w.hint) }}</template>
            <RouterLink
              v-if="w.code === 'after_deploy_failed'"
              :to="{ name: 'app-settings', params: { slug }, hash: '#deploy-commands' }"
              class="ml-1 font-medium underline underline-offset-2"
            >
              Change the deploy commands
            </RouterLink>
          </li>
        </ul>
        <p v-if="d.after_deploy === 'pending' || d.after_deploy === 'running'" class="flex items-center gap-2 text-sm text-muted-foreground">
          <Loader2 class="size-3.5 animate-spin" aria-hidden="true" />
          This version is live. Running its after-deploy command now (up to 5 minutes).
        </p>
      </article>
    </template>

    <ConfirmDialog
      v-model:open="confirmOpen"
      title="Go back to this version?"
      :description="goBackText"
      confirm-label="Go back"
      :loading="rollback.isPending.value"
      @confirm="target && rollback.mutate(target)"
    />
  </div>
</template>
