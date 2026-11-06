<script setup lang="ts">
import { computed, ref } from 'vue'
import { RouterLink } from 'vue-router'
import { useMutation, useQuery, useQueryClient } from '@tanstack/vue-query'
import { ChevronRight, History, ScrollText, Undo2 } from 'lucide-vue-next'
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
import { aboutService, isMulti, serviceRole } from '@/lib/services'
import CancelBuildButton from './CancelBuildButton.vue'
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
      app.data.value?.volume ? 'The app is offline for a few seconds while it switches.' : 'Your app keeps running while the old version starts.',
    )
    target.value = null
    void qc.invalidateQueries({ queryKey: keys.deployments(props.slug) })
    void qc.invalidateQueries({ queryKey: keys.app(props.slug) })
    void qc.invalidateQueries({ queryKey: keys.apps })
  },
  onError: (e) => toastError(e),
})

// Every new version shows up here; how you send one depends on where the code is.
const emptyText = computed(() => {
  const a = app.data.value
  const end = 'Every version shows up here, and you can go back to an earlier one.'
  if (a?.git_source) return `Push to ${a.git_source.branch} to build the first version. ${end}`
  if (a?.catalog_id) return `The first version is being prepared. ${end}`
  return `Upload your files, or ask your AI assistant to deploy to this app. ${end}`
})

function canRollback(d: Deployment): boolean {
  const built = !!d.image || !!d.services?.some((s) => !!s.image)
  return d.state === 'superseded' && built && d.id !== app.data.value?.current_deployment?.id
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
          </div>
        </div>
        <div class="flex flex-wrap gap-x-4 gap-y-1 text-sm text-muted-foreground">
          <span>{{ describeSource(d, app.data.value) }}</span>
          <span v-if="d.commit_sha">
            <a v-if="commitUrl(app.data.value, d.commit_sha)" :href="commitUrl(app.data.value, d.commit_sha)" target="_blank" rel="noopener noreferrer" class="font-mono text-foreground hover:underline">{{ shortSha(d.commit_sha) }}</a>
            <span v-else class="font-mono">{{ shortSha(d.commit_sha) }}</span>
            <span v-if="d.commit_message" class="ml-1.5">{{ d.commit_message }}</span>
          </span>
        </div>
        <div v-if="d.error && d.state !== 'cancelled'" class="rounded-lg bg-destructive/10 p-3 text-sm text-destructive">
          <p class="font-medium">{{ multi ? aboutService(d.error.message, d.error.service) : d.error.message }}</p>
          <p v-if="plainHint(d.error.code, d.error.hint)" class="mt-1">{{ plainHint(d.error.code, d.error.hint) }}</p>
          <details v-if="d.log_tail?.length" class="mt-2">
            <summary class="cursor-pointer text-xs underline">Show the last log lines</summary>
            <div class="mt-2 max-h-60 overflow-auto rounded bg-background/60 p-2 font-mono text-xs leading-relaxed text-foreground"><LogLines :lines="d.log_tail" /></div>
          </details>
          <CopyForAiButton v-if="d.state === 'failed' && app.data.value" class="mt-3" :app="app.data.value" :deployment="d" />
        </div>
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
            :class="w.severity === 'info' ? 'text-muted-foreground' : 'text-[color-mix(in_oklch,var(--warning)_75%,var(--foreground))]'"
          >
            {{ w.message }}<template v-if="w.hint">&nbsp;{{ w.hint }}</template>
          </li>
        </ul>
      </article>
    </template>

    <ConfirmDialog
      v-model:open="confirmOpen"
      title="Go back to this version?"
      :description="`Your app runs ${versionLabel(target?.seq).toLowerCase()} again. You can switch back to the current version later.`"
      confirm-label="Go back"
      :loading="rollback.isPending.value"
      @confirm="target && rollback.mutate(target)"
    />
  </div>
</template>
